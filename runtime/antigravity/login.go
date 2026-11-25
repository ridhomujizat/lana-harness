package antigravity

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	// detailRunes caps a reported error line.
	detailRunes = 240
	// signInScanBytes caps one scanned output line of the sign-in process.
	signInScanBytes = 1 << 20
	// linkHost is the Google sign-in host: the first line that holds a URL
	// with this prefix gives the link lana hands to onLink.
	linkHost = "https://accounts.google.com/"
)

// signInURLRe matches a URL in a line lana reports; every match becomes
// "sign-in link", so no link reaches a log or an API answer.
var signInURLRe = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)

// ansiRe matches ANSI escape sequences, stripped before a line is reported.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// loginLinks are the owner files lana links into a named account's HOME
// (REQ-AGY-010), so git over SSH and gh work there.
var loginLinks = []string{".gitconfig", ".ssh", ".config/gh"}

// stripANSI removes ANSI escape sequences from a line.
func stripANSI(line string) string { return ansiRe.ReplaceAllString(line, "") }

// detailLine redacts one reportable line: URLs to "sign-in link", control
// characters to spaces, runs of whitespace collapsed, and at most detailRunes
// runes. It never holds a credential.
func detailLine(line string) string {
	line = stripANSI(line)
	line = signInURLRe.ReplaceAllString(line, "sign-in link")
	line = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, line)
	line = strings.Join(strings.Fields(line), " ")
	return truncate(line, detailRunes)
}

// lastLineDetail is the last non-empty line of a probe's output, redacted by
// detailLine.
func lastLineDetail(out string) string {
	last := ""
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r", "\n"), "\n") {
		if line := strings.TrimSpace(stripANSI(raw)); line != "" {
			last = line
		}
	}
	return detailLine(last)
}

// linkInLine returns the Google sign-in URL of a line, or "".
func linkInLine(line string) string {
	for _, field := range strings.Fields(stripANSI(line)) {
		if strings.HasPrefix(field, linkHost) {
			return field
		}
	}
	return ""
}

// SignIn signs a named Antigravity account in (REQ-AGY-010): it creates the
// account's HOME (0700) and the links to the owner's git files, runs
// `<binary> acp --login` there, hands the Google link to onLink, waits for one
// code on the code channel, writes it once to agy's stdin, and waits for agy
// to exit (at most SignInTimeout). agy's output is kept only for a failure
// line, with URLs replaced by "sign-in link". The code and the link are never
// stored or logged. Cancelling ctx stops the process group and returns the
// context error; closing the code channel gives agy EOF so it reports the
// cancellation itself. The built-in account cannot be signed in here.
func (r *Runtime) SignIn(ctx context.Context, account core.RuntimeAccount, onLink func(url string), code <-chan string) error {
	if agyAccountName(account) == core.AntigravityProvider {
		return userError("the built-in account signs in with agy in a terminal")
	}
	bin, err := r.resolveBinary(account.Binary)
	if err != nil {
		return err
	}
	home := account.Home
	if err := os.MkdirAll(home, 0o700); err != nil {
		return userError("cannot create the account folder: %v", err)
	}
	if err := r.linkOwnerFiles(home); err != nil {
		return err
	}
	env := buildEnv(r.opts.Environ(), core.RuntimeConfig{Home: home})
	runCtx, cancel := context.WithTimeout(ctx, r.opts.SignInTimeout)
	defer cancel()

	cmd := exec.Command(bin, "acp", "--login")
	cmd.Env = env
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return userError("Could not start Antigravity sign-in: %v", err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return userError("Could not start Antigravity sign-in: %v", err)
	}
	// Stdout and stderr share one pipe, so the lines agy reads on stdin and
	// lana reads here stay in order.
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return userError("Could not start Antigravity sign-in: %v", err)
	}
	_ = pw.Close() // the child holds its own copy

	var mu sync.Mutex
	last := ""
	linkCh := make(chan string, 1)
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		defer pr.Close()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), signInScanBytes)
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(stripANSI(line)) != "" {
				mu.Lock()
				last = line
				mu.Unlock()
			}
			if url := linkInLine(line); url != "" {
				select {
				case linkCh <- url:
				default:
				}
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	lastLine := func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}

	// exitFailure drains the output after agy exited, then reports its last
	// line.
	exitFailure := func() error {
		<-scanDone
		return signInFailure(lastLine())
	}

	// Wait for the link agy prints before it reads the code.
	select {
	case url := <-linkCh:
		if onLink != nil {
			onLink(url)
		}
	case <-scanDone:
		<-done
		return exitFailure()
	case <-runCtx.Done():
		r.stopProbe(cmd, done)
		return signInContextErr(ctx)
	}

	// Wait for one code, then write it once.
	select {
	case value, ok := <-code:
		if !ok {
			// No code will come: give agy EOF and read its answer.
			_ = stdin.Close()
			select {
			case <-done:
			case <-runCtx.Done():
				r.stopProbe(cmd, done)
				return signInContextErr(ctx)
			}
			return exitFailure()
		}
		if _, err := io.WriteString(stdin, value+"\n"); err != nil {
			_ = stdin.Close()
			r.stopProbe(cmd, done)
			return exitFailure()
		}
		_ = stdin.Close()
	case <-scanDone:
		<-done
		return exitFailure()
	case <-runCtx.Done():
		r.stopProbe(cmd, done)
		return signInContextErr(ctx)
	}

	select {
	case waitErr := <-done:
		if waitErr == nil {
			// The account just signed in: its cached `agy models` probe is
			// stale, so the next AccountStatus runs it again.
			r.ForgetAccountStatus(agyAccountName(account))
			return nil
		}
		return exitFailure()
	case <-runCtx.Done():
		r.stopProbe(cmd, done)
		return signInContextErr(ctx)
	}
}

// signInFailure is the error of a failed sign-in: the last output line, URLs
// replaced by "sign-in link", at most detailRunes runes.
func signInFailure(last string) error {
	if detail := detailLine(last); detail != "" {
		return &TurnError{Message: detail}
	}
	return userError("Antigravity sign-in failed")
}

// signInContextErr is the error of a cancelled or timed-out sign-in.
func signInContextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("antigravity sign-in cancelled: %w", err)
	}
	return userError("Antigravity sign-in timed out. Please try again.")
}

// linkOwnerFiles creates the REQ-AGY-010 links inside home: the owner's
// .gitconfig, .ssh, and .config/gh, each only when the source exists and the
// link does not. The owner's HOME is the daemon environment's HOME, never the
// account's own HOME.
func (r *Runtime) linkOwnerFiles(home string) error {
	owner := r.ownerHome()
	if owner == "" || samePath(owner, home) {
		return nil
	}
	for _, rel := range loginLinks {
		src := filepath.Join(owner, rel)
		dst := filepath.Join(home, rel)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if _, err := os.Lstat(dst); !os.IsNotExist(err) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return userError("cannot create the account folder: %v", err)
		}
		if err := os.Symlink(src, dst); err != nil {
			return userError("cannot link %s into the account folder: %v", rel, err)
		}
	}
	return nil
}

// ownerHome returns the daemon environment's HOME (the last match, as exec
// keeps it), else the user's home.
func (r *Runtime) ownerHome() string {
	home := ""
	for _, kv := range r.opts.Environ() {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok && v != "" {
			home = v
		}
	}
	if home != "" {
		return home
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// samePath reports whether two paths name the same folder.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

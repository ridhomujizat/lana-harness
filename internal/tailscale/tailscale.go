// Package tailscale wraps the Tailscale CLI for remote access (spec #57). It
// finds the binary, reads the status and serve state, runs serve and unserve,
// and maps a client IP to its device and login through whois. Every call runs
// without a shell and with a short timeout, and the package takes a runner
// interface so tests use canned JSON instead of the real CLI (REQ-RA-050..054).
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotInstalled reports that no Tailscale binary was found.
var ErrNotInstalled = errors.New("tailscale: the CLI is not installed")

const (
	// callTimeout bounds every CLI call (REQ-RA-050).
	callTimeout = 5 * time.Second
	// whoisTTL is how long a whois result is cached per IP (REQ-RA-053).
	whoisTTL = 5 * time.Minute
	// errorCut caps the CLI stderr carried by an error (REQ-RA-052).
	errorCut = 500
)

// fallbackPaths are the Tailscale binary locations tried after PATH, in order
// (REQ-RA-050).
var fallbackPaths = []string{
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/opt/homebrew/bin/tailscale",
	"/usr/local/bin/tailscale",
}

// Runner runs one CLI call. os/exec backs it in production; tests pass a fake
// that returns canned output and never runs the real binary (REQ-RA-054).
type Runner interface {
	Run(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error)
}

// Status is the Tailscale state the remote access settings show (REQ-RA-051).
type Status struct {
	Installed  bool   `json:"installed"`
	Path       string `json:"path"`
	Version    string `json:"version"`
	Running    bool   `json:"running"`
	DNSName    string `json:"dns_name"`
	Login      string `json:"login"`
	Tailnet    string `json:"tailnet"`
	MagicDNS   bool   `json:"magic_dns"`
	HTTPSCerts bool   `json:"https_certs"`
	Serving    bool   `json:"serving"`
	ServeOther bool   `json:"serve_other"`
}

// Peer is the device and login behind a Tailscale client IP (REQ-RA-053).
type Peer struct {
	Device string `json:"device"`
	Login  string `json:"login"`
}

// Client calls the Tailscale CLI.
type Client struct {
	runner   Runner
	lookPath func() (string, bool)
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]whoisEntry
}

// whoisEntry is one cached whois result.
type whoisEntry struct {
	peer Peer
	at   time.Time
}

// New returns a Client that runs the real Tailscale binary.
func New() *Client {
	return NewWithRunner(execRunner{}, defaultLookPath)
}

// NewWithRunner returns a Client with a test runner and binary lookup. A nil
// runner or find function would panic on use; tests pass both.
func NewWithRunner(r Runner, find func() (string, bool)) *Client {
	return &Client{
		runner:   r,
		lookPath: find,
		now:      time.Now,
		cache:    map[string]whoisEntry{},
	}
}

// execRunner runs the binary through os/exec, without a shell (REQ-RA-050).
type execRunner struct{}

func (execRunner) Run(ctx context.Context, bin string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// defaultLookPath finds the Tailscale binary: PATH first, then the app and the
// two Homebrew locations; the first regular executable wins (REQ-RA-050).
func defaultLookPath() (string, bool) {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path, true
	}
	for _, path := range fallbackPaths {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path, true
		}
	}
	return "", false
}

// run calls the CLI with the 5-second timeout of REQ-RA-050.
func (c *Client) run(ctx context.Context, path string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.runner.Run(ctx, path, args...)
}

// Status reads the Tailscale state (REQ-RA-051). A missing binary is not an
// error: the result has Installed false. A stopped backend reports Installed
// true and Running false, also without an error.
func (c *Client) Status(ctx context.Context, daemonAddr string) (Status, error) {
	path, ok := c.lookPath()
	if !ok {
		return Status{Installed: false}, nil
	}
	status := Status{Installed: true, Path: path}
	if out, _, err := c.run(ctx, path, "version"); err == nil {
		status.Version = firstLine(string(out))
	}

	out, _, err := c.run(ctx, path, "status", "--json")
	data := bytes.TrimSpace(out)
	if len(data) == 0 {
		// A stopped daemon prints no status: installed but not running.
		return status, nil
	}
	var raw statusJSON
	if uerr := json.Unmarshal(data, &raw); uerr != nil {
		if err != nil {
			return status, nil
		}
		return status, fmt.Errorf("tailscale status: %w", uerr)
	}
	status.Running = raw.BackendState == "Running"
	status.DNSName = strings.TrimSuffix(raw.Self.DNSName, ".")
	if user, ok := raw.User[strconv.FormatInt(raw.Self.UserID, 10)]; ok {
		status.Login = user.LoginName
	}
	status.Tailnet = raw.CurrentTailnet.Name
	status.MagicDNS = raw.CurrentTailnet.MagicDNSEnabled
	status.HTTPSCerts = len(raw.CertDomains) > 0
	status.Serving, status.ServeOther = c.serveState(ctx, path, daemonAddr)
	return status, nil
}

// statusJSON is the shape of `tailscale status --json`. User is keyed by the
// decimal user id.
type statusJSON struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName string `json:"DNSName"`
		UserID  int64  `json:"UserID"`
	} `json:"Self"`
	User map[string]struct {
		LoginName string `json:"LoginName"`
	} `json:"User"`
	CurrentTailnet struct {
		Name            string `json:"Name"`
		MagicDNSEnabled bool   `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
	CertDomains []string `json:"CertDomains"`
}

// serveStatusJSON is the shape of `tailscale serve status --json`.
type serveStatusJSON struct {
	TCP map[string]struct {
		HTTPS bool `json:"HTTPS"`
	} `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

// serveState reports whether Serve exposes HTTPS 443 to daemonAddr, and
// whether port 443 already serves something else (REQ-RA-051).
func (c *Client) serveState(ctx context.Context, path, daemonAddr string) (serving, other bool) {
	out, _, err := c.run(ctx, path, "serve", "status", "--json")
	if err != nil {
		return false, false
	}
	data := bytes.TrimSpace(out)
	if len(data) == 0 {
		return false, false
	}
	var raw serveStatusJSON
	if json.Unmarshal(data, &raw) != nil {
		return false, false
	}
	tcp443, hasTCP443 := raw.TCP["443"]
	https443 := hasTCP443 && tcp443.HTTPS
	want := "http://" + daemonAddr
	ours, web443 := false, false
	for key, web := range raw.Web {
		if !strings.HasSuffix(key, ":443") {
			continue
		}
		web443 = true
		if proxy := web.Handlers["/"].Proxy; proxy == want || proxy == want+"/" {
			ours = true
		}
	}
	serving = https443 && ours
	other = (hasTCP443 || web443) && !serving
	return serving, other
}

// Serve points Tailscale Serve's HTTPS 443 at http://daemonAddr (REQ-RA-052).
func (c *Client) Serve(ctx context.Context, daemonAddr string) error {
	path, ok := c.lookPath()
	if !ok {
		return ErrNotInstalled
	}
	if _, stderr, err := c.run(ctx, path, "serve", "--bg", "--https=443", "http://"+daemonAddr); err != nil {
		return cliError("serve", stderr, err)
	}
	return nil
}

// Unserve turns off the Serve handler on 443 (REQ-RA-052).
func (c *Client) Unserve(ctx context.Context) error {
	path, ok := c.lookPath()
	if !ok {
		return ErrNotInstalled
	}
	if _, stderr, err := c.run(ctx, path, "serve", "--https=443", "off"); err != nil {
		return cliError("serve", stderr, err)
	}
	return nil
}

// Whois maps ip to its Tailscale device and login (REQ-RA-053). Results are
// cached for 5 minutes per IP. An IP that does not parse is rejected before
// anything runs.
func (c *Client) Whois(ctx context.Context, ip string) (Peer, error) {
	if net.ParseIP(ip) == nil {
		return Peer{}, fmt.Errorf("tailscale whois: invalid IP %q", ip)
	}
	now := c.now()
	c.mu.Lock()
	entry, ok := c.cache[ip]
	c.mu.Unlock()
	if ok && now.Sub(entry.at) < whoisTTL {
		return entry.peer, nil
	}

	path, ok := c.lookPath()
	if !ok {
		return Peer{}, ErrNotInstalled
	}
	out, stderr, err := c.run(ctx, path, "whois", "--json", ip)
	if err != nil {
		return Peer{}, cliError("whois", stderr, err)
	}
	var raw whoisJSON
	if err := json.Unmarshal(bytes.TrimSpace(out), &raw); err != nil {
		return Peer{}, fmt.Errorf("tailscale whois: %w", err)
	}
	peer := Peer{Device: raw.Node.ComputedName, Login: raw.UserProfile.LoginName}
	if peer.Device == "" {
		peer.Device = firstLabel(raw.Node.Name)
	}
	c.mu.Lock()
	c.cache[ip] = whoisEntry{peer: peer, at: now}
	c.mu.Unlock()
	return peer, nil
}

// whoisJSON is the shape of `tailscale whois --json <ip>`.
type whoisJSON struct {
	Node struct {
		ComputedName string `json:"ComputedName"`
		Name         string `json:"Name"`
	} `json:"Node"`
	UserProfile struct {
		LoginName string `json:"LoginName"`
	} `json:"UserProfile"`
}

// cliError renders a failed CLI call: the trimmed stderr, cut to 500
// characters, or the underlying error when there is no stderr (REQ-RA-052).
func cliError(op string, stderr []byte, err error) error {
	msg := truncate(strings.TrimSpace(string(stderr)), errorCut)
	if msg == "" {
		return fmt.Errorf("tailscale %s: %w", op, err)
	}
	return fmt.Errorf("tailscale %s: %s", op, msg)
}

// firstLine returns the first line of s, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// firstLabel returns name up to the first dot.
func firstLabel(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	if runes := []rune(s); len(runes) > n {
		return string(runes[:n])
	}
	return s
}

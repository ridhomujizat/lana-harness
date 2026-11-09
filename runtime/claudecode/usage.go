package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/ridhomujizat/lana-harness/core"
)

// The usage probe of a Claude Code account (spec 39 Revision 4, ADR 0020
// amendment 2026-11-08). lana runs
//
//	claude -p /usage --output-format stream-json --verbose
//	       --no-session-persistence --setting-sources project
//
// with the account's environment, stdin closed, in a new empty folder. /usage
// is a local command: Claude Code answers it without a model call (the result
// reports 0 input and 0 output tokens), and lana treats any other answer as a
// failed probe. --setting-sources project in the empty folder skips the
// owner's user settings, so no hook or plugin runs; --bare is not used
// because it never reads the OAuth login. Only the plan lines of the text are
// kept; the rest of the answer is dropped.

// DefaultUsageProbeTimeout bounds one usage probe.
const DefaultUsageProbeTimeout = 45 * time.Second

const (
	// usageProbeMaxBytes caps the stdout read from the probe.
	usageProbeMaxBytes = 4 << 20
	// usageErrorRunes caps the reported probe error.
	usageErrorRunes = 240
)

// usageArgs is the argv of the probe after the binary.
var usageArgs = []string{
	"-p", "/usage",
	"--output-format", "stream-json",
	"--verbose",
	"--no-session-persistence",
	"--setting-sources", "project",
}

// UsageProbe runs `claude -p /usage` for the account and parses its plan
// windows (spec 39 Revision 4). An api_key account is no subscription and
// runs nothing. The probe fails when Claude Code reports an error, any input
// or output tokens (a model call), or text without plan lines.
func (r *Runtime) UsageProbe(ctx context.Context, account core.RuntimeAccount) (core.RuntimeUsageReport, error) {
	if account.Auth == core.RuntimeAuthAPIKey {
		return core.RuntimeUsageReport{}, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return core.RuntimeUsageReport{}, userError("Claude Code runtime is shut down")
	}
	r.usageSeq++
	id := r.usageSeq
	r.usageRuns[id] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.usageRuns, id)
		r.mu.Unlock()
	}()

	bin, err := r.accountBinary(account)
	if err != nil {
		return core.RuntimeUsageReport{}, err
	}
	// Subscription auth: the token variables are removed, so /usage reads
	// the account's own login.
	env, _, err := buildEnv(r.opts.Environ(), core.RuntimeConfig{ConfigDir: account.ConfigDir}, nil)
	if err != nil {
		return core.RuntimeUsageReport{}, err
	}
	dir, err := os.MkdirTemp(r.opts.TempDir, "lana-usage-")
	if err != nil {
		return core.RuntimeUsageReport{}, fmt.Errorf("usage probe folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	out, stderr, runErr := r.runUsage(runCtx, bin, env, dir)
	if runErr != nil {
		return core.RuntimeUsageReport{}, runErr
	}
	text, err := usageResult(out)
	if err != nil {
		if detail := stderrDetail(stderr, "link"); detail != "" && errors.Is(err, errNoUsageResult) {
			return core.RuntimeUsageReport{}, &TurnError{Message: "Claude Code /usage gave no answer: " + detail}
		}
		return core.RuntimeUsageReport{}, err
	}
	return parseUsageText(text, time.Now())
}

// runUsage starts the probe and waits for it, at most UsageProbeTimeout. On
// timeout or cancel the process group is stopped.
func (r *Runtime) runUsage(ctx context.Context, bin string, env []string, dir string) ([]byte, string, error) {
	cmd := exec.Command(bin, usageArgs...)
	cmd.Env = env
	cmd.Dir = dir
	// Stdin stays nil: the child reads /dev/null.
	stdout := &capWriter{max: usageProbeMaxBytes}
	stderr := &tailWriter{max: signInTailBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = authWaitDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, "", userError("Could not start Claude Code /usage: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	timer := time.NewTimer(r.opts.UsageProbeTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return stdout.Bytes(), stderr.String(), nil
	case <-ctx.Done():
		r.stopAuth(cmd, done)
		return nil, "", fmt.Errorf("claude code /usage cancelled: %w", ctx.Err())
	case <-timer.C:
		r.stopAuth(cmd, done)
		return nil, "", userError("Claude Code /usage timed out after %s", r.opts.UsageProbeTimeout)
	}
}

// cancelUsageRuns cancels every running usage probe (Shutdown).
func (r *Runtime) cancelUsageRuns() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.usageRuns {
		cancel()
	}
}

var _ core.RuntimeUsageProber = (*Runtime)(nil)

// capWriter keeps the first max bytes written to it and drops the rest.
type capWriter struct {
	max int
	buf bytes.Buffer
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *capWriter) Bytes() []byte { return c.buf.Bytes() }

var errNoUsageResult = errors.New("no result")

// usageLine is the part of a stream-json line the probe reads.
type usageLine struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Usage   *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// usageResult reads the probe's stream-json output and returns the /usage
// text: the result's text, else the assistant's. It fails on an error result,
// on a result that reports input or output tokens (a model call), and when
// no result line came.
func usageResult(out []byte) (string, error) {
	var assistant strings.Builder
	var result *usageLine
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), usageProbeMaxBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev usageLine
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, block := range ev.Message.Content {
				if block.Type == "text" {
					assistant.WriteString(block.Text)
				}
			}
		case "result":
			res := ev
			result = &res
		}
	}
	if result == nil {
		return "", errNoUsageResult
	}
	if result.Usage != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
		return "", userError("Claude Code /usage made a model call (%d input, %d output tokens); the refresh is treated as failed",
			result.Usage.InputTokens, result.Usage.OutputTokens)
	}
	text := result.Result
	if strings.TrimSpace(text) == "" {
		text = assistant.String()
	}
	if result.IsError {
		detail := truncate(redact(strings.Join(strings.Fields(text), " "), ""), usageErrorRunes)
		if detail == "" {
			detail = "unknown error"
		}
		return "", &TurnError{Message: "Claude Code /usage failed: " + detail}
	}
	return text, nil
}

var (
	// usageLineRe matches "Current session: 43% used · resets …" and
	// "Current week (all models): 42% used …".
	usageLineRe = regexp.MustCompile(`(?i)^current\s+(session|week)(?:\s*\(([^)]*)\))?\s*:\s*(\d+(?:\.\d+)?)\s*%\s*used\b(.*)$`)
	// usageResetsRe finds the reset part of the rest of a plan line.
	usageResetsRe = regexp.MustCompile(`(?i)\bresets?\b\s*(?:at\s+|on\s+)?(.*)$`)
	// usageTZRe is a trailing "(Asia/Jakarta)".
	usageTZRe = regexp.MustCompile(`\(\s*([^()]+?)\s*\)\s*\.?$`)
	// usageInRe is a relative reset: "in 2h 5m".
	usageInRe   = regexp.MustCompile(`(?i)^in\s+(.+)$`)
	usagePartRe = regexp.MustCompile(`(?i)(\d+)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
	// usageWhenRe is "Nov 8 at 3:39pm", "Nov 12, 2026 at 12pm", "Nov 12",
	// or "3:39pm".
	usageWhenRe = regexp.MustCompile(`(?i)^(?:([a-z]{3,9})\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?)?\s*,?\s*(?:at\s+)?(?:(\d{1,2})(?::(\d{2}))?\s*(am|pm)?)?$`)
	// usageNotSubRe marks an answer for an account without a subscription.
	usageNotSubRe = regexp.MustCompile(`(?i)\bapi\s+(?:key|usage|billing|credits?)\b|\bconsole\b|not\s+(?:using\s+)?(?:a\s+|your\s+)?(?:claude\s+)?subscription|only\s+available\s+(?:for|to|with)\b|subscription\s+(?:plans?|users?)\s+only`)
	// usageLoginRe marks an answer for an account that is not logged in.
	usageLoginRe = regexp.MustCompile(`(?i)not\s+logged\s+in|please\s+run\s+/login|invalid\s+api\s+key`)
)

var usageMonths = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

// parseUsageText reads the plan lines of the /usage text:
//
//	Current session: N% used · resets <when>           → five_hour
//	Current week (all models): N% used · resets <when> → seven_day
//	Current week (<Model>): N% used · resets <when>    → seven_day_<model>
//
// <when> is "<Mon> <D> at <h:mm>am/pm (<IANA zone>)" (the year is the next
// occurrence at or after now − 1 day), "in 2h 5m", or missing. A text
// without plan lines is either no subscription (Subscription false) or an
// error.
func parseUsageText(text string, now time.Time) (core.RuntimeUsageReport, error) {
	var windows []core.RuntimeLimitWindow
	seen := map[string]bool{}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n") {
		line := strings.TrimSpace(signInANSIRe.ReplaceAllString(raw, ""))
		m := usageLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		window := core.RuntimeLimitWindow{}
		scope := strings.TrimSpace(m[2])
		switch {
		case strings.EqualFold(m[1], "session"):
			window.Type = "five_hour"
		case scope == "" || strings.EqualFold(scope, "all models"):
			window.Type = "seven_day"
		default:
			slug := usageSlug(scope)
			if slug == "" {
				continue
			}
			window.Type = "seven_day_" + slug
			window.Label = "Weekly · " + truncate(scope, 40)
		}
		if seen[window.Type] {
			continue
		}
		pct, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			continue
		}
		util := pct / 100
		window.Utilization = &util
		window.Status = "allowed"
		if util >= 1 {
			window.Status = "rejected"
		}
		if reset := usageResetsRe.FindStringSubmatch(m[4]); reset != nil {
			if at, ok := parseResetWhen(reset[1], now); ok {
				window.ResetsAt = at.UTC()
			}
		}
		seen[window.Type] = true
		windows = append(windows, window)
	}
	if len(windows) > 0 {
		return core.RuntimeUsageReport{Subscription: true, Windows: windows}, nil
	}
	switch {
	case usageLoginRe.MatchString(text):
		return core.RuntimeUsageReport{}, userError("Claude Code /usage: this account is not logged in")
	case strings.Contains(strings.ToLower(text), "using your subscription"):
		return core.RuntimeUsageReport{}, userError("Claude Code /usage listed no plan windows")
	case usageNotSubRe.MatchString(text):
		return core.RuntimeUsageReport{}, nil
	}
	first := ""
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			first = line
			break
		}
	}
	if first == "" {
		return core.RuntimeUsageReport{}, userError("Claude Code /usage answered with no text")
	}
	return core.RuntimeUsageReport{}, userError("Claude Code /usage answered in an unknown form: %s",
		truncate(redact(first, ""), 120))
}

// usageSlug lowercases s and turns each run of non-alphanumerics into "_".
func usageSlug(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		if r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			under = false
			continue
		}
		if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

// parseResetWhen reads the reset time of a plan line, relative to now.
func parseResetWhen(when string, now time.Time) (time.Time, bool) {
	when = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(when), "."))
	if when == "" {
		return time.Time{}, false
	}
	if m := usageInRe.FindStringSubmatch(when); m != nil {
		var d time.Duration
		parts := usagePartRe.FindAllStringSubmatch(m[1], -1)
		for _, p := range parts {
			n, _ := strconv.Atoi(p[1])
			switch unit := strings.ToLower(p[2]); {
			case strings.HasPrefix(unit, "d"):
				d += time.Duration(n) * 24 * time.Hour
			case strings.HasPrefix(unit, "h"):
				d += time.Duration(n) * time.Hour
			case strings.HasPrefix(unit, "m"):
				d += time.Duration(n) * time.Minute
			default:
				d += time.Duration(n) * time.Second
			}
		}
		if len(parts) == 0 {
			return time.Time{}, false
		}
		return now.Add(d), true
	}
	loc := now.Location()
	if m := usageTZRe.FindStringSubmatch(when); m != nil {
		if l, err := time.LoadLocation(strings.TrimSpace(m[1])); err == nil {
			loc = l
		} else if strings.EqualFold(strings.TrimSpace(m[1]), "UTC") {
			loc = time.UTC
		}
		when = strings.TrimSpace(when[:len(when)-len(m[0])])
	}
	m := usageWhenRe.FindStringSubmatch(when)
	if m == nil || (m[1] == "" && m[4] == "") {
		return time.Time{}, false
	}
	hour, minute := 0, 0
	if m[4] != "" {
		hour, _ = strconv.Atoi(m[4])
		if m[5] != "" {
			minute, _ = strconv.Atoi(m[5])
		}
		switch strings.ToLower(m[6]) {
		case "am":
			if hour == 12 {
				hour = 0
			}
		case "pm":
			if hour != 12 {
				hour += 12
			}
		}
		if hour > 23 || minute > 59 {
			return time.Time{}, false
		}
	}
	local := now.In(loc)
	if m[1] == "" {
		// A time only: today, or tomorrow when it passed over an hour ago.
		at := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
		if at.Before(now.Add(-time.Hour)) {
			at = at.AddDate(0, 0, 1)
		}
		return at, true
	}
	month, ok := usageMonths[strings.ToLower(m[1])[:3]]
	if !ok {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(m[2])
	if day < 1 || day > 31 {
		return time.Time{}, false
	}
	if m[3] != "" {
		year, _ := strconv.Atoi(m[3])
		return time.Date(year, month, day, hour, minute, 0, 0, loc), true
	}
	// The next occurrence at or after now − 1 day.
	at := time.Date(local.Year(), month, day, hour, minute, 0, 0, loc)
	if at.Before(now.Add(-24 * time.Hour)) {
		at = time.Date(local.Year()+1, month, day, hour, minute, 0, 0, loc)
	}
	return at, true
}

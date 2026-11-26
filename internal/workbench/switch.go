package workbench

import (
	"errors"
	"os"
	"strings"
	"time"
)

// Spec 82 REQ-WAS-002: switching a pane's Claude Code conversation to another
// account. The core stops the pane's claude with Ctrl+C until the shell owns
// the terminal again, then types the resume line for the new account.
const (
	// switchInterruptEvery is the gap between two Ctrl+C writes.
	switchInterruptEvery = 400 * time.Millisecond
	// switchInterruptMax bounds the Ctrl+C writes of one switch.
	switchInterruptMax = 6
	// switchTimeout bounds how long claude may take to give the terminal back.
	switchTimeout = 5 * time.Second
	// switchPoll is how often the foreground process group is read.
	switchPoll = 25 * time.Millisecond
	// switchSettle is how long the shell must stay quiet after printing its
	// prompt before the line is typed.
	switchSettle = 150 * time.Millisecond
)

// builtinClaudeResumePrefix starts the line that resumes a conversation on the
// built-in account: neither config variable reaches claude, whatever the
// pane's environment holds (spec 82 REQ-WAS-002 step 4).
const builtinClaudeResumePrefix = "env -u CLAUDE_CONFIG_DIR -u CLAUDE_SECURESTORAGE_CONFIG_DIR claude "

// errSwitchBusy answers a second switch of a pane while one runs.
var errSwitchBusy = errors.New("workbench: an account switch is already running in this pane")

// errSwitchConfigDir answers a named account whose config dir is missing or
// cannot be typed safely.
var errSwitchConfigDir = errors.New("workbench: the account's config folder is missing or cannot be used")

// switchableCommand reports whether argv is an interactive shell: a shell
// whose arguments are options only, none of them -c. Such a shell runs claude
// as a job of its own, so the terminal comes back to it when claude exits. A
// pane whose own command is claude, or a `sh -c` line (the form an older core
// got in place of CreateOptions.Input), is not.
func switchableCommand(argv []string) bool {
	if len(argv) == 0 || !isShellCommand(argv[0]) || isLegacyInputCommand(argv) {
		return false
	}
	for _, a := range argv[1:] {
		if !strings.HasPrefix(a, "-") {
			return false
		}
		if !strings.HasPrefix(a, "--") && strings.ContainsRune(a, 'c') {
			return false
		}
	}
	return true
}

// switchAgents are the agents whose conversation can move to another account:
// Claude Code (spec 82) and Antigravity (spec 83).
var switchAgents = map[string]bool{"claude": true, AgentAgy: true}

// switchStateLocked reports what Session.AccountSwitch says for the session,
// with the agent whose conversation a switch would resume, the conversation,
// and its account dir (claude config dir, agy HOME). The session lock is held.
func (s *session) switchStateLocked() (state, agent, conversation, configDir string) {
	a, ok := adapterFor(s.rAgent)
	if !switchAgents[s.rAgent] || !ok || !a.validID(s.agentSession) {
		return CodeNoConversation, "", "", ""
	}
	if s.exited || s.p == nil || !switchableCommand(s.command) {
		return CodeNotSwitchable, s.rAgent, s.agentSession, s.agentConfigDir
	}
	return SwitchReady, s.rAgent, s.agentSession, s.agentConfigDir
}

// noConversationError is the no_conversation refusal for agent, named in its
// message.
func noConversationError(agent string) error {
	if agent == AgentAgy {
		return &codeError{msg: "No Antigravity conversation in this pane", kind: ErrNoConversation}
	}
	return ErrNoConversation
}

// stillRunningError is the still_running refusal for agent.
func stillRunningError(agent string) error {
	if agent == AgentAgy {
		return &codeError{msg: "Antigravity did not exit. Exit it in the pane and try again.", kind: ErrStillRunning}
	}
	return ErrStillRunning
}

// switchResumeLine is the line that resumes conversation id of agent on the
// account of configDir. Claude Code: the spec 60 claudeResumeLine form for a
// named account, the env -u form for the built-in one (configDir ""). agy: the
// account's HOME line of spec 83 REQ-WAG-002. id must be validated already.
func switchResumeLine(agent, id, configDir, home string, env []string) (string, error) {
	if agent == AgentAgy {
		line, ok := agyResumeLine(configDir, "--conversation "+id)
		if !ok {
			return "", errSwitchConfigDir
		}
		return line, nil
	}
	tail := "--resume " + id
	if configDir == "" {
		return builtinClaudeResumePrefix + tail, nil
	}
	if !safeConfigDir(configDir) {
		return "", errSwitchConfigDir
	}
	line, ok := claudeResumeLine(configDir, tail, home, env)
	if !ok {
		return "", errSwitchConfigDir
	}
	return line, nil
}

// switchAccount continues the session's Claude Code conversation on the
// account of o.ConfigDir (spec 82 REQ-WAS-002). It refuses a session without a
// conversation (ErrNoConversation) or whose process is not a shell
// (ErrNotSwitchable) without touching it; it stops claude with Ctrl+C and
// refuses with ErrStillRunning, typing nothing, when claude keeps the terminal
// past switchTimeout; then it types the resume line and records the new config
// dir, so a later restore resumes on it.
func (s *session) switchAccount(o SwitchOptions) error {
	home, _ := os.UserHomeDir()
	want := o.Agent
	if want == "" {
		want = "claude"
	}
	s.mu.Lock()
	state, agent, id, _ := s.switchStateLocked()
	if agent != want {
		state = CodeNoConversation
	}
	p, env, busy := s.p, s.env, s.switching
	if state == SwitchReady && !busy {
		s.switching = true
	}
	s.mu.Unlock()
	switch {
	case state == CodeNoConversation:
		return noConversationError(want)
	case state == CodeNotSwitchable:
		return ErrNotSwitchable
	case busy:
		return errSwitchBusy
	}
	defer func() {
		s.mu.Lock()
		s.switching = false
		s.mu.Unlock()
	}()
	line, err := switchResumeLine(agent, id, o.ConfigDir, home, env)
	if err != nil {
		return err
	}
	since, err := s.stopForeground(p)
	if errors.Is(err, ErrStillRunning) {
		return stillRunningError(agent)
	}
	if err != nil {
		return err
	}
	s.waitPrompt(since)
	s.mu.Lock()
	if s.exited || s.p != p {
		s.mu.Unlock()
		return ErrNotSwitchable
	}
	// Recorded before the line is typed, so the new claude's own lookup can
	// only ever replace it with what it finds.
	s.agentConfigDir = o.ConfigDir
	notify := s.notify
	s.mu.Unlock()
	if agent == AgentAgy {
		// The old agy's record must not be matched to the new one.
		removeAgentRecord(coreHome(), s.id)
	}
	if _, err := p.write([]byte(line + "\r")); err != nil {
		return err
	}
	if notify != nil {
		notify()
	}
	return nil
}

// stopForeground writes Ctrl+C to the pty, then again every
// switchInterruptEvery (at most switchInterruptMax times), until the pty's
// foreground process group is the shell's own, and returns when the first
// Ctrl+C went out. It gives up with ErrStillRunning after switchTimeout.
func (s *session) stopForeground(p *pty) (time.Time, error) {
	shell := p.pid()
	atShell := func() bool {
		pgid, ok := p.foregroundGroup()
		return ok && shell > 0 && pgid == shell
	}
	start := time.Now()
	deadline := start.Add(switchTimeout)
	var next time.Time
	sent := 0
	for {
		now := time.Now()
		if sent < switchInterruptMax && !now.Before(next) {
			if _, err := p.write([]byte{0x03}); err != nil {
				return start, ErrNotSwitchable
			}
			sent++
			next = now.Add(switchInterruptEvery)
		}
		time.Sleep(switchPoll)
		s.mu.Lock()
		exited := s.exited || s.p != p
		s.mu.Unlock()
		if exited {
			return start, ErrNotSwitchable
		}
		if atShell() {
			return start, nil
		}
		if !time.Now().Before(deadline) {
			return start, ErrStillRunning
		}
	}
}

// waitPrompt waits until the shell has printed after since and then stayed
// quiet for switchSettle, so the typed line meets its prompt rather than its
// redraw; it gives up after resumeDelay and types anyway.
func (s *session) waitPrompt(since time.Time) {
	deadline := time.Now().Add(resumeDelay)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		last, exited := s.lastOutput, s.exited
		s.mu.Unlock()
		if exited {
			return
		}
		if last.After(since) && time.Since(last) >= switchSettle {
			return
		}
		time.Sleep(resumePoll)
	}
}

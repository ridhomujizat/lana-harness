package workbench

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agentdetect"
)

// session is a pseudo-terminal of the core and the program it runs.
type session struct {
	id      string
	title   string
	command []string
	path    string
	dir     string
	env     []string
	// extraEnv is the caller's env from create time: the only environment the
	// core persists (env is the full environment and is never written to disk).
	extraEnv []string
	labels   map[string]string
	created  time.Time
	// program names what runs in the session. REQ-WB-013 keeps it on the
	// pty's foreground process, falling back to the started program's name.
	program string
	// notify, when set, is called after the session exits or restarts so the
	// hub can report it without waiting for its next poll. It is called with
	// no session lock held.
	notify func()

	mu          sync.Mutex
	p           *pty
	ring        *ring
	emu         *screen
	clients     map[*attached]struct{}
	controls    int
	bellNeeds   bool
	lastOutput  time.Time
	exited      bool
	exitCode    int
	cols        int
	rows        int
	cwd         string
	oscTitle    string
	oscProgress string
	// oscBuf is the OSC payload being accumulated across pty reads, so a title
	// split mid-rune arrives whole (spec 55 A5).
	oscBuf []byte
	// Working directory tracking (spec 55 A4): procCwd is the last directory
	// read from the foreground process and procAt is when it last changed;
	// oscDir and oscAt are the last OSC 7 report and its arrival. cwdRead is
	// when the process directory was last read, to bound the read rate.
	procCwd    string
	procAt     time.Time
	oscDir     string
	oscAt      time.Time
	cwdRead    time.Time
	agent      string
	agentState string
	// Persisted agent tracking (spec 60 REQ-WR-005): rAgent, agentSession and
	// agentCwd outlive an agent leaving the foreground, unlike the live agent
	// and agentState above, which clear at once. agentConfigDir is the explicit
	// CLAUDE_CONFIG_DIR the conversation was found under, "" for the default
	// account (spec 60 addendum). agentSeen is when the agent was last in the
	// foreground; agentPID, agentStarted and agentLookupAt drive the
	// conversation-id lookup (REQ-WR-010).
	rAgent         string
	agentSession   string
	agentCwd       string
	agentConfigDir string
	agentSeen      time.Time
	agentPID       int
	agentStarted   time.Time
	agentLookupAt  time.Time
	// restored and resumedAgent mark a session brought back after a core
	// restart (spec 60 REQ-WR-024); both clear when it exits or restarts.
	restored     time.Time
	resumedAgent string
	// switching is set while an account switch runs in the session (spec 82
	// REQ-WAS-002), so a second one is refused.
	switching bool
	bellOsc   bool
	bellEsc   bool
	done      chan struct{}
}

// attached is a connection attached to a session, whose output a goroutine
// writes so a slow client never holds up the others.
type attached struct {
	conn    net.Conn
	out     chan []byte
	mode    string
	outOnce sync.Once
}

// send queues a chunk, or reports that the client is too far behind.
func (a *attached) send(p []byte) bool {
	select {
	case a.out <- p:
		return true
	default:
		return false
	}
}

// finish ends the stream after the queued chunks are written.
func (a *attached) finish() { a.outOnce.Do(func() { close(a.out) }) }

// drop ends the stream now, closing the connection to unblock a stuck writer.
func (a *attached) drop() {
	a.finish()
	_ = a.conn.Close()
}

// writer sends the queued chunks and closes the connection when done.
func (a *attached) writer() {
	for p := range a.out {
		if _, err := a.conn.Write(p); err != nil {
			break
		}
	}
	_ = a.conn.Close()
	for range a.out {
	}
}

// newSession starts o and returns the session running it. notify, when set,
// is called after the session exits or restarts.
func newSession(id string, o CreateOptions, notify func()) (*session, error) {
	path, argv, _, err := shellCommand(o.Command)
	if err != nil {
		return nil, err
	}
	cols, rows := o.Cols, o.Rows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	dir := o.Dir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	env := sessionEnv(id, o.Env)
	p, err := startPTY(path, argv, dir, env, cols, rows)
	if err != nil {
		return nil, err
	}
	s := &session{
		id: id, title: o.Title, command: argv, path: path, dir: dir, env: env,
		extraEnv: o.Env, labels: o.Labels, created: time.Now(), program: filepath.Base(path),
		notify: notify,
		p:      p, ring: newRing(ringSize), clients: map[*attached]struct{}{},
		cols: cols, rows: rows, procCwd: physicalDir(dir), done: make(chan struct{}),
	}
	s.mu.Lock()
	s.ring.mark(cols, rows)
	s.mu.Unlock()
	s.emu = newScreen(cols, rows, s.onProgress, s.onWorkingDirectory)
	s.run(p)
	return s, nil
}

// consts for the restore of an agent conversation.
const (
	// agentPersistTTL is how long the persisted agent fields survive an agent
	// leaving the foreground (spec 60 REQ-WR-005).
	agentPersistTTL = 10 * time.Minute
	// agentLookupInterval is the shortest gap between two conversation-id
	// lookups for the same agent pid (spec 60 REQ-WR-010).
	agentLookupInterval = 5 * time.Second
	// agentStartSkew is subtracted from the moment an agent pid is first seen,
	// so a session file it wrote just before is not filtered as too old.
	agentStartSkew = 5 * time.Second
	// resumeDelay is how long the core waits for a restored shell's first
	// output before typing the resume command anyway (spec 60 REQ-WR-022).
	resumeDelay = 1500 * time.Millisecond
	// resumePoll is how often typeResume checks for that first output.
	resumePoll = 50 * time.Millisecond
)

// newExitedSession builds a session that is already exited, without a pty: the
// form a restored record whose program had ended comes back as, and the form a
// running non-shell record comes back as when it must not be started (spec 60
// REQ-WR-021, REQ-WR-022). Its restart starts the command fresh.
func newExitedSession(rec sessionRecord) (*session, error) {
	path, argv, _, err := shellCommand(rec.Command)
	if err != nil {
		if len(rec.Command) == 0 {
			return nil, err
		}
		// Keep the argv so restart can report the failure; the path falls back
		// to argv[0].
		path, argv = rec.Command[0], rec.Command
	}
	cols, rows := rec.Cols, rec.Rows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	dir := rec.Dir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	done := make(chan struct{})
	close(done)
	s := &session{
		id: rec.ID, title: rec.Title, command: argv, path: path, dir: dir,
		env: sessionEnv(rec.ID, rec.Env), extraEnv: rec.Env, labels: rec.Labels,
		created: rec.Created, program: filepath.Base(path),
		ring: newRing(ringSize), clients: map[*attached]struct{}{},
		cols: cols, rows: rows, procCwd: physicalDir(dir),
		exited: true, exitCode: rec.ExitCode, done: done,
		rAgent: rec.Agent, agentSession: rec.AgentSession, agentCwd: rec.AgentCwd,
		agentConfigDir: rec.AgentConfigDir, agentSeen: rec.AgentSeen,
	}
	s.mu.Lock()
	s.ring.mark(cols, rows)
	s.mu.Unlock()
	// There is no pty and no output, so the session has no screen: a screen
	// would start an answer-pipe drain that never ends. The emulator users
	// (attach, resize, restart) handle a nil one.
	return s, nil
}

// pid returns the session's process id, or 0 when it has no pty (an exited
// session restored without one).
func (s *session) pid() int {
	if s.p == nil {
		return 0
	}
	return s.p.pid()
}

// typeResume types line and a carriage return into the session's pty, once the
// shell has printed its first output or resumeDelay has passed, whichever comes
// first (spec 60 REQ-WR-022). It runs in its own goroutine and types only once.
func (s *session) typeResume(line string) {
	if line == "" {
		return
	}
	deadline := time.Now().Add(resumeDelay)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		exited, printed := s.exited, !s.lastOutput.IsZero()
		s.mu.Unlock()
		if exited || printed {
			break
		}
		time.Sleep(resumePoll)
	}
	s.mu.Lock()
	p, exited := s.p, s.exited
	s.mu.Unlock()
	if exited || p == nil {
		return
	}
	_, _ = p.write([]byte(line + "\r"))
}

// onProgress records the OSC 9;4 progress report, e.g. "4;0;100". It is called
// from the emulator while the session lock is held.
func (s *session) onProgress(progress string) { s.oscProgress = progress }

// onWorkingDirectory records the OSC 7 working directory report, with the time
// it arrived. It is called from the emulator while the session lock is held
// (spec 55 A4).
func (s *session) onWorkingDirectory(dir string) { s.oscDir, s.oscAt = dir, time.Now() }

// sessionEnv is the session's environment: the core's, with the entries a
// terminal sets and any extra entries the caller asked for.
func sessionEnv(id string, extra []string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		// A core started from inside a Claude Code session inherits its child
		// markers; a terminal must start clean, like a new window would.
		if k == "TERM" || k == "COLORTERM" || k == "LANA_WORKBENCH_SESSION" ||
			k == "LANA_WORKBENCH_TTY" || k == envWorkbenchHome || k == "CLAUDECODE" || strings.HasPrefix(k, "CLAUDE_CODE_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANA_WORKBENCH_SESSION="+id,
	)
	// Spec 83 REQ-WAG-001: a hook running under an account HOME finds the
	// core's Lana home here.
	if home := coreHome(); home != "" {
		env = append(env, envWorkbenchHome+"="+home)
	}
	env = append(env, extra...)
	// A non-login shell started from launchd has no locale: the core runs under
	// launchd, which sets none, and a non-login shell skips /etc/zprofile,
	// where macOS would set LANG. zsh then counts a multibyte prompt glyph in
	// bytes, measures it too wide and redraws zle at the wrong column. C.UTF-8
	// matches what /etc/zprofile sets and exists on modern Linux.
	if !envHasValue(env, "LANG") && !envHasValue(env, "LC_ALL") && !envHasValue(env, "LC_CTYPE") {
		env = append(env, "LANG=C.UTF-8")
	}
	return env
}

// envHasValue reports whether env gives key a non-empty value, a later entry
// winning over an earlier one the way the OS reads the list.
func envHasValue(env []string, key string) bool {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v
		}
	}
	return value != ""
}

// run reads the pty and, when the program ends, records the exit and closes
// the stream of every attachment.
func (s *session) run(p *pty) {
	done := s.done
	readDone := make(chan struct{})
	go func() {
		s.read(p)
		close(readDone)
	}()
	go func() {
		code := p.wait()
		// What the program printed last is still to read; then hang up
		// whatever else holds the terminal.
		select {
		case <-readDone:
		case <-time.After(150 * time.Millisecond):
			_ = p.close()
			<-readDone
		}
		s.mu.Lock()
		s.exited, s.exitCode = true, code
		s.emu.close()
		if s.controls == 0 {
			s.bellNeeds = true
		}
		for a := range s.clients {
			s.removeClientLocked(a)
		}
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify()
		}
		close(done)
	}()
}

func (s *session) read(p *pty) {
	buf := make([]byte, 64<<10)
	for {
		n, err := p.read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			s.ring.write(data)
			s.emu.write(data)
			s.lastOutput = time.Now()
			s.scanBell(data)
			s.fanoutLocked(data)
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// oscTitleMax caps the OSC payload the title parser keeps. A longer payload is
// dropped, and the parser keeps scanning for the terminator.
const oscTitleMax = 1024

// scanBell reads the terminal control stream as it arrives. It records the
// sticky bell source when data carries a terminal bell that is not the
// terminator of an OSC string (REQ-WB-012), and it records the window title an
// OSC 0 or OSC 2 string carries, which the emulator cannot decode whole when a
// title is split across reads (spec 55 A5). Its parser state, including the OSC
// payload being accumulated, is kept across chunks, and the caller holds the
// session lock.
func (s *session) scanBell(data []byte) {
	for _, b := range data {
		switch {
		case s.bellOsc && s.bellEsc:
			// Inside an OSC string, after ESC: ESC \ ends it.
			if b == '\\' {
				s.bellOsc, s.bellEsc = false, false
				s.finishOSC()
			} else {
				// The ESC was payload, not a terminator.
				s.bellEsc = false
			}
		case s.bellOsc:
			// Inside an OSC string: BEL and ESC \ both end it, and a BEL here
			// is its terminator, not a bell.
			switch b {
			case '\a':
				s.bellOsc = false
				s.finishOSC()
			case 0x1b:
				s.bellEsc = true
			default:
				s.appendOSC(b)
			}
		case s.bellEsc:
			// After ESC: "ESC ]" starts an OSC string; anything else is not
			// one, so a BEL here is a real bell.
			switch b {
			case ']':
				s.bellOsc, s.bellEsc, s.oscBuf = true, false, s.oscBuf[:0]
			case '\a':
				s.bellEsc = false
				s.ringBell()
			default:
				s.bellEsc = b == 0x1b
			}
		default:
			switch b {
			case 0x1b:
				s.bellEsc = true
			case '\a':
				s.ringBell()
			}
		}
	}
}

// ringBell records a terminal bell in the sticky bell source. An agent's state
// comes from its screen, so its bell is parsed but not used: recording it would
// make the bell stay ignored only until the agent leaves, when it would surface
// as if it were a shell's (spec 55 A6). The caller holds the session lock.
func (s *session) ringBell() {
	if s.agent == "" {
		s.bellNeeds = true
	}
}

// appendOSC adds one byte to the OSC payload being accumulated, dropping
// anything past the cap.
func (s *session) appendOSC(b byte) {
	if len(s.oscBuf) < oscTitleMax {
		s.oscBuf = append(s.oscBuf, b)
	}
}

// finishOSC records the OSC string that just ended. An OSC 0 or OSC 2 payload
// is the window title with the leading "0;" or "2;" dropped, decoded leniently
// so a title split mid-rune is not dropped; any other OSC string is ignored.
func (s *session) finishOSC() {
	payload := string(s.oscBuf)
	s.oscBuf = s.oscBuf[:0]
	title, ok := strings.CutPrefix(payload, "0;")
	if !ok {
		if title, ok = strings.CutPrefix(payload, "2;"); !ok {
			return
		}
	}
	s.oscTitle = strings.ToValidUTF8(title, "")
}

// fanoutLocked queues data for every attachment; one that is too far behind
// is dropped.
func (s *session) fanoutLocked(data []byte) {
	for a := range s.clients {
		if !a.send(data) {
			s.dropClientLocked(a)
		}
	}
}

// redrawNudgeDelay is how long an attach leaves the pty one row short before
// restoring it (REQ-WB-022). The wait is what makes the nudge work: a program
// that redraws only when its SIGWINCH handler reads a changed size (Node, e.g.
// Claude Code) sees the shorter size on the first signal, and the restore a
// moment later is a second change it can redraw from. It is a variable so tests
// can shorten the wait.
var redrawNudgeDelay = 150 * time.Millisecond

// attach turns a connection into a terminal stream: it gets the ring buffer,
// then live output, and what it writes reaches the pty in control mode only.
func (s *session) attach(conn net.Conn, mode string, cols, rows int, initial []byte) {
	a := &attached{conn: conn, out: make(chan []byte, attachQueue), mode: mode}
	s.mu.Lock()
	p := s.p
	if cols > 0 && rows > 0 {
		s.cols, s.rows = cols, rows
		s.ring.mark(cols, rows)
		if s.emu != nil {
			s.emu.resize(cols, rows)
		}
	}
	if mode == modeControl {
		s.bellNeeds = false
	}
	a.out <- s.ring.snapshot()
	exited := s.exited
	if exited {
		a.finish()
	} else {
		if mode == modeControl {
			s.controls++
		}
		s.clients[a] = struct{}{}
	}
	width, height := s.cols, s.rows
	s.mu.Unlock()

	go a.writer()
	if exited {
		return
	}
	if cols > 0 && rows > 0 {
		_ = p.resize(cols, rows)
	}
	if mode == modeControl {
		// REQ-WB-022: force a full-screen program to redraw for this client.
		// The size is nudged one row short and restored a moment later, off
		// the input loop. A program that redraws only when a SIGWINCH handler
		// sees the size change (Node, e.g. Claude Code) would miss two
		// back-to-back resizes, since it reads the same size again by the time
		// it runs.
		if height > 1 {
			_ = p.resize(width, height-1)
			s.mu.Lock()
			s.ring.mark(width, height-1)
			s.mu.Unlock()
			time.AfterFunc(redrawNudgeDelay, func() {
				s.mu.Lock()
				cols, rows, exited, cur := s.cols, s.rows, s.exited, s.p
				s.mu.Unlock()
				// Restore to whatever the session size is now, so a resize
				// another client made in the meantime wins; skip it when the
				// session ended or the program restarted on a new pty, whose
				// size is already the session size.
				if !exited && cur == p {
					_ = cur.resize(cols, rows)
					s.mu.Lock()
					s.ring.mark(cols, rows)
					s.mu.Unlock()
				}
			})
		}
		if len(initial) > 0 {
			s.writeInput(p, initial)
		}
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				s.writeInput(p, buf[:n])
			}
			if err != nil {
				break
			}
		}
	} else {
		// REQ-WB-021: view mode drops the client's input.
		_, _ = io.Copy(io.Discard, conn)
	}
	s.mu.Lock()
	s.removeClientLocked(a)
	s.mu.Unlock()
}

// writeInput writes typed bytes to the pty unless the program has exited.
func (s *session) writeInput(p *pty, b []byte) {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if !exited {
		_, _ = p.write(b)
	}
}

// removeClientLocked detaches a and lets its writer flush what is queued.
func (s *session) removeClientLocked(a *attached) {
	if _, ok := s.clients[a]; !ok {
		return
	}
	delete(s.clients, a)
	if a.mode == modeControl {
		s.controls--
	}
	a.finish()
}

// dropClientLocked detaches a and closes its connection now.
func (s *session) dropClientLocked(a *attached) {
	if _, ok := s.clients[a]; !ok {
		return
	}
	delete(s.clients, a)
	if a.mode == modeControl {
		s.controls--
	}
	a.drop()
}

// resize sets the window size, which the core keeps per session.
func (s *session) resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.ring.mark(cols, rows)
	if s.emu != nil {
		s.emu.resize(cols, rows)
	}
	p, exited := s.p, s.exited
	s.mu.Unlock()
	if !exited {
		_ = p.resize(cols, rows)
	}
}

// kill hangs up a running session and waits for it to end.
func (s *session) kill() {
	s.mu.Lock()
	exited, done, p := s.exited, s.done, s.p
	s.mu.Unlock()
	if exited {
		return
	}
	p.hangup()
	select {
	case <-done:
	case <-time.After(killGrace):
		p.kill()
		select {
		case <-done:
		case <-time.After(killGrace):
		}
	}
}

// restart runs the same command, dir and env again under the same id.
func (s *session) restart() error {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if !exited {
		s.kill()
	}
	s.mu.Lock()
	path, argv, dir, env, cols, rows := s.path, s.command, s.dir, s.env, s.cols, s.rows
	s.mu.Unlock()
	p, err := startPTY(path, argv, dir, env, cols, rows)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.p = p
	s.exited, s.exitCode, s.bellNeeds = false, 0, false
	s.ring = newRing(ringSize)
	s.ring.mark(cols, rows)
	if s.emu != nil {
		s.emu.close()
	}
	s.emu = newScreen(cols, rows, s.onProgress, s.onWorkingDirectory)
	s.lastOutput = time.Time{}
	s.oscTitle, s.oscProgress, s.oscBuf = "", "", nil
	s.oscDir, s.oscAt = "", time.Time{}
	s.cwd, s.procCwd, s.procAt, s.cwdRead = "", physicalDir(dir), time.Time{}, time.Time{}
	s.agent, s.agentState = "", ""
	// A restart is a fresh run, not a restore, so the restored markers go away
	// (spec 60 REQ-WR-024, REQ-WR-025).
	s.restored, s.resumedAgent = time.Time{}, ""
	s.bellOsc, s.bellEsc = false, false
	s.done = make(chan struct{})
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	s.run(p)
	return nil
}

// refreshProgram reads the pty's foreground job and records the program and
// working directory (REQ-WB-013), keeping the started program's name when the
// read fails. When a coding agent runs there it also identifies it and derives
// its state from the screen (spec 55 A3).
func (s *session) refreshProgram() {
	s.mu.Lock()
	exited, p, fallback := s.exited, s.p, s.program
	s.mu.Unlock()
	if exited {
		return
	}
	program, cwd, ok := foreground(p.fd(), p.pid())
	if !ok {
		program, cwd = filepath.Base(fallback), ""
	}
	var label, state string
	var agentPID int
	var skip bool
	if job, jobOK := foregroundJob(p.fd()); jobOK {
		if identified, pid, agentOK := agentdetect.Identify(job); agentOK {
			label = identified
			agentPID = pid
			program = identified
			state, skip = s.detectAgent(identified)
		}
	}
	now := time.Now()
	// The conversation-id lookup reads files, so it runs outside the session
	// lock: the fields it needs are snapshotted here and its result applied
	// below (spec 60 REQ-WR-010).
	var (
		lookup    bool
		lookupPID int
		query     agentQuery
	)
	s.mu.Lock()
	if !s.exited {
		s.program = program
		s.mergeCwdLocked(cwd)
		switch label {
		case "":
			// The foreground program is no longer an agent, so its screen
			// state goes away and any agent-derived needs-you goes with it; a
			// bell from before stays ignored (spec 55 A6).
			s.agent, s.agentState = "", ""
			// The persisted agent fields outlive that, then expire (spec 60
			// REQ-WR-005).
			s.expireAgentLocked(now)
		default:
			s.agent = label
			// A screen that only shows an agent-owned viewer keeps the last
			// state it reported.
			if !skip {
				s.agentState = state
			}
			s.rAgent, s.agentSeen = label, now
			if agentPID != s.agentPID {
				s.agentPID = agentPID
				s.agentStarted = now.Add(-agentStartSkew)
				s.agentSession, s.agentCwd, s.agentConfigDir = "", "", ""
				s.agentLookupAt = time.Time{}
			}
			_, hasAdapter := adapterFor(label)
			if hasAdapter && s.agentSession == "" && now.Sub(s.agentLookupAt) >= agentLookupInterval {
				s.agentLookupAt = now
				lookup, lookupPID = true, s.agentPID
				home, _ := os.UserHomeDir()
				query = agentQuery{
					Home: home, Env: s.env, PID: s.agentPID,
					Cwd: s.cwd, Started: s.agentStarted,
					SessionID: s.id, LanaHome: coreHome(),
				}
			}
		}
	}
	s.mu.Unlock()
	if !lookup {
		return
	}
	// The agent's own environment is read outside the session lock, like the
	// lookup itself, so a cswap pane's CLAUDE_CONFIG_DIR is found even though
	// the session environment does not carry it (spec 60 addendum).
	query.ProcEnv = processEnv(lookupPID)
	found, ok := lookupAgentSession(label, query)
	if !ok {
		return
	}
	s.mu.Lock()
	if !s.exited && s.rAgent == label && s.agentPID == lookupPID {
		s.agentSession = found.ID
		// The adapter's directory is authoritative; an empty one keeps the
		// directory the core already knows.
		dir := found.Dir
		if dir == "" {
			dir = s.cwd
		}
		s.agentCwd, s.agentConfigDir = dir, found.ConfigDir
	}
	s.mu.Unlock()
}

// expireAgentLocked clears the persisted agent fields once an agent has been
// out of the foreground for agentPersistTTL, so an agent quit right before a
// reboot is offered back but an old one is not (spec 60 REQ-WR-005). The
// session lock is held.
func (s *session) expireAgentLocked(now time.Time) {
	if s.rAgent == "" || now.Sub(s.agentSeen) <= agentPersistTTL {
		return
	}
	s.rAgent, s.agentSession, s.agentCwd, s.agentConfigDir = "", "", "", ""
	s.agentSeen = time.Time{}
	s.agentPID, s.agentStarted, s.agentLookupAt = 0, time.Time{}, time.Time{}
}

// physicalDir resolves dir's symlinks, the form the OS reports a process's
// working directory in. A session seeds its process reading with this, so the
// first read is not mistaken for a change when the start directory is a symlink
// (macOS /tmp is /private/tmp).
func physicalDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// cwdReadInterval is the shortest gap between two reads of a session's process
// working directory. refreshProgram runs every other hub tick, so about once a
// second; the gate is one tick wide, which bounds a faster caller without timer
// jitter ever skipping a read (spec 55 A4).
const cwdReadInterval = updateInterval

// mergeCwdLocked folds a process working-directory read into the reported
// working directory (spec 55 A4). An OSC 7 report is authoritative until the
// process changes to another directory after it arrived; a process read that
// changed after the report wins over it. A read that repeats the last one is
// not a change, so a foreground process that stays put does not override the
// shell's OSC 7. The session lock is held.
func (s *session) mergeCwdLocked(procCwd string) {
	if time.Since(s.cwdRead) >= cwdReadInterval {
		s.cwdRead = time.Now()
		if procCwd != "" && procCwd != s.procCwd {
			s.procCwd, s.procAt = procCwd, s.cwdRead
		}
	}
	switch {
	case s.oscDir == "":
		if s.procCwd != "" {
			s.cwd = s.procCwd
		}
	case s.procAt.After(s.oscAt):
		s.cwd = s.procCwd
	default:
		s.cwd = s.oscDir
	}
}

// detectAgent renders the screen and runs the manifest detector for label. It
// leaves the session lock as it found it.
func (s *session) detectAgent(label string) (state string, skip bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited || s.emu == nil {
		return "", false
	}
	detection := agentdetect.Detect(label, agentdetect.Input{
		Screen:      s.emu.text(),
		OSCTitle:    s.oscTitle,
		OSCProgress: s.oscProgress,
	})
	return detection.State, detection.Skip
}

// info reports the session as it is now.
func (s *session) info() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "idle"
	switch {
	case s.exited:
		state = "exited"
	case s.agent != "":
		// A running agent's state comes from its screen, not from output
		// recency (spec 55 A3).
		if s.agentState == agentdetect.StateWorking {
			state = "working"
		}
	case time.Since(s.lastOutput) < workingWindow && !s.lastOutput.IsZero():
		state = "working"
	}
	title := s.title
	if title == "" {
		// The caller gave no title, so the terminal's window title is used.
		title = s.oscTitle
	}
	out := Session{
		ID: s.id, Title: title, Command: s.command, Dir: s.dir, Labels: s.labels,
		PID: s.pid(), Created: s.created, Cols: s.cols, Rows: s.rows,
		Program: s.program, Cwd: s.cwd, State: state, ExitCode: s.exitCode,
		NeedsYou: s.needsYouLocked(), LastOutput: s.lastOutput, Attached: len(s.clients),
		Agent: s.agent, AgentState: s.agentState,
	}
	// The restored markers are reported only while the program is running; they
	// go away when it exits or is restarted (spec 60 REQ-WR-024).
	if !s.exited {
		if !s.restored.IsZero() {
			restored := s.restored
			out.Restored = &restored
		}
		out.ResumedAgent = s.resumedAgent
	}
	out.AccountSwitch, out.ConversationAgent, out.ConversationID, out.AgentConfigDir = s.switchStateLocked()
	return out
}

// needsYouLocked reports the session's "needs you" (spec 55 A6). An agent's
// state comes from its screen: it needs you while it is blocked, live and not
// sticky, and its bell is ignored. An agent that exits with no control client
// attached is real attention, like a shell's, until a control attach clears it.
// A shell or other program keeps the sticky bell-or-exit source. The session
// lock is held.
func (s *session) needsYouLocked() bool {
	if s.agent != "" {
		if s.agentState == agentdetect.StateBlocked {
			return true
		}
		return s.exited && s.bellNeeds
	}
	return s.bellNeeds
}

package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// server owns the sessions of one core.
type server struct {
	home    string
	started time.Time

	mu       sync.Mutex
	sessions map[string]*session
	order    []string
	layout   json.RawMessage

	// persistLast is the bytes last written to sessions.json; a snapshot equal
	// to it is not written again. persistOff freezes the persister once the
	// final flush is done (spec 60 REQ-WR-002, REQ-WR-004).
	persistMu   sync.Mutex
	persistLast []byte
	persistOff  bool

	hub      *hub
	quit     chan struct{}
	quitOnce sync.Once
}

// Serve runs the core until it is shut down (REQ-WB-001). It refuses to start
// when another core already answers on the socket, and it handles SIGTERM and
// SIGINT by killing every session before it returns.
func Serve(ctx context.Context, home string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if c, err := Dial(ctx, home); err == nil {
		_ = c.Close()
		return errors.New("workbench: a core is already running")
	}
	sock := sockPath(home)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = os.Remove(sock)
		return err
	}
	pidPath := filepath.Join(dir, "core.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		_ = os.Remove(sock)
		return err
	}
	defer func() {
		_ = os.Remove(sock)
		_ = os.Remove(pidPath)
	}()

	s := &server{home: home, started: time.Now(), sessions: map[string]*session{}, quit: make(chan struct{}), hub: newHub()}
	if b, err := os.ReadFile(filepath.Join(dir, "layout.json")); err == nil && json.Valid(b) {
		s.layout = b
	}
	// Bring the previous core's sessions back before the socket accepts, so the
	// first list already shows them (spec 60 REQ-WR-020 .. 022, REQ-WR-024).
	s.restore()
	go s.hub.run(s.quit)
	go s.runPersister(s.quit)
	go func() {
		<-ctx.Done()
		s.shutdown()
	}()
	return s.serve(ln)
}

// restore brings back the sessions the previous core left in sessions.json. A
// record that cannot be started is logged and comes back exited with code -1;
// a failure never stops the core (spec 60 REQ-WR-020 .. 022).
func (s *server) restore() {
	recs, err := readRecords(s.home)
	if err != nil {
		logf("%v", err)
		return
	}
	for _, rec := range recs {
		ss, line, err := s.restoreOne(rec)
		if err != nil {
			logf("restore %s: %v", rec.ID, err)
			rec.Exited, rec.ExitCode = true, -1
			if ss, err = newExitedSession(rec); err != nil {
				logf("restore %s: %v", rec.ID, err)
				continue
			}
			line = ""
		}
		s.mu.Lock()
		s.sessions[rec.ID] = ss
		s.order = append(s.order, rec.ID)
		s.mu.Unlock()
		// Track it like a created session, so its state is polled and reported.
		s.hub.created(ss, ss.info())
		logf("restored session %s agent=%q resumed=%q", rec.ID, rec.Agent, line)
	}
}

// restoreOne builds the session a record comes back as. The second return is
// the resume line typed into it, "" when nothing was typed. A running record
// that cannot be started returns no session and the error, so the caller
// downgrades it to an exited session (spec 60 REQ-WR-021, REQ-WR-022).
func (s *server) restoreOne(rec sessionRecord) (*session, string, error) {
	notify := func() { s.hub.markDirty(rec.ID) }
	home, _ := os.UserHomeDir()
	switch {
	case rec.Exited:
		ss, err := newExitedSession(rec)
		return ss, "", err
	case rec.Agent != "":
		// A session that ran an agent comes back as a login shell in the
		// agent's directory, with the conversation resumed (REQ-WR-022).
		dir := firstDir(rec.AgentCwd, rec.LastDir, rec.Dir, home)
		ss, err := newSession(rec.ID, CreateOptions{
			Dir: dir, Env: rec.Env, Cols: rec.Cols, Rows: rec.Rows,
			Title: rec.Title, Labels: rec.Labels,
		}, notify)
		if err != nil {
			return nil, "", err
		}
		ss.mu.Lock()
		ss.rAgent, ss.agentSession, ss.agentCwd, ss.agentSeen =
			rec.Agent, rec.AgentSession, rec.AgentCwd, rec.AgentSeen
		ss.restored, ss.created = time.Now(), rec.Created
		ss.mu.Unlock()
		if _, hasAdapter := adapterFor(rec.Agent); !hasAdapter {
			return ss, "", nil
		}
		line, _ := resumeLine(rec.Agent, rec.AgentSession)
		ss.mu.Lock()
		ss.resumedAgent = rec.Agent
		ss.mu.Unlock()
		go ss.typeResume(line)
		return ss, line, nil
	case len(rec.Command) == 0 || isShellCommand(rec.Command[0]):
		// A shell comes back as itself in its last directory; what ran inside
		// it is not re-run (REQ-WR-022).
		dir := firstDir(rec.LastDir, rec.Dir, home)
		ss, err := newSession(rec.ID, CreateOptions{
			Command: rec.Command, Dir: dir, Env: rec.Env, Cols: rec.Cols,
			Rows: rec.Rows, Title: rec.Title, Labels: rec.Labels,
		}, notify)
		if err != nil {
			return nil, "", err
		}
		ss.mu.Lock()
		ss.rAgent, ss.agentSession, ss.agentCwd, ss.agentSeen =
			rec.Agent, rec.AgentSession, rec.AgentCwd, rec.AgentSeen
		ss.restored, ss.created = time.Now(), rec.Created
		ss.mu.Unlock()
		return ss, "", nil
	default:
		// A non-shell command is not started again; it comes back exited so the
		// owner can restart it (REQ-WR-022).
		rec.Exited, rec.ExitCode = true, -1
		ss, err := newExitedSession(rec)
		return ss, "", err
	}
}

// shellNames are the programs REQ-WR-022 restores as a shell.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"nu": true, "dash": true, "ksh": true, "tcsh": true,
}

// isShellCommand reports whether argv0 names a shell, with a leading "-" (a
// login shell) ignored.
func isShellCommand(argv0 string) bool {
	base := strings.TrimPrefix(filepath.Base(argv0), "-")
	return shellNames[base]
}

// firstDir returns the first path that is an existing directory, or "" when
// none is.
func firstDir(paths ...string) string {
	for _, p := range paths {
		if isDir(p) {
			return p
		}
	}
	return ""
}

// serve accepts connections until the core is shut down.
func (s *server) serve(ln net.Listener) error {
	defer s.closeAll()
	go func() {
		<-s.quit
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return nil
			default:
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go s.handle(conn)
	}
}

// shutdown asks the core to stop; killing the sessions happens in closeAll.
func (s *server) shutdown() { s.quitOnce.Do(func() { close(s.quit) }) }

// closeAll kills every session. It flushes one last snapshot and freezes the
// persister first, so the kills below never turn a running session into an
// exited one in the file (spec 60 REQ-WR-004).
func (s *server) closeAll() {
	s.freezePersist()
	s.mu.Lock()
	all := make([]*session, 0, len(s.order))
	for _, id := range s.order {
		if ss := s.sessions[id]; ss != nil {
			all = append(all, ss)
		}
	}
	s.mu.Unlock()
	for _, ss := range all {
		ss.kill()
	}
}

// handle serves one connection: an attach turns it into a terminal stream, and
// anything else is a control connection.
func (s *server) handle(conn net.Conn) {
	r := bufio.NewReaderSize(conn, 64<<10)
	line, err := r.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		_ = conn.Close()
		return
	}
	if req.Op == "attach" {
		s.attach(conn, r, req)
		return
	}
	if req.Op == "subscribe" {
		s.subscribe(conn, r)
		return
	}
	s.control(conn, r, line)
}

// attach answers an attach request (REQ-WB-020) and hands the connection to
// the session as a raw stream.
func (s *server) attach(conn net.Conn, r *bufio.Reader, req Request) {
	if req.Mode != modeControl && req.Mode != modeView {
		writeAttachErr(conn, fmt.Errorf("unknown attach mode %q", req.Mode))
		_ = conn.Close()
		return
	}
	ss, err := s.session(req.SID)
	if err != nil {
		writeAttachErr(conn, err)
		_ = conn.Close()
		return
	}
	if _, err := conn.Write([]byte(`{"ok":true}` + "\n")); err != nil {
		_ = conn.Close()
		return
	}
	// Whatever the reader buffered past the first line is already input.
	var initial []byte
	if n := r.Buffered(); n > 0 {
		if b, err := r.Peek(n); err == nil {
			initial = append([]byte(nil), b...)
		}
	}
	ss.attach(conn, req.Mode, req.Cols, req.Rows, initial)
}

// subscribe answers a subscribe request (REQ-WB-030) and streams events as
// JSON lines until the client goes away. The subscriber is registered before
// the OK reply, so a client that acts on it misses no event.
func (s *server) subscribe(conn net.Conn, r *bufio.Reader) {
	sub := &subscriber{conn: conn, out: make(chan Event, subscribeQueue)}
	go sub.writer()
	s.hub.add(sub)
	if _, err := conn.Write([]byte(`{"ok":true}` + "\n")); err != nil {
		s.hub.remove(sub)
		return
	}
	// Reading the connection is how a client that went away is noticed.
	_, _ = io.Copy(io.Discard, r)
	s.hub.remove(sub)
}

// control serves a control connection: one request per line.
func (s *server) control(conn net.Conn, r *bufio.Reader, first []byte) {
	defer func() { _ = conn.Close() }()
	var wmu sync.Mutex
	reply := func(res Response) {
		b, _ := json.Marshal(res)
		wmu.Lock()
		_, _ = conn.Write(append(b, '\n'))
		wmu.Unlock()
	}
	line := first
	for {
		var req Request
		if err := json.Unmarshal(line, &req); err == nil && req.Op != "" {
			if req.Op == "kill" {
				// A kill waits for the process; answer it aside.
				go func() { reply(s.request(req)) }()
			} else {
				reply(s.request(req))
			}
		}
		var err error
		line, err = r.ReadBytes('\n')
		if err != nil {
			return
		}
	}
}

func (s *server) request(req Request) Response {
	data, err := s.do(req)
	res := Response{ID: req.ID}
	if err != nil {
		res.Error, res.Code = err.Error(), codeFor(err)
	} else if data != nil {
		res.Data, _ = json.Marshal(data)
	}
	return res
}

// session finds a session by id, or ErrNotFound.
func (s *server) session(id string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil {
		return ss, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
}

func (s *server) do(req Request) (any, error) {
	switch req.Op {
	case "hello":
		return Hello{Version: ProtocolVersion, PID: os.Getpid(), Started: s.started}, nil
	case "list":
		s.mu.Lock()
		all := make([]*session, 0, len(s.order))
		for _, id := range s.order {
			if ss := s.sessions[id]; ss != nil {
				all = append(all, ss)
			}
		}
		s.mu.Unlock()
		list := make([]Session, len(all))
		for i, ss := range all {
			list[i] = ss.info()
		}
		return list, nil
	case "get":
		ss, err := s.session(req.SID)
		if err != nil {
			return nil, err
		}
		return ss.info(), nil
	case "create":
		o := CreateOptions{}
		if req.Create != nil {
			o = *req.Create
		}
		id := newID()
		ss, err := newSession(id, o, func() { s.hub.markDirty(id) })
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.sessions[id] = ss
		s.order = append(s.order, id)
		s.mu.Unlock()
		info := ss.info()
		s.hub.created(ss, info)
		return info, nil
	case "kill":
		ss, err := s.session(req.SID)
		if err != nil {
			return nil, err
		}
		ss.kill()
		s.mu.Lock()
		delete(s.sessions, req.SID)
		s.order = slices.DeleteFunc(s.order, func(id string) bool { return id == req.SID })
		s.mu.Unlock()
		s.hub.removed(ss.id, ss.info())
		return nil, nil
	case "restart":
		ss, err := s.session(req.SID)
		if err != nil {
			return nil, err
		}
		if err := ss.restart(); err != nil {
			return nil, err
		}
		return ss.info(), nil
	case "resize":
		ss, err := s.session(req.SID)
		if err != nil {
			return nil, err
		}
		ss.resize(req.Cols, req.Rows)
		return nil, nil
	case "layout.get":
		s.mu.Lock()
		l := s.layout
		s.mu.Unlock()
		if len(l) == 0 {
			return json.RawMessage("{}"), nil
		}
		return l, nil
	case "layout.set":
		return nil, s.setLayout(req.Layout)
	case "shutdown":
		time.AfterFunc(30*time.Millisecond, s.shutdown)
		return nil, nil
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

// setLayout stores the opaque layout document (REQ-WB-031) and reports it.
func (s *server) setLayout(layout json.RawMessage) error {
	if !json.Valid(layout) {
		return errors.New("layout is not valid JSON")
	}
	if len(layout) > layoutMax {
		return fmt.Errorf("layout is larger than %d bytes", layoutMax)
	}
	doc := bytes.Clone(layout)
	if err := writeLayout(Dir(s.home), doc); err != nil {
		return err
	}
	s.mu.Lock()
	s.layout = doc
	s.mu.Unlock()
	s.hub.layout(doc)
	return nil
}

// writeLayout replaces layout.json atomically.
func writeLayout(dir string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".layout-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "layout.json"))
}

// writeAttachErr writes the answer to an attach that could not be served.
func writeAttachErr(conn net.Conn, err error) {
	b, _ := json.Marshal(attachResult{Error: err.Error(), Code: codeFor(err)})
	_, _ = conn.Write(append(b, '\n'))
}

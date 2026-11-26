package workbench

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Client is a control connection to the core, safe for concurrent use.
type Client struct {
	conn   net.Conn
	socket string

	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan Response
	err     error
	closed  chan struct{}
	// hello is the core's last Hello answer, nil until one is read.
	hello *Hello
}

// Dial connects to a running core (REQ-WB-002); it does not start one.
func Dial(ctx context.Context, home string) (*Client, error) {
	socket := sockPath(home)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, socket: socket, pending: map[int64]chan Response{}, closed: make(chan struct{})}
	go c.read()
	return c, nil
}

// startCore starts a detached core process; tests replace it.
var startCore = spawnCore

// Ensure returns a client to the core, starting one when none answers
// (REQ-WB-002). It checks the protocol version before returning it.
func Ensure(ctx context.Context, home string) (*Client, error) {
	if c, err := Dial(ctx, home); err == nil {
		if err := checkVersion(ctx, c); err != nil {
			_ = c.Close()
			return nil, err
		}
		return c, nil
	}
	// No listener: a stale socket file may be in the way.
	_ = os.Remove(sockPath(home))
	if err := startCore(home); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(coreStartTimeout)
	for {
		c, err := Dial(ctx, home)
		if err == nil {
			if err := checkVersion(ctx, c); err != nil {
				_ = c.Close()
				return nil, err
			}
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("workbench: starting the core: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
}

// checkVersion reads hello and refuses another protocol version (REQ-WB-004).
func checkVersion(ctx context.Context, c *Client) error {
	h, err := c.Hello(ctx)
	if err != nil {
		return err
	}
	if h.Version != ProtocolVersion {
		return fmt.Errorf("%w: core speaks %d, client %d", ErrVersionMismatch, h.Version, ProtocolVersion)
	}
	return nil
}

// spawnCore starts "<lana> workbench serve" in a session of its own, which
// outlives the caller, with its output appended to core.log.
func spawnCore(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "core.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { _ = devnull.Close() }()
	cmd := exec.Command(exe, "workbench", "serve")
	cmd.Env = append(os.Environ(), "LANA_HOME="+home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (c *Client) read() {
	r := bufio.NewReaderSize(c.conn, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			c.mu.Lock()
			if c.err == nil {
				c.err = err
			}
			for id, ch := range c.pending {
				ch <- Response{ID: id, Error: "connection to the workbench core lost"}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			close(c.closed)
			return
		}
		var res Response
		if json.Unmarshal(line, &res) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[res.ID]
		delete(c.pending, res.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
}

// call sends one request and decodes the response's data into out.
func (c *Client) call(ctx context.Context, req Request, out any) error {
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	c.mu.Unlock()

	b, err := json.Marshal(req)
	if err != nil {
		c.forget(req.ID)
		return err
	}
	c.wmu.Lock()
	_, err = c.conn.Write(append(b, '\n'))
	c.wmu.Unlock()
	if err != nil {
		c.forget(req.ID)
		return err
	}

	timer := time.NewTimer(callTimeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		if err := errorFor(res); err != nil {
			return err
		}
		if out != nil && len(res.Data) > 0 {
			return json.Unmarshal(res.Data, out)
		}
		return nil
	case <-timer.C:
		c.forget(req.ID)
		return fmt.Errorf("workbench: %s timed out", req.Op)
	case <-ctx.Done():
		c.forget(req.ID)
		return ctx.Err()
	case <-c.closed:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return err
	}
}

func (c *Client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Hello returns the core's version, pid and start time.
func (c *Client) Hello(ctx context.Context) (h Hello, err error) {
	err = c.call(ctx, Request{Op: "hello"}, &h)
	if err == nil {
		c.mu.Lock()
		c.hello = &h
		c.mu.Unlock()
	}
	return
}

// HasFeature reports whether the core advertises feature in its Hello,
// reading the Hello once when the client has none yet.
func (c *Client) HasFeature(ctx context.Context, feature string) (bool, error) {
	c.mu.Lock()
	h := c.hello
	c.mu.Unlock()
	if h == nil {
		got, err := c.Hello(ctx)
		if err != nil {
			return false, err
		}
		h = &got
	}
	return slices.Contains(h.Features, feature), nil
}

// List returns every session, in creation order.
func (c *Client) List(ctx context.Context) (list []Session, err error) {
	err = c.call(ctx, Request{Op: "list"}, &list)
	return
}

// Get returns one session, or ErrNotFound.
func (c *Client) Get(ctx context.Context, id string) (s Session, err error) {
	err = c.call(ctx, Request{Op: "get", SID: id}, &s)
	return
}

// Create starts a session.
//
// A core started by an older app keeps running until it is empty and drops an
// Input it does not know, so against a core without FeatureCreateInput the
// input runs under `/bin/sh -c` and is followed by the user's shell instead.
func (c *Client) Create(ctx context.Context, opts CreateOptions) (s Session, err error) {
	if opts.Input != "" {
		ok, err := c.HasFeature(ctx, FeatureCreateInput)
		if err != nil {
			return Session{}, err
		}
		if !ok {
			opts = legacyInputOptions(opts)
		}
	}
	err = c.call(ctx, Request{Op: "create", Create: &opts}, &s)
	return
}

// Kill removes a session, hanging up a running one first.
func (c *Client) Kill(ctx context.Context, id string) error {
	return c.call(ctx, Request{Op: "kill", SID: id}, nil)
}

// Restart runs a session's command again under the same id.
func (c *Client) Restart(ctx context.Context, id string) (s Session, err error) {
	err = c.call(ctx, Request{Op: "restart", SID: id}, &s)
	return
}

// SwitchAccount continues a pane's conversation of agent ("claude" or "agy")
// on the account of configDir, "" for the built-in account (spec 82
// REQ-WAS-002, spec 83 REQ-WAG-003). A core without
// FeatureSwitchAccount gets no request: the answer is ErrCoreOutdated. The
// core's refusals come back as ErrNoConversation, ErrNotSwitchable and
// ErrStillRunning.
func (c *Client) SwitchAccount(ctx context.Context, id, agent, configDir string) (s Session, err error) {
	ok, err := c.HasFeature(ctx, FeatureSwitchAccount)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, ErrCoreOutdated
	}
	err = c.call(ctx, Request{Op: "switch_account", SID: id, Switch: &SwitchOptions{Agent: agent, ConfigDir: configDir}}, &s)
	return
}

// Resize sets a session's window size.
func (c *Client) Resize(ctx context.Context, id string, cols, rows int) error {
	return c.call(ctx, Request{Op: "resize", SID: id, Cols: cols, Rows: rows}, nil)
}

// Layout returns the layout document, or "{}" when there is none.
func (c *Client) Layout(ctx context.Context) (l json.RawMessage, err error) {
	err = c.call(ctx, Request{Op: "layout.get"}, &l)
	return
}

// SetLayout replaces the layout document; it must be valid JSON of at most
// 256 KiB.
func (c *Client) SetLayout(ctx context.Context, layout json.RawMessage) error {
	if len(layout) > layoutMax {
		return fmt.Errorf("workbench: layout is larger than %d bytes", layoutMax)
	}
	return c.call(ctx, Request{Op: "layout.set", Layout: layout}, nil)
}

// Attach opens a raw stream to a session on its own connection. mode is
// "control" or "view"; the stream carries the ring buffer, then live output.
func (c *Client) Attach(ctx context.Context, id, mode string) (io.ReadWriteCloser, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(Request{Op: "attach", SID: id, Mode: mode})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := readAttachResult(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// Subscribe opens its own connection and streams events until ctx is done or
// the core goes away; the channel is closed then.
func (c *Client) Subscribe(ctx context.Context) (<-chan Event, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(Request{Op: "subscribe"})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := readAttachResult(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	events := make(chan Event)
	go func() {
		defer close(events)
		defer func() { _ = conn.Close() }()
		stop := make(chan struct{})
		defer close(stop)
		// Closing the connection on ctx done unblocks a pending read.
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-stop:
			}
		}()
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var ev Event
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

// readAttachResult reads the one-line answer, a byte at a time so it reads
// nothing of the terminal stream that follows it.
func readAttachResult(conn net.Conn) error {
	var line []byte
	b := make([]byte, 1)
	for {
		if _, err := conn.Read(b); err != nil {
			return err
		}
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
		if len(line) > 4096 {
			return errors.New("workbench: bad attach reply")
		}
	}
	var res attachResult
	if err := json.Unmarshal(line, &res); err != nil {
		return err
	}
	if !res.OK {
		if kind := kindFor(res.Code); kind != nil {
			return &codeError{msg: res.Error, kind: kind}
		}
		return errors.New(res.Error)
	}
	return nil
}

// Shutdown asks the core to stop, killing every session.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.call(ctx, Request{Op: "shutdown"}, nil)
}

// Close closes the control connection.
func (c *Client) Close() error { return c.conn.Close() }

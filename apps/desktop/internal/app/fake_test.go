package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
)

// errNotAttached is what the fake connection answers for an attach: the
// window test needs no real terminal.
var errNotAttached = errors.New("the fake connection does not attach")

// fakeConn is a Conn that needs no daemon: it answers from its fields and
// records what the app asked of it.
type fakeConn struct {
	mu       sync.Mutex
	base     string
	sessions []lana.Session
	layout   json.RawMessage
	puts     []json.RawMessage
	creates  []lana.CreateOptions
	killed   []string

	restarted []string
	// previewFile and previewErr are the answer the fake gives a Preview, and
	// previews records what the app asked of it.
	previewFile lana.PreviewFile
	previewErr  error
	previews    []previewCall
}

// previewCall is one Preview the app asked of the fake connection.
type previewCall struct {
	session string
	link    string
	dir     string
}

var _ Conn = (*fakeConn)(nil)

func (f *fakeConn) BaseURL() string {
	if f.base != "" {
		return f.base
	}
	return "http://127.0.0.1:18300"
}

func (f *fakeConn) ListSessions(context.Context) ([]lana.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lana.Session{}, f.sessions...), nil
}

func (f *fakeConn) CreateSession(_ context.Context, opts lana.CreateOptions) (lana.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, opts)
	return lana.Session{ID: "new", Title: "new", Dir: opts.Dir, Cols: opts.Cols, Rows: opts.Rows}, nil
}

func (f *fakeConn) KillSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, id)
	return nil
}

func (f *fakeConn) RestartSession(_ context.Context, id string) (lana.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, id)
	return lana.Session{ID: id, State: "working"}, nil
}

func (f *fakeConn) Resize(context.Context, string, int, int) error { return nil }

func (f *fakeConn) Preview(_ context.Context, session, link, dir string) (lana.PreviewFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previews = append(f.previews, previewCall{session: session, link: link, dir: dir})
	if f.previewErr != nil {
		return lana.PreviewFile{}, f.previewErr
	}
	return f.previewFile, nil
}

func (f *fakeConn) Attach(context.Context, string) (*lana.Attachment, error) {
	return nil, errNotAttached
}

func (f *fakeConn) GetLayout(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.layout) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return f.layout, nil
}

func (f *fakeConn) PutLayout(_ context.Context, document json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, append(json.RawMessage{}, document...))
	return nil
}

func (f *fakeConn) Subscribe(ctx context.Context, onEvent func(lana.Event)) error {
	<-ctx.Done()
	return nil
}

// fakeHost is a HostInfo whose values a test chooses.
type fakeHost struct {
	name   string
	chip   string
	memory int
}

func (h fakeHost) Name() string  { return h.name }
func (h fakeHost) Chip() string  { return h.chip }
func (h fakeHost) MemoryGB() int { return h.memory }

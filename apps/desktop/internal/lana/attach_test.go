package lana

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// resizeMessage is the text frame a pane sends on a size change.
type resizeMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func TestAttachFraming(t *testing.T) {
	d := newFakeDaemon(t)
	const output = "hello from the shell"
	var (
		mu       sync.Mutex
		mode     string
		input    []byte
		resize   resizeMessage
		finished = make(chan struct{})
	)
	d.attach = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mode = r.URL.Query().Get("mode")
		mu.Unlock()
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageBinary, []byte(output)); err != nil {
			return
		}
		typ, data, err := conn.Read(ctx)
		if err != nil || typ != websocket.MessageBinary {
			return
		}
		mu.Lock()
		input = data
		mu.Unlock()
		typ, data, err = conn.Read(ctx)
		if err != nil || typ != websocket.MessageText {
			return
		}
		var message resizeMessage
		if json.Unmarshal(data, &message) != nil {
			return
		}
		mu.Lock()
		resize = message
		mu.Unlock()
		close(finished)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}

	conn := d.connect(t)
	attachment, err := conn.Attach(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer attachment.Close()

	data, err := attachment.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != output {
		t.Fatalf("output = %q, want %q", data, output)
	}
	if err := attachment.Write([]byte("ls\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := attachment.Resize(120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not read the input and the resize")
	}

	mu.Lock()
	defer mu.Unlock()
	if mode != AttachModeControl {
		t.Fatalf("mode = %q, want %q", mode, AttachModeControl)
	}
	if string(input) != "ls\n" {
		t.Fatalf("input = %q, want ls\\n", input)
	}
	if resize.Type != "resize" || resize.Cols != 120 || resize.Rows != 40 {
		t.Fatalf("resize = %+v", resize)
	}
}

func TestAttachSessionNotFound(t *testing.T) {
	d := newFakeDaemon(t)
	d.attach = func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_ = conn.Close(closeCodeNotFound, "session not found")
	}

	conn := d.connect(t)
	attachment, err := conn.Attach(context.Background(), "gone")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer attachment.Close()

	if _, err := attachment.Read(); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("read error = %v, want ErrSessionNotFound", err)
	}
}

func TestAttachErrorEnvelope(t *testing.T) {
	// The daemon checks the session before the upgrade, so an unknown id is a
	// plain 404 with the error envelope.
	d := newFakeDaemon(t)
	d.attach = func(w http.ResponseWriter, _ *http.Request) {
		d.writeError(w, http.StatusNotFound, "not_found", "the workbench session does not exist")
	}

	conn := d.connect(t)
	_, err := conn.Attach(context.Background(), "gone")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "not_found" {
		t.Fatalf("attach error = %v, want a not_found APIError", err)
	}
}

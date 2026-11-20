package lana

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestConnectSignsInAndSendsHeaders(t *testing.T) {
	d := newFakeDaemon(t)
	conn := d.connect(t)

	if got := conn.BaseURL(); got != d.base() {
		t.Fatalf("BaseURL = %q, want %q", got, d.base())
	}
	if links := d.appLinkCount(); links != 1 {
		t.Fatalf("app links = %d, want 1", links)
	}

	sessions, err := conn.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %+v, want none", sessions)
	}

	// An unsafe request carries the CSRF value the sign-in read.
	if _, err := conn.CreateSession(context.Background(), CreateOptions{Title: "shell", Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("create: %v", err)
	}

	d.mu.Lock()
	origins, csrfHeaders := append([]string(nil), d.origins...), append([]string(nil), d.csrfHeaders...)
	createBody := append([]byte(nil), d.createBody...)
	d.mu.Unlock()

	for _, origin := range origins {
		if origin != d.base() {
			t.Fatalf("Origin = %q, want %q", origin, d.base())
		}
	}
	if got := csrfHeaders[len(csrfHeaders)-1]; got != d.csrfValue() {
		t.Fatalf("create CSRF = %q, want %q", got, d.csrfValue())
	}
	if !strings.Contains(string(createBody), `"title":"shell"`) {
		t.Fatalf("create body = %q, want the title", createBody)
	}
}

func TestKillAndRestart(t *testing.T) {
	// The fake daemon enforces the CSRF header, so an unsafe request that
	// forgot it would be refused.
	d := newFakeDaemon(t)
	conn := d.connect(t)
	if err := conn.KillSession(context.Background(), "sess-1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	session, err := conn.RestartSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if session.State != "running" {
		t.Fatalf("restart session = %+v", session)
	}
}

func TestExpiredSessionSignsInAgainOnce(t *testing.T) {
	d := newFakeDaemon(t)
	conn := d.connect(t)
	d.revoke()

	sessions, err := conn.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list after expiry: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %+v, want none", sessions)
	}
	if links := d.appLinkCount(); links != 2 {
		t.Fatalf("app links = %d, want 2 (one sign-in and one retry)", links)
	}
}

func TestAPIErrorEnvelope(t *testing.T) {
	d := newFakeDaemon(t)
	d.errStatus = http.StatusNotFound
	d.errCode = "not_found"
	d.errMessage = "the workbench session does not exist"
	conn := d.connect(t)

	_, err := conn.RestartSession(context.Background(), "nope")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusNotFound || apiErr.Code != "not_found" || apiErr.Message != d.errMessage {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestUnavailableIsRecognizable(t *testing.T) {
	d := newFakeDaemon(t)
	d.errStatus = http.StatusServiceUnavailable
	d.errCode = codeWorkbenchUnavailable
	d.errMessage = "the workbench core is not available"
	conn := d.connect(t)

	_, err := conn.ListSessions(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), d.errMessage) {
		t.Fatalf("error = %q, want the core's reason", err)
	}
}

func TestConnectUnreachable(t *testing.T) {
	// A port with nothing listening: the daemon is down.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	home := t.TempDir()
	writeSecret(t, home, addr, "s3cret")
	conn := NewLocal(home)
	if err := conn.Connect(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("connect error = %v, want ErrUnreachable", err)
	}
}

func TestConnectWithoutSecret(t *testing.T) {
	conn := NewLocal(t.TempDir())
	if err := conn.Connect(context.Background()); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("connect error = %v, want ErrNoSecret", err)
	}
}

func TestLayoutRoundTrip(t *testing.T) {
	d := newFakeDaemon(t)
	conn := d.connect(t)

	layout, err := conn.GetLayout(context.Background())
	if err != nil {
		t.Fatalf("get layout: %v", err)
	}
	if string(layout) != "{}" {
		t.Fatalf("empty layout = %q, want {}", layout)
	}

	// The document is sent unchanged, so unknown fields survive.
	raw := json.RawMessage(`{"version":1,"unknown":{"x":1}}`)
	if err := conn.PutLayout(context.Background(), raw); err != nil {
		t.Fatalf("put layout: %v", err)
	}
	got, err := conn.GetLayout(context.Background())
	if err != nil {
		t.Fatalf("get layout: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("layout = %q, want %q", got, raw)
	}
}

func TestResizeBody(t *testing.T) {
	d := newFakeDaemon(t)
	conn := d.connect(t)
	if err := conn.Resize(context.Background(), "sess-1", 120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	d.mu.Lock()
	body := string(d.resizeBody)
	d.mu.Unlock()
	if !strings.Contains(body, `"cols":120`) || !strings.Contains(body, `"rows":40`) {
		t.Fatalf("resize body = %q", body)
	}
}

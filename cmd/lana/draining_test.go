package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestWebDrainingRefusesTurns covers AC-TFB-004 on the web port: while the
// runtime drains, sending a message and creating a session answer
// 503 daemon_draining with the contract message (API-TFB-008).
func TestWebDrainingRefusesTurns(t *testing.T) {
	rt, home := webRuntime(t, mock.Script{})
	_, ts, cookie, csrf := webServer(t, rt, home)

	info, err := rt.CreateChannelSession(context.Background(), "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	rt.SetDraining(true)
	t.Cleanup(func() { rt.SetDraining(false) })

	resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"hi"}`, cookie, csrf)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("send status = %d, body = %q, want 503", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "daemon_draining") || !strings.Contains(string(body), "lana is restarting. Try again in a moment.") {
		t.Fatalf("send body = %q", body)
	}

	resp, body = webDo(t, http.MethodPost, ts.URL+"/v1/sessions", `{"agent":"assistant"}`, cookie, csrf)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("create session status = %d, body = %q, want 503", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "daemon_draining") {
		t.Fatalf("create session body = %q", body)
	}

	if rt.RunningTurnCount() != 0 {
		t.Fatalf("running turns = %d, want 0", rt.RunningTurnCount())
	}
}

package lana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// attachSSE serves the minimal stream that ends a turn with one text answer.
func attachSSE(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// attachRuntime builds a runtime whose agent workspace is ws.
func attachRuntime(t *testing.T, baseURL, ws string) *Runtime {
	t.Helper()
	return newTestRuntime(t, baseURL, func(cfg string) string {
		return strings.Replace(cfg, `model = "m"`, fmt.Sprintf("model = \"m\"\nworkspace = %q", ws), 1)
	})
}

// pngBytes encodes a small opaque PNG.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 1, G: 2, B: 3, A: 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// REQ-ATT-008: a path outside the workspace attachments directory is refused
// before any session or turn exists.
func TestRunRejectsAttachmentOutside(t *testing.T) {
	srv := attachSSE(t)
	ws := t.TempDir()
	rt := attachRuntime(t, srv.URL, ws)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"../x", "notes.txt"} {
		if _, err := rt.Run(ctx, Request{Prompt: "hi", Attachments: []string{path}}); err == nil {
			t.Fatalf("Run with attachment %q = nil error, want an error", path)
		}
	}
	all, err := rt.ListAllSessions(ctx, core.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("a rejected attachment created %d sessions", len(all))
	}
}

// API-ATT-006: an empty prompt with a valid attachment runs a turn and stores
// the image block.
func TestRunEmptyPromptWithAttachment(t *testing.T) {
	srv := attachSSE(t)
	ws := t.TempDir()
	rt := attachRuntime(t, srv.URL, ws)
	ctx := context.Background()
	const path = "attachments/2026-11-01/a.png"
	if err := os.MkdirAll(filepath.Join(ws, "attachments", "2026-11-01"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(path)), pngBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := rt.Run(ctx, Request{Prompt: "", Attachments: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", result.StopReason)
	}

	events, err := rt.History(ctx, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		for _, block := range payload.Content {
			if block.Kind == core.BlockImage && block.Path == path && len(block.Data) > 0 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("stored owner message has no image block")
	}
}

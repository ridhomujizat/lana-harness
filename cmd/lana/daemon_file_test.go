package main

import (
	"context"
	"errors"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-AGF-004: a telegram session's file reaches the telegram sender and no
// note is returned on success.
func TestFileRouterTelegramDelivery(t *testing.T) {
	var gotSession string
	var gotFile core.SentFile
	router := &FileRouter{
		sessionInfo: func(context.Context, string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: "telegram-session", Channel: "telegram"}, nil
		},
		telegram: func(_ context.Context, sessionID string, f core.SentFile) error {
			gotSession = sessionID
			gotFile = f
			return nil
		},
	}
	file := core.SentFile{Path: "attachments/2026-11-11/a.png", Name: "a.png", MediaType: "image/png", Size: 3}
	out := router.Send(context.Background(), core.FileSendRequest{SessionID: "telegram-session", File: file})
	if out.Note != "" {
		t.Fatalf("note=%q, want empty", out.Note)
	}
	if gotSession != "telegram-session" || gotFile.Name != "a.png" {
		t.Fatalf("session=%q file=%+v", gotSession, gotFile)
	}
}

// REQ-AGF-004: a telegram delivery error becomes the tool's note.
func TestFileRouterTelegramError(t *testing.T) {
	router := &FileRouter{
		sessionInfo: func(context.Context, string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: "telegram-session", Channel: "telegram"}, nil
		},
		telegram: func(context.Context, string, core.SentFile) error {
			return errors.New("chat is blocked")
		},
	}
	out := router.Send(context.Background(), core.FileSendRequest{SessionID: "telegram-session", File: core.SentFile{Name: "a.png"}})
	if out.Note != "Telegram delivery failed: chat is blocked" {
		t.Fatalf("note=%q", out.Note)
	}
}

// REQ-AGF-004: a web session has nothing to deliver; the web chat reads the
// file/sent event from the stream.
func TestFileRouterWebNoDelivery(t *testing.T) {
	calls := 0
	router := &FileRouter{
		sessionInfo: func(context.Context, string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: "web-session", Channel: "web"}, nil
		},
		telegram: func(context.Context, string, core.SentFile) error {
			calls++
			return nil
		},
	}
	out := router.Send(context.Background(), core.FileSendRequest{SessionID: "web-session", File: core.SentFile{Name: "a.png"}})
	if calls != 0 {
		t.Fatalf("telegram calls=%d, want 0", calls)
	}
	if out.Note != "" {
		t.Fatalf("note=%q, want empty", out.Note)
	}
}

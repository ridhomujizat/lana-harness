package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// Spec 40: the in-memory store keeps the title projection like SQLite.
func TestSetSessionTitle(t *testing.T) {
	ctx := context.Background()
	s := New()
	info, err := s.Create(ctx, core.NewSession{Agent: "a", Channel: "web", Workspace: t.TempDir(), Title: "Calendar · Standup", TitleSource: core.TitleSourceSystem})
	if err != nil || info.Title != "Calendar · Standup" || info.TitleSource != core.TitleSourceSystem {
		t.Fatalf("create = %+v err=%v", info, err)
	}
	if changed, err := s.SetSessionTitle(ctx, info.ID, "Auto", core.TitleSourceAuto, true); err != nil || changed {
		t.Fatalf("onlyIfUntitled overwrote a system title: %v %v", changed, err)
	}
	if changed, err := s.SetSessionTitle(ctx, info.ID, "", core.TitleSourceOwner, false); err != nil || !changed {
		t.Fatalf("clear: %v %v", changed, err)
	}
	if got, _ := s.Info(ctx, info.ID); got.Title != "" || got.TitleSource != "" {
		t.Fatalf("after clear: %+v", got)
	}
	if _, err := s.SetSessionTitle(ctx, "nope", "x", core.TitleSourceOwner, false); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

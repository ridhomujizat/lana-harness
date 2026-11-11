package lana

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
)

func TestChannelSessions(t *testing.T) {
	home := t.TempDir()
	cfg := "[providers.local]\nkind = \"openai_compat\"\nbase_url = \"http://127.0.0.1:1\"\n\n[agents.helper]\ntoolset = \"custom\"\nprovider = \"local\"\nmodel = \"m\"\n\n[agents.coder]\ntoolset = \"custom\"\nprovider = \"local\"\nmodel = \"m\"\nworkspace = \"" + t.TempDir() + "\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"helper\"\n"
	rt, err := New(context.Background(), Options{Home: home, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	ctx := context.Background()

	if agent, err := rt.ActiveAgent(ctx, "telegram", "42"); err != nil || agent != "" {
		t.Fatalf("active agent on empty chat = %q, %v", agent, err)
	}
	first, err := rt.ChannelSession(ctx, "telegram", "42", "helper")
	if err != nil {
		t.Fatal(err)
	}
	wantWS, _ := filepath.EvalSymlinks(filepath.Join(home, "workspaces", "helper"))
	if first.Channel != "telegram" || first.Address != "42" || first.Workspace != wantWS {
		t.Fatalf("session = %+v, want workspace %s", first, wantWS)
	}
	if info, err := os.Stat(filepath.Join(home, "workspaces", "helper")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("workspace dir = %v, %v", info, err)
	}
	again, err := rt.ChannelSession(ctx, "telegram", "42", "helper")
	if err != nil || again.ID != first.ID {
		t.Fatalf("lookup = %s, want %s (%v)", again.ID, first.ID, err)
	}
	// Sessions are ordered by updated_at in milliseconds; keep creations apart.
	time.Sleep(3 * time.Millisecond)
	coder, err := rt.ChannelSession(ctx, "telegram", "42", "coder")
	if err != nil || coder.ID == first.ID {
		t.Fatalf("coder session = %+v, %v", coder, err)
	}
	if agent, _ := rt.ActiveAgent(ctx, "telegram", "42"); agent != "coder" {
		t.Fatalf("active agent = %q, want coder", agent)
	}
	time.Sleep(3 * time.Millisecond)
	fresh, err := rt.CreateChannelSession(ctx, "telegram", "42", "helper")
	if err != nil || fresh.ID == first.ID {
		t.Fatalf("new session = %+v, %v", fresh, err)
	}
	if latest, _ := rt.ChannelSession(ctx, "telegram", "42", "helper"); latest.ID != fresh.ID {
		t.Fatalf("latest = %s, want %s", latest.ID, fresh.ID)
	}
	if _, err := rt.ChannelSession(ctx, "telegram", "42", "ghost"); err == nil {
		t.Fatal("unknown agent accepted")
	}
}

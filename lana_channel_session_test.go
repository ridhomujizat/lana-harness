package lana

import (
	"context"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
)

func newChannelTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	home := t.TempDir()
	cfg := "[providers.local]\nkind = \"openai_compat\"\nbase_url = \"http://127.0.0.1:1\"\n\n[agents.helper]\ntoolset = \"custom\"\nprovider = \"local\"\nmodel = \"m\"\n\n[agents.coder]\ntoolset = \"custom\"\nprovider = \"local\"\nmodel = \"m\"\nworkspace = \"" + t.TempDir() + "\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"helper\"\n"
	rt, err := New(context.Background(), Options{Home: home, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// TestChannelSessionConcurrentCreatesOne checks that concurrent get-or-create
// calls for one key return a single session.
func TestChannelSessionConcurrentCreatesOne(t *testing.T) {
	rt := newChannelTestRuntime(t)
	ctx := context.Background()
	const n = 10
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			info, err := rt.ChannelSession(ctx, "telegram", "7", "helper")
			if err != nil {
				t.Errorf("channel session: %v", err)
				return
			}
			ids[i] = info.ID
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("session ids differ: %s vs %s", ids[i], ids[0])
		}
	}
	list, err := rt.store.List(ctx, core.SessionFilter{Channel: "telegram", Address: "7", Agent: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("sessions=%d want 1", len(list))
	}
}

// TestChannelSessionFindsOlderSession checks that an agent keeps its old session
// even when other sessions of the address are newer.
func TestChannelSessionFindsOlderSession(t *testing.T) {
	rt := newChannelTestRuntime(t)
	ctx := context.Background()
	first, err := rt.ChannelSession(ctx, "telegram", "9", "helper")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		if _, err := rt.CreateChannelSession(ctx, "telegram", "9", "coder"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rt.ChannelSession(ctx, "telegram", "9", "helper")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID {
		t.Fatalf("got %s want %s", got.ID, first.ID)
	}
}

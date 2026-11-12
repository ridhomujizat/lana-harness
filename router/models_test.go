package router

import (
	"context"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-MDL-010: CachedModel reads the provider's own model list from the cache
// and returns its context window, max output, name, and facts for an address
// whose model the static catalog does not know.
func TestCachedModelFromProviderList(t *testing.T) {
	b := &fakeBehavior{models: []ModelInfo{
		{ID: "deepseek/deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", ContextWindow: 1000000, MaxOutput: 384000, Vision: true, Thinking: true},
	}}
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{
		"commandcode": fakeProvider(t, "commandcode", b),
	}})
	// Seed the cache through the normal path, so the test does not depend on
	// CachedModel's own (asynchronous) refresh.
	if _, err := h.r.Models(context.Background(), "commandcode", true); err != nil {
		t.Fatalf("seed models: %v", err)
	}

	got, ok := h.r.CachedModel("commandcode/deepseek/deepseek-v4.1-flash")
	if !ok {
		t.Fatal("CachedModel missed a provider-listed model")
	}
	if got.ContextWindow != 1000000 || got.MaxOutput != 384000 || got.Name != "DeepSeek V4.1 Flash" || !got.Vision || got.Thinking == "" {
		t.Fatalf("CachedModel = %+v", got)
	}

	// A provider the router does not know falls back to scanning every cached
	// list (first hit).
	if m, ok := h.r.CachedModel("other/deepseek/deepseek-v4.1-flash"); !ok || m.ContextWindow != 1000000 {
		t.Fatalf("scan-all lookup = %+v ok=%v", m, ok)
	}
	// A model no cached list holds is a miss.
	if _, ok := h.r.CachedModel("commandcode/nope"); ok {
		t.Fatal("CachedModel matched a model the provider list does not hold")
	}
}

// REQ-MDL-010: CachedModel returns false when nothing is cached, without any
// fetch on the caller's path.
func TestCachedModelNothingCached(t *testing.T) {
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{
		"p": fakeProvider(t, "p", &fakeBehavior{}),
	}})
	if _, ok := h.r.CachedModel("p/m"); ok {
		t.Fatal("CachedModel hit an unseeded cache")
	}
	if _, ok := h.r.CachedModel("unknown/m"); ok {
		t.Fatal("CachedModel hit with no cache at all")
	}
}

// blockingModels is a provider whose ListModels blocks until release, so a
// CachedModel call that fetched on the caller's path would block with it.
type blockingModels struct{ release chan struct{} }

func (p *blockingModels) Stream(context.Context, core.ChatRequest) (core.ChatStream, error) {
	return func(func(core.ChatEvent, error) bool) {}, nil
}

func (p *blockingModels) ListModels(ctx context.Context) ([]ModelInfo, error) {
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	return nil, ErrNotSupported
}

// REQ-MDL-010: CachedModel never does network I/O on the caller's path; a miss
// returns at once even when the provider's list would block.
func TestCachedModelDoesNotBlock(t *testing.T) {
	p := &blockingModels{release: make(chan struct{})}
	h := newHarness(t, Config{}, WithProviders(map[string]core.Provider{"slow": p}))
	t.Cleanup(func() { close(p.release) })

	done := make(chan bool, 1)
	go func() {
		_, ok := h.r.CachedModel("slow/m")
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("CachedModel returned a hit with no cache")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CachedModel blocked on a network fetch")
	}
}

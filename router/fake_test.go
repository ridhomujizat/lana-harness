package router

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// The fake kind looks up its behavior by base URL, so each test controls its
// own providers.
func init() {
	RegisterKind("fake", func(name string, cfg core.ProviderConfig, deps Deps) (Adapter, error) {
		fakesMu.Lock()
		b, ok := fakes[cfg.BaseURL]
		fakesMu.Unlock()
		if !ok {
			return nil, errors.New("no fake behavior for " + cfg.BaseURL)
		}
		return &fakeAdapter{name: name, b: b, deps: deps}, nil
	})
}

var (
	fakesMu sync.Mutex
	fakes   = map[string]*fakeBehavior{}
)

type fakeBehavior struct {
	mu sync.Mutex
	// errs are returned by the next calls, in order; then calls succeed.
	errs []error
	// always, when set, is returned by every call after errs.
	always error
	// text is the reply; "" means "hi from <provider>".
	text  string
	usage *core.Usage
	// midErr is yielded after the text.
	midErr error
	// block, when set, holds the stream after its first event until closed.
	block chan struct{}
	// clock and advance move the fake clock before the first event and
	// again before the end of the stream.
	clock   *fakeClock
	advance time.Duration

	calls   []string
	models  []ModelInfo
	listErr error
	lists   int

	balance  *Balance
	balErr   error
	balances int

	embedErr error
	embeds   int
}

func (b *fakeBehavior) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

type fakeAdapter struct {
	name string
	b    *fakeBehavior
	deps Deps
}

func (a *fakeAdapter) Stream(_ context.Context, req core.ChatRequest) (core.ChatStream, error) {
	b := a.b
	b.mu.Lock()
	b.calls = append(b.calls, req.Model)
	var err error
	if len(b.errs) > 0 {
		err, b.errs = b.errs[0], b.errs[1:]
	} else if b.always != nil {
		err = b.always
	}
	text := b.text
	if text == "" {
		text = "hi from " + a.name
	}
	usage, midErr, block, clock, advance := b.usage, b.midErr, b.block, b.clock, b.advance
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if clock != nil {
		clock.Advance(advance)
	}
	return func(yield func(core.ChatEvent, error) bool) {
		if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil) {
			return
		}
		if block != nil {
			<-block
		}
		if clock != nil {
			clock.Advance(advance)
		}
		if midErr != nil {
			yield(core.ChatEvent{}, midErr)
			return
		}
		if usage != nil {
			if !yield(core.ChatEvent{Kind: core.ChatUsage, Usage: *usage}, nil) {
				return
			}
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (a *fakeAdapter) ListModels(context.Context) ([]ModelInfo, error) {
	a.b.mu.Lock()
	defer a.b.mu.Unlock()
	a.b.lists++
	if a.b.listErr != nil {
		return nil, a.b.listErr
	}
	if a.b.models == nil {
		return nil, ErrNotSupported
	}
	return a.b.models, nil
}

func (a *fakeAdapter) FetchBalance(context.Context) (LimitUpdate, error) {
	a.b.mu.Lock()
	defer a.b.mu.Unlock()
	a.b.balances++
	if a.b.balErr != nil {
		return LimitUpdate{}, a.b.balErr
	}
	return LimitUpdate{Balance: a.b.balance}, nil
}

func (a *fakeAdapter) Embed(_ context.Context, _ string, inputs []string) ([][]float32, error) {
	a.b.mu.Lock()
	defer a.b.mu.Unlock()
	a.b.embeds++
	if a.b.embedErr != nil {
		return nil, a.b.embedErr
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{float32(i)}
	}
	return out, nil
}

// fakeProvider registers a behavior and returns its provider config.
func fakeProvider(t *testing.T, id string, b *fakeBehavior) core.ProviderConfig {
	t.Helper()
	url := "fake://" + t.Name() + "/" + id
	fakesMu.Lock()
	fakes[url] = b
	fakesMu.Unlock()
	t.Cleanup(func() {
		fakesMu.Lock()
		delete(fakes, url)
		fakesMu.Unlock()
	})
	return core.ProviderConfig{Kind: "fake", BaseURL: url}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(at string) *fakeClock {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		panic(err)
	}
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type sleeper struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *sleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()
	return ctx.Err()
}

type recorder struct {
	mu      sync.Mutex
	records []CallRecord
}

func (r *recorder) Record(rec CallRecord) {
	r.mu.Lock()
	r.records = append(r.records, rec)
	r.mu.Unlock()
}

func (r *recorder) all() []CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CallRecord(nil), r.records...)
}

type mapSecrets map[string]string

func (m mapSecrets) Lookup(name string) (core.Secret, bool) {
	v, ok := m[name]
	return core.NewSecret(v), ok
}

type harness struct {
	r     *Router
	clock *fakeClock
	sleep *sleeper
	rec   *recorder
}

func newHarness(t *testing.T, cfg Config, opts ...Option) *harness {
	t.Helper()
	h := &harness{clock: newClock("2026-11-08T10:00:00Z"), sleep: &sleeper{}, rec: &recorder{}}
	all := append([]Option{
		WithClock(h.clock.Now), WithSleep(h.sleep.Sleep), WithRecorder(h.rec),
		WithJitter(func() float64 { return 1 }),
	}, opts...)
	r, err := New(cfg, all...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h.r = r
	return h
}

// drain reads a stream to the end and returns its text and error.
func drain(stream core.ChatStream) (string, error) {
	var text strings.Builder
	for event, err := range stream {
		if err != nil {
			return text.String(), err
		}
		if event.Kind == core.ChatTextDelta {
			text.WriteString(event.Text)
		}
	}
	return text.String(), nil
}

func pe(class string, status int, msg string) *core.ProviderError {
	return &core.ProviderError{Class: class, Status: status, Message: msg}
}

func chain(addresses ...string) []Target {
	out := make([]Target, 0, len(addresses))
	for _, a := range addresses {
		t, ok := ParseAddress(a)
		if !ok {
			panic(a)
		}
		out = append(out, t)
	}
	return out
}

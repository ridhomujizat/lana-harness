package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// fakeLLM is a one-provider LLM registry.
type fakeLLM struct{ provider core.Provider }

func (f fakeLLM) Provider(string) (core.Provider, bool) { return f.provider, true }
func (f fakeLLM) Names() []string                       { return []string{"mock"} }

// fakeProvider is a scripted provider. A non-nil gate blocks Stream until it is
// closed, so a test can hold a compile in flight.
type fakeProvider struct {
	mu       sync.Mutex
	text     string
	usage    core.Usage
	err      error
	gate     chan struct{}
	calls    int
	requests []core.ChatRequest
}

func (p *fakeProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.calls++
	p.requests = append(p.requests, req)
	text, usage, err, gate := p.text, p.usage, p.err, p.gate
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return func(yield func(core.ChatEvent, error) bool) {
		if text != "" {
			if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil) {
				return
			}
		}
		yield(core.ChatEvent{Kind: core.ChatUsage, Usage: usage}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (p *fakeProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeProvider) recorded() []core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]core.ChatRequest(nil), p.requests...)
}

// testEnv is one service over a fresh store and home.
type testEnv struct {
	store    *memstore.Store
	svc      *Service
	session  string
	home     string
	agent    string
	provider *fakeProvider
	cancel   context.CancelFunc
}

func newTestEnv(t *testing.T, provider *fakeProvider) *testEnv {
	t.Helper()
	store := memstore.New()
	home := t.TempDir()
	const agent = "assistant"
	info, err := store.Create(context.Background(), core.NewSession{Agent: agent, Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	svc := NewService(base, home, core.KnowledgeConfig{}, store, fakeLLM{provider: provider})
	svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		if name != agent {
			return core.AgentConfig{}, false
		}
		return core.AgentConfig{Provider: "mock", Model: "m"}, true
	}
	t.Cleanup(func() {
		cancel()
		svc.drain()
		_ = svc.Close()
	})
	return &testEnv{store: store, svc: svc, session: info.ID, home: home, agent: agent, provider: provider, cancel: cancel}
}

// waitStatus waits until the source state is one of want.
func (e *testEnv) waitStatus(t *testing.T, src string, want ...string) kv.SourceState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, idx, err := e.svc.open()
		if err == nil {
			if st, ok, err := idx.GetSourceState(src); err == nil && ok {
				for _, w := range want {
					if st.Status == w {
						return st
					}
				}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("source %s did not reach state %v", src, want)
	return kv.SourceState{}
}

// page reads and parses one wiki page.
func (e *testEnv) page(t *testing.T, title string) kv.Page {
	t.Helper()
	vault, _, err := e.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	page, ok, err := vault.FindPage(title)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("page %q not found", title)
	}
	return page
}

func compiledEvents(t *testing.T, store *memstore.Store, sessionID string) []core.KnowledgeCompiled {
	t.Helper()
	events, err := store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.KnowledgeCompiled
	for _, event := range events {
		if event.Kind != core.KindKnowledgeCompiled {
			continue
		}
		var payload core.KnowledgeCompiled
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// AC-KNW-004 (REQ-KNW-021, -022, -024): create and append ops produce pages
// with wikilinks and sources, a source page, and exactly one
// knowledge/compiled event.
func TestCompileCreatesPagesAndEvent(t *testing.T) {
	provider := &fakeProvider{
		text: `[
			{"op":"create","title":"Pak Budi","type":"person","description":"Klien utama proyek X","body":"Klien utama. Lihat [[Proyek X]]."},
			{"op":"create","title":"Proyek X","type":"project","description":"Proyek klien","body":"Proyek milik [[Pak Budi]]."},
			{"op":"append","title":"Proyek X","heading":"Kontrak","text":"Kontrak ditandatangani 12 Okt."}
		]`,
		usage: core.Usage{InputTokens: 12000, OutputTokens: 900, CachedInputTokens: 30},
	}
	env := newTestEnv(t, provider)
	env.svc.start()

	res, err := env.svc.Ingest(context.Background(), core.KnowledgeIngest{
		Title: "Rapat Q4", Ext: "txt",
		Data:      []byte("Rapat Q4 membahas kontrak.\n\nPak Budi klien utama."),
		SessionID: env.session, TurnID: "t", AgentName: env.agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := env.waitStatus(t, res.Path, "done")
	if st.Error != "" {
		t.Fatalf("state error = %q", st.Error)
	}

	link := "[[" + res.Path + "]]"
	budi := env.page(t, "Pak Budi")
	if budi.Meta.Type != "person" || !contains(budi.Meta.Sources, link) || !strings.Contains(budi.Body, "[[Proyek X]]") {
		t.Fatalf("Pak Budi page = %+v", budi)
	}
	proyek := env.page(t, "Proyek X")
	if !contains(proyek.Meta.Sources, link) || !strings.Contains(proyek.Body, "[[Pak Budi]]") || !strings.Contains(proyek.Body, "## Kontrak") {
		t.Fatalf("Proyek X page = %+v", proyek)
	}
	src := env.page(t, sourceTitle(res.Path))
	if src.Meta.Type != "source" || !contains(src.Meta.Sources, link) || !strings.Contains(src.Body, "Source: "+link) {
		t.Fatalf("source page = %+v", src)
	}

	events := compiledEvents(t, env.store, env.session)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Source != res.Path || events[0].Error != "" {
		t.Fatalf("event = %+v", events[0])
	}
	if len(events[0].Ops) != 3 {
		t.Fatalf("ops = %+v", events[0].Ops)
	}
	if events[0].Usage.InputTokens != 12000 || events[0].Usage.OutputTokens != 900 {
		t.Fatalf("usage = %+v", events[0].Usage)
	}
}

// AC-KNW-004: invalid JSON marks the source failed and the event carries the
// error.
func TestCompileInvalidJSONFails(t *testing.T) {
	provider := &fakeProvider{text: "this is not json"}
	env := newTestEnv(t, provider)
	env.svc.start()

	res, err := env.svc.Ingest(context.Background(), core.KnowledgeIngest{
		Title: "Bad", Ext: "txt", Data: []byte("some text"),
		SessionID: env.session, TurnID: "t", AgentName: env.agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := env.waitStatus(t, res.Path, "failed")
	if st.Error == "" {
		t.Fatal("failed state has no error")
	}
	events := compiledEvents(t, env.store, env.session)
	if len(events) != 1 || events[0].Error == "" {
		t.Fatalf("events = %+v", events)
	}
	if len(events[0].Ops) != 0 {
		t.Fatalf("ops = %+v", events[0].Ops)
	}
}

// REQ-KNW-025: an unknown agent leaves the source failed and calls no provider.
func TestCompileUnknownAgentFails(t *testing.T) {
	provider := &fakeProvider{text: "[]"}
	env := newTestEnv(t, provider)
	env.svc.start()

	res, err := env.svc.Ingest(context.Background(), core.KnowledgeIngest{
		Title: "Ghost", Ext: "txt", Data: []byte("text"),
		SessionID: env.session, TurnID: "t", AgentName: "ghost",
	})
	if err != nil {
		t.Fatal(err)
	}
	st := env.waitStatus(t, res.Path, "failed")
	if !strings.Contains(st.Error, "unknown agent") {
		t.Fatalf("state error = %q", st.Error)
	}
	if provider.count() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.count())
	}
}

// AC-KNW-003 (REQ-KNW-023): compile state survives reopening the index, and a
// source left pending is re-queued at startup.
func TestCompileStateSurvivesReopen(t *testing.T) {
	provider := &fakeProvider{text: "[]"}
	env := newTestEnv(t, provider)

	vault, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	src, dup, err := vault.SaveSource("Rapat Q4", "txt", []byte("hello world"))
	if err != nil || dup {
		t.Fatalf("save source: dup=%v err=%v", dup, err)
	}
	if err := idx.SetSourceState(kv.SourceState{
		Path: src, Status: statePending, SessionID: env.session, TurnID: "t", Agent: env.agent,
		UpdatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	// Close the first service; the pending row is persisted in knowledge.db.
	env.cancel()
	env.svc.drain()
	if err := env.svc.Close(); err != nil {
		t.Fatal(err)
	}

	base, cancel := context.WithCancel(context.Background())
	svc2 := NewService(base, env.home, core.KnowledgeConfig{}, env.store, fakeLLM{provider: provider})
	svc2.LookupAgent = env.svc.LookupAgent
	t.Cleanup(func() {
		cancel()
		svc2.drain()
		_ = svc2.Close()
	})
	svc2.start()

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, idx2, err := svc2.open()
		if err == nil {
			if st, ok, err := idx2.GetSourceState(src); err == nil && ok && st.Status == stateDone {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending source %s was not compiled after reopen", src)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if provider.count() == 0 {
		t.Fatal("provider was never called after reopen")
	}
}

// REQ-KNW-023: the compile queue is FIFO.
func TestCompileQueueIsFIFO(t *testing.T) {
	gate := make(chan struct{})
	provider := &fakeProvider{text: "[]", gate: gate}
	env := newTestEnv(t, provider)
	env.svc.start()

	var paths []string
	for _, title := range []string{"One", "Two", "Three"} {
		res, err := env.svc.Ingest(context.Background(), core.KnowledgeIngest{
			Title: title, Ext: "txt", Data: []byte(title + " source text"),
			SessionID: env.session, TurnID: "t", AgentName: env.agent,
		})
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, res.Path)
	}
	close(gate)

	for _, p := range paths {
		env.waitStatus(t, p, "done")
	}
	requests := provider.recorded()
	if len(requests) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(requests))
	}
	for i, req := range requests {
		source := sourceLine(req)
		if source != paths[i] {
			t.Fatalf("compile order[%d] = %q, want %q", i, source, paths[i])
		}
	}
}

// sourceLine reads the "Source: <path>" line of a compile instruction.
func sourceLine(req core.ChatRequest) string {
	if len(req.Messages) == 0 || len(req.Messages[0].Content) == 0 {
		return ""
	}
	line, _, _ := strings.Cut(req.Messages[0].Content[0].Text, "\n")
	return strings.TrimPrefix(line, "Source: ")
}

// REQ-KNW-041: Read returns the requested line range with 1-based numbers.
func TestReadLineRange(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{text: "[]"})
	vault, _, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(vault.Root(), "wiki", "Notes.md")
	if err := os.WriteFile(page, []byte("line one\nline two\nline three"), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := env.svc.Read(context.Background(), "Notes", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Path != "wiki/Notes.md" || doc.From != 2 || doc.To != 3 || doc.Total < 3 {
		t.Fatalf("doc = %+v", doc)
	}
	if len(doc.Lines) != 2 || !strings.Contains(doc.Lines[0], "line two") || !strings.Contains(doc.Lines[1], "line three") {
		t.Fatalf("lines = %v", doc.Lines)
	}
}

// REQ-KNW-043: Read refuses a path outside the vault.
func TestReadRejectsEscape(t *testing.T) {
	env := newTestEnv(t, &fakeProvider{text: "[]"})
	if _, err := env.svc.Read(context.Background(), "../etc/passwd", 0, 0); err == nil {
		t.Fatal("Read accepted a path outside the vault")
	}
}

// REQ-KNW-023: a compile cut short by shutdown leaves the source pending, so
// the next start compiles it again instead of marking it failed.
func TestCompileStoppedAtShutdownStaysPending(t *testing.T) {
	gate := make(chan struct{})
	provider := &fakeProvider{text: "[]", gate: gate}
	env := newTestEnv(t, provider)
	env.svc.start()
	res, err := env.svc.Ingest(context.Background(), core.KnowledgeIngest{
		Title: "Late", Ext: "txt", Data: []byte("late source text"),
		SessionID: env.session, TurnID: "t", AgentName: env.agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for provider.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	env.cancel()
	env.svc.drain()
	_, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := idx.GetSourceState(res.Path)
	if err != nil || !ok {
		t.Fatalf("state: ok=%v err=%v", ok, err)
	}
	if st.Status != statePending {
		t.Fatalf("status = %q (%s), want pending", st.Status, st.Error)
	}
}

package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// fakeClock is a settable service clock for the upkeep tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// vaultEnv is a knowledge service over a fixed vault folder with a fake
// knowledge agent and a settable clock.
type vaultEnv struct {
	t        *testing.T
	svc      *Service
	vaultDir string
	provider *fakeProvider
	clock    *fakeClock
}

// knowledgeAgentConfig is the agent the fake registry resolves.
func knowledgeAgentConfig() core.AgentConfig {
	return core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}}
}

func newVaultEnv(t *testing.T, provider *fakeProvider) *vaultEnv {
	t.Helper()
	home := t.TempDir()
	vaultDir := filepath.Join(home, "vault")
	if _, err := kv.Open(vaultDir); err != nil {
		t.Fatal(err)
	}
	cfg := core.KnowledgeConfig{Path: vaultDir, Agent: "assistant"}
	base, cancel := context.WithCancel(context.Background())
	svc := NewService(base, home, cfg, memstore.New(), fakeLLM{provider: provider})
	svc.Config = core.Config{
		Knowledge: cfg,
		Agents:    map[string]core.AgentConfig{"assistant": knowledgeAgentConfig()},
	}
	svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		if name != "assistant" {
			return core.AgentConfig{}, false
		}
		return knowledgeAgentConfig(), true
	}
	clock := &fakeClock{t: time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)}
	svc.now = clock.now
	t.Cleanup(func() {
		cancel()
		svc.drain()
		_ = svc.Close()
	})
	return &vaultEnv{t: t, svc: svc, vaultDir: vaultDir, provider: provider, clock: clock}
}

func (e *vaultEnv) writeWiki(name, content string) {
	e.t.Helper()
	path := filepath.Join(e.vaultDir, "wiki", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *vaultEnv) writeJournal(day, content string) {
	e.t.Helper()
	path := filepath.Join(e.vaultDir, "journal", day+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *vaultEnv) appendJournal(day, content string) {
	e.t.Helper()
	path := filepath.Join(e.vaultDir, "journal", day+".md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		e.t.Fatal(err)
	}
}

func (e *vaultEnv) readLog() string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.vaultDir, "log.md"))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func (e *vaultEnv) countConsolidations() int {
	count := 0
	for _, line := range strings.Split(e.readLog(), "\n") {
		if strings.HasPrefix(line, "## ") && strings.Contains(line, "consolidation") {
			count++
		}
	}
	return count
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// AC-KNU-005 (REQ-KNU-020, -021, -024): with a fake clock the run fires once at
// 03:00 and not twice on the same day.
func TestConsolidationFiresOncePerDay(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	at := time.Date(2026, 11, 1, 3, 0, 0, 0, time.Local)
	env.clock.set(at)

	ctx := context.Background()
	env.svc.upkeepTick(ctx)
	env.svc.upkeepTick(ctx)

	if n := env.countConsolidations(); n != 1 {
		t.Fatalf("consolidation entries = %d, want 1:\n%s", n, env.readLog())
	}
	last, ok := env.svc.lastConsolidation()
	if !ok || last.UnixMilli() != at.UnixMilli() {
		t.Fatalf("last_consolidation = %v (ok %v), want %v", last, ok, at)
	}
}

// AC-KNU-005: a missed run (clock after 03:00, last run older) fires after
// StartUpkeep.
func TestConsolidationMissedRunFiresAfterStart(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	now := time.Date(2026, 11, 1, 4, 0, 0, 0, time.Local)
	env.clock.set(now)
	old := time.Date(2026, 10, 31, 3, 0, 0, 0, time.Local)
	env.svc.setUpkeepValue(upkeepKeyConsolidationAt, strconv.FormatInt(old.UnixMilli(), 10))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.svc.StartUpkeep(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if last, ok := env.svc.lastConsolidation(); ok && last.UnixMilli() == now.UnixMilli() {
			// Wait for the run to finish its log write so TempDir cleanup
			// does not race the consolidation goroutine.
			for env.svc.upkeep.running.Load() && time.Now().Before(deadline) {
				time.Sleep(2 * time.Millisecond)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("a missed consolidation did not fire after start")
}

// REQ-KNU-020: a fresh install stores the current time as last_consolidation_at
// on the first start and runs no consolidation; the next run waits for the next
// consolidate_at instead of firing on the very first tick.
func TestFreshInstallDoesNotConsolidateOnStart(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	at := time.Date(2026, 11, 1, 4, 0, 0, 0, time.Local)
	env.clock.set(at)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.svc.StartUpkeep(ctx)

	// Wait for the loop's first tick (an inbox scan) to finish so the vault is
	// not written while the temp dir is removed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		env.svc.upkeep.mu.Lock()
		scanned := !env.svc.upkeep.lastScan.IsZero()
		env.svc.upkeep.mu.Unlock()
		if scanned {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	last, ok := env.svc.lastConsolidation()
	if !ok || last.UnixMilli() != at.UnixMilli() {
		t.Fatalf("last_consolidation = %v (ok %v), want %v", last, ok, at)
	}
	if fileExists(filepath.Join(env.vaultDir, "log.md")) && env.countConsolidations() != 0 {
		t.Fatalf("a fresh install consolidated on start:\n%s", env.readLog())
	}
}

// AC-KNU-005 (REQ-KNU-021): promotion compiles only the journal text added
// since the last promotion.
func TestPromotionCompilesOnlyNewText(t *testing.T) {
	provider := &fakeProvider{text: `[{"op":"create","title":"Topik","type":"topic","description":"topik","body":"isi"}]`}
	env := newVaultEnv(t, provider)
	day := "2026-11-01"
	env.writeJournal(day, "# 2026-11-01\n\n## 09:00 · lana\n- note one\n")

	ctx := context.Background()
	promotions, err := env.svc.promote(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(promotions) != 1 || promotions[0].Path != "journal/"+day+".md" || promotions[0].Ops != 1 {
		t.Fatalf("promotions = %+v", promotions)
	}
	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
	if req := provider.recorded()[0]; !strings.Contains(req.System, compileJournalNote) {
		t.Fatalf("promotion system prompt missing the journal note:\n%s", req.System)
	}

	vault, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	page, ok, err := vault.FindPage("Topik")
	if err != nil || !ok {
		t.Fatalf("Topik page not found: ok=%v err=%v", ok, err)
	}
	if !contains(page.Meta.Sources, "[[journal/"+day+"]]") {
		t.Fatalf("page sources = %v, want the journal link", page.Meta.Sources)
	}
	size := int64(0)
	if info, err := os.Stat(filepath.Join(env.vaultDir, "journal", day+".md")); err == nil {
		size = info.Size()
	}
	if offset, err := idx.PromoteOffset("journal/" + day + ".md"); err != nil || offset != size {
		t.Fatalf("promote offset = %d (err %v), want %d", offset, err, size)
	}

	// Second run with an unchanged file compiles nothing.
	promotions, err = env.svc.promote(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(promotions) != 0 || provider.count() != 1 {
		t.Fatalf("unchanged promotion ran again: %+v calls=%d", promotions, provider.count())
	}

	// Appending text compiles only the appended part.
	env.appendJournal(day, "\n## 10:00 · lana\n- appended note\n")
	if _, err := env.svc.promote(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.count())
	}
	instruction := provider.recorded()[1].Messages[0].Content[0].Text
	if !strings.Contains(instruction, "appended note") {
		t.Fatalf("appended text not compiled:\n%s", instruction)
	}
	if strings.Contains(instruction, "note one") {
		t.Fatalf("promotion recompiled old text:\n%s", instruction)
	}
}

// REQ-KNU-020, API-KNU-006: a run already in progress makes Consolidate return
// ErrKnowledgeBusy.
func TestConsolidateWhileRunningIsBusy(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.svc.upkeep.running.Store(true)
	defer env.svc.upkeep.running.Store(false)

	if err := env.svc.Consolidate(context.Background()); !errors.Is(err, core.ErrKnowledgeBusy) {
		t.Fatalf("Consolidate = %v, want ErrKnowledgeBusy", err)
	}
}

// API-KNU-003: KnowledgeStatus carries every field with its JSON name and never
// a null list.
func TestKnowledgeStatusShape(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeWiki("Halaman.md", "---\ntype: topic\ndescription: halaman\n---\nIsi halaman.\n")
	vault, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.SyncFile(filepath.Join(vault.Root(), "wiki", "Halaman.md")); err != nil {
		t.Fatal(err)
	}
	status, err := env.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Counts.Pages != 1 {
		t.Fatalf("pages = %d, want 1", status.Counts.Pages)
	}
	if status.Embedding.Chunks == 0 {
		t.Fatal("embedding chunks = 0 after indexing a page")
	}
	if !status.Available {
		t.Fatal("available = false, want true")
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"available", "path", "exists", "counts", "sources_pending", "sources_failed", "inbox_unsupported", "embedding", "agent", "agent_error", "consolidation", "log"} {
		if _, ok := top[key]; !ok {
			t.Fatalf("status JSON missing %q: %s", key, data)
		}
	}
	for _, key := range []string{"sources_pending", "sources_failed", "inbox_unsupported", "log"} {
		if string(top[key]) == "null" {
			t.Fatalf("status list %q is null: %s", key, data)
		}
	}
	assertJSONKeys(t, top["counts"], "counts", "pages", "sources", "journal_days")
	assertJSONKeys(t, top["embedding"], "embedding", "provider", "model", "chunks", "vectors")
	assertJSONKeys(t, top["consolidation"], "consolidation", "running", "last_at", "last_error", "next_at", "report")
}

func assertJSONKeys(t *testing.T, raw json.RawMessage, name string, keys ...string) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s is not an object: %v", name, err)
	}
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			t.Fatalf("%s JSON missing %q: %s", name, key, raw)
		}
	}
}

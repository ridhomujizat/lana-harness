package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// domainEnv builds a vault env whose config has two domain agents, dana and
// rio, and whose lookup resolves no agent (so the lint contradiction pass never
// calls a provider).
func newDomainEnv(t *testing.T, provider *fakeProvider) *vaultEnv {
	t.Helper()
	env := newVaultEnv(t, provider)
	env.svc.Config = core.Config{
		Knowledge: env.svc.cfg,
		Agents: map[string]core.AgentConfig{
			"dana": {Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}},
			"rio":  {Provider: "mock", Model: "m", Tools: []string{"knowledge_search"}},
		},
	}
	env.svc.LookupAgent = func(string) (core.AgentConfig, bool) { return core.AgentConfig{}, false }
	return env
}

// writeVault writes content to a vault-relative path, creating parents.
func (e *vaultEnv) writeVault(rel, content string) {
	e.t.Helper()
	abs := filepath.Join(e.vaultDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// exists reports whether a vault file exists.
func (e *vaultEnv) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(e.vaultDir, filepath.FromSlash(rel)))
	return err == nil
}

// sync indexes every vault file now.
func (e *vaultEnv) sync() {
	e.t.Helper()
	_, idx, err := e.svc.open()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := idx.Sync(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// REQ-DKN-020: Write routes "" and "shared" to the shared wiki, a domain agent
// name to its domain, and refuses an agent with no domain.
func TestWriteScopeRouting(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	ctx := context.Background()

	def, err := env.svc.Write(ctx, core.KnowledgeWrite{Action: "create", Title: "Def", Type: "topic", Body: "b"})
	if err != nil || def.Path != "wiki/Def.md" {
		t.Fatalf("default write = %+v err %v", def, err)
	}
	shared, err := env.svc.Write(ctx, core.KnowledgeWrite{Action: "create", Title: "Sh", Type: "topic", Body: "b", Scope: "shared"})
	if err != nil || shared.Path != "wiki/Sh.md" {
		t.Fatalf("shared write = %+v err %v", shared, err)
	}
	domain, err := env.svc.Write(ctx, core.KnowledgeWrite{Action: "create", Title: "Gaji", Type: "topic", Body: "b", Scope: "dana"})
	if err != nil || domain.Path != "agents/dana/wiki/Gaji.md" {
		t.Fatalf("domain write = %+v err %v", domain, err)
	}
	if !env.exists("agents/dana/wiki/Gaji.md") {
		t.Fatal("dana page was not written")
	}
	_, err = env.svc.Write(ctx, core.KnowledgeWrite{Action: "create", Title: "Ghost", Type: "topic", Body: "b", Scope: "ghost"})
	if err == nil || !strings.Contains(err.Error(), `agent "ghost" has no knowledge domain`) {
		t.Fatalf("non-domain write error = %v", err)
	}
}

// REQ-DKN-021: Propose stores a pending proposal with its identity and refuses
// an unknown or own target.
func TestProposeStoresPending(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	ctx := context.Background()

	p, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "rio", FromAgent: "dana", SessionID: "s1", TurnID: "t1", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "append", Title: "Gaji", Text: "new"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.ID, "p_") || p.Status != "pending" || p.CreatedAt == 0 {
		t.Fatalf("proposal = %+v", p)
	}
	pending, err := env.svc.PendingProposals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	got := pending[0]
	if got.ID != p.ID || got.Agent != "rio" || got.FromAgent != "dana" || !got.OwnerTurn || got.SessionID != "s1" || got.TurnID != "t1" {
		t.Fatalf("stored proposal = %+v", got)
	}
	if got.Write.Action != "append" || got.Write.Title != "Gaji" || got.Write.Text != "new" {
		t.Fatalf("decoded write = %+v", got.Write)
	}

	if _, err := env.svc.Propose(ctx, core.KnowledgeProposal{Agent: "ghost", FromAgent: "dana"}); err == nil || !strings.Contains(err.Error(), `agent "ghost" has no knowledge domain`) {
		t.Fatalf("unknown-target error = %v", err)
	}
	if _, err := env.svc.Propose(ctx, core.KnowledgeProposal{Agent: "dana", FromAgent: "dana"}); err == nil || !strings.Contains(err.Error(), "cannot propose to your own domain; write it") {
		t.Fatalf("own-domain error = %v", err)
	}
}

// REQ-DKN-061: approving prompts a promotion once and marks approved; rejecting
// marks rejected; deciding twice is a not-found error.
func TestDecideProposal(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	ctx := context.Background()
	calls := make(chan string, 4)
	env.svc.promoteDomainFn = func(ctx context.Context, agent string) error {
		calls <- agent
		return nil
	}

	p, err := env.svc.Propose(ctx, core.KnowledgeProposal{Agent: "rio", FromAgent: "dana", Write: core.KnowledgeWrite{Action: "append", Title: "Gaji"}})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := env.svc.DecideProposal(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Status != "approved" || decided.DecidedAt == 0 {
		t.Fatalf("decided = %+v", decided)
	}
	select {
	case agent := <-calls:
		if agent != "rio" {
			t.Fatalf("promoted agent = %q, want rio", agent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approve did not call promoteDomainFn")
	}

	if _, err := env.svc.DecideProposal(ctx, p.ID, true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second decision error = %v, want os.ErrNotExist", err)
	}
	if _, err := env.svc.DecideProposal(ctx, "p_unknown", true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown decision error = %v, want os.ErrNotExist", err)
	}

	p2, err := env.svc.Propose(ctx, core.KnowledgeProposal{Agent: "rio", FromAgent: "dana", Write: core.KnowledgeWrite{Action: "append", Title: "Lain"}})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := env.svc.DecideProposal(ctx, p2.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("rejected = %+v", rejected)
	}
	if pending, _ := env.svc.PendingProposals(ctx); len(pending) != 0 {
		t.Fatalf("pending after decisions = %+v", pending)
	}
}

// REQ-DKN-011: ReadIn resolves a title in the caller's domain, then shared, then
// the other domains in name order; an explicit scope restricts it.
func TestReadInOrder(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	ctx := context.Background()
	page := func(rel string) { env.writeVault(rel, "---\ntype: topic\ndescription: d\n---\nbody of "+rel+"\n") }
	page("wiki/Gaji.md")
	page("agents/dana/wiki/Gaji.md")
	page("agents/rio/wiki/Gaji.md")
	page("wiki/Proyek.md")
	page("agents/rio/wiki/Proyek.md")
	page("agents/rio/wiki/Topik.md")

	cases := []struct {
		ref, caller, scope, wantPath, wantScope string
	}{
		{"Gaji", "dana", "", "agents/dana/wiki/Gaji.md", "dana"},
		{"Proyek", "dana", "", "wiki/Proyek.md", "shared"},
		{"Topik", "dana", "", "agents/rio/wiki/Topik.md", "rio"},
		{"Gaji", "dana", "rio", "agents/rio/wiki/Gaji.md", "rio"},
		{"Gaji", "dana", "shared", "wiki/Gaji.md", "shared"},
		{"wiki/Gaji.md", "dana", "", "wiki/Gaji.md", "shared"},
	}
	for _, c := range cases {
		doc, err := env.svc.ReadIn(ctx, c.ref, c.caller, c.scope, 0, 0)
		if err != nil {
			t.Fatalf("ReadIn(%q,%q,%q) error = %v", c.ref, c.caller, c.scope, err)
		}
		if doc.Path != c.wantPath || doc.Scope != c.wantScope {
			t.Fatalf("ReadIn(%q,%q,%q) = %s (%s), want %s (%s)", c.ref, c.caller, c.scope, doc.Path, doc.Scope, c.wantPath, c.wantScope)
		}
	}
}

// API-DKN-005: Page validates the path and returns the raw markdown.
func TestPagePathValidation(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	ctx := context.Background()
	env.writeVault("agents/dana/wiki/Gaji.md", "---\ntype: topic\ndescription: d\n---\nbody\n")

	doc, raw, err := env.svc.Page(ctx, "agents/dana/wiki/Gaji.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Path != "agents/dana/wiki/Gaji.md" || doc.Scope != "dana" || !strings.Contains(raw, "body") {
		t.Fatalf("page = %+v raw %q", doc, raw)
	}
	if len(doc.Lines) == 0 {
		t.Fatalf("page has no lines: %+v", doc)
	}

	for _, bad := range []string{
		"", "../secret.md", "wiki/../x.md", "sources/x.md", "wiki/A.txt",
		"agents/dana/notes.md", "agents/dana/wiki", "/abs/wiki/A.md",
	} {
		if _, _, err := env.svc.Page(ctx, bad); err == nil || !strings.Contains(err.Error(), "invalid path") {
			t.Fatalf("Page(%q) error = %v, want invalid path", bad, err)
		}
	}
	if _, _, err := env.svc.Page(ctx, "wiki/Missing.md"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing page error = %v, want os.ErrNotExist", err)
	}
}

// API-DKN-008: DomainStats counts pages, the last 7 days, and the 8 weekly
// buckets against a fixed now.
func TestDomainStatsBuckets(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	pages := []struct {
		name string
		old  time.Duration
	}{
		{"P1.md", time.Hour},
		{"P2.md", 10 * 24 * time.Hour},
		{"P3.md", 20 * 24 * time.Hour},
		{"P4.md", 60 * 24 * time.Hour},
	}
	for _, p := range pages {
		rel := "agents/dana/wiki/" + p.name
		env.writeVault(rel, "---\ntype: topic\ndescription: d\n---\nbody\n")
		at := now.Add(-p.old)
		if err := os.Chtimes(filepath.Join(env.vaultDir, filepath.FromSlash(rel)), at, at); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := env.svc.DomainStats(context.Background(), "dana", now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pages != 4 || stats.Changed7d != 1 {
		t.Fatalf("stats = %+v, want 4 pages and 1 changed", stats)
	}
	if stats.Weeks != [8]int{0, 0, 0, 0, 0, 1, 1, 1} {
		t.Fatalf("weeks = %v", stats.Weeks)
	}
	if len(stats.Recent) != 4 || stats.Recent[0].Title != "P1" || stats.Recent[0].Scope != "dana" {
		t.Fatalf("recent = %+v", stats.Recent)
	}
}

// API-DKN-003: Status carries the scope stats and the pending review count and
// never a nil scopes list.
func TestStatusScopesAndReview(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	ctx := context.Background()
	env.writeVault("wiki/Shared.md", "---\ntype: topic\ndescription: d\n---\nb\n")
	env.writeVault("agents/dana/wiki/Gaji.md", "---\ntype: topic\ndescription: d\n---\nb\n")
	env.sync()
	if _, err := env.svc.Propose(ctx, core.KnowledgeProposal{Agent: "rio", FromAgent: "dana", Write: core.KnowledgeWrite{Action: "append", Title: "Gaji"}}); err != nil {
		t.Fatal(err)
	}

	status, err := env.svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Scopes == nil {
		t.Fatal("scopes is nil")
	}
	byScope := map[string]core.KnowledgeScopeStat{}
	for _, s := range status.Scopes {
		byScope[s.Scope] = s
	}
	if byScope["shared"].Pages != 1 || byScope["dana"].Pages != 1 {
		t.Fatalf("scopes = %+v", status.Scopes)
	}
	if status.ReviewPending != 1 {
		t.Fatalf("review_pending = %d, want 1", status.ReviewPending)
	}
}

// REQ-DKN-030: a domain agent's session journals into its domain; a team name
// (an agent with no domain) keeps the shared journal.
func TestJournalRoutesByDomain(t *testing.T) {
	provider := &fakeProvider{text: `{"notes": ["a note"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	agents := map[string]core.AgentConfig{
		"dana": {Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}},
	}
	env.svc.Config = core.Config{Agents: agents}
	env.svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		if cfg, ok := agents[name]; ok {
			return cfg, true
		}
		// A team resolves to the lead's knowledge config but owns no domain.
		if name == "team" {
			return core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}}, true
		}
		return core.AgentConfig{}, false
	}

	at := time.Now()
	env.store.setRows(twoTurnRows(env.info, at))
	day := at.Format("2006-01-02")
	vault, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}

	dana := env.info
	dana.Agent = "dana"
	env.svc.journalSession(context.Background(), dana)
	if !fileExists(filepath.Join(vault.Root(), "agents", "dana", "journal", day+".md")) {
		t.Fatal("dana session did not journal into its domain")
	}
	if fileExists(filepath.Join(vault.Root(), "journal", day+".md")) {
		t.Fatal("dana session wrote the shared journal")
	}

	// The next session reuses the same rows: reset the journal mark.
	if err := idx.SetJournalMark(env.info.ID, 0); err != nil {
		t.Fatal(err)
	}
	team := env.info
	team.Agent = "team"
	env.svc.journalSession(context.Background(), team)
	if !fileExists(filepath.Join(vault.Root(), "journal", day+".md")) {
		t.Fatal("a team session did not journal into the shared journal")
	}
}

// design §4.4: lint labels a domain page with its scope and groups duplicates
// per scope, so a shared title and a domain title are not duplicates.
func TestLintScopesAndDuplicates(t *testing.T) {
	env := newDomainEnv(t, &fakeProvider{text: "[]"})
	env.writeVault("agents/dana/wiki/Gaji.md", "---\ntype: topic\n---\nIsi.\n")
	env.writeVault("wiki/Budi.md", "---\ntype: person\ndescription: klien\n---\nIsi.\n")
	env.writeVault("agents/dana/wiki/Budi.md", "---\ntype: person\ndescription: klien\n---\nIsi.\n")
	env.writeVault("agents/dana/wiki/Alias Budi.md", "---\ntype: person\ndescription: klien\naliases:\n  - Budi\n---\nIsi.\n")

	path, err := env.svc.lint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(env.vaultDir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	if !strings.Contains(report, "- [[Gaji]] (dana): no description") {
		t.Fatalf("report missing the scoped domain line:\n%s", report)
	}
	if !strings.Contains(report, "- [[Alias Budi]] (dana) and [[Budi]] (dana)") {
		t.Fatalf("report missing the per-scope duplicate:\n%s", report)
	}
	if strings.Contains(report, "[[Budi]] and [[Budi]] (dana)") || strings.Contains(report, "[[Budi]] (dana) and [[Budi]]") {
		t.Fatalf("report paired a shared and a domain title:\n%s", report)
	}
}

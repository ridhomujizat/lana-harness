package lana

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// fakeDomains is a core.KnowledgeDomains stub for the review and knowledge
// pass-through tests.
type fakeDomains struct {
	pending    []core.KnowledgeProposal
	decided    []core.KnowledgeProposal
	decisions  map[string]core.KnowledgeProposal
	hasDomain  map[string]bool
	pages      []core.KnowledgePageInfo
	pagesTotal int
	stats      core.KnowledgeDomainStats
}

func (f *fakeDomains) Propose(context.Context, core.KnowledgeProposal) (core.KnowledgeProposal, error) {
	return core.KnowledgeProposal{}, nil
}

func (f *fakeDomains) PendingProposals(context.Context) ([]core.KnowledgeProposal, error) {
	return f.pending, nil
}

func (f *fakeDomains) DecideProposal(_ context.Context, id string, approve bool) (core.KnowledgeProposal, error) {
	p, ok := f.decisions[id]
	if !ok {
		return core.KnowledgeProposal{}, fmt.Errorf("proposal %s: %w", id, os.ErrNotExist)
	}
	if approve {
		p.Status = "approved"
	} else {
		p.Status = "rejected"
	}
	return p, nil
}

func (f *fakeDomains) PromoteSoon(string) {}

func (f *fakeDomains) HasDomain(agent string) bool { return f.hasDomain[agent] }

func (f *fakeDomains) Pages(context.Context, string, string, int) ([]core.KnowledgePageInfo, int, error) {
	return f.pages, f.pagesTotal, nil
}

func (f *fakeDomains) Page(context.Context, string) (core.KnowledgeDoc, string, error) {
	return core.KnowledgeDoc{}, "", nil
}

func (f *fakeDomains) Graph(context.Context, string, bool, bool) (core.KnowledgeGraph, error) {
	return core.KnowledgeGraph{}, nil
}

func (f *fakeDomains) Activity(context.Context, int, time.Time, string) (core.KnowledgeActivityPage, error) {
	return core.KnowledgeActivityPage{}, nil
}

func (f *fakeDomains) Health(context.Context) (core.KnowledgeHealth, error) {
	return core.KnowledgeHealth{}, nil
}

func (f *fakeDomains) DecidedProposals(context.Context, int) ([]core.KnowledgeProposal, error) {
	return f.decided, nil
}

func (f *fakeDomains) DomainStats(context.Context, string, time.Time) (core.KnowledgeDomainStats, error) {
	return f.stats, nil
}

func (f *fakeDomains) ScopeStats(context.Context) ([]core.KnowledgeScopeStat, error) {
	return nil, nil
}

func (f *fakeDomains) ReadIn(context.Context, string, string, string, int, int) (core.KnowledgeDoc, error) {
	return core.KnowledgeDoc{}, nil
}

// fakeDomainsModule provides fakeDomainsSvc as the knowledge service so a
// hand-built Runtime exercises the knowledge pass-throughs without booting the
// real service.
type fakeDomainsModule struct{}

func (fakeDomainsModule) ID() string                  { return "test-knowledge-domains" }
func (fakeDomainsModule) Requires() []core.ServiceKey { return nil }
func (fakeDomainsModule) Provides() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceKnowledge}
}
func (fakeDomainsModule) Install(b *core.Builder, _ core.ModuleConfig) error {
	return b.Provide(core.ServiceKnowledge, fakeDomainsSvc)
}
func (fakeDomainsModule) Stop(context.Context) error { return nil }

var (
	fakeDomainsOnce sync.Once
	fakeDomainsSvc  core.KnowledgeDomains
)

// newDomainsRuntime builds a Runtime with only the fields the knowledge
// pass-throughs read: the service context, the agent home, and an empty config.
func newDomainsRuntime(t *testing.T, home string, domains core.KnowledgeDomains) *Runtime {
	t.Helper()
	fakeDomainsOnce.Do(func() {
		core.RegisterModule("test-knowledge-domains", func() core.Module { return fakeDomainsModule{} })
	})
	fakeDomainsSvc = domains
	bootCtx, err := core.Boot(context.Background(), core.BootInput{Config: core.Config{
		Modules: []core.ModuleRow{{ID: "test-knowledge", Module: "test-knowledge-domains"}},
	}})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = bootCtx.Shutdown(context.Background()) })
	return &Runtime{coreCtx: bootCtx, home: home, result: &config.Result{}}
}

// TestReviewItemsMergeOrder covers REQ-DKN-060: knowledge proposals and skill
// proposals merge into one queue, newest first.
func TestReviewItemsMergeOrder(t *testing.T) {
	home := t.TempDir()
	if err := agenthome.ProposeSkill(home, "dana", "payroll", "When payroll runs", "steps", agenthome.SkillProposalMeta{CreatedAt: 200}); err != nil {
		t.Fatal(err)
	}
	if err := agenthome.ProposeSkill(home, "rio", "budget", "When budgets change", "steps", agenthome.SkillProposalMeta{CreatedAt: 400}); err != nil {
		t.Fatal(err)
	}
	domains := &fakeDomains{pending: []core.KnowledgeProposal{
		{ID: "p_1", Agent: "rio", FromAgent: "dana", Title: "Gaji", CreatedAt: 100, Write: core.KnowledgeWrite{Action: "append", Text: "gaji naik"}},
		{ID: "p_2", Agent: "dana", FromAgent: "rio", Title: "Budget", CreatedAt: 300, Write: core.KnowledgeWrite{Action: "create", Body: "body text"}},
	}}
	rt := newDomainsRuntime(t, home, domains)

	items, err := rt.ReviewItems(context.Background(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"s_rio.budget", "p_2", "s_dana.payroll", "p_1"}
	if len(items) != len(wantIDs) {
		t.Fatalf("items = %d, want %d: %+v", len(items), len(wantIDs), items)
	}
	for i, want := range wantIDs {
		if items[i].ID != want {
			t.Fatalf("items[%d].id = %q, want %q (all: %+v)", i, items[i].ID, want, items)
		}
	}
	if items[1].Kind != "knowledge" || items[1].From != "rio" || items[1].Action != "create" || items[1].Body != "body text" {
		t.Fatalf("knowledge item = %+v", items[1])
	}
	if items[0].Kind != "skill" || items[0].Action != "create" || items[0].Description != "When budgets change" || items[0].From != "rio" {
		t.Fatalf("skill item = %+v", items[0])
	}
}

// TestReviewItemsSkillExistsAction covers the create/update action of a skill
// item (API-DKN-006).
func TestReviewItemsSkillExistsAction(t *testing.T) {
	home := t.TempDir()
	if _, err := agenthome.WriteSkill(home, "dana", "payroll", "When payroll runs", "steps"); err != nil {
		t.Fatal(err)
	}
	if err := agenthome.ProposeSkill(home, "dana", "payroll", "When payroll runs", "steps", agenthome.SkillProposalMeta{CreatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	rt := newDomainsRuntime(t, home, &fakeDomains{})
	items, err := rt.ReviewItems(context.Background(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != "update" {
		t.Fatalf("items = %+v, want one update item", items)
	}
}

// TestRenderKnowledgeChange covers the readable body rendering of API-DKN-006.
func TestRenderKnowledgeChange(t *testing.T) {
	cases := []struct {
		name  string
		write core.KnowledgeWrite
		want  string
	}{
		{"create", core.KnowledgeWrite{Action: "create", Body: "page body"}, "page body"},
		{"append", core.KnowledgeWrite{Action: "append", Text: "new line"}, "new line"},
		{"replace", core.KnowledgeWrite{Action: "replace", OldText: "old", NewText: "new"}, "old -> new"},
		{"supersede", core.KnowledgeWrite{Action: "supersede", OldText: "old", NewText: "new"}, "old -> new"},
		{"meta", core.KnowledgeWrite{Action: "meta", Type: "topic", Description: "desc", Tags: []string{"a", "b"}}, "type: topic\ndescription: desc\ntags: a, b"},
	}
	for _, tc := range cases {
		if got := renderKnowledgeChange(tc.write); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSplitSkillProposalID covers the review-id split of design §7.
func TestSplitSkillProposalID(t *testing.T) {
	agent, name, ok := splitSkillProposalID("s_dana.payroll-check")
	if !ok || agent != "dana" || name != "payroll-check" {
		t.Fatalf("split = %q, %q, %v", agent, name, ok)
	}
	for _, bad := range []string{"s_dana", "s_.name", "s_dana.", "p_1"} {
		if _, _, ok := splitSkillProposalID(bad); ok {
			t.Fatalf("split(%q) = ok, want false", bad)
		}
	}
}

// TestReviewDecideRouting covers REQ-DKN-061: the id prefix routes to the
// knowledge decision or the skill decision.
func TestReviewDecideRouting(t *testing.T) {
	home := t.TempDir()
	if err := agenthome.ProposeSkill(home, "dana", "payroll", "When payroll runs", "steps", agenthome.SkillProposalMeta{CreatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	domains := &fakeDomains{decisions: map[string]core.KnowledgeProposal{"p_1": {ID: "p_1", Agent: "rio"}}}
	rt := newDomainsRuntime(t, home, domains)

	if status, err := rt.ReviewDecide(context.Background(), "p_1", true); err != nil || status != "approved" {
		t.Fatalf("knowledge approve = %q, %v", status, err)
	}
	if status, err := rt.ReviewDecide(context.Background(), "p_1", false); err != nil || status != "rejected" {
		t.Fatalf("knowledge reject = %q, %v", status, err)
	}
	if status, err := rt.ReviewDecide(context.Background(), "s_dana.payroll", true); err != nil || status != "applied" {
		t.Fatalf("skill approve = %q, %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(agenthome.AgentSkillsDir(home, "dana"), "payroll", "SKILL.md")); err != nil {
		t.Fatalf("approved skill not written: %v", err)
	}
}

// TestReviewDecideRejectSkill covers the skill reject route.
func TestReviewDecideRejectSkill(t *testing.T) {
	home := t.TempDir()
	if err := agenthome.ProposeSkill(home, "dana", "payroll", "When payroll runs", "steps", agenthome.SkillProposalMeta{CreatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	rt := newDomainsRuntime(t, home, &fakeDomains{})
	status, err := rt.ReviewDecide(context.Background(), "s_dana.payroll", false)
	if err != nil || status != "rejected" {
		t.Fatalf("skill reject = %q, %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(agenthome.AgentProposedSkillsDir(home, "dana"), "payroll")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proposal folder still present: %v", err)
	}
}

// TestReviewDecideMissingIsNotExist covers the 404 case of API-DKN-007.
func TestReviewDecideMissingIsNotExist(t *testing.T) {
	rt := newDomainsRuntime(t, t.TempDir(), &fakeDomains{decisions: map[string]core.KnowledgeProposal{}})
	if _, err := rt.ReviewDecide(context.Background(), "p_missing", true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing proposal err = %v, want os.ErrNotExist", err)
	}
	if _, err := rt.ReviewDecide(context.Background(), "unknown", true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown id err = %v, want os.ErrNotExist", err)
	}
	if _, err := rt.ReviewDecide(context.Background(), "s_dana.missing", true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing skill err = %v, want os.ErrNotExist", err)
	}
}

// TestReviewDecideInvalidSkill covers the invalid-input error of API-DKN-007: a
// proposal whose body no longer passes the skill rules.
func TestReviewDecideInvalidSkill(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(agenthome.AgentProposedSkillsDir(home, "dana"), "empty")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// ParseSkill accepts an empty body; WriteSkill refuses it.
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: empty\ndescription: When nothing\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{home: home}
	if _, err := rt.ReviewDecide(context.Background(), "s_dana.empty", true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid skill err = %v, want ErrInvalidInput", err)
	}
}

// TestKnowledgeUnavailable covers the typed unavailable error of the knowledge
// pass-throughs when the runtime has no service.
func TestKnowledgeUnavailable(t *testing.T) {
	rt := &Runtime{}
	if _, _, err := rt.KnowledgePages(context.Background(), "all", "", 10); !errors.Is(err, ErrKnowledgeUnavailable) {
		t.Fatalf("KnowledgePages err = %v, want ErrKnowledgeUnavailable", err)
	}
	if _, _, err := rt.KnowledgePage(context.Background(), "wiki/a.md"); !errors.Is(err, ErrKnowledgeUnavailable) {
		t.Fatalf("KnowledgePage err = %v, want ErrKnowledgeUnavailable", err)
	}
	if _, err := rt.ReviewDecide(context.Background(), "p_1", true); !errors.Is(err, ErrKnowledgeUnavailable) {
		t.Fatalf("ReviewDecide err = %v, want ErrKnowledgeUnavailable", err)
	}
	available, _, err := rt.AgentKnowledge(context.Background(), "dana")
	if err != nil || available {
		t.Fatalf("AgentKnowledge = %v, %v, want false, nil", available, err)
	}
}

// TestAgentKnowledgeTeamLead covers API-DKN-008: a team resolves to its lead and
// a domain-less agent reports unavailable.
func TestAgentKnowledgeTeamLead(t *testing.T) {
	domains := &fakeDomains{
		hasDomain: map[string]bool{"lead": true},
		stats:     core.KnowledgeDomainStats{Pages: 12, Changed7d: 3, Weeks: [8]int{0, 0, 1, 2, 0, 4, 2, 3}},
	}
	rt := newDomainsRuntime(t, t.TempDir(), domains)
	rt.result.Config.Agents = map[string]core.AgentConfig{"lead": {}, "other": {}}
	rt.result.Config.Teams = map[string]core.TeamConfig{"squad": {Lead: "lead"}}

	available, stats, err := rt.AgentKnowledge(context.Background(), "squad")
	if err != nil || !available || stats.Pages != 12 {
		t.Fatalf("AgentKnowledge(squad) = %v, %+v, %v", available, stats, err)
	}
	available, _, err = rt.AgentKnowledge(context.Background(), "other")
	if err != nil || available {
		t.Fatalf("AgentKnowledge(other) = %v, %v, want false, nil", available, err)
	}
}

// TestKnowledgeStatusCountsSkillProposals covers API-DKN-003: the review
// pending count adds the skill proposals, and scopes is never null even without
// a knowledge service.
func TestKnowledgeStatusCountsSkillProposals(t *testing.T) {
	home := t.TempDir()
	if err := agenthome.ProposeSkill(home, "dana", "payroll", "When payroll runs", "steps", agenthome.SkillProposalMeta{CreatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{home: home}
	status, err := rt.KnowledgeStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewPending != 1 {
		t.Fatalf("review_pending = %d, want 1", status.ReviewPending)
	}
	if status.Scopes == nil {
		t.Fatal("scopes is null")
	}
}

// TestKnowledgePagesPassThrough covers the runtime pass-throughs.
func TestKnowledgePagesPassThrough(t *testing.T) {
	domains := &fakeDomains{pages: []core.KnowledgePageInfo{{Path: "agents/dana/wiki/Gaji.md", Title: "Gaji"}}, pagesTotal: 3}
	rt := newDomainsRuntime(t, t.TempDir(), domains)
	pages, total, err := rt.KnowledgePages(context.Background(), "dana", "gaji", 10)
	if err != nil || total != 3 || len(pages) != 1 || pages[0].Title != "Gaji" {
		t.Fatalf("KnowledgePages = %+v, %d, %v", pages, total, err)
	}
}

func TestReviewItemFromSkill(t *testing.T) {
	item := reviewItemFromSkill(agenthome.SkillProposal{Agent: "dana", Name: "payroll", Description: "d", Body: "b", Exists: true, SessionID: "s1", OwnerTurn: true, CreatedAt: 5})
	if item.ID != "s_dana.payroll" || item.Kind != "skill" || item.Action != "update" || item.From != "dana" {
		t.Fatalf("item = %+v", item)
	}
}

// TestReviewItemsDecided covers API-KGR-003: the decided list maps status,
// result, decided_at, and old_text, newest decided first.
func TestReviewItemsDecided(t *testing.T) {
	domains := &fakeDomains{decided: []core.KnowledgeProposal{
		{ID: "p_2", Agent: "karin", FromAgent: "dana", Title: "Gaji", Status: "applied", Result: "append Gaji", CreatedAt: 100, DecidedAt: 200, Write: core.KnowledgeWrite{Action: "replace", OldText: "gaji lama", NewText: "gaji baru"}},
		{ID: "p_1", Agent: "dana", Title: "Budget", Status: "rejected", DecidedAt: 50, Write: core.KnowledgeWrite{Action: "append", Text: "x"}},
	}}
	rt := newDomainsRuntime(t, t.TempDir(), domains)

	items, err := rt.ReviewItems(context.Background(), "decided")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ID != "p_2" || items[0].Status != "applied" || items[0].Result != "append Gaji" || items[0].DecidedAt != 200 || items[0].OldText != "gaji lama" {
		t.Fatalf("item = %+v", items[0])
	}
	if items[0].Kind != "knowledge" || items[0].From != "dana" {
		t.Fatalf("kind/from = %+v", items[0])
	}
	if items[1].ID != "p_1" || items[1].Status != "rejected" || items[1].OldText != "" {
		t.Fatalf("item = %+v", items[1])
	}
}

// TestReviewItemsPendingOldText covers API-KGR-003: a pending knowledge item
// carries the op's OldText and no decision fields.
func TestReviewItemsPendingOldText(t *testing.T) {
	domains := &fakeDomains{pending: []core.KnowledgeProposal{
		{ID: "p_1", Agent: "rio", FromAgent: "dana", Title: "Gaji", CreatedAt: 5, Write: core.KnowledgeWrite{Action: "supersede", OldText: "gaji lama", NewText: "gaji baru"}},
	}}
	rt := newDomainsRuntime(t, t.TempDir(), domains)

	items, err := rt.ReviewItems(context.Background(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].OldText != "gaji lama" || items[0].Status != "" || items[0].DecidedAt != 0 {
		t.Fatalf("item = %+v", items[0])
	}
}

// TestReviewItemsDecidedUnavailable covers API-KGR-003 without a knowledge
// service.
func TestReviewItemsDecidedUnavailable(t *testing.T) {
	rt := &Runtime{}
	if _, err := rt.ReviewItems(context.Background(), "decided"); !errors.Is(err, ErrKnowledgeUnavailable) {
		t.Fatalf("err = %v, want ErrKnowledgeUnavailable", err)
	}
}

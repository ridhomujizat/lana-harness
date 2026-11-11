package agent

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

// fakeKnowledgeRecall is a core.Knowledge whose Recall applies the recall rule
// approximately: a page qualifies when its title, description, and snippet
// share at least two distinct query tokens (REQ-KNW-050). The rule itself lives
// in the knowledge service, so this fake only stands in for it.
type fakeKnowledgeRecall struct {
	pages       []core.KnowledgeHit
	recallCalls int
	// recallScopes and recallPrefer record the arguments of the last Recall, so
	// the turn's scope choice can be asserted (REQ-DKN-012).
	recallScopes []string
	recallPrefer string
}

func (f *fakeKnowledgeRecall) Search(context.Context, core.KnowledgeQuery) ([]core.KnowledgeHit, error) {
	return nil, nil
}

func (f *fakeKnowledgeRecall) Read(context.Context, string, int, int) (core.KnowledgeDoc, error) {
	return core.KnowledgeDoc{}, nil
}

func (f *fakeKnowledgeRecall) Write(context.Context, core.KnowledgeWrite) (core.KnowledgeWriteResult, error) {
	return core.KnowledgeWriteResult{}, nil
}

func (f *fakeKnowledgeRecall) Ingest(context.Context, core.KnowledgeIngest) (core.KnowledgeIngestResult, error) {
	return core.KnowledgeIngestResult{}, nil
}

func (f *fakeKnowledgeRecall) Recall(_ context.Context, text string, limit int, scopes []string, prefer string, skip func(string) bool) ([]core.KnowledgeHit, error) {
	f.recallCalls++
	f.recallScopes = append([]string(nil), scopes...)
	f.recallPrefer = prefer
	query := knowledgeTokens(text)
	var out []core.KnowledgeHit
	for _, page := range f.pages {
		pageTokens := knowledgeTokens(page.Title + " " + page.Description + " " + page.Snippet)
		if len(knowledgeIntersect(query, pageTokens)) < 2 {
			continue
		}
		if skip != nil && skip(page.Path) {
			continue
		}
		out = append(out, page)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// knowledgeTokens lower-cases text and returns its distinct runs of letters and
// digits of at least two runes.
func knowledgeTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(field)) >= 2 {
			out[field] = true
		}
	}
	return out
}

// knowledgeIntersect returns the tokens in both sets.
func knowledgeIntersect(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for token := range a {
		if b[token] {
			out[token] = true
		}
	}
	return out
}

// fakeDomainRecall is fakeKnowledgeRecall plus core.KnowledgeDomains, so the
// recall scope behavior can be tested (AC-DKN-002).
type fakeDomainRecall struct {
	*fakeKnowledgeRecall
	domains []string
}

func (f *fakeDomainRecall) HasDomain(agent string) bool { return slices.Contains(f.domains, agent) }

func (f *fakeDomainRecall) ReadIn(context.Context, string, string, string, int, int) (core.KnowledgeDoc, error) {
	return core.KnowledgeDoc{}, nil
}

func (f *fakeDomainRecall) Propose(context.Context, core.KnowledgeProposal) (core.KnowledgeProposal, error) {
	return core.KnowledgeProposal{}, nil
}

func (f *fakeDomainRecall) PendingProposals(context.Context) ([]core.KnowledgeProposal, error) {
	return nil, nil
}

func (f *fakeDomainRecall) DecideProposal(context.Context, string, bool) (core.KnowledgeProposal, error) {
	return core.KnowledgeProposal{}, nil
}

func (f *fakeDomainRecall) PromoteSoon(string) {}

func (f *fakeDomainRecall) Pages(context.Context, string, string, int) ([]core.KnowledgePageInfo, int, error) {
	return nil, 0, nil
}

func (f *fakeDomainRecall) Page(context.Context, string) (core.KnowledgeDoc, string, error) {
	return core.KnowledgeDoc{}, "", nil
}

func (f *fakeDomainRecall) DomainStats(context.Context, string, time.Time) (core.KnowledgeDomainStats, error) {
	return core.KnowledgeDomainStats{}, nil
}

func (f *fakeDomainRecall) ScopeStats(context.Context) ([]core.KnowledgeScopeStat, error) {
	return nil, nil
}

func (f *fakeDomainRecall) Graph(context.Context, string, bool, bool) (core.KnowledgeGraph, error) {
	return core.KnowledgeGraph{}, nil
}

func (f *fakeDomainRecall) Activity(context.Context, int, time.Time, string) (core.KnowledgeActivityPage, error) {
	return core.KnowledgeActivityPage{}, nil
}

func (f *fakeDomainRecall) Health(context.Context) (core.KnowledgeHealth, error) {
	return core.KnowledgeHealth{}, nil
}

func (f *fakeDomainRecall) DecidedProposals(context.Context, int) ([]core.KnowledgeProposal, error) {
	return nil, nil
}

// newKnowledgeHarness builds a harness whose agent offers agentTools and whose
// registry has knowledge.
func newKnowledgeHarness(t *testing.T, agentTools []string, knowledge core.Knowledge) *harness {
	t.Helper()
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("one"), textResponse("two"), textResponse("three"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: agentTools}})
	if err := h.tools.Register(tools.NewKnowledgeSearch()); err != nil {
		t.Fatal(err)
	}
	if knowledge != nil {
		h.reg.SetKnowledge(knowledge)
	}
	return h
}

// AC-KNW-007 (REQ-KNW-050): a matching page appears once per session in
// <context>, a one-word overlap does not appear, auto_recall=false turns recall
// off, and recall is skipped when knowledge_search is not offered.
func TestTurnRelevantKnowledgeContext(t *testing.T) {
	page := core.KnowledgeHit{Path: "wiki/Pak Budi.md", Title: "Pak Budi", Description: "Klien utama proyek X"}

	// A message that matches the page by at least two tokens shows a line, once.
	h := newKnowledgeHarness(t, []string{"knowledge_search"}, &fakeKnowledgeRecall{pages: []core.KnowledgeHit{page}})
	h.run(t, "kontrak pak budi")
	first := userText(h.firstUserMessage(t))
	for _, want := range []string{"relevant knowledge:", "- Pak Budi (wiki/Pak Budi.md): Klien utama proyek X"} {
		if !strings.Contains(first, want) {
			t.Fatalf("first user message lacks %q:\n%s", want, first)
		}
	}
	if head := h.systemPrompts(t)[0]; strings.Contains(head, "relevant knowledge:") {
		t.Fatalf("relevant knowledge leaked into the head prompt:\n%s", head)
	}

	// The same page is not shown again in the session.
	h.run(t, "kontrak pak budi")
	if second := userText(h.lastUserMessage(t)); strings.Contains(second, "relevant knowledge:") {
		t.Fatalf("second turn repeated relevant knowledge:\n%s", second)
	}
}

// AC-KNW-007 (REQ-KNW-050): a one-word overlap does not qualify.
func TestTurnRelevantKnowledgeOneWordOverlap(t *testing.T) {
	page := core.KnowledgeHit{Path: "wiki/Pak Budi.md", Title: "Pak Budi", Description: "Klien utama proyek X"}
	h := newKnowledgeHarness(t, []string{"knowledge_search"}, &fakeKnowledgeRecall{pages: []core.KnowledgeHit{page}})
	h.run(t, "pak saja")
	if first := userText(h.firstUserMessage(t)); strings.Contains(first, "relevant knowledge:") {
		t.Fatalf("a one-word overlap must not recall:\n%s", first)
	}
}

// AC-KNW-007 (REQ-KNW-050): auto_recall=false turns recall off.
func TestTurnRelevantKnowledgeAutoRecallOff(t *testing.T) {
	page := core.KnowledgeHit{Path: "wiki/Pak Budi.md", Title: "Pak Budi", Description: "Klien utama proyek X"}
	h := newKnowledgeHarness(t, []string{"knowledge_search"}, &fakeKnowledgeRecall{pages: []core.KnowledgeHit{page}})
	off := false
	h.reg.config.Knowledge.AutoRecall = &off
	h.run(t, "kontrak pak budi")
	if first := userText(h.firstUserMessage(t)); strings.Contains(first, "relevant knowledge:") {
		t.Fatalf("auto_recall=false must not recall:\n%s", first)
	}
}

// AC-KNW-007 (REQ-KNW-050): recall is skipped when knowledge_search is not
// offered in the turn.
func TestTurnRelevantKnowledgeNotOffered(t *testing.T) {
	page := core.KnowledgeHit{Path: "wiki/Pak Budi.md", Title: "Pak Budi", Description: "Klien utama proyek X"}
	fake := &fakeKnowledgeRecall{pages: []core.KnowledgeHit{page}}
	h := newKnowledgeHarness(t, []string{"read_file"}, fake)
	h.run(t, "kontrak pak budi")
	if first := userText(h.firstUserMessage(t)); strings.Contains(first, "relevant knowledge:") {
		t.Fatalf("recall must be skipped without knowledge_search:\n%s", first)
	}
	if fake.recallCalls != 0 {
		t.Fatalf("recall calls = %d, want 0", fake.recallCalls)
	}
}

// AC-DKN-002 (REQ-DKN-012, design §8): recallScopes returns the caller's
// domain and the shared wiki for a domain agent, else the shared wiki alone,
// and prefers the caller's domain on a tie.
func TestRecallScopes(t *testing.T) {
	service := &fakeDomainRecall{fakeKnowledgeRecall: &fakeKnowledgeRecall{}, domains: []string{"dana"}}
	if scopes, prefer := recallScopes(service, "dana"); !slices.Equal(scopes, []string{"dana", "shared"}) || prefer != "dana" {
		t.Fatalf("domain agent = %v prefer %q, want [dana shared] and dana", scopes, prefer)
	}
	if scopes, prefer := recallScopes(service, "rio"); !slices.Equal(scopes, []string{"shared"}) || prefer != "rio" {
		t.Fatalf("agent without domain = %v prefer %q, want [shared] and rio", scopes, prefer)
	}
	if scopes, prefer := recallScopes(&fakeKnowledgeRecall{}, "dana"); !slices.Equal(scopes, []string{"shared"}) || prefer != "dana" {
		t.Fatalf("no domain service = %v prefer %q, want [shared] and dana", scopes, prefer)
	}
}

// AC-DKN-002 (REQ-DKN-012): a turn recalls with the caller's domain and the
// shared wiki, preferring the caller's domain.
func TestTurnRelevantKnowledgeScopes(t *testing.T) {
	page := core.KnowledgeHit{Path: "agents/assistant/wiki/Rapat.md", Title: "Rapat", Scope: "assistant", Description: "Klien utama proyek X"}
	fake := &fakeDomainRecall{fakeKnowledgeRecall: &fakeKnowledgeRecall{pages: []core.KnowledgeHit{page}}, domains: []string{"assistant"}}
	h := newKnowledgeHarness(t, []string{"knowledge_search"}, fake)
	h.run(t, "kontrak pak budi")
	if !slices.Equal(fake.recallScopes, []string{"assistant", "shared"}) || fake.recallPrefer != "assistant" {
		t.Fatalf("recall scopes = %v prefer %q, want [assistant shared] and assistant", fake.recallScopes, fake.recallPrefer)
	}
}

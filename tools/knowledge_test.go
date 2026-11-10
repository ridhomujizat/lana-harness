package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeKnowledge is a core.Knowledge that records calls and returns canned data
// (AC-KNW-006).
type fakeKnowledge struct {
	searchHits  []core.KnowledgeHit
	searchErr   error
	searchCalls []core.KnowledgeQuery

	readDoc          core.KnowledgeDoc
	readErr          error
	readRef          string
	readFrom, readTo int

	writeRes core.KnowledgeWriteResult
	writeErr error
	writeIn  core.KnowledgeWrite
	writeN   int

	ingestRes core.KnowledgeIngestResult
	ingestErr error
	ingestIn  core.KnowledgeIngest
}

func (f *fakeKnowledge) Search(_ context.Context, q core.KnowledgeQuery) ([]core.KnowledgeHit, error) {
	f.searchCalls = append(f.searchCalls, q)
	return f.searchHits, f.searchErr
}

func (f *fakeKnowledge) Read(_ context.Context, ref string, from, to int) (core.KnowledgeDoc, error) {
	f.readRef, f.readFrom, f.readTo = ref, from, to
	return f.readDoc, f.readErr
}

func (f *fakeKnowledge) Write(_ context.Context, w core.KnowledgeWrite) (core.KnowledgeWriteResult, error) {
	f.writeIn = w
	f.writeN++
	return f.writeRes, f.writeErr
}

func (f *fakeKnowledge) Ingest(_ context.Context, in core.KnowledgeIngest) (core.KnowledgeIngestResult, error) {
	f.ingestIn = in
	return f.ingestRes, f.ingestErr
}

func (f *fakeKnowledge) Recall(context.Context, string, int, []string, string, func(string) bool) ([]core.KnowledgeHit, error) {
	return nil, nil
}

// fakeKnowledgeDomains is a core.Knowledge that also implements
// core.KnowledgeDomains, so the scope behavior of the knowledge tools can be
// tested (AC-DKN-002, -003).
type fakeKnowledgeDomains struct {
	fakeKnowledge
	domains []string

	readInDoc   core.KnowledgeDoc
	readInErr   error
	readInRef   string
	readInCall  string
	readInScope string
	// readInByScope, when set, returns a canned doc for one explicit scope and
	// "not found" for the others.
	readInByScope map[string]core.KnowledgeDoc

	proposeRes core.KnowledgeProposal
	proposeErr error
	proposeIn  core.KnowledgeProposal
	proposals  int
}

func (f *fakeKnowledgeDomains) HasDomain(agent string) bool {
	return slices.Contains(f.domains, agent)
}

func (f *fakeKnowledgeDomains) ReadIn(_ context.Context, ref, caller, scope string, from, to int) (core.KnowledgeDoc, error) {
	f.readInRef, f.readInCall, f.readInScope = ref, caller, scope
	if f.readInByScope != nil {
		if doc, ok := f.readInByScope[scope]; ok {
			return doc, nil
		}
		return core.KnowledgeDoc{}, errors.New("knowledge: not found")
	}
	return f.readInDoc, f.readInErr
}

func (f *fakeKnowledgeDomains) Propose(_ context.Context, p core.KnowledgeProposal) (core.KnowledgeProposal, error) {
	f.proposals++
	f.proposeIn = p
	return f.proposeRes, f.proposeErr
}

func (f *fakeKnowledgeDomains) PendingProposals(context.Context) ([]core.KnowledgeProposal, error) {
	return nil, nil
}

func (f *fakeKnowledgeDomains) DecideProposal(context.Context, string, bool) (core.KnowledgeProposal, error) {
	return core.KnowledgeProposal{}, nil
}

func (f *fakeKnowledgeDomains) PromoteSoon(string) {}

func (f *fakeKnowledgeDomains) Pages(context.Context, string, string, int) ([]core.KnowledgePageInfo, int, error) {
	return nil, 0, nil
}

func (f *fakeKnowledgeDomains) Page(context.Context, string) (core.KnowledgeDoc, string, error) {
	return core.KnowledgeDoc{}, "", nil
}

func (f *fakeKnowledgeDomains) DomainStats(context.Context, string, time.Time) (core.KnowledgeDomainStats, error) {
	return core.KnowledgeDomainStats{}, nil
}

func (f *fakeKnowledgeDomains) ScopeStats(context.Context) ([]core.KnowledgeScopeStat, error) {
	return nil, nil
}

func callKnowledgeTool(t *testing.T, tool core.Tool, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// AC-KNW-006 (REQ-KNW-040): the four tools carry the right names, risks, and a
// guidance note on knowledge_search only.
func TestKnowledgeToolSpecs(t *testing.T) {
	cases := []struct {
		name     string
		tool     core.Tool
		risk     core.Risk
		guidance bool
	}{
		{"knowledge_search", NewKnowledgeSearch(), core.RiskRead, true},
		{"knowledge_read", NewKnowledgeRead(), core.RiskRead, false},
		{"knowledge_write", NewKnowledgeWrite(), core.RiskMemory, false},
		{"knowledge_ingest", NewKnowledgeIngest(), core.RiskMemory, false},
	}
	for _, tc := range cases {
		if spec := tc.tool.Spec(); spec.Name != tc.name {
			t.Fatalf("name = %q, want %q", spec.Name, tc.name)
		}
		if risk := tc.tool.Risk(nil); risk != tc.risk {
			t.Fatalf("%s risk = %q, want %q", tc.name, risk, tc.risk)
		}
		_, ok := tc.tool.(core.ToolGuidance)
		if ok != tc.guidance {
			t.Fatalf("%s ToolGuidance = %v, want %v", tc.name, ok, tc.guidance)
		}
	}
	guidance := NewKnowledgeSearch().(core.ToolGuidance).PromptGuidance()
	for _, want := range []string{"knowledge_search before answering about people, projects", "knowledge_write", "knowledge_ingest", "memory_write for short facts", "search scope=agent:<name> to look into another agent's field"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance lacks %q:\n%s", want, guidance)
		}
	}
}

// AC-KNW-006 (REQ-KNW-040..042): the search schema requires query and the
// search forwards kind, type, and a clamped limit.
func TestKnowledgeSearchForwarding(t *testing.T) {
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(NewKnowledgeSearch().Spec().InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Fatalf("required = %v, want [query]", schema.Required)
	}

	fake := &fakeKnowledge{}
	out := callKnowledgeTool(t, NewKnowledgeSearch(), core.ToolCtx{Knowledge: fake}, `{"query":"pak budi","kind":"page","type":"person","limit":50}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q output = %q", out.Status, out.Output)
	}
	if out.Output != `Nothing in knowledge matched "pak budi".` {
		t.Fatalf("no-hit output = %q", out.Output)
	}
	if len(fake.searchCalls) != 1 {
		t.Fatalf("search calls = %d, want 1", len(fake.searchCalls))
	}
	got := fake.searchCalls[0]
	if got.Text != "pak budi" || got.Kind != "page" || got.Type != "person" || got.Limit != knowledgeSearchLimitMax {
		t.Fatalf("search query = %+v", got)
	}
}

// AC-KNW-006 (REQ-KNW-032, REQ-DKN-010): the search renders the described
// result format with each hit's scope in brackets.
func TestKnowledgeSearchOutput(t *testing.T) {
	fake := &fakeKnowledge{searchHits: []core.KnowledgeHit{
		{
			Path: "wiki/Pak Budi.md", Scope: "shared", Type: "person", Description: "Klien utama proyek X",
			Updated: "2026-10-30", Heading: "Kontrak", StartLine: 14,
			Snippet: "… «kontrak» ditandatangani 12 Okt …",
		},
		{Path: "agents/dana/wiki/Gaji.md", Scope: "dana", Type: "topic", StartLine: 40, Snippet: "…"},
	}}
	out := callKnowledgeTool(t, NewKnowledgeSearch(), core.ToolCtx{Knowledge: fake}, `{"query":"kontrak pak budi"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q output = %q", out.Status, out.Output)
	}
	want := "Found 2 results for \"kontrak pak budi\":\n" +
		"1. wiki/Pak Budi.md [shared] — person — Klien utama proyek X (updated 2026-10-30)\n" +
		"   ## Kontrak, line 14: … «kontrak» ditandatangani 12 Okt …\n" +
		"2. agents/dana/wiki/Gaji.md [dana] — topic, line 40: …\n" +
		"Read a file with knowledge_read path=<path>."
	if out.Output != want {
		t.Fatalf("output:\n%s\n--- want:\n%s", out.Output, want)
	}
}

func TestKnowledgeSearchInputErrors(t *testing.T) {
	fake := &fakeKnowledge{}
	if out := callKnowledgeTool(t, NewKnowledgeSearch(), core.ToolCtx{Knowledge: fake}, `{}`); out.Status != core.ToolStatusError || !strings.Contains(out.Output, "query is required") {
		t.Fatalf("missing query output = %+v", out)
	}
	if out := callKnowledgeTool(t, NewKnowledgeSearch(), core.ToolCtx{Knowledge: fake}, `{"query":"x","kind":"bogus"}`); out.Status != core.ToolStatusError || !strings.Contains(out.Output, "kind must be") {
		t.Fatalf("bad kind output = %+v", out)
	}
}

// AC-KNW-006 (REQ-KNW-041): knowledge_read numbers lines and marks the rest.
func TestKnowledgeRead(t *testing.T) {
	fake := &fakeKnowledge{readDoc: core.KnowledgeDoc{
		Path: "wiki/Pak Budi.md", From: 1, To: 2, Total: 4,
		Lines: []string{"line one", "line two"},
	}}
	out := callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake}, `{"path":"wiki/Pak Budi.md"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q output = %q", out.Status, out.Output)
	}
	want := fmt.Sprintf("%6d\t%s\n%6d\t%s\n[more: from_line=3]", 1, "line one", 2, "line two")
	if out.Output != want {
		t.Fatalf("output:\n%q\n--- want:\n%q", out.Output, want)
	}
	if fake.readRef != "wiki/Pak Budi.md" || fake.readFrom != 1 || fake.readTo != 0 {
		t.Fatalf("read call = ref %q from %d to %d", fake.readRef, fake.readFrom, fake.readTo)
	}

	// A title is used when path is empty.
	callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake}, `{"title":"Pak Budi","from_line":3}`)
	if fake.readRef != "Pak Budi" || fake.readFrom != 3 {
		t.Fatalf("title read = ref %q from %d", fake.readRef, fake.readFrom)
	}
}

func TestKnowledgeReadInputErrors(t *testing.T) {
	fake := &fakeKnowledge{}
	if out := callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake}, `{}`); out.Status != core.ToolStatusError || !strings.Contains(out.Output, "path or title is required") {
		t.Fatalf("no ref output = %+v", out)
	}
	if out := callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake}, `{"path":"x","from_line":5,"to_line":2}`); out.Status != core.ToolStatusError || !strings.Contains(out.Output, "to_line must be at least from_line") {
		t.Fatalf("bad range output = %+v", out)
	}
}

// AC-KNW-006 (REQ-KNW-010): knowledge_write checks the per-action fields and
// renders the applied change with the vault verbs.
func TestKnowledgeWriteActions(t *testing.T) {
	// Required-field errors.
	bad := []struct {
		input string
		want  string
	}{
		{`{"action":"create","title":"Pak Budi"}`, "body is required for create"},
		{`{"action":"append","title":"Pak Budi"}`, "text is required for append"},
		{`{"action":"replace","title":"Pak Budi","old_text":"a"}`, "new_text is required for replace"},
		{`{"action":"replace","title":"Pak Budi","new_text":"b"}`, "old_text is required for replace"},
		{`{"action":"supersede","title":"Pak Budi","new_text":"b"}`, "old_text is required for supersede"},
		{`{"action":"supersede","title":"Pak Budi","old_text":"a"}`, "new_text is required for supersede"},
		{`{"action":"meta","title":"Pak Budi"}`, "metadata is required for meta"},
		{`{"action":"nope","title":"Pak Budi"}`, "action must be create, append, replace, meta, or supersede"},
		{`{"action":"create","body":"x"}`, "title is required"},
	}
	for _, c := range bad {
		out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: &fakeKnowledge{}}, c.input)
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, c.want) {
			t.Fatalf("input %s output = %+v, want %q", c.input, out, c.want)
		}
	}

	// Applied-change messages.
	ops := []struct {
		input string
		op    string
		want  string
	}{
		{`{"action":"create","title":"Pak Budi","body":"hi"}`, "create", "created wiki/Pak Budi.md (1234 characters)"},
		{`{"action":"append","title":"Pak Budi","text":"hi"}`, "append", "appended to wiki/Pak Budi.md (1234 characters)"},
		{`{"action":"replace","title":"Pak Budi","old_text":"a","new_text":"b"}`, "replace", "replaced text in wiki/Pak Budi.md (1234 characters)"},
		{`{"action":"supersede","title":"Pak Budi","old_text":"a","new_text":"b"}`, "supersede", "superseded text in wiki/Pak Budi.md (1234 characters)"},
		{`{"action":"meta","title":"Pak Budi","description":"d"}`, "meta", "updated details of wiki/Pak Budi.md (1234 characters)"},
	}
	for _, c := range ops {
		fake := &fakeKnowledge{writeRes: core.KnowledgeWriteResult{Path: "wiki/Pak Budi.md", Op: c.op, Size: 1234}}
		out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "lana"}, c.input)
		if out.Status != core.ToolStatusSuccess || out.Output != c.want {
			t.Fatalf("op %s output = %q, want %q", c.op, out.Output, c.want)
		}
		if fake.writeIn.Actor != "agent lana" {
			t.Fatalf("actor = %q, want %q", fake.writeIn.Actor, "agent lana")
		}
	}
}

// AC-KNW-006 (REQ-KNW-042, -043): knowledge_ingest needs exactly one source,
// resolves the path inside the workspace, and renders the saved/duplicate
// messages.
func TestKnowledgeIngest(t *testing.T) {
	ws := t.TempDir()

	// Exactly one of path, url, text.
	none := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws}, `{}`)
	if none.Status != core.ToolStatusError || !strings.Contains(none.Output, "provide exactly one of path, url, or text") {
		t.Fatalf("none output = %+v", none)
	}
	two := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws}, `{"text":"a","url":"https://example.com"}`)
	if two.Status != core.ToolStatusError || !strings.Contains(two.Output, "provide exactly one of path, url, or text") {
		t.Fatalf("two output = %+v", two)
	}

	// A path that escapes the workspace is rejected.
	escape := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws, WorkspaceOnly: true}, `{"path":"../secret.txt"}`)
	if escape.Status != core.ToolStatusRejected {
		t.Fatalf("escape output = %+v", escape)
	}

	// A symlink that points outside the workspace is rejected.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link.txt")); err != nil {
		t.Fatal(err)
	}
	link := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws, WorkspaceOnly: true}, `{"path":"link.txt"}`)
	if link.Status != core.ToolStatusRejected {
		t.Fatalf("symlink output = %+v", link)
	}

	// An unsupported extension is refused.
	if err := os.WriteFile(filepath.Join(ws, "a.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws}, `{"path":"a.bin"}`)
	if bad.Status != core.ToolStatusError || !strings.Contains(bad.Output, "unsupported file type") {
		t.Fatalf("bad ext output = %+v", bad)
	}

	// A markdown file ingests with its base name as the title.
	if err := os.WriteFile(filepath.Join(ws, "notes.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeKnowledge{ingestRes: core.KnowledgeIngestResult{Path: "sources/2026-11-01 notes.md", Queued: true}}
	out := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: fake, Workspace: ws, AgentName: "lana", SessionID: "s1", TurnID: "t1"}, `{"path":"notes.md"}`)
	want := "Saved sources/2026-11-01 notes.md. Compiling it into wiki pages in the background; they appear in knowledge_search when done."
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("md output = %q, want %q", out.Output, want)
	}
	if fake.ingestIn.Ext != "md" || fake.ingestIn.Title != "notes" || string(fake.ingestIn.Data) != "hello" {
		t.Fatalf("md ingest = %+v", fake.ingestIn)
	}
	if fake.ingestIn.AgentName != "lana" || fake.ingestIn.SessionID != "s1" || fake.ingestIn.TurnID != "t1" {
		t.Fatalf("md ingest identity = %+v", fake.ingestIn)
	}

	// Duplicate messages.
	dup := &fakeKnowledge{ingestRes: core.KnowledgeIngestResult{Path: "sources/2026-11-01 notes.md", Duplicate: true}}
	if out := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: dup, Workspace: ws}, `{"path":"notes.md"}`); out.Output != "Already saved as sources/2026-11-01 notes.md." {
		t.Fatalf("duplicate output = %q", out.Output)
	}
	dup.ingestRes.Queued = true
	if out := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: dup, Workspace: ws}, `{"path":"notes.md"}`); out.Output != "Already saved as sources/2026-11-01 notes.md. Compiling again." {
		t.Fatalf("duplicate queued output = %q", out.Output)
	}
}

// AC-KNW-006 (REQ-KNW-020): text ingests as markdown with the first line as the
// default title, HTML is converted, and a URL stores its source.
func TestKnowledgeIngestConversions(t *testing.T) {
	ws := t.TempDir()

	text := &fakeKnowledge{ingestRes: core.KnowledgeIngestResult{Path: "sources/x.md", Queued: true}}
	callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: text, Workspace: ws}, `{"text":"First line\nsecond line"}`)
	if text.ingestIn.Ext != "md" || text.ingestIn.Title != "First line" || string(text.ingestIn.Data) != "First line\nsecond line" {
		t.Fatalf("text ingest = %+v", text.ingestIn)
	}

	if err := os.WriteFile(filepath.Join(ws, "page.html"), []byte("<html><head><title>W</title></head><body><h1>Hi</h1></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	html := &fakeKnowledge{ingestRes: core.KnowledgeIngestResult{Path: "sources/x.md", Queued: true}}
	callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: html, Workspace: ws}, `{"path":"page.html"}`)
	if html.ingestIn.Ext != "md" || html.ingestIn.Title != "page" {
		t.Fatalf("html ingest = %+v", html.ingestIn)
	}
	if data := string(html.ingestIn.Data); !strings.Contains(data, "Hi") || strings.Contains(data, "<h1>") {
		t.Fatalf("html not converted:\n%s", data)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Rapat Q4</title></head><body><p>hello</p></body></html>"))
	}))
	defer server.Close()

	web := &fakeKnowledge{ingestRes: core.KnowledgeIngestResult{Path: "sources/x.md", Queued: true}}
	callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: web, Workspace: ws}, fmt.Sprintf(`{"url":%q}`, server.URL))
	if web.ingestIn.Ext != "md" || web.ingestIn.Title != "Rapat Q4" || web.ingestIn.SourceURL != server.URL {
		t.Fatalf("url ingest = %+v", web.ingestIn)
	}
	data := string(web.ingestIn.Data)
	if !strings.HasPrefix(data, "---\nsource_url: ") || !strings.Contains(data, "hello") {
		t.Fatalf("url data =\n%s", data)
	}
}

// knowledge_ingest is memory unless the input fetches a URL, which needs the
// same owner approval as any other network access (REQ-KNW-040).
func TestKnowledgeIngestRisk(t *testing.T) {
	tool := NewKnowledgeIngest()
	if got := tool.Risk(json.RawMessage(`{"url":"https://example.com"}`)); got != core.RiskNetwork {
		t.Fatalf("url risk = %q, want %q", got, core.RiskNetwork)
	}
	for _, input := range []string{`{"text":"x"}`, `{"path":"notes.md"}`, `{"url":""}`, `{}`, `not-json`} {
		if got := tool.Risk(json.RawMessage(input)); got != core.RiskMemory {
			t.Fatalf("risk(%s) = %q, want %q", input, got, core.RiskMemory)
		}
	}
}

// Every knowledge tool reports a missing service.
func TestKnowledgeUnavailable(t *testing.T) {
	cases := []struct {
		tool  core.Tool
		input string
	}{
		{NewKnowledgeSearch(), `{"query":"x"}`},
		{NewKnowledgeRead(), `{"path":"x"}`},
		{NewKnowledgeWrite(), `{"action":"create","title":"t","body":"b"}`},
		{NewKnowledgeIngest(), `{"text":"x"}`},
	}
	for _, c := range cases {
		out := callKnowledgeTool(t, c.tool, core.ToolCtx{}, c.input)
		if out.Status != core.ToolStatusError || out.Output != "knowledge is not available" {
			t.Fatalf("%s output = %+v", c.tool.Spec().Name, out)
		}
	}
}

// AC-DKN-002 (REQ-DKN-010, API-DKN-001): the search scope maps to the right
// index scopes and reports the documented errors.
func TestKnowledgeSearchScopes(t *testing.T) {
	cases := []struct {
		name       string
		caller     string
		domains    []string
		noService  bool
		scope      string
		wantScopes []string
		wantErr    string
	}{
		{"default mine", "dana", []string{"dana", "rio"}, false, "", []string{"dana", "shared"}, ""},
		{"mine", "dana", []string{"dana"}, false, "mine", []string{"dana", "shared"}, ""},
		{"domain", "dana", []string{"dana"}, false, "domain", []string{"dana"}, ""},
		{"shared", "dana", []string{"dana"}, false, "shared", []string{"shared"}, ""},
		{"all", "dana", []string{"dana"}, false, "all", nil, ""},
		{"agent other", "dana", []string{"dana", "rio"}, false, "agent:rio", []string{"rio"}, ""},
		{"mine without domain", "dana", []string{"rio"}, false, "mine", []string{"shared"}, ""},
		{"domain without domain", "dana", []string{"rio"}, false, "domain", nil, "you have no knowledge domain; use scope shared"},
		{"unknown agent", "dana", []string{"dana"}, false, "agent:ghost", nil, `no knowledge domain for agent "ghost"`},
		{"invalid", "dana", []string{"dana"}, false, "bogus", nil, "invalid input: scope must be mine, domain, shared, all, or agent:<name>"},
		{"no service mine", "dana", nil, true, "mine", []string{"shared"}, ""},
		{"no service agent", "dana", nil, true, "agent:rio", nil, `no knowledge domain for agent "rio"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var (
				fake   core.Knowledge
				search *[]core.KnowledgeQuery
			)
			if c.noService {
				f := &fakeKnowledge{}
				fake, search = f, &f.searchCalls
			} else {
				f := &fakeKnowledgeDomains{domains: c.domains}
				fake, search = f, &f.searchCalls
			}
			out := callKnowledgeTool(t, NewKnowledgeSearch(), core.ToolCtx{Knowledge: fake, AgentName: c.caller}, fmt.Sprintf(`{"query":"x","scope":%q}`, c.scope))
			if c.wantErr != "" {
				if out.Status != core.ToolStatusError || !strings.Contains(out.Output, c.wantErr) {
					t.Fatalf("output = %+v, want error %q", out, c.wantErr)
				}
				return
			}
			if out.Status != core.ToolStatusSuccess {
				t.Fatalf("status = %q output = %q", out.Status, out.Output)
			}
			if len(*search) != 1 {
				t.Fatalf("search calls = %d, want 1", len(*search))
			}
			if got := (*search)[0].Scopes; !slices.Equal(got, c.wantScopes) {
				t.Fatalf("scopes = %v, want %v", got, c.wantScopes)
			}
		})
	}
}

// AC-DKN-003 (REQ-DKN-020): a create without a scope goes by page type; a
// caller without a domain writes to shared only.
func TestKnowledgeWriteDefaultScopes(t *testing.T) {
	cases := []struct {
		name      string
		domains   []string
		noService bool
		input     string
		wantScope string
	}{
		{"shared type", []string{"dana"}, false, `{"action":"create","title":"Pak Budi","type":"person","body":"b"}`, "shared"},
		{"domain type", []string{"dana"}, false, `{"action":"create","title":"Gaji","type":"topic","body":"b"}`, "dana"},
		{"no service", nil, true, `{"action":"create","title":"Gaji","type":"topic","body":"b"}`, "shared"},
		{"caller without domain", []string{"rio"}, false, `{"action":"create","title":"Gaji","type":"topic","body":"b"}`, "shared"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var (
				fake core.Knowledge
				w    *core.KnowledgeWrite
				n    *int
			)
			if c.noService {
				f := &fakeKnowledge{}
				fake, w, n = f, &f.writeIn, &f.writeN
			} else {
				f := &fakeKnowledgeDomains{domains: c.domains}
				fake, w, n = f, &f.writeIn, &f.writeN
			}
			out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, c.input)
			if out.Status != core.ToolStatusSuccess {
				t.Fatalf("status = %q output = %q", out.Status, out.Output)
			}
			if *n != 1 || w.Scope != c.wantScope {
				t.Fatalf("write = %+v (calls %d), want scope %q", *w, *n, c.wantScope)
			}
		})
	}
}

// AC-DKN-003 (REQ-DKN-020): an append or replace without a scope lands in the
// caller's domain when the page is there, else in the shared wiki.
func TestKnowledgeWriteScopeLookup(t *testing.T) {
	domain := &fakeKnowledgeDomains{
		domains:       []string{"dana"},
		readInByScope: map[string]core.KnowledgeDoc{"dana": {Path: "agents/dana/wiki/Gaji.md", Scope: "dana"}},
	}
	out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: domain, AgentName: "dana"}, `{"action":"append","title":"Gaji","text":"x"}`)
	if out.Status != core.ToolStatusSuccess || domain.writeIn.Scope != "dana" {
		t.Fatalf("domain append = %+v scope %q", out, domain.writeIn.Scope)
	}

	shared := &fakeKnowledgeDomains{
		domains:       []string{"dana"},
		readInByScope: map[string]core.KnowledgeDoc{"shared": {Path: "wiki/Gaji.md", Scope: "shared"}},
	}
	out = callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: shared, AgentName: "dana"}, `{"action":"replace","title":"Gaji","old_text":"a","new_text":"b"}`)
	if out.Status != core.ToolStatusSuccess || shared.writeIn.Scope != "shared" {
		t.Fatalf("shared replace = %+v scope %q", out, shared.writeIn.Scope)
	}
}

// AC-DKN-003 (API-DKN-001): scope=shared is always allowed, scope=mine without
// a domain is refused, and a bad scope is an input error.
func TestKnowledgeWriteScopeErrors(t *testing.T) {
	fake := &fakeKnowledgeDomains{domains: []string{"rio"}}
	out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, `{"action":"create","title":"Gaji","type":"topic","body":"b","scope":"mine"}`)
	if out.Status != core.ToolStatusError || out.Output != "you have no knowledge domain; use scope shared" {
		t.Fatalf("mine without domain = %+v", out)
	}
	out = callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, `{"action":"create","title":"Gaji","type":"topic","body":"b","scope":"bogus"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "invalid input: scope must be mine, shared, or agent:<name>") {
		t.Fatalf("bad scope = %+v", out)
	}
	out = callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, `{"action":"create","title":"Gaji","type":"topic","body":"b","scope":"shared"}`)
	if out.Status != core.ToolStatusSuccess || fake.writeIn.Scope != "shared" {
		t.Fatalf("shared scope = %+v", out)
	}
}

// AC-DKN-003 (REQ-DKN-021, API-DKN-001): a write to another agent's domain
// files a proposal with the turn's identity and does not change a file.
func TestKnowledgeWriteProposal(t *testing.T) {
	fake := &fakeKnowledgeDomains{
		domains:    []string{"dana", "rio"},
		proposeRes: core.KnowledgeProposal{ID: "p_01JC"},
	}
	out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{
		Knowledge: fake, AgentName: "dana", SessionID: "s1", TurnID: "t1", OwnerTurn: true,
	}, `{"action":"create","title":"Gaji","type":"topic","body":"b","scope":"agent:rio"}`)
	want := "proposed to rio as p_01JC; rio applies it at its next promotion"
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("proposal output = %+v, want %q", out, want)
	}
	if fake.proposals != 1 || fake.writeN != 0 {
		t.Fatalf("proposals = %d write calls = %d, want 1 and 0", fake.proposals, fake.writeN)
	}
	p := fake.proposeIn
	if p.Agent != "rio" || p.FromAgent != "dana" || p.Title != "Gaji" || !p.OwnerTurn || p.SessionID != "s1" || p.TurnID != "t1" {
		t.Fatalf("proposal = %+v", p)
	}
	if p.Write.Scope != "rio" || p.Write.Action != "create" || p.Write.Title != "Gaji" {
		t.Fatalf("proposal write = %+v", p.Write)
	}

	// A caller proposing to itself writes its own domain instead.
	fake.proposeIn, fake.proposals = core.KnowledgeProposal{}, 0
	fake.writeIn = core.KnowledgeWrite{}
	out = callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, `{"action":"create","title":"Gaji","type":"topic","body":"b","scope":"agent:dana"}`)
	if out.Status != core.ToolStatusSuccess || fake.proposals != 0 || fake.writeIn.Scope != "dana" {
		t.Fatalf("self propose = %+v proposals %d write %+v", out, fake.proposals, fake.writeIn)
	}
}

// AC-DKN-004 (REQ-DKN-025): supersede passes its source to the write.
func TestKnowledgeWriteSupersedeSource(t *testing.T) {
	fake := &fakeKnowledgeDomains{domains: []string{"dana"}}
	out := callKnowledgeTool(t, NewKnowledgeWrite(), core.ToolCtx{Knowledge: fake, AgentName: "dana"},
		`{"action":"supersede","title":"Gaji","old_text":"Lama","new_text":"Baru","source":"Rapat Q4"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q output = %q", out.Status, out.Output)
	}
	if fake.writeIn.Action != "supersede" || fake.writeIn.Source != "Rapat Q4" {
		t.Fatalf("write = %+v", fake.writeIn)
	}
}

// AC-DKN-002 (REQ-DKN-011): a title read through knowledge_read calls ReadIn
// with the caller and the explicit scope; a path read ignores the scope.
func TestKnowledgeReadScope(t *testing.T) {
	fake := &fakeKnowledgeDomains{domains: []string{"dana", "rio"}}
	fake.readInDoc = core.KnowledgeDoc{Path: "agents/dana/wiki/Gaji.md", Scope: "dana", From: 1, Total: 1, Lines: []string{"x"}}
	cases := []struct {
		scope     string
		wantScope string
	}{
		{"", ""},
		{"mine", "dana"},
		{"domain", "dana"},
		{"shared", "shared"},
		{"all", ""},
		{"agent:rio", "rio"},
	}
	for _, c := range cases {
		fake.readInScope = "unset"
		out := callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, fmt.Sprintf(`{"title":"Gaji","scope":%q}`, c.scope))
		if out.Status != core.ToolStatusSuccess {
			t.Fatalf("scope %q status = %q output = %q", c.scope, out.Status, out.Output)
		}
		if fake.readInRef != "Gaji" || fake.readInCall != "dana" || fake.readInScope != c.wantScope {
			t.Fatalf("scope %q ReadIn ref=%q caller=%q scope=%q, want scope %q", c.scope, fake.readInRef, fake.readInCall, fake.readInScope, c.wantScope)
		}
	}

	// A path ref is read as is and ignores the scope.
	callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, `{"path":"wiki/x.md","scope":"bogus"}`)
	if fake.readRef != "wiki/x.md" {
		t.Fatalf("path read ref = %q", fake.readRef)
	}

	// An invalid scope with a title is refused.
	out := callKnowledgeTool(t, NewKnowledgeRead(), core.ToolCtx{Knowledge: fake, AgentName: "dana"}, `{"title":"Gaji","scope":"bogus"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "invalid input: scope must be mine, domain, shared, all, or agent:<name>") {
		t.Fatalf("bad scope = %+v", out)
	}
}

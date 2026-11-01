package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	return f.writeRes, f.writeErr
}

func (f *fakeKnowledge) Ingest(_ context.Context, in core.KnowledgeIngest) (core.KnowledgeIngestResult, error) {
	f.ingestIn = in
	return f.ingestRes, f.ingestErr
}

func (f *fakeKnowledge) Recall(context.Context, string, int, func(string) bool) ([]core.KnowledgeHit, error) {
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
	for _, want := range []string{"knowledge_search before answering about people, projects", "knowledge_write", "knowledge_ingest", "memory_write for short facts"} {
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

// AC-KNW-006 (REQ-KNW-032): the search renders the described result format.
func TestKnowledgeSearchOutput(t *testing.T) {
	fake := &fakeKnowledge{searchHits: []core.KnowledgeHit{
		{
			Path: "wiki/Pak Budi.md", Type: "person", Description: "Klien utama proyek X",
			Updated: "2026-10-30", Heading: "Kontrak", StartLine: 14,
			Snippet: "… «kontrak» ditandatangani 12 Okt …",
		},
		{Path: "sources/2026-10-30 Kontrak PT X.pdf", Type: "source", StartLine: 40, Snippet: "…"},
	}}
	out := callKnowledgeTool(t, NewKnowledgeSearch(), core.ToolCtx{Knowledge: fake}, `{"query":"kontrak pak budi"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q output = %q", out.Status, out.Output)
	}
	want := "Found 2 results for \"kontrak pak budi\":\n" +
		"1. wiki/Pak Budi.md — person — Klien utama proyek X (updated 2026-10-30)\n" +
		"   ## Kontrak, line 14: … «kontrak» ditandatangani 12 Okt …\n" +
		"2. sources/2026-10-30 Kontrak PT X.pdf — source, line 40: …\n" +
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
		{`{"action":"meta","title":"Pak Budi"}`, "metadata is required for meta"},
		{`{"action":"nope","title":"Pak Budi"}`, "action must be create, append, replace, or meta"},
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
	escape := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws}, `{"path":"../secret.txt"}`)
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
	link := callKnowledgeTool(t, NewKnowledgeIngest(), core.ToolCtx{Knowledge: &fakeKnowledge{}, Workspace: ws}, `{"path":"link.txt"}`)
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

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeSessionSearcher is a core.SessionSearcher that returns canned results
// and records the calls it receives.
type fakeSessionSearcher struct {
	hits      []core.TextHit
	hitErr    error
	info      core.SessionInfo
	rows      []core.TextRow
	windowErr error

	queries []core.TextQuery
	windows []sessionWindowCall
}

type sessionWindowCall struct {
	sessionID string
	aroundSeq int64
	radius    int
}

func (f *fakeSessionSearcher) SearchText(_ context.Context, q core.TextQuery) ([]core.TextHit, error) {
	f.queries = append(f.queries, q)
	return f.hits, f.hitErr
}

func (f *fakeSessionSearcher) TextWindow(_ context.Context, sessionID string, aroundSeq int64, radius int) (core.SessionInfo, []core.TextRow, error) {
	f.windows = append(f.windows, sessionWindowCall{sessionID: sessionID, aroundSeq: aroundSeq, radius: radius})
	return f.info, f.rows, f.windowErr
}

func callSessionSearch(t *testing.T, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := NewSessionSearch().Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// AC-SSR-003 (REQ-SSR-011, -012, -014, -015): discovery forwards the query to
// the searcher and renders the ranked hits, the first with its around rows.
func TestSessionSearchDiscovery(t *testing.T) {
	ts := time.Date(2026, 10, 28, 14, 2, 0, 0, time.Local).UnixMilli()
	fake := &fakeSessionSearcher{hits: []core.TextHit{
		{
			Session: core.SessionInfo{ID: "s1", Agent: "lana", Channel: "telegram", Title: "Deploy staging"},
			Row:     core.TextRow{SessionID: "s1", Seq: 142, Role: "user", CreatedAt: ts, Text: "deploy staging failed"},
			Snippet: "«deploy» ke «staging» gagal",
			Around: []core.TextRow{
				{SessionID: "s1", Seq: 140, Role: "user", CreatedAt: ts, Text: "deploy staging plan"},
				{SessionID: "s1", Seq: 142, Role: "user", CreatedAt: ts, Text: "deploy staging failed"},
				{SessionID: "s1", Seq: 143, Role: "assistant", CreatedAt: ts, Text: "I will look"},
			},
		},
		{
			Session: core.SessionInfo{ID: "s2", Agent: "lana", Channel: "web", Title: "Untitled"},
			Row:     core.TextRow{SessionID: "s2", Seq: 18, Role: "assistant", CreatedAt: ts, Text: "staging"},
			Snippet: "… «staging» …",
		},
	}}

	out := callSessionSearch(t, core.ToolCtx{SessionID: "caller", Sessions: fake}, `{"query":"deploy staging"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q, output = %q", out.Status, out.Output)
	}
	want := "Found 2 sessions for \"deploy staging\":\n\n" +
		"1. session s1 (lana, telegram, \"Deploy staging\"), 2026-10-28 14:02\n" +
		"   seq 140 owner: deploy staging plan\n" +
		" > seq 142 owner: «deploy» ke «staging» gagal\n" +
		"   seq 143 lana: I will look\n" +
		"2. session s2 (lana, web, \"Untitled\"), 2026-10-28 14:02\n" +
		" > seq 18 lana: … «staging» …\n" +
		"\nRead more with session_search session_id=s1 around_seq=142.\n"
	if out.Output != want {
		t.Fatalf("output =\n%q\nwant\n%q", out.Output, want)
	}

	if len(fake.queries) != 1 {
		t.Fatalf("SearchText called %d times, want 1", len(fake.queries))
	}
	q := fake.queries[0]
	if strings.Join(q.Terms, ",") != "deploy,staging" {
		t.Fatalf("terms = %v, want [deploy staging]", q.Terms)
	}
	if q.Agent != "" || q.CallerSession != "caller" || q.Limit != 5 || q.Context != 3 {
		t.Fatalf("query = %+v, want empty agent, caller session, limit 5, context 3", q)
	}
}

// REQ-SSR-014: the agent filter is forwarded to the searcher.
func TestSessionSearchAgentFilter(t *testing.T) {
	fake := &fakeSessionSearcher{}
	callSessionSearch(t, core.ToolCtx{SessionID: "caller", Sessions: fake}, `{"query":"deploy","agent":"coder"}`)
	if len(fake.queries) != 1 || fake.queries[0].Agent != "coder" {
		t.Fatalf("queries = %+v, want the agent filter coder", fake.queries)
	}
}

// REQ-SSR-011: the tokenizer lower-cases, drops stop words and one-rune tokens,
// deduplicates, and keeps the first 12 terms.
func TestSessionSearchTokenizing(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"lower case words", "Deploy STAGING", []string{"deploy", "staging"}},
		{"drops stop words", "the deploy and staging", []string{"deploy", "staging"}},
		{"drops one-rune tokens", "deploy a b staging x", []string{"deploy", "staging"}},
		{"deduplicates", "deploy deploy staging", []string{"deploy", "staging"}},
		{"keeps digits and splits on punctuation", "v2 deploy-staging", []string{"v2", "deploy", "staging"}},
		{"punctuation only", `"(*`, nil},
		{
			"first twelve",
			"t00 t01 t02 t03 t04 t05 t06 t07 t08 t09 t10 t11 t12 t13",
			[]string{"t00", "t01", "t02", "t03", "t04", "t05", "t06", "t07", "t08", "t09", "t10", "t11"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSessionSearcher{}
			out := callSessionSearch(t, core.ToolCtx{Sessions: fake}, fmt.Sprintf(`{"query":%q}`, tc.query))
			if out.Status != core.ToolStatusSuccess {
				t.Fatalf("status = %q, output = %q", out.Status, out.Output)
			}
			var got []string
			if len(fake.queries) == 1 {
				got = fake.queries[0].Terms
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("terms = %v, want %v", got, tc.want)
			}
		})
	}
}

// AC-SSR-004 (REQ-SSR-016): a punctuation-only query does not error and returns
// the no-match message.
func TestSessionSearchNoMatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fake  *fakeSessionSearcher
		input string
		want  string
	}{
		{"no tokens", &fakeSessionSearcher{}, `{"query":"(*"}`, "No past conversation matched \"(*\"."},
		{"no hits", &fakeSessionSearcher{}, `{"query":"deploy"}`, "No past conversation matched \"deploy\"."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := callSessionSearch(t, core.ToolCtx{Sessions: tc.fake}, tc.input)
			if out.Status != core.ToolStatusSuccess || out.Output != tc.want {
				t.Fatalf("out = %+v, want %q", out, tc.want)
			}
		})
	}
}

// REQ-SSR-011: limit is clamped to 1..10 with a default of 5.
func TestSessionSearchLimitClamp(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"default", `{"query":"deploy"}`, 5},
		{"below one", `{"query":"deploy","limit":0}`, 1},
		{"negative", `{"query":"deploy","limit":-3}`, 1},
		{"above ten", `{"query":"deploy","limit":50}`, 10},
		{"inside", `{"query":"deploy","limit":7}`, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSessionSearcher{}
			callSessionSearch(t, core.ToolCtx{Sessions: fake}, tc.input)
			if len(fake.queries) != 1 {
				t.Fatalf("SearchText called %d times, want 1", len(fake.queries))
			}
			if fake.queries[0].Limit != tc.want {
				t.Fatalf("limit = %d, want %d", fake.queries[0].Limit, tc.want)
			}
		})
	}
}

// AC-SSR-004 (REQ-SSR-013): scroll returns the window in seq order with the
// target row marked, and asks for the fixed radius.
func TestSessionSearchScroll(t *testing.T) {
	ts := time.Date(2026, 10, 20, 9, 13, 0, 0, time.Local).UnixMilli()
	fake := &fakeSessionSearcher{
		info: core.SessionInfo{ID: "s1", Agent: "lana", Channel: "web", Title: "Deploy"},
		rows: []core.TextRow{
			{SessionID: "s1", Seq: 17, Role: "user", CreatedAt: ts, Text: "start"},
			{SessionID: "s1", Seq: 18, Role: "assistant", CreatedAt: ts, Text: "middle"},
			{SessionID: "s1", Seq: 19, Role: "user", CreatedAt: ts, Text: "end"},
		},
	}
	out := callSessionSearch(t, core.ToolCtx{SessionID: "caller", Sessions: fake}, `{"session_id":"s1","around_seq":18}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q, output = %q", out.Status, out.Output)
	}
	want := "session s1 (lana, web, \"Deploy\"), seq 17–19:\n" +
		"seq 17 2026-10-20 09:13 owner: start\n" +
		"> seq 18 2026-10-20 09:13 lana: middle\n" +
		"seq 19 2026-10-20 09:13 owner: end\n"
	if out.Output != want {
		t.Fatalf("output =\n%q\nwant\n%q", out.Output, want)
	}
	if len(fake.windows) != 1 {
		t.Fatalf("TextWindow called %d times, want 1", len(fake.windows))
	}
	if w := fake.windows[0]; w.sessionID != "s1" || w.aroundSeq != 18 || w.radius != 8 {
		t.Fatalf("window = %+v, want s1 around 18 radius 8", w)
	}
}

// REQ-SSR-013: an empty window still renders the session header.
func TestSessionSearchScrollEmptyWindow(t *testing.T) {
	fake := &fakeSessionSearcher{info: core.SessionInfo{ID: "s1", Agent: "lana", Channel: "web", Title: "Empty"}}
	out := callSessionSearch(t, core.ToolCtx{Sessions: fake}, `{"session_id":"s1","around_seq":5}`)
	if out.Status != core.ToolStatusSuccess || !strings.HasPrefix(out.Output, "session s1 (lana, web, \"Empty\"), seq 5:") {
		t.Fatalf("out = %+v", out)
	}
}

// REQ-SSR-012: a context row is cut to 400 runes with the cut marked.
func TestSessionSearchContextCut(t *testing.T) {
	long := strings.Repeat("x", 500)
	fake := &fakeSessionSearcher{hits: []core.TextHit{{
		Session: core.SessionInfo{ID: "s1", Agent: "lana", Channel: "web", Title: "Long"},
		Row:     core.TextRow{SessionID: "s1", Seq: 42, Role: "user", Text: "hit"},
		Snippet: "«hit»",
		Around: []core.TextRow{
			{SessionID: "s1", Seq: 41, Role: "user", Text: long},
			{SessionID: "s1", Seq: 42, Role: "user", Text: "hit"},
		},
	}}}
	out := callSessionSearch(t, core.ToolCtx{Sessions: fake}, `{"query":"deploy"}`)
	if !strings.Contains(out.Output, "   seq 41 owner: "+strings.Repeat("x", 400)+"…\n") {
		t.Fatalf("context row is not cut at 400 runes:\n%s", out.Output)
	}
	if strings.Contains(out.Output, strings.Repeat("x", 401)) {
		t.Fatalf("context row kept more than 400 runes:\n%s", out.Output)
	}
}

// REQ-SSR-013: a scroll row is cut to 1500 runes with the cut marked.
func TestSessionSearchScrollCut(t *testing.T) {
	long := strings.Repeat("y", 1600)
	fake := &fakeSessionSearcher{
		info: core.SessionInfo{ID: "s1", Agent: "lana", Channel: "web", Title: "Long"},
		rows: []core.TextRow{{SessionID: "s1", Seq: 3, Role: "assistant", Text: long}},
	}
	out := callSessionSearch(t, core.ToolCtx{Sessions: fake}, `{"session_id":"s1","around_seq":3}`)
	if !strings.Contains(out.Output, strings.Repeat("y", 1500)+"…") {
		t.Fatalf("scroll row is not cut at 1500 runes:\n%s", out.Output)
	}
	if strings.Contains(out.Output, strings.Repeat("y", 1501)) {
		t.Fatalf("scroll row kept more than 1500 runes:\n%s", out.Output)
	}
}

// AC-SSR-004 (REQ-SSR-016): output never exceeds 12,000 runes; the overflow is
// cut and marked.
func TestSessionSearchOutputCut(t *testing.T) {
	hits := make([]core.TextHit, 0, 40)
	for i := 0; i < 40; i++ {
		hits = append(hits, core.TextHit{
			Session: core.SessionInfo{ID: fmt.Sprintf("s%02d", i), Agent: "lana", Channel: "web", Title: "T"},
			Row:     core.TextRow{SessionID: fmt.Sprintf("s%02d", i), Seq: int64(i), Role: "user", Text: "hit"},
			Snippet: strings.Repeat("z", 400),
		})
	}
	fake := &fakeSessionSearcher{hits: hits}
	out := callSessionSearch(t, core.ToolCtx{Sessions: fake}, `{"query":"deploy"}`)
	if !strings.HasSuffix(out.Output, "\n[output cut]") {
		t.Fatalf("output has no cut marker, length = %d", len([]rune(out.Output)))
	}
	if got := len([]rune(strings.TrimSuffix(out.Output, "\n[output cut]"))); got != sessionSearchOutputCut {
		t.Fatalf("kept %d runes, want %d", got, sessionSearchOutputCut)
	}
}

// AC-SSR-005 (REQ-SSR-016): the mode inputs are validated.
func TestSessionSearchInputErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"scroll without around_seq", `{"session_id":"s1"}`},
		{"scroll with zero around_seq", `{"session_id":"s1","around_seq":0}`},
		{"scroll with negative around_seq", `{"session_id":"s1","around_seq":-1}`},
		{"blank query", `{"query":"   "}`},
		{"no query and no session", `{}`},
		{"unknown field", `{"query":"deploy","bogus":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := callSessionSearch(t, core.ToolCtx{Sessions: &fakeSessionSearcher{}}, tc.input)
			if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input:") {
				t.Fatalf("out = %+v, want an input error", out)
			}
		})
	}
}

// The store may have no text index, so session_search reports that it is
// unavailable.
func TestSessionSearchUnavailable(t *testing.T) {
	out := callSessionSearch(t, core.ToolCtx{SessionID: "s1"}, `{"query":"deploy"}`)
	if out.Status != core.ToolStatusError || out.Output != "session search is not available" {
		t.Fatalf("out = %+v, want the unavailable error", out)
	}
}

// REQ-SSR-010: the searcher error surfaces as the tool error.
func TestSessionSearchSearcherError(t *testing.T) {
	fake := &fakeSessionSearcher{hitErr: fmt.Errorf("disk is full")}
	out := callSessionSearch(t, core.ToolCtx{Sessions: fake}, `{"query":"deploy"}`)
	if out.Status != core.ToolStatusError || out.Output != "disk is full" {
		t.Fatalf("out = %+v, want the searcher error", out)
	}
}

// AC-SSR-005 (REQ-SSR-010, -017): the tool is read risk, carries a description
// that names both modes, and renders its prompt note.
func TestSessionSearchSpecAndGuidance(t *testing.T) {
	tool := NewSessionSearch()
	if got := tool.Risk(nil); got != core.RiskRead {
		t.Fatalf("risk = %q, want %q", got, core.RiskRead)
	}
	if !strings.Contains(tool.Spec().Description, "earlier conversations") {
		t.Fatalf("description = %q", tool.Spec().Description)
	}
	guidance, ok := tool.(core.ToolGuidance)
	if !ok {
		t.Fatal("session_search does not implement core.ToolGuidance")
	}
	text := guidance.PromptGuidance()
	for _, want := range []string{
		"the owner mentions an earlier conversation",
		"not in memory or the current context",
		"Quote what you find; do not guess",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("guidance lacks %q:\n%s", want, text)
		}
	}
}

// Every session_search input carries a property description.
func TestSessionSearchSpecDescribesInputs(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(NewSessionSearch().Spec().InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"query", "agent", "limit", "session_id", "around_seq"} {
		if strings.TrimSpace(schema.Properties[name].Description) == "" {
			t.Fatalf("%s has no property description", name)
		}
	}
}

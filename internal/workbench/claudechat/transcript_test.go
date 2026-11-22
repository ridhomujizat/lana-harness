package claudechat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile writes the lines as a JSONL transcript and returns its path.
func writeFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func itemByID(items []Item, id string) (Item, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

func idsOf(items []Item) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func TestBasicFixture(t *testing.T) {
	tr := Open("testdata/basic.jsonl", "/work")
	items, metaChanged, err := tr.Poll()
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !metaChanged {
		t.Errorf("metaChanged should be true")
	}

	want := []string{"u1", "a1:0", "a2:0", "a3:0", "turn:u2", "u2"}
	if got := idsOf(items); !equalStrings(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}

	user, _ := itemByID(items, "u1")
	if user.Kind != "user" || user.Text != "please edit the file" || user.At == "" {
		t.Errorf("user = %+v", user)
	}

	text, _ := itemByID(items, "a1:0")
	if text.Kind != "assistant" || text.Text != "I'll edit it." || text.Model != "claude-opus-5-5" {
		t.Errorf("assistant text = %+v", text)
	}

	thinking, _ := itemByID(items, "a2:0")
	if thinking.Kind != "thinking" || thinking.Text != "hmm" {
		t.Errorf("thinking = %+v", thinking)
	}

	edit, _ := itemByID(items, "a3:0")
	if edit.Kind != "tool" || edit.Status != "done" || edit.Name != "Edit" || edit.ToolUseID != "toolu_1" {
		t.Errorf("edit tool = %+v", edit)
	}
	if edit.Summary == nil || edit.Summary.Verb != "Edit" || edit.Summary.Target != "web/ui/src/workbench.tsx" || edit.Summary.Meta != "+3 −2" {
		t.Errorf("edit summary = %+v", edit.Summary)
	}
	if edit.Diff == nil || edit.Diff.Old != "a\nb" || edit.Diff.New != "a\nb\nc" {
		t.Errorf("edit diff = %+v", edit.Diff)
	}
	if edit.Result == nil || edit.Result.IsError || edit.Result.Text != "The file /work/web/ui/src/workbench.tsx has been updated." || edit.Result.Lines != 1 {
		t.Errorf("edit result = %+v", edit.Result)
	}

	turn, _ := itemByID(items, "turn:u2")
	if turn.Kind != "turn_end" || turn.DurationMS != 3000 || turn.OutputTokens != 15 {
		t.Errorf("turn_end = %+v", turn)
	}

	meta := tr.Meta()
	if meta.Model != "claude-opus-5-5" || meta.Version != "2.1.330" {
		t.Errorf("meta = %+v", meta)
	}
	if meta.Context.Window != 200000 || meta.Context.Used != 350 || meta.Context.Input != 100 || meta.Context.CacheRead != 200 || meta.Context.Output != 5 {
		t.Errorf("context = %+v", meta.Context)
	}
}

func TestImageFixture(t *testing.T) {
	tr := Open("testdata/images.jsonl", "/work")
	items, _, err := tr.Poll()
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	user, ok := itemByID(items, "u1")
	if !ok {
		t.Fatalf("user item missing: %v", idsOf(items))
	}
	if user.Text != "look at this screenshot" {
		t.Errorf("user text = %q", user.Text)
	}
	if len(user.Images) != 1 || user.Images[0].Ref != "u1:1" || user.Images[0].MediaType != "image/png" {
		t.Fatalf("user images = %+v", user.Images)
	}

	mediaType, data, ok := tr.ImageBytes("u1:1")
	if !ok || mediaType != "image/png" || string(data) != "hello" {
		t.Errorf("ImageBytes(u1:1) = %q %q %v", mediaType, data, ok)
	}
	if _, _, ok := tr.ImageBytes("u1:0"); ok {
		t.Errorf("text block should not decode as an image")
	}
	if _, _, ok := tr.ImageBytes("missing:1"); ok {
		t.Errorf("unknown ref should not decode")
	}
}

func TestBashErrorResult(t *testing.T) {
	path := writeFile(t,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"run it"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:01Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"false\nsecond line"}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"boom","is_error":true}]}}`,
	)
	tr := Open(path, "/work")
	items, _, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := itemByID(items, "a1:0")
	if !ok {
		t.Fatalf("tool item missing: %v", idsOf(items))
	}
	if tool.Status != "error" || tool.Summary == nil || tool.Summary.Verb != "Bash" || tool.Summary.Target != "false" || tool.Summary.Meta != "" {
		t.Errorf("tool = %+v", tool)
	}
	if tool.Result == nil || !tool.Result.IsError || tool.Result.Text != "boom" {
		t.Errorf("result = %+v", tool.Result)
	}
}

func TestAskUserQuestionAnswered(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"q1","name":"AskUserQuestion","input":{"questions":[{"question":"Default scope?","header":"Scope","multiSelect":true,"options":[{"label":"Uncommitted","description":"d"}]}]}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"q1","content":"Uncommitted"}]}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	q, ok := itemByID(items, "a1:0")
	if !ok {
		t.Fatalf("question missing: %v", idsOf(items))
	}
	if q.Kind != "question" || q.ToolUseID != "q1" || !q.Answered || q.Answer != "Uncommitted" {
		t.Errorf("question = %+v", q)
	}
	if len(q.Questions) != 1 {
		t.Fatalf("questions = %+v", q.Questions)
	}
	if q.Questions[0].Question != "Default scope?" || q.Questions[0].Header != "Scope" || !q.Questions[0].MultiSelect {
		t.Errorf("question[0] = %+v", q.Questions[0])
	}
	if len(q.Questions[0].Options) != 1 || q.Questions[0].Options[0].Label != "Uncommitted" {
		t.Errorf("options = %+v", q.Questions[0].Options)
	}
}

func TestExitPlanModeStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		isError bool
		state   string
	}{
		{"approved", false, "approved"},
		{"rejected", true, "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errVal := "false"
			if tc.isError {
				errVal = "true"
			}
			path := writeFile(t,
				`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"p1","name":"ExitPlanMode","input":{"plan":"# Plan\n1. do"}}]}}`,
				`{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"p1","content":"ok","is_error":`+errVal+`}]}}`,
			)
			tr := Open(path, "/work")
			items, _, _ := tr.Poll()
			p, ok := itemByID(items, "a1:0")
			if !ok {
				t.Fatalf("plan missing: %v", idsOf(items))
			}
			if p.Kind != "plan" || p.Plan != "# Plan\n1. do" || p.State != tc.state {
				t.Errorf("plan = %+v", p)
			}
		})
	}
}

func TestUserStringCommandAndOutput(t *testing.T) {
	path := writeFile(t,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"hello world"}}`,
		`{"type":"user","uuid":"c1","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":"<command-name>compact</command-name>\n<command-args>now</command-args>"}}`,
		`{"type":"user","uuid":"c2","timestamp":"2026-11-23T12:00:02Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","uuid":"o1","timestamp":"2026-11-23T12:00:03Z","message":{"role":"user","content":"<local-command-stdout>\u001b[32mCompacted.\u001b[0m</local-command-stdout>"}}`,
		`{"type":"user","uuid":"k1","timestamp":"2026-11-23T12:00:04Z","message":{"role":"user","content":"<local-command-caveat>skip me</local-command-caveat>"}}`,
		`{"type":"user","uuid":"k2","timestamp":"2026-11-23T12:00:05Z","message":{"role":"user","content":"Caveat: also skip"}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()

	user, _ := itemByID(items, "u1")
	if user.Kind != "user" || user.Text != "hello world" {
		t.Errorf("user = %+v", user)
	}
	cmd, _ := itemByID(items, "c1")
	if cmd.Kind != "command" || cmd.Name != "/compact" || cmd.Args != "now" {
		t.Errorf("command = %+v", cmd)
	}
	cmd2, _ := itemByID(items, "c2")
	if cmd2.Kind != "command" || cmd2.Name != "/clear" || cmd2.Args != "" {
		t.Errorf("command2 = %+v", cmd2)
	}
	out, _ := itemByID(items, "o1")
	if out.Kind != "output" || out.Text != "Compacted." {
		t.Errorf("output = %+v", out)
	}
	if _, ok := itemByID(items, "k1"); ok {
		t.Errorf("caveat tag line should be skipped")
	}
	if _, ok := itemByID(items, "k2"); ok {
		t.Errorf("Caveat: line should be skipped")
	}
}

func TestSkipsMetaAndSidechain(t *testing.T) {
	path := writeFile(t,
		`{"type":"user","uuid":"m1","isMeta":true,"timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"meta line"}}`,
		`{"type":"user","uuid":"s1","isSidechain":true,"timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":"side line"}}`,
		`{"type":"assistant","uuid":"s2","isSidechain":true,"timestamp":"2026-11-23T12:00:02Z","message":{"id":"m","role":"assistant","model":"x","content":[{"type":"text","text":"side"}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:03Z","message":{"role":"user","content":"real"}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	if got := idsOf(items); !equalStrings(got, []string{"u1"}) {
		t.Fatalf("items = %v, want [u1]", got)
	}
	if n := tr.Meta().UnknownLines; n != 0 {
		t.Errorf("unknown = %d, want 0", n)
	}
}

func TestGarbageAndOddShapeCountUnknown(t *testing.T) {
	path := writeFile(t,
		`this is not json`,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"ok"}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":42}}`,
	)
	tr := Open(path, "/work")
	items, metaChanged, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if !metaChanged {
		t.Errorf("metaChanged should be true")
	}
	if n := tr.Meta().UnknownLines; n != 2 {
		t.Errorf("unknown = %d, want 2", n)
	}
	if got := idsOf(items); !equalStrings(got, []string{"u1"}) {
		t.Errorf("items = %v, want [u1]", got)
	}
}

func TestPartialLastLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	l1 := `{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"one"}}`
	l2 := `{"type":"user","uuid":"u2","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":"two"}}`
	if err := os.WriteFile(path, []byte(l1+"\n"+l2[:20]), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := Open(path, "/work")
	items, _, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(items); !equalStrings(got, []string{"u1"}) {
		t.Fatalf("first poll items = %v, want [u1]", got)
	}

	if err := os.WriteFile(path, []byte(l1+"\n"+l2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, _, err = tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(items); !equalStrings(got, []string{"u2"}) {
		t.Fatalf("second poll items = %v, want [u2]", got)
	}

	items, metaChanged, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || metaChanged {
		t.Fatalf("third poll items = %v metaChanged = %v, want none", idsOf(items), metaChanged)
	}
}

func TestNoTurnEndWithoutAssistant(t *testing.T) {
	path := writeFile(t,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"one"}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":"two"}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	for _, it := range items {
		if it.Kind == "turn_end" {
			t.Errorf("unexpected turn_end: %+v", it)
		}
	}
}

func TestSnapshotAndBefore(t *testing.T) {
	lines := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","uuid":"u%d","timestamp":"2026-11-23T12:00:0%dZ","message":{"role":"user","content":"m%d"}}`, i, i, i))
	}
	tr := Open(writeFile(t, lines...), "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}

	snap, truncated := tr.Snapshot(3)
	if got := idsOf(snap); !equalStrings(got, []string{"u3", "u4", "u5"}) || !truncated {
		t.Errorf("snapshot = %v truncated = %v", got, truncated)
	}
	snap, truncated = tr.Snapshot(10)
	if len(snap) != 5 || truncated {
		t.Errorf("snapshot all = %v truncated = %v", idsOf(snap), truncated)
	}

	page, more := tr.Before("u5", 2)
	if got := idsOf(page); !equalStrings(got, []string{"u3", "u4"}) || !more {
		t.Errorf("before u5 = %v more = %v", got, more)
	}
	page, more = tr.Before("u2", 5)
	if got := idsOf(page); !equalStrings(got, []string{"u1"}) || more {
		t.Errorf("before u2 = %v more = %v", got, more)
	}
	page, more = tr.Before("u1", 3)
	if len(page) != 0 || more {
		t.Errorf("before u1 = %v more = %v", idsOf(page), more)
	}
	if page, more := tr.Before("missing", 3); page != nil || more {
		t.Errorf("before missing = %v more = %v", page, more)
	}
}

func TestCompactBoundaryDivider(t *testing.T) {
	path := writeFile(t,
		`{"type":"system","subtype":"compact_boundary","uuid":"d1","timestamp":"2026-11-23T12:00:00Z"}`,
		`{"type":"user","uuid":"u1","isCompactSummary":true,"timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":"summary text"}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	if got := idsOf(items); !equalStrings(got, []string{"d1", "u1"}) {
		t.Fatalf("items = %v", got)
	}
	for _, it := range items {
		if it.Kind != "divider" || it.Text != "Conversation compacted" {
			t.Errorf("divider = %+v", it)
		}
	}
}

func TestMetaModelEffortModeAndWindow(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","effort":"high","version":"2.1.330","message":{"id":"m1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":40}}}`,
		`{"type":"permission-mode","permissionMode":"acceptEdits"}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:01Z","permissionMode":"plan","message":{"role":"user","content":"hi"}}`,
	)
	tr := Open(path, "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	meta := tr.Meta()
	if meta.Model != "claude-opus-5-5" || meta.Effort != "high" || meta.Mode != "plan" || meta.Version != "2.1.330" {
		t.Errorf("meta = %+v", meta)
	}
	if meta.Context.Used != 60 || meta.Context.Window != 200000 || meta.Context.Input != 10 || meta.Context.CacheRead != 20 || meta.Context.Output != 40 {
		t.Errorf("context = %+v", meta.Context)
	}

	long := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"claude-opus-5-5[1m]","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":2}}}`,
	)
	tr2 := Open(long, "/work")
	if _, _, err := tr2.Poll(); err != nil {
		t.Fatal(err)
	}
	if got := tr2.Meta().Context.Window; got != 1000000 {
		t.Errorf("window = %d, want 1000000", got)
	}
}

func TestMissingFileIsNotError(t *testing.T) {
	tr := Open(filepath.Join(t.TempDir(), "nope.jsonl"), "/work")
	items, metaChanged, err := tr.Poll()
	if err != nil || items != nil || metaChanged {
		t.Fatalf("items = %v metaChanged = %v err = %v", items, metaChanged, err)
	}
}

func TestMCPToolSummary(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t1","name":"mcp__github__list_issues","input":{"repo":"lana"}}]}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	tool, _ := itemByID(items, "a1:0")
	if tool.Summary == nil || tool.Summary.Target != "github · list_issues" {
		t.Errorf("summary = %+v", tool.Summary)
	}
}

func TestInputStringsCut(t *testing.T) {
	long := strings.Repeat("x", maxInputString+10)
	path := writeFile(t, fmt.Sprintf(
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t1","name":"Write","input":{"file_path":"/work/a.txt","content":"%s"}}]}}`, long))
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	tool, _ := itemByID(items, "a1:0")
	var input struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(tool.Input, &input); err != nil {
		t.Fatalf("input: %v", err)
	}
	if !strings.HasSuffix(input.Content, "…") || len(input.Content) > maxInputString+len("…") {
		t.Errorf("content length = %d, suffix cut = %v", len(input.Content), strings.HasSuffix(input.Content, "…"))
	}
	if tool.Summary == nil || tool.Summary.Target != "a.txt" {
		t.Errorf("summary = %+v", tool.Summary)
	}
}

func TestItemJSONTags(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"q1","name":"AskUserQuestion","input":{"questions":[{"question":"Q","multiSelect":true,"options":[{"label":"A"}]}]}}]}}`,
	)
	tr := Open(path, "/work")
	items, _, _ := tr.Poll()
	q, _ := itemByID(items, "a1:0")
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"question"`, `"tool_use_id":"q1"`, `"multi_select":true`, `"questions":[`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("question json %s missing %s", b, want)
		}
	}
}

func TestMetaJSONTags(t *testing.T) {
	b, err := json.Marshal(Meta{Context: Context{Used: 84200, Window: 200000, CacheRead: 71000, Input: 13200, Output: 2300}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"cache_read":71000`, `"unknown_lines":0`, `"window":200000`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("meta json %s missing %s", b, want)
		}
	}
}

func TestImageBytesDecodesBase64(t *testing.T) {
	want := []byte{0x00, 0x01, 0xfe, 0xff}
	path := writeFile(t, fmt.Sprintf(
		`{"type":"user","uuid":"u1","timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"%s"}}]}}`,
		base64.StdEncoding.EncodeToString(want)))
	tr := Open(path, "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	mediaType, data, ok := tr.ImageBytes("u1:0")
	if !ok || mediaType != "image/jpeg" || string(data) != string(want) {
		t.Errorf("ImageBytes = %q %v %v", mediaType, data, ok)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

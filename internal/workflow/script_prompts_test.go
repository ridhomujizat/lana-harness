package workflow

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCutTextShortUnchanged keeps a short string as-is.
func TestCutTextShortUnchanged(t *testing.T) {
	s := "hello world"
	if got := CutText(s, 100, 100); got != s {
		t.Fatalf("CutText = %q, want %q", got, s)
	}
	if got := CutText(s, len(s), 0); got != s {
		t.Fatalf("CutText at the exact length = %q, want %q", got, s)
	}
}

// TestCutTextLongCutMarker cuts a long string and states the byte counts.
func TestCutTextLongCutMarker(t *testing.T) {
	s := strings.Repeat("a", 1000)
	got := CutText(s, 100, 50)
	marker := "…[cut: 1000 bytes total, showing first 100 and last 50]…"
	if !strings.Contains(got, marker) {
		t.Fatalf("CutText = %q, want marker %q", got, marker)
	}
	if len(got) >= len(s) {
		t.Fatalf("CutText length = %d, want shorter than %d", len(got), len(s))
	}
	if !strings.HasPrefix(got, s[:100]) {
		t.Fatalf("CutText head = %q, want prefix %q", got, s[:100])
	}
	if !strings.HasSuffix(got, s[len(s)-50:]) {
		t.Fatalf("CutText tail = %q, want suffix %q", got, s[len(s)-50:])
	}
}

// TestCutTextZeroSide keeps nothing on a side set to zero.
func TestCutTextZeroSide(t *testing.T) {
	s := strings.Repeat("a", 100)
	got := CutText(s, 0, 0)
	if !strings.Contains(got, "…[cut: 100 bytes total, showing first 0 and last 0]…") {
		t.Fatalf("CutText = %q, want a zero-side marker", got)
	}
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("CutText = %q, want only the marker", got)
	}
}

// TestCutTextRuneSafe cuts multibyte text on rune boundaries.
func TestCutTextRuneSafe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		r       string
		repeats int
		head    int
		tail    int
	}{
		{name: "two-byte runes", r: "é", repeats: 1000, head: 101, tail: 51},
		{name: "four-byte runes", r: "😀", repeats: 300, head: 7, tail: 3},
		{name: "odd head only", r: "é", repeats: 500, head: 3, tail: 0},
	} {
		s := strings.Repeat(tc.r, tc.repeats)
		got := CutText(s, tc.head, tc.tail)
		if !utf8.ValidString(got) {
			t.Fatalf("%s: CutText produced invalid UTF-8", tc.name)
		}
		if !strings.Contains(got, "…[cut:") {
			t.Fatalf("%s: CutText = %q, want a cut marker", tc.name, got)
		}
	}
}

// TestSampleExcerptFindsEntry covers REQ-SP-007: a long page whose entry sits
// past the header boilerplate is still shown, and the excerpt stays bounded.
func TestSampleExcerptFindsEntry(t *testing.T) {
	const budget = 24 << 10
	name := "lana-harness"
	entry := `<a href="/` + name + `">` + name + `</a>`
	sample := strings.Repeat("navigation boilerplate ", 1400) + entry + strings.Repeat("tail bytes ", 4000)
	expected := map[string]any{"repos": []any{map[string]any{"name": name}}}

	got := SampleExcerpt(sample, expected, budget)
	if !strings.Contains(got, entry) {
		t.Fatalf("SampleExcerpt missing the entry %q", entry)
	}
	if len(got) > budget+512 {
		t.Fatalf("SampleExcerpt length = %d, want <= %d", len(got), budget+512)
	}
}

// TestSampleExcerptNoNeedlesFallback returns the CutText tail/head cut when no
// needle steers the window (REQ-SP-007).
func TestSampleExcerptNoNeedlesFallback(t *testing.T) {
	const budget = 24 << 10
	sample := strings.Repeat("a", 60<<10)
	got := SampleExcerpt(sample, nil, budget)
	want := CutText(sample, budget*4/5, budget/5)
	if got != want {
		t.Fatalf("SampleExcerpt = %q, want CutText fallback %q", got, want)
	}
}

// TestSampleExcerptShortUnchanged keeps a sample within the budget as-is.
func TestSampleExcerptShortUnchanged(t *testing.T) {
	s := "small sample that fits the budget"
	if got := SampleExcerpt(s, map[string]any{"name": "needle-value"}, 24<<10); got != s {
		t.Fatalf("SampleExcerpt = %q, want unchanged %q", got, s)
	}
}

// TestSampleExcerptThousands finds the thousands-separated form of an integral
// numeric leaf (REQ-SP-007).
func TestSampleExcerptThousands(t *testing.T) {
	const budget = 24 << 10
	sample := strings.Repeat("z", 40<<10) + "stars 2,372 today" + strings.Repeat("y", 8<<10)
	if strings.Contains(sample, "2372") {
		t.Fatalf("sample must not contain the plain form 2372")
	}
	got := SampleExcerpt(sample, 2372, budget)
	if !strings.Contains(got, "2,372") {
		t.Fatalf("SampleExcerpt missing the thousands form in:\n%s", got)
	}
}

// TestSampleExcerptMultibyte cuts multibyte text on rune boundaries.
func TestSampleExcerptMultibyte(t *testing.T) {
	const budget = 24 << 10
	name := "lana-harness"
	sample := strings.Repeat("中", 12<<10) + name + strings.Repeat("🌸", 4<<10)
	got := SampleExcerpt(sample, map[string]any{"name": name}, budget)
	if !utf8.ValidString(got) {
		t.Fatalf("SampleExcerpt produced invalid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("SampleExcerpt contains the replacement rune")
	}
	if !strings.Contains(got, name) {
		t.Fatalf("SampleExcerpt missing the needle %q", name)
	}
}

// TestBuildScriptPromptSampleExcerpt covers REQ-SP-007 in BuildScriptPrompt: the
// full-size header, the entry in the excerpt and the new sandbox line.
func TestBuildScriptPromptSampleExcerpt(t *testing.T) {
	name := "lana-harness"
	sample := strings.Repeat("nav\n", 20<<10) + name + strings.Repeat("x", 20<<10)
	got := BuildScriptPrompt(ScriptPromptInput{
		Goal:     "Extract the trending repositories",
		Schema:   map[string]any{"repos": "object[]"},
		Input:    sample,
		Expected: map[string]any{"repos": []any{map[string]any{"name": name}}},
	})
	header := "Sample input (" + strconv.Itoa(len(sample)) + " bytes in total):"
	if !strings.Contains(got, header) {
		t.Fatalf("BuildScriptPrompt missing header %q", header)
	}
	if !strings.Contains(got, "scan all of it (do not rely on positions)") {
		t.Fatalf("BuildScriptPrompt missing the excerpt sandbox line")
	}
	if !strings.Contains(got, name) {
		t.Fatalf("BuildScriptPrompt excerpt missing the entry %q", name)
	}
}

// TestBuildFallbackPrompt covers REQ-SP-005: the goal, the compact schema, the
// item schema line, the answer line, and the cut of a large input.
func TestBuildFallbackPrompt(t *testing.T) {
	goal := "Extract the trending repositories"
	schema := map[string]any{"repos": "object[]"}
	itemSchema := map[string]any{"repos": map[string]any{"url": "string"}}
	input := strings.Repeat("x", 200<<10)
	got := BuildFallbackPrompt(goal, schema, itemSchema, input)

	for _, want := range []string{
		goal,
		compactJSON(schema),
		"Each element of repos has: " + compactJSON(map[string]any{"url": "string"}),
		"ONE JSON object",
		"Do not call tools.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildFallbackPrompt missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "…[cut:") {
		t.Fatalf("BuildFallbackPrompt did not cut the large input")
	}
	if len(got) >= 120<<10 {
		t.Fatalf("BuildFallbackPrompt length = %d, want the cut input to stay bounded", len(got))
	}
}

// TestBuildScriptPromptGenerate covers REQ-SP-007 without regeneration: the role,
// the sandbox signature, the reply contract and no repair section.
func TestBuildScriptPromptGenerate(t *testing.T) {
	got := BuildScriptPrompt(ScriptPromptInput{
		Goal:   "Extract the trending repositories",
		Schema: map[string]any{"repos": "object[]"},
		Input:  map[string]any{"html": "<html></html>"},
	})
	for _, want := range []string{
		"You write a deterministic JavaScript parser for a lana workflow step.",
		"function parse(input, ctx)",
		scriptReplyContract,
		"Do not call tools.",
		"Never hardcode values from the sample",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildScriptPrompt missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Script to repair or replace") {
		t.Fatalf("BuildScriptPrompt has a repair section without an old script:\n%s", got)
	}
}

// TestBuildScriptPromptRegenerate covers REQ-SP-007: the old script, the drift
// error and the problems of the previous attempt.
func TestBuildScriptPromptRegenerate(t *testing.T) {
	oldScript := "function parse(input, ctx) { return {repos: []}; }"
	drift := `empty_drift: field "repos": empty`
	problem := "agreement 0.50 is below 0.80"
	got := BuildScriptPrompt(ScriptPromptInput{
		Goal:       "Extract the trending repositories",
		Schema:     map[string]any{"repos": "object[]"},
		Input:      map[string]any{"html": "<html></html>"},
		Expected:   map[string]any{"repos": []any{map[string]any{"url": "https://example.com"}}},
		OldScript:  oldScript,
		DriftError: drift,
		Problems:   []string{problem},
	})
	for _, want := range []string{
		"Script to repair or replace",
		oldScript,
		drift,
		problem,
		"Expected output",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildScriptPrompt missing %q in:\n%s", want, got)
		}
	}
}

// TestBuildScriptPromptConvert covers REQ-SP-017: the agent prompt with its {{ }}
// references, the judgment guidance and the convert reply contract.
func TestBuildScriptPromptConvert(t *testing.T) {
	agentPrompt := "Summarize {{ nodes.fetch.output.markdown }} into a short digest."
	got := BuildScriptPrompt(ScriptPromptInput{
		AgentPrompt: agentPrompt,
		Input:       map[string]any{"markdown": "# hello"},
	})
	for _, want := range []string{
		agentPrompt,
		"{{ nodes.fetch.output.markdown }}",
		"must hold ONLY the {{ }} data references",
		"with no instruction text",
		"Prefer a single reference",
		`join several in one string with "\n\n"`,
		"{{ nodes.a.output.markdown }}\\n\\n{{ nodes.b.output.output }}",
		"feasible=false",
		`"goal"`,
		`"input"`,
		`"output_schema"`,
		scriptConvertReplyContract,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildScriptPrompt missing %q in:\n%s", want, got)
		}
	}
}

// TestBuildScriptPromptInputWording says which input kind the script receives
// (REQ-SP-007): a string sample is a string, any other sample is parsed JSON.
func TestBuildScriptPromptInputWording(t *testing.T) {
	schema := map[string]any{"text": "string"}

	strGot := BuildScriptPrompt(ScriptPromptInput{
		Goal:   "Extract the text",
		Schema: schema,
		Input:  "plain text sample",
	})
	if !strings.Contains(strGot, "is a string") || !strings.Contains(strGot, "JSON.parse") {
		t.Fatalf("string sample wording missing in:\n%s", strGot)
	}

	mapGot := BuildScriptPrompt(ScriptPromptInput{
		Goal:   "Extract the text",
		Schema: schema,
		Input:  map[string]any{"text": "value"},
	})
	if !strings.Contains(mapGot, "is a parsed JSON value") {
		t.Fatalf("map sample wording missing in:\n%s", mapGot)
	}
}

// TestParseScriptReplyCases covers API-SP-008: a valid object, a stripped fence,
// the feasible cases, prose around the JSON and the error cases.
func TestParseScriptReplyCases(t *testing.T) {
	fenced := `{"feasible": true, "script": ` + strconv.Quote("```js\nfunction parse(input, ctx){ return {}; }\n```") + `}`
	bareFenced := `{"feasible": true, "script": ` + strconv.Quote("```\nvar x = 1;\n```") + `}`
	cases := []struct {
		name       string
		text       string
		wantErr    bool
		wantFeas   bool
		wantScript string
		wantGoal   string
		wantInput  string
		wantSchema map[string]any
	}{
		{
			name:       "valid object",
			text:       `{"feasible": true, "script": "function parse(input, ctx){ return {}; }"}`,
			wantFeas:   true,
			wantScript: "function parse(input, ctx){ return {}; }",
		},
		{
			name:       "fenced js script stripped",
			text:       fenced,
			wantFeas:   true,
			wantScript: "function parse(input, ctx){ return {}; }",
		},
		{
			name:       "bare fence stripped",
			text:       bareFenced,
			wantFeas:   true,
			wantScript: "var x = 1;",
		},
		{
			name:     "infeasible without script is fine",
			text:     `{"feasible": false, "reason": "needs judgment"}`,
			wantFeas: false,
		},
		{
			name:    "infeasible with empty script is fine",
			text:    `{"feasible": false, "script": ""}`,
			wantErr: false,
		},
		{
			name:    "feasible with empty script errors",
			text:    `{"feasible": true, "script": ""}`,
			wantErr: true,
		},
		{
			name:    "feasible without script errors",
			text:    `{"feasible": true, "reason": "looks easy"}`,
			wantErr: true,
		},
		{
			name:       "prose around json",
			text:       "Sure, here it is:\n{\"feasible\": true, \"script\": \"function parse(){\"}\nHope it helps.",
			wantFeas:   true,
			wantScript: "function parse(){",
		},
		{
			name:       "missing feasible means true",
			text:       `{"script": "function parse(){}"}`,
			wantFeas:   true,
			wantScript: "function parse(){}",
		},
		{
			name:       "convert fields",
			text:       `{"feasible": true, "script": "var s = 1;", "goal": "Extract repos", "input": "{{ nodes.fetch.output.markdown }}", "output_schema": {"text": "string"}}`,
			wantFeas:   true,
			wantScript: "var s = 1;",
			wantGoal:   "Extract repos",
			wantInput:  "{{ nodes.fetch.output.markdown }}",
			wantSchema: map[string]any{"text": "string"},
		},
		{
			name:    "non-json text errors",
			text:    "no json at all",
			wantErr: true,
		},
		{
			name:    "array instead of object errors",
			text:    `[1, 2, 3]`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		got, err := ParseScriptReply(tc.text)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: no error, want one", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: error = %v", tc.name, err)
		}
		if got.Feasible != tc.wantFeas {
			t.Fatalf("%s: Feasible = %v, want %v", tc.name, got.Feasible, tc.wantFeas)
		}
		if got.Script != tc.wantScript {
			t.Fatalf("%s: Script = %q, want %q", tc.name, got.Script, tc.wantScript)
		}
		if tc.wantGoal != "" && got.Goal != tc.wantGoal {
			t.Fatalf("%s: Goal = %q, want %q", tc.name, got.Goal, tc.wantGoal)
		}
		if tc.wantInput != "" && got.Input != tc.wantInput {
			t.Fatalf("%s: Input = %q, want %q", tc.name, got.Input, tc.wantInput)
		}
		if tc.wantSchema != nil && compactJSON(got.OutputSchema) != compactJSON(tc.wantSchema) {
			t.Fatalf("%s: OutputSchema = %v, want %v", tc.name, got.OutputSchema, tc.wantSchema)
		}
	}
}

// TestParseScriptReplyRules covers the optional rules object of a generation
// reply (REQ-DG-006): a string value is taken as is and an array of strings is
// joined by spaces, while any other value type is ignored.
func TestParseScriptReplyRules(t *testing.T) {
	got, err := ParseScriptReply(`{"feasible":true,"script":"var x = 1;","rules":{"f":">0","g":"min:3 max:5"}}`)
	if err != nil {
		t.Fatalf("ParseScriptReply: %v", err)
	}
	if want := map[string]string{"f": ">0", "g": "min:3 max:5"}; !reflect.DeepEqual(got.Rules, want) {
		t.Fatalf("Rules = %v, want %v", got.Rules, want)
	}

	got, err = ParseScriptReply(`{"feasible":true,"script":"var x = 1;","rules":{"f":[">0","max:5"],"g":7,"h":["url"]}}`)
	if err != nil {
		t.Fatalf("ParseScriptReply: %v", err)
	}
	if want := map[string]string{"f": ">0 max:5", "h": "url"}; !reflect.DeepEqual(got.Rules, want) {
		t.Fatalf("Rules = %v, want %v", got.Rules, want)
	}

	got, err = ParseScriptReply(`{"feasible":true,"script":"var x = 1;"}`)
	if err != nil {
		t.Fatalf("ParseScriptReply: %v", err)
	}
	if got.Rules != nil {
		t.Fatalf("Rules = %v, want nil", got.Rules)
	}
}

// TestBuildScriptPromptRules covers REQ-DG-006: the generate and convert prompts
// ask for optional rules and list the vocabulary, while a regeneration, which
// never changes rules, does not.
func TestBuildScriptPromptRules(t *testing.T) {
	generate := BuildScriptPrompt(ScriptPromptInput{Goal: "extract", Schema: map[string]any{"count": "integer"}, Input: "sample"})
	convert := BuildScriptPrompt(ScriptPromptInput{AgentPrompt: "Summarize {{ input.text }}", Input: "sample"})
	regenerate := BuildScriptPrompt(ScriptPromptInput{
		Goal: "extract", Schema: map[string]any{"count": "integer"}, Input: "sample",
		OldScript: "function parse(input, ctx) { return {count: 1}; }",
	})

	vocabulary := []string{`"rules"`, `"<field path>"`, `"<tokens>"`, ">0", ">=0", "url", "min:N", "max:N", "Add a rule only when real data can never break it"}
	for _, want := range vocabulary {
		if !strings.Contains(generate, want) {
			t.Fatalf("generate prompt missing %q in:\n%s", want, generate)
		}
		if !strings.Contains(convert, want) {
			t.Fatalf("convert prompt missing %q in:\n%s", want, convert)
		}
	}
	if strings.Contains(regenerate, "Add a rule only when real data can never break it") || strings.Contains(regenerate, `"<field path>"`) {
		t.Fatalf("regeneration prompt must not ask for rules:\n%s", regenerate)
	}
}

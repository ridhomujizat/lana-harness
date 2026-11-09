package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file holds the prompts for the Script Parse node (REQ-SP-005, REQ-SP-007,
// REQ-SP-017, design section "Prompts (script_prompts.go)"): the fallback prompt
// that lets an agent answer the node's goal as JSON, the generation prompt that
// asks the guardian for the JavaScript parser, and the parsing of the generation
// reply (API-SP-008).

// fallbackInputHead and fallbackInputTail size the input of the fallback prompt
// (REQ-SP-005): head 80 KiB plus tail 16 KiB, 96 KiB in total.
const (
	fallbackInputHead = 80 << 10
	fallbackInputTail = 16 << 10
)

// scriptSampleBudget sizes the sample input of the generation prompt
// (REQ-SP-007): a 24 KiB excerpt around the part of the input that holds an
// expected value, so a long page whose entries start past the header boilerplate
// is still shown.
const scriptSampleBudget = 24 << 10

// sampleExcerpt tuning (REQ-SP-007): the first 1 KiB is always kept as context,
// the window starts at most 2 KiB before the matched needle, the tail keeps an
// eighth of the budget, and at most 50 needles are searched. A string leaf needs
// 4 bytes to be a needle so short fragments do not steer the window.
const (
	sampleHeadBytes = 1024
	sampleGapBefore = 2048
	sampleTailDiv   = 8
	sampleNeedleMax = 50
	sampleNeedleMin = 4
)

// scriptExpectedMaxBytes bounds the expected output shown to the guardian
// (REQ-SP-007): 8 KiB.
const scriptExpectedMaxBytes = 8 << 10

// scriptReplyContract is the exact reply the generation prompt asks for when no
// conversion is involved (API-SP-008).
const scriptReplyContract = `Reply with ONE JSON object only: {"feasible": true|false, "reason": "...", "script": "<JavaScript source>"}`

// scriptConvertReplyContract is the reply the generation prompt asks for when it
// converts an agent node (REQ-SP-017, API-SP-008); it adds the derived goal, the
// input template and the output schema.
const scriptConvertReplyContract = `Reply with ONE JSON object only: {"feasible": true|false, "reason": "...", "script": "<JavaScript source>", "goal": "<goal>", "input": "<template>", "output_schema": {"text": "string"}}`

// CutText keeps the head and tail bytes of s, cutting on UTF-8 rune boundaries.
// When s is longer than head+tail it returns the head, a marker naming the total
// byte count and the kept byte counts, and the tail. A head or tail of zero (or
// less) keeps nothing on that side.
func CutText(s string, head, tail int) string {
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if len(s) <= head+tail {
		return s
	}
	he := runeFloor(s, head)
	ts := runeCeil(s, len(s)-tail)
	marker := fmt.Sprintf("\n…[cut: %d bytes total, showing first %d and last %d]…\n", len(s), he, len(s)-ts)
	return s[:he] + marker + s[ts:]
}

// runeFloor returns the largest index <= i that starts a UTF-8 rune, clamped to
// [0, len(s)].
func runeFloor(s string, i int) int {
	if i > len(s) {
		i = len(s)
	}
	if i < 0 {
		return 0
	}
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// runeCeil returns the smallest index >= i that starts a UTF-8 rune, clamped to
// [0, len(s)].
func runeCeil(s string, i int) int {
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

// SampleExcerpt returns the part of sample the generation prompt should show
// (REQ-SP-007). A sample no longer than budget is returned unchanged. Otherwise
// the needles of expected (trimmed string leaves of at least 4 bytes and the
// integer forms of numeric leaves) locate the first region that holds an expected
// value: the excerpt keeps the first 1 KiB, a window starting at most 2 KiB before
// that region, and the last budget/8 bytes, with markers that name the skipped
// byte ranges and the total size. When no needle is found it falls back to
// CutText(sample, budget*4/5, budget/5). Every cut falls on a UTF-8 rune boundary
// and the result stays within budget+512 bytes.
func SampleExcerpt(sample string, expected any, budget int) string {
	if len(sample) <= budget {
		return sample
	}
	idx := findSampleNeedle(sample, sampleNeedles(expected))
	if idx < 0 {
		return CutText(sample, budget*4/5, budget/5)
	}
	n := len(sample)

	tailLen := budget / sampleTailDiv
	if tailLen < 0 {
		tailLen = 0
	}
	winStart := idx - sampleGapBefore
	if winStart < sampleHeadBytes {
		winStart = sampleHeadBytes
	}
	winLen := budget - sampleHeadBytes - tailLen
	if winLen < 0 {
		winLen = 0
	}
	tailStart := n - tailLen
	if tailStart < 0 {
		tailStart = 0
	}

	// End the window at the start of the tail so the kept parts never overlap, and
	// snap every boundary to a rune start so no cut splits a rune.
	winEnd := winStart + winLen
	if winEnd > tailStart {
		winEnd = tailStart
	}
	if winEnd < winStart {
		winEnd = winStart
	}
	headEnd := runeFloor(sample, sampleHeadBytes)
	winStart = runeCeil(sample, winStart)
	winEnd = runeFloor(sample, winEnd)
	tailStart = runeCeil(sample, tailStart)
	if winEnd < winStart {
		winEnd = winStart
	}

	var b strings.Builder
	b.WriteString(sample[:headEnd])
	fmt.Fprintf(&b, "\n…[skipped %d bytes: %d–%d of %d]…\n", winStart-headEnd, headEnd, winStart, n)
	b.WriteString(sample[winStart:winEnd])
	fmt.Fprintf(&b, "\n…[skipped %d bytes: %d–%d of %d]…\n", tailStart-winEnd, winEnd, tailStart, n)
	b.WriteString(sample[tailStart:])
	return b.String()
}

// sampleNeedles collects the search needles of expected (REQ-SP-007): the trimmed
// string leaves of at least sampleNeedleMin bytes (keeping their original case)
// and, for each integral numeric leaf, the plain and thousands-separated forms.
// Maps are walked in sorted key order and slices in document order, so the list is
// deterministic; at most sampleNeedleMax needles are kept.
func sampleNeedles(expected any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] || len(out) >= sampleNeedleMax {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	var walk func(v any)
	walk = func(v any) {
		if len(out) >= sampleNeedleMax {
			return
		}
		switch t := normalize(v).(type) {
		case map[string]any:
			for _, k := range sortedKeys(t) {
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case string:
			if s := strings.TrimSpace(t); len(s) >= sampleNeedleMin {
				add(s)
			}
		case json.Number, float64, float32, int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64:
			if f, ok := asFloat(t); ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) {
				plain := strconv.FormatInt(int64(f), 10)
				add(plain)
				add(withThousands(plain))
			}
		}
	}
	walk(expected)
	return out
}

// withThousands inserts a comma every three digits of a base-10 integer string.
func withThousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	digits := strings.TrimPrefix(s, "-")
	if len(digits) <= 3 {
		return s
	}
	first := len(digits) % 3
	if first == 0 {
		first = 3
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(digits[:first])
	for i := first; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// findSampleNeedle returns the smallest byte offset in sample where any needle
// occurs, comparing case-insensitively, or -1 when none matches. It lowercases
// the sample once. strings.ToLower keeps byte offsets valid only while the byte
// length is unchanged, so a changed length falls back to a per-position
// strings.EqualFold scan to keep the offsets correct.
func findSampleNeedle(sample string, needles []string) int {
	if len(needles) == 0 {
		return -1
	}
	best := -1
	lower := strings.ToLower(sample)
	for _, nd := range needles {
		if nd == "" {
			continue
		}
		var j int
		if len(lower) == len(sample) {
			j = strings.Index(lower, strings.ToLower(nd))
		} else {
			j = indexFold(sample, nd)
		}
		if j >= 0 && (best < 0 || j < best) {
			best = j
		}
	}
	return best
}

// indexFold returns the smallest byte offset where needle occurs in s ignoring
// case (strings.EqualFold on equal-length slices), or -1.
func indexFold(s, needle string) int {
	for i := 0; i+len(needle) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// inputText renders any input as text for prompts: a string as-is, anything else
// as indented JSON. A value that does not marshal yields an empty string.
func inputText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// BuildFallbackPrompt builds the fallback prompt of REQ-SP-005: the role, the
// goal, the input cut to 96 KiB (head 80 KiB plus tail 16 KiB, marked), one line
// per item_schema entry, and the answer shape.
func BuildFallbackPrompt(goal string, schema, itemSchema map[string]any, input any) string {
	var b strings.Builder
	b.WriteString("You extract data for a lana workflow step.\n")
	b.WriteString("Goal: " + goal + "\n")
	b.WriteString("Input:\n")
	b.WriteString(CutText(inputText(input), fallbackInputHead, fallbackInputTail))
	b.WriteString("\n")
	for _, field := range sortedKeys(itemSchema) {
		fmt.Fprintf(&b, "Each element of %s has: %s\n", field, compactJSON(asMapParam(itemSchema[field])))
	}
	b.WriteString("Answer with ONE JSON object only matching: " + compactJSON(schema) + "\n")
	b.WriteString("Do not call tools.\n")
	return b.String()
}

// ScriptPromptInput holds everything the generation prompt needs (REQ-SP-007,
// REQ-SP-017). Only Goal, Schema and Input are set for a plain generation;
// OldScript, DriftError and Problems carry the regeneration context; AgentPrompt
// is set when an agent node is converted.
type ScriptPromptInput struct {
	Goal        string
	Schema      map[string]any // output_schema
	ItemSchema  map[string]any
	AllowEmpty  []string
	Input       any    // the sample input
	Expected    any    // expected output (fallback or agent), nil when unknown
	OldScript   string // regeneration only
	DriftError  string // regeneration only, e.g. "empty_drift: field \"repos\": empty"
	Problems    []string
	AgentPrompt string // convert only: the agent node's raw prompt (with {{ }} templates)
}

// BuildScriptPrompt builds the guardian generation prompt of REQ-SP-007 and
// REQ-SP-017: the role, the goal (or, for a conversion, the agent prompt and the
// instruction to derive a goal and an input template), the contract, the sandbox
// rules, the sample input with its full size, the expected output, the old script
// and drift error, the problems of a previous attempt, and the reply contract of
// API-SP-008 as the last line.
func BuildScriptPrompt(in ScriptPromptInput) string {
	convert := strings.TrimSpace(in.AgentPrompt) != ""
	var b strings.Builder
	b.WriteString("You write a deterministic JavaScript parser for a lana workflow step.\n\n")

	if convert {
		b.WriteString("This replaces an agent step. Convert the agent prompt below into a script that extracts the same data without an LLM.\n")
		b.WriteString("Agent prompt:\n")
		b.WriteString(in.AgentPrompt)
		b.WriteString("\n\n")
		b.WriteString("Derive a short goal and an input template from that prompt. The input template must hold ONLY the {{ }} data references of the agent prompt, with no instruction text, for example \"{{ nodes.fetch.output.markdown }}\". Prefer a single reference; join several in one string with \"\\n\\n\", for example \"{{ nodes.a.output.markdown }}\\n\\n{{ nodes.b.output.output }}\".\n\n")
	} else {
		b.WriteString("Goal: " + in.Goal + "\n\n")
	}

	b.WriteString("Contract:\n")
	b.WriteString("Output schema: " + compactJSON(in.Schema) + "\n")
	for _, field := range sortedKeys(in.ItemSchema) {
		fmt.Fprintf(&b, "Each element of %s has: %s\n", field, compactJSON(asMapParam(in.ItemSchema[field])))
	}
	if len(in.AllowEmpty) > 0 {
		b.WriteString("allow_empty: " + strings.Join(in.AllowEmpty, ", ") + "\n")
	}
	b.WriteString("A string field must not be empty and an array field must not be empty, unless the field is named in allow_empty.\n\n")

	b.WriteString("Sandbox:\n")
	b.WriteString("- Define `function parse(input, ctx)` at the top level. It must return ONE object.\n")
	b.WriteString("- Use ECMAScript 2020 as goja supports it. No require, fetch, timers, or IO.\n")
	b.WriteString("- console.log(...) writes to the node logs. html(str) parses HTML and returns a selection with find(css), text(), attr(name), html(), first(), eq(i), map(fn), toArray() and length.\n")
	if _, ok := in.Input.(string); ok {
		b.WriteString("- The input to parse() is a string. A JSON string must be parsed with JSON.parse.\n")
	} else {
		b.WriteString("- The input to parse() is a parsed JSON value.\n")
	}
	b.WriteString("- Never hardcode values from the sample. Anchor on structure (headings, link patterns, JSON keys), not line numbers, and tolerate small whitespace changes.\n")
	b.WriteString("- Write readable code for the owner: one statement per line, 2-space indent, short comments on the anchors you rely on. Inside the JSON reply, encode newlines as \\n.\n")
	b.WriteString("- The sample excerpt may skip parts of the input; parse() always receives the full input, so scan all of it (do not rely on positions).\n\n")

	sample := inputText(in.Input)
	fmt.Fprintf(&b, "Sample input (%d bytes in total):\n%s\n\n", len(sample), SampleExcerpt(sample, in.Expected, scriptSampleBudget))

	if in.Expected != nil {
		b.WriteString("Expected output:\n" + CutText(compactJSON(in.Expected), scriptExpectedMaxBytes, 0) + "\n\n")
	}

	if in.OldScript != "" || in.DriftError != "" {
		b.WriteString("Script to repair or replace:\n")
		if in.OldScript != "" {
			b.WriteString(in.OldScript + "\n")
		}
		if in.DriftError != "" {
			b.WriteString("Drift error: " + in.DriftError + "\n")
		}
		b.WriteString("\n")
	}

	if len(in.Problems) > 0 {
		b.WriteString("Problems with the previous attempt:\n")
		for _, p := range in.Problems {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}

	if convert {
		b.WriteString("If the step needs judgment (real summarization, ranking by taste, or opinions), answer feasible=false with a reason instead of a script.\n")
		b.WriteString("For convert also answer \"goal\", \"input\" (a template string using the agent prompt's {{ }} references) and \"output_schema\" (keep the given schema when there is one, else {\"text\": \"string\"}).\n")
	}
	b.WriteString("Do not call tools.\n")
	if convert {
		b.WriteString(scriptConvertReplyContract + "\n")
	} else {
		b.WriteString(scriptReplyContract + "\n")
	}
	return b.String()
}

// ScriptReply is a parsed generation answer (API-SP-008): whether the step is
// feasible, the reason when it is not, the script, and, for a conversion, the
// derived goal, input template and output schema.
type ScriptReply struct {
	Feasible     bool
	Reason       string
	Script       string
	Goal         string
	Input        string
	OutputSchema map[string]any
}

// ParseScriptReply extracts the guardian's generation answer from text with
// ExtractJSON (API-SP-008). A text without a JSON object is an error, and so is a
// feasible answer without a script; an infeasible answer needs no script. A
// missing "feasible" key counts as true. A ```js, ```javascript or bare ```
// fence around the script is stripped.
func ParseScriptReply(text string) (ScriptReply, error) {
	v, err := ExtractJSON(text)
	if err != nil {
		return ScriptReply{}, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ScriptReply{}, errors.New("workflow: script reply is not a JSON object")
	}
	reply := ScriptReply{Feasible: true}
	if f, ok := m["feasible"].(bool); ok {
		reply.Feasible = f
	}
	reply.Reason, _ = m["reason"].(string)
	if s, ok := m["script"].(string); ok {
		reply.Script = stripScriptFence(s)
	}
	reply.Goal, _ = m["goal"].(string)
	reply.Input, _ = m["input"].(string)
	if os, ok := m["output_schema"].(map[string]any); ok {
		reply.OutputSchema = os
	}
	if reply.Feasible && strings.TrimSpace(reply.Script) == "" {
		return ScriptReply{}, errors.New("workflow: script reply is feasible but has no script")
	}
	return reply, nil
}

// stripScriptFence removes a ```js, ```javascript or bare ``` fence around a
// script the model added anyway; other content is returned trimmed.
func stripScriptFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	rest := t[3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return t
	}
	switch strings.TrimSpace(rest[:nl]) {
	case "", "js", "javascript":
	default:
		return t
	}
	body := rest[nl+1:]
	if end := strings.Index(body, "```"); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}

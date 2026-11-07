package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return out
}

// The full request: system and last-tool cache_control, an image, a
// tool_result with an image, an error tool_result, signed and redacted
// thinking sent back, an unsigned thinking block dropped, tool IDs made
// API-safe, consecutive user messages merged with tool_result blocks first,
// cache_control on the last user block, and effort high on Opus 5.5.
func TestRequestBodyGolden(t *testing.T) {
	temp := 0.5
	req := core.ChatRequest{
		Model:           "claude-opus-5-5",
		System:          "You are lana.",
		ReasoningEffort: "high",
		Temperature:     &temp,
		Tools: []core.ToolSpec{
			{Name: "read_file", Description: "Read a file.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
			{Name: "write_file", Description: "Write a file."},
		},
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: "hi"},
				{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("PNG"), Path: "a.png"},
			}},
			{Role: core.RoleAssistant, Content: []core.ContentBlock{
				{Kind: core.BlockThinking, Text: "Let me look", Signature: "sig1"},
				{Kind: core.BlockThinking, Signature: core.RedactedThinkingPrefix + "REDACTED"},
				{Kind: core.BlockThinking, Text: "", Signature: "sig2"},
				{Kind: core.BlockThinking, Text: "deepseek reasoning without a signature"},
				{Kind: core.BlockText, Text: "Reading"},
				{Kind: core.BlockToolUse, ID: "toolu_01A", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)},
				{Kind: core.BlockToolUse, ID: "call:x/1", Name: "write_file"},
			}},
			{Role: core.RoleUser, Content: []core.ContentBlock{
				{Kind: core.BlockToolResult, ID: "toolu_01A", Output: "file content", Images: []core.ContentBlock{{Kind: core.BlockImage, MediaType: "image/jpeg", Data: []byte("JPG")}}},
			}},
			{Role: core.RoleUser, Content: []core.ContentBlock{
				{Kind: core.BlockText, Text: "and more"},
				{Kind: core.BlockToolResult, ID: "call:x/1", Output: "boom", IsError: true},
			}},
		},
	}
	body, err := buildRequestBody(req)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "request_full.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decode(t, body), decode(t, golden); !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("body:\n%s", gotJSON)
	}
}

// A thinking block the adapter produced comes back unchanged: same text,
// same signature.
func TestThinkingSignatureRoundTrip(t *testing.T) {
	produced := core.ContentBlock{Kind: core.BlockThinking, Text: "Let me look at the file.", Signature: "EqQBCgIYAhIM1gbcDa9GJwZA2b3h"}
	stored, err := json.Marshal(produced)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded core.ContentBlock
	if err := json.Unmarshal(stored, &reloaded); err != nil {
		t.Fatal(err)
	}
	body, err := buildRequestBody(core.ChatRequest{Model: "claude-sonnet-5-5", Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "q"}}},
		{Role: core.RoleAssistant, Content: []core.ContentBlock{reloaded, {Kind: core.BlockToolUse, ID: "toolu_1", Name: "t", Input: json.RawMessage(`{}`)}}},
		{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockToolResult, ID: "toolu_1", Output: "ok"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	first := parsed.Messages[1].Content[0]
	if first["type"] != "thinking" || first["thinking"] != produced.Text || first["signature"] != produced.Signature {
		t.Fatalf("thinking block=%v", first)
	}
}

// The effort mapping per model family (claude-api skill, Thinking & Effort).
func TestEffortMapping(t *testing.T) {
	temp := 0.3
	cases := []struct {
		model     string
		effort    string
		maxTokens int
		thinking  string // JSON of the thinking field, "" when absent
		effortOut string
		temp      bool
		wantMax   int
	}{
		// Always-on thinking: off becomes the lowest effort.
		{"claude-opus-5-5", "", 0, `{"type":"adaptive","display":"summarized"}`, "", false, 32000},
		{"claude-opus-5-5", "off", 0, `{"type":"adaptive","display":"summarized"}`, "low", false, 32000},
		{"claude-opus-5-5", "low", 0, `{"type":"adaptive","display":"summarized"}`, "low", false, 32000},
		{"claude-opus-5-5", "max", 1000, `{"type":"adaptive","display":"summarized"}`, "max", false, 1000},
		{"claude-fable-5-1", "off", 0, `{"type":"adaptive","display":"summarized"}`, "low", false, 32000},
		{"claude-opus-5", "off", 0, `{"type":"adaptive","display":"summarized"}`, "low", false, 32000},
		// Opus 4.7/4.8: off omits thinking; no sampling parameters.
		{"claude-opus-4-8", "off", 0, "", "", false, 32000},
		{"claude-opus-4-7", "high", 0, `{"type":"adaptive","display":"summarized"}`, "high", false, 32000},
		// Sonnet 5.5 turns thinking off with between_tools.
		{"claude-sonnet-5-5", "off", 0, `{"type":"between_tools"}`, "", false, 32000},
		{"claude-sonnet-5-5", "high", 0, `{"type":"adaptive","display":"summarized"}`, "high", false, 32000},
		{"claude-sonnet-5", "off", 0, `{"type":"disabled"}`, "", false, 32000},
		{"claude-haiku-5-5", "off", 0, `{"type":"disabled"}`, "", false, 32000},
		// 4.6: adaptive without display; temperature only with thinking off.
		{"claude-sonnet-4-6", "off", 0, "", "", true, 32000},
		{"anthropic/claude-sonnet-4.6", "high", 0, `{"type":"adaptive"}`, "high", false, 32000},
		{"claude-opus-4-6", "", 0, `{"type":"adaptive"}`, "", false, 32000},
		// Budget models: budget_tokens below max_tokens, no effort.
		{"claude-haiku-4-5", "", 0, "", "", true, 32000},
		{"claude-haiku-4-5", "low", 0, `{"type":"enabled","budget_tokens":4096}`, "", false, 32000},
		{"claude-haiku-4-5-20251001", "high", 4096, `{"type":"enabled","budget_tokens":16000}`, "", false, 24192},
		{"claude-haiku-4-5", "max", 0, `{"type":"enabled","budget_tokens":32000}`, "", false, 40192},
		{"claude-3-7-sonnet-20250219", "high", 0, `{"type":"enabled","budget_tokens":16000}`, "", false, 24192},
		{"claude-3-5-haiku-20241022", "high", 0, "", "", true, 8192},
		// Unknown model: adaptive, no sampling.
		{"my-proxy-model", "high", 0, `{"type":"adaptive"}`, "high", false, 8192},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			body, err := buildRequestBody(core.ChatRequest{Model: tc.model, ReasoningEffort: tc.effort, MaxTokens: tc.maxTokens, Temperature: &temp,
				Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "q"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				MaxTokens    int             `json:"max_tokens"`
				Thinking     json.RawMessage `json:"thinking"`
				OutputConfig *outputConfig   `json:"output_config"`
				Temperature  *float64        `json:"temperature"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatal(err)
			}
			if string(parsed.Thinking) != tc.thinking {
				t.Fatalf("thinking=%s want %s", parsed.Thinking, tc.thinking)
			}
			gotEffort := ""
			if parsed.OutputConfig != nil {
				gotEffort = parsed.OutputConfig.Effort
			}
			if gotEffort != tc.effortOut {
				t.Fatalf("effort=%q want %q", gotEffort, tc.effortOut)
			}
			if (parsed.Temperature != nil) != tc.temp {
				t.Fatalf("temperature=%v want sent=%v", parsed.Temperature, tc.temp)
			}
			if parsed.MaxTokens != tc.wantMax {
				t.Fatalf("max_tokens=%d want %d", parsed.MaxTokens, tc.wantMax)
			}
		})
	}
}

func TestNormalizeBase(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.anthropic.com":       "https://api.anthropic.com",
		"https://api.anthropic.com/":      "https://api.anthropic.com",
		"https://api.anthropic.com/v1":    "https://api.anthropic.com",
		"https://api.anthropic.com/v1/":   "https://api.anthropic.com",
		"https://proxy.example/anthropic": "https://proxy.example/anthropic",
	} {
		if got := normalizeBase(in); got != want {
			t.Fatalf("%s: got %s want %s", in, got, want)
		}
	}
}

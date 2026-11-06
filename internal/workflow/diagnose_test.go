package workflow

// Tests for the guardian diagnosis prompt and parsing (REQ-WF-008).

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIsTransient(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{context.Canceled, false},
		{context.DeadlineExceeded, true},
		{errors.New("request timeout"), true},
		{errors.New("the call timed out"), true},
		{errors.New("HTTP 429 too many requests"), true},
		{errors.New("rate limit exceeded"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("connection refused"), true},
		{errors.New("unexpected EOF"), true},
		{errors.New("dial tcp: no such host"), true},
		{errors.New("temporary failure, try again"), true},
		{errors.New("server returned 503"), true},
		{errors.New("bad gateway 502"), true},
		{errors.New("boom"), false},
		{errors.New("schema_error: no JSON object found"), false},
		{errors.New("status 404 not found"), false},
		{errors.New("permission denied"), false},
	}
	for _, tc := range cases {
		if got := IsTransient(tc.err); got != tc.want {
			t.Fatalf("IsTransient(%v)=%v want %v", tc.err, got, tc.want)
		}
	}
}

func TestParseDiagnosis(t *testing.T) {
	text := "Sure:\n```json\n" +
		`{"action":"retry_fix","reason":"bump budget","fix":{"max_tokens":4000},"persistent":false,"confidence":0.8}` +
		"\n```"
	d, err := ParseDiagnosis(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Action != "retry_fix" || d.Reason != "bump budget" || d.Persistent || d.Confidence != 0.8 {
		t.Fatalf("diagnosis=%+v", d)
	}
	if d.Fix["max_tokens"] != float64(4000) {
		t.Fatalf("fix=%v", d.Fix)
	}

	if _, err := ParseDiagnosis("nothing to see here"); err == nil {
		t.Fatal("expected an error for text without JSON")
	}
	if _, err := ParseDiagnosis(`{"action":"explode","reason":"x"}`); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
	if _, err := ParseDiagnosis(`{"reason":"no action"}`); err == nil {
		t.Fatal("expected an error for a missing action")
	}
}

func TestBuildDiagnosisPrompt(t *testing.T) {
	in := DiagnosisInput{
		Workflow: Workflow{Name: "Riset", Description: "Scrape five sources"},
		Node:     Node{ID: "scrape", Type: "browser", Name: "Scrape", Params: map[string]any{"url": "https://x"}},
		Input:    map[string]any{"q": "news"},
		Error:    "HTTP 403",
		Logs:     []string{"attempt 1: HTTP 403"},
		Recent:   []Run{{Number: 7, Status: RunFailed, Error: "scrape: HTTP 403"}},
		Lock:     []string{"browser", "webfetch"},
	}
	p := BuildDiagnosisPrompt(in)
	for _, want := range []string{
		"You are the guardian",
		"Scrape five sources",
		"scrape",
		"HTTP 403",
		"browser, webfetch",
		"#7 failed",
		"retry_fix",
		"skip",
		"fallback",
		"ask",
		"stop",
		"Reply with ONE JSON object only",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, p)
		}
	}
}

func TestBuildDiagnosisPromptTruncatesInput(t *testing.T) {
	big := strings.Repeat("x", 20<<10)
	in := DiagnosisInput{
		Workflow: Workflow{Name: "W"},
		Node:     Node{ID: "n", Type: "agent", Name: "N"},
		Input:    map[string]any{"blob": big},
		Error:    "boom",
	}
	p := BuildDiagnosisPrompt(in)
	if strings.Contains(p, big) {
		t.Fatal("prompt did not truncate the oversized input")
	}
}

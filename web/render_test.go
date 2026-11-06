package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolStatusWord(t *testing.T) {
	cases := map[string]string{
		"running":   "running",
		"success":   "ok",
		"ok":        "ok",
		"error":     "error",
		"rejected":  "rejected",
		"cancelled": "cancelled",
		"canceled":  "cancelled",
		"":          "",
	}
	for status, want := range cases {
		if got := ToolStatusWord(status); got != want {
			t.Fatalf("ToolStatusWord(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestToolLine(t *testing.T) {
	if got := ToolLine("bash", json.RawMessage(`{"command":"df -h"}`), "success"); got != "bash df -h · ok" {
		t.Fatalf("tool line = %q", got)
	}
	if got := ToolLine("bash", json.RawMessage(`{"command":"df -h"}`), "running"); got != "bash df -h · running" {
		t.Fatalf("running tool line = %q", got)
	}
	if got := ToolLine("read_file", json.RawMessage(`{"path":"main.go"}`), "error"); got != "read_file main.go · error" {
		t.Fatalf("error tool line = %q", got)
	}
	if got := ToolLine("bash", json.RawMessage(`{"command":"   "}`), "rejected"); got != "bash · rejected" {
		t.Fatalf("empty argument line = %q", got)
	}
}

func TestToolLineAskUser(t *testing.T) {
	two := json.RawMessage(`{"questions":[{"question":"Where should I save it?"},{"question":"Which sections?"}]}`)
	if got := ToolLine("ask_user", two, "answered"); got != "ask_user · 2 questions · answered" {
		t.Fatalf("answered ask_user tool line = %q", got)
	}
	one := json.RawMessage(`{"questions":[{"question":"Where should I save it?"}]}`)
	if got := ToolLine("ask_user", one, "unavailable"); got != "ask_user · 1 question · unavailable" {
		t.Fatalf("unavailable ask_user tool line = %q", got)
	}
	three := json.RawMessage(`{"questions":[{"question":"One?"},{"question":"Two?"},{"question":"Three?"}]}`)
	if got := ToolLine("ask_user", three, ""); got != "ask_user · 3 questions" {
		t.Fatalf("ask_user tool line without a status = %q", got)
	}
}

func TestToolLineCutsPrimaryArgumentTo60Characters(t *testing.T) {
	command := strings.Repeat("x", 80)
	got := ToolLine("bash", json.RawMessage(`{"command":"`+command+`"}`), "success")
	arg := strings.TrimSuffix(strings.TrimPrefix(got, "bash "), " · ok")
	if runes := []rune(arg); len(runes) != 60 {
		t.Fatalf("argument has %d runes, want 60: %q", len(runes), arg)
	}
	if !strings.HasSuffix(arg, "…") {
		t.Fatalf("cut argument = %q, want an ellipsis", arg)
	}
}

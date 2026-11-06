package toolfmt

import (
	"encoding/json"
	"testing"
)

func TestPrimaryArgAskUser(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no questions key", `{"header":"Pick"}`, ""},
		{"empty questions", `{"questions":[]}`, ""},
		{"unparseable input", `not json`, ""},
		{"one question", `{"questions":[{"question":"Where should I save it?"}]}`, "1 question"},
		{"three questions", `{"questions":[{"question":"One?"},{"question":"Two?"},{"question":"Three?"}]}`, "3 questions"},
	}
	for _, tc := range cases {
		if got := PrimaryArg("ask_user", json.RawMessage(tc.input)); got != tc.want {
			t.Fatalf("%s: PrimaryArg(ask_user) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPrimaryArgOtherTools(t *testing.T) {
	if got := PrimaryArg("bash", json.RawMessage(`{"command":"cargo test"}`)); got != "cargo test" {
		t.Fatalf("PrimaryArg(bash) = %q, want %q", got, "cargo test")
	}
}

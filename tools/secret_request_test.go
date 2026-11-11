package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeSecretRequester records the request and returns a fixed outcome.
type fakeSecretRequester struct {
	calls   []core.SecretRequest
	outcome core.SecretOutcome
	err     error
}

func (f *fakeSecretRequester) RequestSecret(_ context.Context, req core.SecretRequest) (core.SecretOutcome, error) {
	f.calls = append(f.calls, req)
	return f.outcome, f.err
}

func callSecretRequest(t *testing.T, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := NewSecretRequest().Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return out
}

// Spec 53 REQ-SEC-001, REQ-SEC-006: the result names the outcome and the
// secret, never a value, with the contract key order.
func TestSecretRequestOutcomes(t *testing.T) {
	for _, tc := range []struct {
		outcome core.SecretOutcome
		want    string
	}{
		{core.SecretOutcome{Status: core.SecretStatusSaved}, `{"status":"saved","name":"GITHUB_TOKEN"}`},
		{core.SecretOutcome{Status: core.SecretStatusCancelled, Reason: "owner"}, `{"status":"cancelled","name":"GITHUB_TOKEN","reason":"owner"}`},
		{core.SecretOutcome{Status: core.SecretStatusExpired, Reason: "timeout"}, `{"status":"expired","name":"GITHUB_TOKEN","reason":"timeout"}`},
		{core.SecretOutcome{Status: "weird"}, `{"status":"unavailable","name":"GITHUB_TOKEN"}`},
	} {
		fake := &fakeSecretRequester{outcome: tc.outcome}
		out := callSecretRequest(t, core.ToolCtx{SessionID: "s1", TurnID: "t1", CallID: "c1", AgentName: "a", Secrets: fake},
			`{"name":" GITHUB_TOKEN ","description":"Read your repos.","link":"https://github.com/settings/tokens"}`)
		if out.Status != core.ToolStatusSuccess || out.Output != tc.want {
			t.Fatalf("output = %+v, want %s", out, tc.want)
		}
		if len(fake.calls) != 1 {
			t.Fatalf("calls = %d", len(fake.calls))
		}
		got := fake.calls[0]
		if got.Name != "GITHUB_TOKEN" || got.Description != "Read your repos." || got.Link != "https://github.com/settings/tokens" ||
			got.SessionID != "s1" || got.TurnID != "t1" || got.CallID != "c1" || got.AgentName != "a" {
			t.Fatalf("request = %+v", got)
		}
	}
}

// REQ-SEC-002: an invalid name, a missing description, and a non-http link
// are refused before any card opens.
func TestSecretRequestRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"name":"my-token","description":"x"}`, `"my-token" is not a valid secret name`},
		{`{"name":"1TOKEN","description":"x"}`, "not a valid secret name"},
		{`{"name":"","description":"x"}`, "name is required"},
		{`{"name":"TOKEN","description":"  "}`, "description is required"},
		{`{"name":"TOKEN","description":"x","link":"javascript:alert(1)"}`, "link must be an http or https URL"},
		{`{"name":"TOKEN","description":"x","value":"hunter2"}`, "invalid input"},
	} {
		fake := &fakeSecretRequester{outcome: core.SecretOutcome{Status: core.SecretStatusSaved}}
		out := callSecretRequest(t, core.ToolCtx{SessionID: "s1", Secrets: fake}, tc.input)
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, tc.want) {
			t.Fatalf("%s: output = %+v, want error containing %q", tc.input, out, tc.want)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("%s: a card opened for invalid input", tc.input)
		}
	}
}

// REQ-SEC-009: a Telegram session gets an error that sends the owner to the
// web, and a runtime without the service says it needs the web chat.
func TestSecretRequestChannelErrors(t *testing.T) {
	fake := &fakeSecretRequester{err: core.ErrSecretTelegram}
	out := callSecretRequest(t, core.ToolCtx{SessionID: "s1", Secrets: fake}, `{"name":"TOKEN","description":"x"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "Telegram") || !strings.Contains(out.Output, "web") {
		t.Fatalf("telegram output = %+v", out)
	}
	out = callSecretRequest(t, core.ToolCtx{SessionID: "s1"}, `{"name":"TOKEN","description":"x"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "web chat") {
		t.Fatalf("no service output = %+v", out)
	}
	fake = &fakeSecretRequester{err: errors.New("boom")}
	out = callSecretRequest(t, core.ToolCtx{SessionID: "s1", Secrets: fake}, `{"name":"TOKEN","description":"x"}`)
	if out.Status != core.ToolStatusError || out.Output != "boom" {
		t.Fatalf("error output = %+v", out)
	}
}

// REQ-SEC-007: the tool is in the standard toolset, and its description tells
// the agent to prefer existing names and that the value is never returned.
func TestSecretRequestSpecAndToolset(t *testing.T) {
	spec := NewSecretRequest().Spec()
	if spec.Name != "secret_request" {
		t.Fatalf("name = %q", spec.Name)
	}
	for _, want := range []string{"Prefer a name that already exists", "replaces it", "never shown to you", "Deleting secrets is not possible"} {
		if !strings.Contains(spec.Description, want) {
			t.Fatalf("description lacks %q: %s", want, spec.Description)
		}
	}
	if !slices.Contains(core.StandardTools, "secret_request") {
		t.Fatal("secret_request is not in core.StandardTools")
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
}

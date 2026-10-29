package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func (h *harness) systemPrompts(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, event := range h.events(t) {
		if event.Kind != core.KindSystemMessage {
			continue
		}
		var payload core.SystemMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload.Text)
	}
	return out
}

func writeHomeFile(t *testing.T, home, agent, name, text string) {
	t.Helper()
	dir := filepath.Join(home, "agents", agent)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPromptWithoutAgentHome(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	h.run(t, "hi")
	prompts := h.systemPrompts(t)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %d", len(prompts))
	}
	prompt := prompts[0]
	ws, err := filepath.EvalSymlinks(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prompt, "# Runtime context\n") {
		t.Fatalf("prompt should hold only runtime context:\n%s", prompt)
	}
	for _, want := range []string{"- agent: assistant", "- channel: library", "- workspace: " + ws, "- date: " + time.Now().Format("2006-01-02")} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestPromptFromAgentHome(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("one"), textResponse("two")}}, nil)
	writeHomeFile(t, h.home, "assistant", "AGENT.md", "You are the home persona.")
	writeHomeFile(t, h.home, "assistant", "USER.md", "Name: Ridho")
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", "Server kenari")
	h.run(t, "hi")
	prompt := h.systemPrompts(t)[0]
	for _, want := range []string{"You are the home persona.", "# About the user\nName: Ridho", "# Your memory\nServer kenari"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	writeHomeFile(t, h.home, "assistant", "AGENT.md", "You are the edited persona.")
	h.run(t, "again")
	prompts := h.systemPrompts(t)
	if !strings.Contains(prompts[len(prompts)-1], "You are the edited persona.") {
		t.Fatalf("edit not applied on next turn:\n%s", prompts[len(prompts)-1])
	}
}

func TestPromptFileWinsOverAgentHome(t *testing.T) {
	agents := map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", PromptFile: "custom.md"}}
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, agents)
	if err := os.WriteFile(filepath.Join(h.home, "custom.md"), []byte("You are from prompt_file."), 0o600); err != nil {
		t.Fatal(err)
	}
	writeHomeFile(t, h.home, "assistant", "AGENT.md", "You are the home persona.")
	h.run(t, "hi")
	prompt := h.systemPrompts(t)[0]
	if !strings.HasPrefix(prompt, "You are from prompt_file.") || strings.Contains(prompt, "home persona") {
		t.Fatalf("prompt_file should win:\n%s", prompt)
	}
}

func TestPromptBuiltOncePerTurn(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"missing.txt"}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	h.run(t, "read it")
	if got := len(h.systemPrompts(t)); got != 1 {
		t.Fatalf("system messages in one turn = %d, want 1", got)
	}
}

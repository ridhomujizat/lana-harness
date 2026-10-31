package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
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

func writeSharedFile(t *testing.T, home, text string) {
	t.Helper()
	if err := os.WriteFile(agenthome.SharedUser(home), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// firstUserMessage returns the session's first user/message.
func (h *harness) firstUserMessage(t *testing.T) core.UserMessage {
	t.Helper()
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	t.Fatal("no user/message")
	return core.UserMessage{}
}

// lastUserMessage returns the session's last user/message.
func (h *harness) lastUserMessage(t *testing.T) core.UserMessage {
	t.Helper()
	found := core.UserMessage{}
	have := false
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		if err := json.Unmarshal(event.Payload, &found); err != nil {
			t.Fatal(err)
		}
		have = true
	}
	if !have {
		t.Fatal("no user/message")
	}
	return found
}

// userText joins the text blocks of a user message.
func userText(msg core.UserMessage) string {
	parts := make([]string, 0, len(msg.Content))
	for _, block := range msg.Content {
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n")
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
	// The prompt keeps the fixed blocking rules before the runtime context.
	if !strings.Contains(prompt, "# Runtime context\n") {
		t.Fatalf("prompt has no runtime context:\n%s", prompt)
	}
	for _, want := range []string{"- agent: assistant", "- channel: library", "- workspace: " + ws} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	// REQ-LTC-001: the date line moved to the first user message.
	if strings.Contains(prompt, "- date:") {
		t.Fatalf("head prompt must not carry the date:\n%s", prompt)
	}
	if user := userText(h.firstUserMessage(t)); !strings.Contains(user, "<context>\ndate: ") {
		t.Fatalf("first user message lacks the <context> block:\n%s", user)
	}
}

func TestPromptFromAgentHome(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("one"), textResponse("two")}}, nil)
	writeHomeFile(t, h.home, "assistant", "AGENT.md", "You are the home persona.")
	writeSharedFile(t, h.home, "Name: Ridho")
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
	// REQ-LTC-002: the head stays stable; the persona change goes out as a
	// <system-update> on the next turn's first user message.
	if got := len(h.systemPrompts(t)); got != 1 {
		t.Fatalf("system messages = %d, want 1", got)
	}
	update := userText(h.lastUserMessage(t))
	if !strings.Contains(update, "<system-update>") || !strings.Contains(update, "You are the edited persona.") {
		t.Fatalf("the persona change must go out as a <system-update>:\n%s", update)
	}
}

// TestPromptSharedUserCoversACHOME004 covers AC-HOME-004: every agent reads the
// same $LANA_HOME/USER.md.
func TestPromptSharedUserCoversACHOME004(t *testing.T) {
	agents := map[string]core.AgentConfig{
		"helper": {Provider: "mock", Model: "m"},
		"worker": {Provider: "mock", Model: "m"},
	}
	// A fresh session per agent so each writes its own head prompt.
	for _, name := range []string{"helper", "worker"} {
		h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, agents)
		writeSharedFile(t, h.home, "Name: Ridho")
		if err := h.reg.SetAgent(context.Background(), h.info.ID, name); err != nil {
			t.Fatal(err)
		}
		h.run(t, "hi")
		prompt := h.systemPrompts(t)[0]
		if !strings.Contains(prompt, "# About the user\nName: Ridho") {
			t.Fatalf("%s prompt lacks the shared user entry:\n%s", name, prompt)
		}
	}
}

// TestPromptIgnoresPerAgentUserCoversACHOME005 covers AC-HOME-005: a legacy
// per-agent USER.md is not read once the shared file exists.
func TestPromptIgnoresPerAgentUserCoversACHOME005(t *testing.T) {
	agents := map[string]core.AgentConfig{"helper": {Provider: "mock", Model: "m"}}
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, agents)
	writeHomeFile(t, h.home, "helper", "USER.md", "Name: Old")
	writeSharedFile(t, h.home, "Name: Ridho")
	if err := h.reg.SetAgent(context.Background(), h.info.ID, "helper"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "hi")
	prompt := h.systemPrompts(t)[0]
	if !strings.Contains(prompt, "Name: Ridho") || strings.Contains(prompt, "Name: Old") {
		t.Fatalf("prompt should hold only the shared user entry:\n%s", prompt)
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

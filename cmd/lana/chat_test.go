package main

import (
	"context"
	"encoding/json"
	"testing"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func newChatRuntime(t *testing.T) *lana.Runtime {
	t.Helper()
	text := "ok"
	end := "end_turn"
	layer := "[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.assistant]\nprovider = \"mock\"\nmodel = \"m\"\n\n[[routes]]\nchannel = \"cli\"\nagent = \"assistant\"\n"
	rt, err := lana.New(context.Background(), lana.Options{
		Layers:    []config.Layer{{Name: "test", TOML: []byte(layer)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{{Events: []mock.Event{{Text: &text}, {Stop: &end}}}}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestChatSessionContinueAndResume(t *testing.T) {
	rt := newChatRuntime(t)
	ctx := context.Background()
	info, _, notice, err := chatSession(ctx, rt, "assistant", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if notice == "" {
		t.Fatal("continue without a session must give a notice")
	}
	if info.Channel != "cli" || info.Agent != "assistant" {
		t.Fatalf("session = %+v", info)
	}
	again, _, notice, err := chatSession(ctx, rt, "assistant", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != info.ID || notice != "" {
		t.Fatalf("continue = %+v notice=%q", again, notice)
	}
	resumed, _, _, err := chatSession(ctx, rt, "assistant", false, info.ID[:6])
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != info.ID {
		t.Fatalf("resume = %+v", resumed)
	}
	if _, _, _, err := chatSession(ctx, rt, "assistant", false, "missing"); err == nil {
		t.Fatal("resume with a wrong id must error")
	}
}

func TestRestoreEventsLimitedToMessages(t *testing.T) {
	user, _ := json.Marshal(core.UserMessage{Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}})
	assistant, _ := json.Marshal(core.AssistantMessage{Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}})
	events := []core.SessionEvent{
		{Kind: core.KindToolCall, Payload: json.RawMessage("{}")},
		{Kind: core.KindUserMessage, Payload: user},
		{Kind: core.KindAssistantMessage, Payload: assistant},
	}
	out := restoreEvents(events)
	if len(out) != 2 || out[0].Kind != core.KindUserMessage || out[1].Kind != core.KindAssistantMessage {
		t.Fatalf("restore = %+v", out)
	}
}

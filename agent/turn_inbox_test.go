package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestTurnClaimedMessageSurvivesStepError covers that an owner message popInbox
// claimed for a step is returned to the inbox when that step fails before its
// user/message event is stored, so the next turn still handles it.
func TestTurnClaimedMessageSurvivesStepError(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		textResponse("owner handled"),
	}}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}},
	})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fail the second pre-step call: by then the queued owner message has been
	// popped for the step, so the error exit must return it to the inbox.
	var calls atomic.Int32
	h.hooks.AgentPreStep.SetDefault(func(_ context.Context, in core.PreStepInput) (core.PreStepDecision, error) {
		if calls.Add(1) == 2 {
			return core.PreStepDecision{}, errors.New("boom")
		}
		return core.PreStepDecision{Input: in.Input}, nil
	})
	// Queue the owner message during the first stream call, while the turn runs.
	h.llm.providers["mock"] = &queueingProvider{
		inner: h.llm.providers["mock"], reg: h.reg, sessionID: h.info.ID,
		text: "owner interjection", after: 1,
	}

	h.run(t, "hi")
	waitForIdle(t, h)

	found := false
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(userText(payload), "owner interjection") {
			found = true
		}
	}
	if !found {
		t.Fatal("the claimed owner message was lost on the step error")
	}
}

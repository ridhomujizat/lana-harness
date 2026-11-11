package lana

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// newPostTestRuntime builds a runtime with agent "coder" and scripted mock
// responses, for the tests that need real turn timing.
func newPostTestRuntime(t *testing.T, responses []mock.Response) *Runtime {
	t.Helper()
	cfg := `
[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
toolset = "custom"
provider = "mock"
model = "m"

[[routes]]
channel = "library"
agent = "coder"
`
	rt, err := New(context.Background(), Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(cfg)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: responses})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// PostAgentMessage posts into the agent's existing web chat session.
func TestPostAgentMessageIntoExistingSession(t *testing.T) {
	rt := newNotifyTestRuntime(t)
	ctx := context.Background()
	info := titledSession(t, rt, "web", "Chat")

	sessionID, err := rt.PostAgentMessage(ctx, "coder", "Daily report", "workflow", "Daily digest")
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != info.ID {
		t.Fatalf("session = %q, want %q", sessionID, info.ID)
	}
	events, err := rt.History(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	var assistant *core.AssistantMessage
	var starts, ends int
	for _, event := range events {
		switch event.Kind {
		case core.KindTurnStart:
			starts++
		case core.KindTurnEnd:
			ends++
		case core.KindAssistantMessage:
			var msg core.AssistantMessage
			if json.Unmarshal(event.Payload, &msg) != nil {
				continue
			}
			if msg.Source != "" {
				assistant = &msg
			}
		}
	}
	if assistant == nil {
		t.Fatal("no out-of-band assistant message")
	}
	if assistant.Source != "workflow" || assistant.SourceName != "Daily digest" || assistant.StopReason != "end_turn" {
		t.Fatalf("assistant = %+v", assistant)
	}
	if len(assistant.Content) != 1 || assistant.Content[0].Kind != core.BlockText || assistant.Content[0].Text != "Daily report" {
		t.Fatalf("content = %+v", assistant.Content)
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("turn/start = %d, turn/end = %d, want 1 and 1", starts, ends)
	}
}

// PostAgentMessage creates a web session on channel "web" with address "owner"
// when the agent has none.
func TestPostAgentMessageCreatesSession(t *testing.T) {
	rt := newNotifyTestRuntime(t)
	ctx := context.Background()

	sessionID, err := rt.PostAgentMessage(ctx, "coder", "report", "workflow", "wf")
	if err != nil {
		t.Fatal(err)
	}
	info, err := rt.SessionInfo(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Agent != "coder" || info.Channel != "web" || info.Address != "owner" || info.ParentSessionID != "" {
		t.Fatalf("session = %+v", info)
	}
	// A second post reuses the same session.
	again, err := rt.PostAgentMessage(ctx, "coder", "report 2", "workflow", "wf")
	if err != nil {
		t.Fatal(err)
	}
	if again != sessionID {
		t.Fatalf("second post session = %q, want %q", again, sessionID)
	}
}

// PostAgentMessage rejects an unknown agent and empty text.
func TestPostAgentMessageRejects(t *testing.T) {
	rt := newNotifyTestRuntime(t)
	ctx := context.Background()
	if _, err := rt.PostAgentMessage(ctx, "ghost", "x", "workflow", "wf"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("unknown agent error = %v", err)
	}
	if _, err := rt.PostAgentMessage(ctx, "coder", "   ", "workflow", "wf"); !errors.Is(err, ErrEmptyText) {
		t.Fatalf("empty text error = %v", err)
	}
}

// PostAgentMessage waits for a running turn on the chat before it appends its
// own synthetic turn, so the two never interleave.
func TestPostAgentMessageWaitsForRunningTurn(t *testing.T) {
	delay := 500
	text, stop := "done", "end_turn"
	rt := newPostTestRuntime(t, []mock.Response{{Events: []mock.Event{{DelayMS: &delay}, {Text: &text}, {Stop: &stop}}}})
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "coder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SetSessionTitle(ctx, info.ID, "Busy"); err != nil {
		t.Fatal(err)
	}

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = rt.Run(ctx, Request{SessionID: info.ID, Prompt: "hi"})
	}()
	waitFor(t, "the turn to become active", func() bool { return rt.TurnActive(info.ID) })

	postDone := make(chan error, 1)
	go func() {
		_, err := rt.PostAgentMessage(ctx, "coder", "report", "workflow", "wf")
		postDone <- err
	}()
	select {
	case err := <-postDone:
		t.Fatalf("PostAgentMessage returned while the turn ran: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	<-runDone
	select {
	case err := <-postDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PostAgentMessage did not return after the turn ended")
	}
	if rt.TurnActive(info.ID) {
		t.Fatal("a turn is still active after the post")
	}
}

// A posted message fires the desktop reply notification like any other web
// reply, opening #/chats/<agent>.
func TestPostAgentMessageFiresReplyNotification(t *testing.T) {
	rt := newNotifyTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.StartNotifications(ctx)
	_, events, unsubscribe := rt.Notifications().Subscribe("")
	defer unsubscribe()

	info := titledSession(t, rt, "web", "Chat")
	if _, err := rt.PostAgentMessage(ctx, "coder", "Daily report", "workflow", "Daily digest"); err != nil {
		t.Fatal(err)
	}
	e, ok := waitNotify(t, events, notify.KindReply, 5*time.Second)
	if !ok {
		t.Fatal("no reply notification for a posted message")
	}
	if e.SessionID != info.ID || e.URL != "#/chats/coder" || e.Body != "Daily report" {
		t.Fatalf("reply = %+v", e)
	}
}

// After a post and a following owner message the derived history has only the
// two owner messages: the sourced post is not context unless the owner replies.
func TestPostAgentMessageDerivedHistory(t *testing.T) {
	rt := newNotifyTestRuntime(t)
	ctx := context.Background()
	info := titledSession(t, rt, "web", "Chat")

	appendUser := func(text string) {
		t.Helper()
		if _, err := rt.store.Append(ctx, info.ID, []core.EventDraft{{
			Kind:    core.KindUserMessage,
			Payload: core.UserMessage{V: core.PayloadVersion, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	appendUser("hi")
	if _, err := rt.PostAgentMessage(ctx, "coder", "Daily report", "workflow", "Daily digest"); err != nil {
		t.Fatal(err)
	}
	appendUser("explain item 2")

	messages, err := rt.store.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].Role != core.RoleUser || messages[0].Content[0].Text != "hi" {
		t.Fatalf("first user = %+v", messages[0])
	}
	if messages[1].Role != core.RoleUser || messages[1].Content[0].Text != "explain item 2" {
		t.Fatalf("second user = %+v", messages[1])
	}
}

// A reply to a posted automation message is allowed and carries the quote; the
// derived history keeps it. Any other reply_to is rejected.
func TestReplyToAutomationMessage(t *testing.T) {
	rt := newNotifyTestRuntime(t, "answer")
	ctx := context.Background()
	info := titledSession(t, rt, "web", "Chat")
	if _, err := rt.PostAgentMessage(ctx, "coder", "Daily report", "workflow", "Daily digest"); err != nil {
		t.Fatal(err)
	}
	events, err := rt.History(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sourced int64
	for _, event := range events {
		if event.Kind != core.KindAssistantMessage {
			continue
		}
		var msg core.AssistantMessage
		if json.Unmarshal(event.Payload, &msg) != nil {
			continue
		}
		if msg.Source != "" {
			sourced = event.Seq
		}
	}
	if sourced == 0 {
		t.Fatal("no sourced message")
	}

	// A reply to the sourced message runs and stores the quote and ReplyTo.
	if _, err := rt.Run(ctx, Request{SessionID: info.ID, Prompt: "explain item 2", ReplyTo: sourced}); err != nil {
		t.Fatal(err)
	}
	chain, err := rt.History(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	var reply core.UserMessage
	var userSeq, normalSeq int64
	for _, event := range chain {
		switch event.Kind {
		case core.KindUserMessage:
			var msg core.UserMessage
			if json.Unmarshal(event.Payload, &msg) != nil {
				continue
			}
			if userSeq == 0 {
				userSeq = event.Seq
			}
			if msg.ReplyTo != nil {
				reply = msg
			}
		case core.KindAssistantMessage:
			var msg core.AssistantMessage
			if json.Unmarshal(event.Payload, &msg) != nil {
				continue
			}
			if msg.Source == "" && normalSeq == 0 {
				normalSeq = event.Seq
			}
		}
	}
	if reply.ReplyTo == nil || reply.ReplyTo.Seq != sourced || reply.ReplyTo.SourceName != "Daily digest" {
		t.Fatalf("reply = %+v", reply.ReplyTo)
	}
	if reply.ContextBlocks < 2 {
		t.Fatalf("ContextBlocks = %d, want at least 2", reply.ContextBlocks)
	}
	if got := core.TextContent(reply.Content[reply.ContextBlocks:]); got != "explain item 2" {
		t.Fatalf("owner text = %q", got)
	}
	// The derived history carries the quoted text for later turns.
	history, err := rt.store.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	quoted := false
	for _, message := range history {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "Daily report") {
				quoted = true
			}
		}
	}
	if !quoted {
		t.Fatalf("history lost the quote: %+v", history)
	}

	// A reply to a user message, a normal assistant message, or a missing seq
	// is rejected.
	for name, seq := range map[string]int64{"missing": 9999, "user": userSeq, "normal": normalSeq} {
		if err := rt.SendReply(ctx, info.ID, "x", seq); !errors.Is(err, ErrInvalidReplyTo) {
			t.Fatalf("%s seq %d error = %v", name, seq, err)
		}
	}
	// Another session's sourced event is rejected as well.
	other, err := rt.CreateChannelSession(ctx, "web", "owner", "coder")
	if err != nil {
		t.Fatal(err)
	}
	drafts := make([]core.EventDraft, 0, 5)
	for i := 0; i < 4; i++ {
		drafts = append(drafts, core.EventDraft{Kind: core.KindUserMessage, Payload: core.UserMessage{V: core.PayloadVersion, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "filler"}}}})
	}
	drafts = append(drafts, core.EventDraft{Kind: core.KindAssistantMessage, Payload: core.AssistantMessage{
		V: core.PayloadVersion, StopReason: "end_turn", Source: "workflow", SourceName: "wf",
		Content: []core.ContentBlock{{Kind: core.BlockText, Text: "other report"}},
	}})
	appended, err := rt.store.Append(ctx, other.ID, drafts)
	if err != nil {
		t.Fatal(err)
	}
	seqOther := appended[len(appended)-1].Seq
	if err := rt.SendReply(ctx, info.ID, "x", seqOther); !errors.Is(err, ErrInvalidReplyTo) {
		t.Fatalf("other session seq %d error = %v", seqOther, err)
	}
}

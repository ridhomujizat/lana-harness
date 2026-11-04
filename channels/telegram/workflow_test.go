package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// workflowCallback pushes a callback query with a message so the handler can
// keep its text when it edits the message.
func (f *fakeAPI) workflowCallback(chatID int64, chatType string, from int64, data, text string) {
	f.push(Update{CallbackQuery: &CallbackQuery{
		ID:   fmt.Sprintf("cb%d", f.nextID),
		From: User{ID: from},
		Data: data,
		Message: &Message{
			MessageID: 42,
			Chat:      Chat{ID: chatID, Type: chatType},
			Text:      text,
		},
	}})
}

// API-WF-022: SendOwnerButtons sends one message with the keyboard to the owner
// private chat, and a nil keyboard sends none.
func TestWorkflowSendOwnerButtons(t *testing.T) {
	api := newFakeAPI(t)
	ch, _, _ := startChannel(t, api, newFakeBackend())
	rows := [][]Button{{{Text: "Apply", Data: "w:d_1:apply"}, {Text: "Skip", Data: "w:d_1:skip"}}}

	if err := ch.SendOwnerButtons(context.Background(), "Riset harian · Run #8 needs you", rows); err != nil {
		t.Fatalf("SendOwnerButtons: %v", err)
	}
	call := api.waitCall("sendMessage", textIs("Riset harian · Run #8 needs you"))
	if id, _ := call.Params["chat_id"].(float64); int64(id) != ownerChat {
		t.Fatalf("chat_id = %v, want %d", call.Params["chat_id"], ownerChat)
	}
	if _, ok := call.Params["parse_mode"]; ok {
		t.Fatalf("parse_mode set: %v", call.Params)
	}
	if got := buttonData(t, call, "Apply"); got != "w:d_1:apply" {
		t.Fatalf("callback data = %q", got)
	}
	if n := api.count("sendMessage", textIs("Riset harian · Run #8 needs you")); n != 1 {
		t.Fatalf("messages = %d, want 1", n)
	}

	if err := ch.SendOwnerButtons(context.Background(), "no buttons", nil); err != nil {
		t.Fatalf("SendOwnerButtons: %v", err)
	}
	plain := api.waitCall("sendMessage", textIs("no buttons"))
	if _, ok := plain.Params["reply_markup"]; ok {
		t.Fatalf("nil rows sent a keyboard: %v", plain.Params)
	}
}

// API-WF-022: SendOwnerButtons reaches every owner private chat and skips an
// owner without a private-chat route.
func TestWorkflowSendOwnerButtonsEveryOwner(t *testing.T) {
	api := newFakeAPI(t)
	cfg, err := ParseConfig(map[string]any{"api_base": api.srv.URL, "owner_ids": []any{ownerID, memberID, int64(333)}},
		[]core.RouteConfig{
			{Channel: "telegram", Agent: "helper", Match: map[string]any{"chat_id": ownerChat}},
			{Channel: "telegram", Agent: "helper", Match: map[string]any{"chat_id": memberID}},
		})
	if err != nil {
		t.Fatal(err)
	}
	ch := New(cfg, "TOKEN123", newFakeBackend())
	rows := [][]Button{{{Text: "Apply", Data: "w:d_1:apply"}}}

	if err := ch.SendOwnerButtons(context.Background(), "notice", rows); err != nil {
		t.Fatalf("SendOwnerButtons: %v", err)
	}
	if n := api.count("sendMessage", textIs("notice")); n != 2 {
		t.Fatalf("messages = %d, want 2", n)
	}
}

// API-WF-022: with no owner private chat SendOwnerButtons sends nothing.
func TestWorkflowSendOwnerButtonsNoOwners(t *testing.T) {
	api := newFakeAPI(t)
	cfg, err := ParseConfig(map[string]any{"api_base": api.srv.URL, "owner_ids": []any{}},
		[]core.RouteConfig{{Channel: "telegram", Agent: "helper", Match: map[string]any{"chat_id": ownerChat}}})
	if err != nil {
		t.Fatal(err)
	}
	ch := New(cfg, "TOKEN123", newFakeBackend())

	if err := ch.SendOwnerButtons(context.Background(), "notice", nil); err != nil {
		t.Fatalf("SendOwnerButtons: %v", err)
	}
	if n := api.count("sendMessage", textIs("notice")); n != 0 {
		t.Fatalf("messages = %d, want 0", n)
	}
}

// API-WF-022: an owner command a workflow claims is answered with the hook text
// and starts no agent turn.
func TestWorkflowCommandHandled(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	ch, _, _ := startChannel(t, api, backend)
	var got string
	ch.SetWorkflowHooks(WorkflowHooks{Command: func(_ context.Context, text string) (string, bool) {
		got = text
		return "Run #8 of Riset harian started", true
	}})

	api.text(ownerChat, "private", ownerID, "/riset ai")
	api.waitCall("sendMessage", textIs("Run #8 of Riset harian started"))
	if got != "/riset ai" {
		t.Fatalf("command text = %q, want %q", got, "/riset ai")
	}
	if prompts := backend.promptList(); len(prompts) != 0 {
		t.Fatalf("a turn started: %v", prompts)
	}
}

// API-WF-022: a workflow that does not handle an owner command falls back to
// the unknown-command reply.
func TestWorkflowCommandNotHandled(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	ch, _, _ := startChannel(t, api, backend)
	ch.SetWorkflowHooks(WorkflowHooks{Command: func(context.Context, string) (string, bool) {
		return "", false
	}})

	api.text(ownerChat, "private", ownerID, "/riset")
	api.waitCall("sendMessage", textIs("· unknown command: /riset"))
	if prompts := backend.promptList(); len(prompts) != 0 {
		t.Fatalf("a turn started: %v", prompts)
	}
}

// API-WF-022: a built-in command keeps priority over the workflow hook.
func TestWorkflowCommandBuiltinPriority(t *testing.T) {
	api := newFakeAPI(t)
	ch, _, _ := startChannel(t, api, newFakeBackend())
	var called atomic.Bool
	ch.SetWorkflowHooks(WorkflowHooks{Command: func(context.Context, string) (string, bool) {
		called.Store(true)
		return "hook", true
	}})

	api.text(ownerChat, "private", ownerID, "/new")
	api.waitCall("sendMessage", textIs("· new session S1"))
	if called.Load() {
		t.Fatal("a built-in command reached the workflow hook")
	}
}

// API-WF-022: a non-owner command does not reach the workflow hook.
func TestWorkflowCommandNonOwner(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	ch, _, _ := startChannel(t, api, backend)
	var called atomic.Bool
	ch.SetWorkflowHooks(WorkflowHooks{Command: func(context.Context, string) (string, bool) {
		called.Store(true)
		return "hook", true
	}})

	api.text(groupChat, "supergroup", memberID, "/riset")
	api.waitCall("sendMessage", textIs("· unknown command: /riset"))
	if called.Load() {
		t.Fatal("a non-owner reached the workflow hook")
	}
	if prompts := backend.promptList(); len(prompts) != 0 {
		t.Fatalf("a turn started: %v", prompts)
	}
}

// API-WF-022: an owner "w:" press answers the decision, replies "Done", and
// edits the message with the result line and no keyboard.
func TestWorkflowCallbackAnswersDecision(t *testing.T) {
	api := newFakeAPI(t)
	ch, _, _ := startChannel(t, api, newFakeBackend())
	var gotDecision, gotChoice string
	ch.SetWorkflowHooks(WorkflowHooks{Callback: func(_ context.Context, decisionID, choice string) (string, error) {
		gotDecision, gotChoice = decisionID, choice
		return "✓ Applied; retrying the node", nil
	}})

	api.workflowCallback(ownerChat, "private", ownerID, "w:d_1:apply", "🔐 The ranking reply was cut off")
	api.waitCall("answerCallbackQuery", textIs("Done"))
	edit := api.waitCall("editMessageText", textIs("🔐 The ranking reply was cut off\n\n✓ Applied; retrying the node"))
	if _, ok := edit.Params["reply_markup"]; ok {
		t.Fatalf("keyboard was not removed: %v", edit.Params)
	}
	if gotDecision != "d_1" || gotChoice != "apply" {
		t.Fatalf("callback args = %q/%q, want d_1/apply", gotDecision, gotChoice)
	}
}

// API-WF-022: a callback error answers with the error and records it on the
// message.
func TestWorkflowCallbackError(t *testing.T) {
	api := newFakeAPI(t)
	ch, _, _ := startChannel(t, api, newFakeBackend())
	ch.SetWorkflowHooks(WorkflowHooks{Callback: func(context.Context, string, string) (string, error) {
		return "", errors.New("decision already resolved")
	}})

	api.workflowCallback(ownerChat, "private", ownerID, "w:d_1:apply", "decide")
	api.waitCall("answerCallbackQuery", textIs("decision already resolved"))
	api.waitCall("editMessageText", textIs("decide\n\nError: decision already resolved"))
}

// API-WF-022: without hooks a "w:" press answers "Workflows are not available"
// and edits nothing.
func TestWorkflowCallbackUnavailable(t *testing.T) {
	api := newFakeAPI(t)
	startChannel(t, api, newFakeBackend())

	api.workflowCallback(ownerChat, "private", ownerID, "w:d_1:apply", "decide")
	api.waitCall("answerCallbackQuery", textIs("Workflows are not available"))
	if n := api.count("editMessageText", nil); n != 0 {
		t.Fatalf("unavailable callback edited %d messages", n)
	}
}

// API-WF-022: a non-owner cannot answer a workflow decision.
func TestWorkflowCallbackNonOwner(t *testing.T) {
	api := newFakeAPI(t)
	ch, _, _ := startChannel(t, api, newFakeBackend())
	var called atomic.Bool
	ch.SetWorkflowHooks(WorkflowHooks{Callback: func(context.Context, string, string) (string, error) {
		called.Store(true)
		return "", nil
	}})

	api.workflowCallback(groupChat, "supergroup", memberID, "w:d_1:apply", "decide")
	api.waitCall("answerCallbackQuery", textIs("only the owner can answer"))
	if called.Load() {
		t.Fatal("a non-owner reached the workflow callback hook")
	}
	if n := api.count("editMessageText", nil); n != 0 {
		t.Fatalf("non-owner edited %d messages", n)
	}
}

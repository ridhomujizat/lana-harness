package telegram

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// challengeRe extracts the 4-digit challenge a Block-tier bubble asks for.
var challengeRe = regexp.MustCompile(`Reply with (\d{4}) to run it`)

// keyboardTexts flattens an approval or edit call's inline keyboard into its
// button labels, so a test can count buttons and check for absence.
func keyboardTexts(t *testing.T, call apiCall) []string {
	t.Helper()
	markup, ok := call.Params["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("%s call has no keyboard: %v", call.Method, call.Params)
	}
	rows, _ := markup["inline_keyboard"].([]any)
	var out []string
	for _, row := range rows {
		buttons, _ := row.([]any)
		for _, b := range buttons {
			item, _ := b.(map[string]any)
			text, _ := item["text"].(string)
			out = append(out, text)
		}
	}
	return out
}

// hasText reports whether labels contains text.
func hasText(labels []string, text string) bool {
	for _, label := range labels {
		if label == text {
			return true
		}
	}
	return false
}

// callbackID returns the request id embedded in an approval callback data.
func callbackID(t *testing.T, data string) string {
	t.Helper()
	parts := strings.Split(data, ":")
	if len(parts) < 2 || parts[0] != "a" {
		t.Fatalf("callback data %q is not an approval", data)
	}
	return parts[1]
}

// blockTurn installs a turn that asks approval of one Block-tier hardline call
// and forwards its outcome to outcomes.
func blockTurn(backend *fakeBackend, ch **Channel, outcomes chan<- core.ApprovalOutcome) {
	input := []byte(`{"command":"rm -rf ~"}`)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_call", CallID: "c1", Name: "bash", Input: input})
		time.Sleep(30 * time.Millisecond)
		outcomes <- (*ch).Approve(ctx, core.ApprovalRequest{
			SessionID: session, CallID: "c1", ToolName: "bash", Input: input,
			Hardline: &core.HardlineMatch{Rule: "HL-B-001", Tier: core.HardlineBlock, Reason: "recursive delete of home"},
		})
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
}

// sentList copies the texts the backend delivered to the agent's inbox.
func sentList(backend *fakeBackend) []string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]string(nil), backend.sent...)
}

// AC-HLF-014: a Block-tier request shows the full command and only offers
// "Allow once…" and "Reject"; the owner confirms by replying with the 4-digit
// challenge, which allows the call once and verified without reaching the agent.
func TestHardlineBlockChallenge(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 1)
	blockTurn(backend, &ch, outcomes)
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "clean up")
	ask := api.waitCall("sendMessage", textHas("⚠️ HL-B-001 · recursive delete of home"))
	if text, _ := ask.Params["text"].(string); !strings.Contains(text, "bash: rm -rf ~") {
		t.Fatalf("bubble does not show the full argument: %q", text)
	}
	labels := keyboardTexts(t, ask)
	if len(labels) != 2 || !hasText(labels, "Allow once…") || !hasText(labels, "Reject") {
		t.Fatalf("block keyboard = %v", labels)
	}
	if hasText(labels, "Allow for session") {
		t.Fatalf("block keyboard offers a session grant: %v", labels)
	}

	id := callbackID(t, buttonData(t, ask, "Allow once…"))
	api.callback(ownerChat, "private", ownerID, "a:"+id+":verify")
	edit := api.waitCall("editMessageText", textHas("Reply with "))
	match := challengeRe.FindStringSubmatch(edit.Params["text"].(string))
	if match == nil {
		t.Fatalf("no challenge in %q", edit.Params["text"])
	}
	if armed := keyboardTexts(t, edit); len(armed) != 1 || !hasText(armed, "Reject") {
		t.Fatalf("armed keyboard = %v", armed)
	}

	api.text(ownerChat, "private", ownerID, match[1])
	if got := <-outcomes; got != core.OutcomeAllowedVerified {
		t.Fatalf("outcome = %q", got)
	}
	if sent := sentList(backend); len(sent) != 0 {
		t.Fatalf("challenge text reached the agent: %v", sent)
	}
	api.waitCall("editMessageText", textIs("✅ Allowed (confirmed) · bash"))
}

// AC-HLF-014: a wrong challenge rejects the call.
func TestHardlineBlockWrongChallenge(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 1)
	blockTurn(backend, &ch, outcomes)
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "clean up")
	ask := api.waitCall("sendMessage", textHas("⚠️ HL-B-001"))
	id := callbackID(t, buttonData(t, ask, "Allow once…"))
	api.callback(ownerChat, "private", ownerID, "a:"+id+":verify")
	api.waitCall("editMessageText", textHas("Reply with "))

	// 0000 is never a challenge (1000-9999).
	api.text(ownerChat, "private", ownerID, "0000")
	if got := <-outcomes; got != core.OutcomeRejected {
		t.Fatalf("outcome = %q", got)
	}
	api.waitCall("editMessageText", textIs("⊘ rejected · bash"))
}

// AC-HLF-014: a non-owner text is ignored by the capture and follows the inbox
// path; the request stays open.
func TestHardlineBlockNonOwnerIgnored(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 1)
	blockTurn(backend, &ch, outcomes)
	ch, _, _ = startChannel(t, api, backend)

	api.text(groupChat, "supergroup", ownerID, "@lana_bot clean up")
	ask := api.waitCall("sendMessage", textHas("⚠️ HL-B-001"))
	id := callbackID(t, buttonData(t, ask, "Allow once…"))
	api.callback(groupChat, "supergroup", ownerID, "a:"+id+":verify")
	edit := api.waitCall("editMessageText", textHas("Reply with "))
	challenge := challengeRe.FindStringSubmatch(edit.Params["text"].(string))[1]

	// A member who is not the owner sends the right challenge: it must reach the
	// agent's inbox, not confirm the call.
	api.text(groupChat, "supergroup", memberID, "@lana_bot "+challenge)
	deadline := time.Now().Add(2 * time.Second)
	for len(sentList(backend)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	sent := sentList(backend)
	if len(sent) != 1 || !strings.Contains(sent[0], challenge) {
		t.Fatalf("non-owner text not delivered to the agent: %v", sent)
	}

	// The request is still open; the owner rejects it.
	api.callback(groupChat, "supergroup", ownerID, "a:"+id+":reject")
	if got := <-outcomes; got != core.OutcomeRejected {
		t.Fatalf("outcome = %q", got)
	}
}

// REQ-HLF-008: an Escalate-tier request offers only "Allow once" and "Reject",
// and "Allow once" returns allowed_once.
func TestHardlineEscalateKeyboard(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 1)
	input := []byte(`{"command":"sudo ls"}`)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_call", CallID: "c1", Name: "bash", Input: input})
		time.Sleep(30 * time.Millisecond)
		outcomes <- ch.Approve(ctx, core.ApprovalRequest{
			SessionID: session, CallID: "c1", ToolName: "bash", Input: input,
			Hardline: &core.HardlineMatch{Rule: "HL-E-002", Tier: core.HardlineEscalate, Reason: "privilege change"},
		})
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "run")
	ask := api.waitCall("sendMessage", textHas("⚠️ HL-E-002"))
	labels := keyboardTexts(t, ask)
	if len(labels) != 2 || !hasText(labels, "Allow once") || !hasText(labels, "Reject") {
		t.Fatalf("escalate keyboard = %v", labels)
	}
	if hasText(labels, "Allow for session") {
		t.Fatalf("escalate keyboard offers a session grant: %v", labels)
	}

	id := callbackID(t, buttonData(t, ask, "Allow once"))
	api.callback(ownerChat, "private", ownerID, "a:"+id+":once")
	if got := <-outcomes; got != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", got)
	}
	api.waitCall("editMessageText", textIs("✓ allowed once · bash"))
}

// REQ-HLF-007: a request without a hardline rule keeps its three ordinary
// choices.
func TestNonHardlineKeyboardUnchanged(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 1)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_call", CallID: "c1", Name: "bash", Input: []byte(`{"command":"ls"}`)})
		time.Sleep(30 * time.Millisecond)
		outcomes <- ch.Approve(ctx, core.ApprovalRequest{SessionID: session, CallID: "c1", ToolName: "bash"})
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "list")
	ask := api.waitCall("sendMessage", textHas("🔐 Allow bash"))
	labels := keyboardTexts(t, ask)
	if len(labels) != 3 || !hasText(labels, "Allow once") || !hasText(labels, "Allow for session") || !hasText(labels, "Reject") {
		t.Fatalf("ordinary keyboard = %v", labels)
	}

	api.callback(ownerChat, "private", ownerID, "a:"+callbackID(t, buttonData(t, ask, "Allow once"))+":once")
	if got := <-outcomes; got != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", got)
	}
}

// REQ-HLF-009: a Block-tier request with no confirmation expires like any other.
func TestHardlineBlockTimeout(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 1)
	blockTurn(backend, &ch, outcomes)
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "clean up")
	api.waitCall("sendMessage", textHas("⚠️ HL-B-001"))
	if got := <-outcomes; got != core.OutcomeUnavailable {
		t.Fatalf("outcome = %q", got)
	}
	api.waitCall("editMessageText", textHas("⊘ approval expired"))
}

package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func publishText(backend *fakeBackend, session, text string) {
	backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: text})
}

func publishToolCall(backend *fakeBackend, session, callID, name, input string) {
	backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_call", CallID: callID, Name: name, Input: []byte(input)})
}

func publishToolResult(backend *fakeBackend, session, callID string) {
	backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_result", CallID: callID, Status: "success"})
}

// approvalData returns the callback data of the first approval button.
func approvalData(t *testing.T, call apiCall) string {
	t.Helper()
	markup, ok := call.Params["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("%s call has no approval keyboard: %v", call.Method, call.Params)
	}
	rows, _ := markup["inline_keyboard"].([]any)
	if len(rows) == 0 {
		t.Fatalf("%s call has no keyboard rows: %v", call.Method, call.Params)
	}
	row, _ := rows[0].([]any)
	if len(row) == 0 {
		t.Fatalf("%s call has no buttons: %v", call.Method, call.Params)
	}
	button, _ := row[0].(map[string]any)
	data, _ := button["callback_data"].(string)
	if data == "" {
		t.Fatalf("%s call has no callback data: %v", call.Method, call.Params)
	}
	return data
}

// pressApproval answers the approval shown by call with choice.
func pressApproval(t *testing.T, api *fakeAPI, call apiCall, choice string) {
	t.Helper()
	parts := strings.Split(approvalData(t, call), ":")
	parts[len(parts)-1] = choice
	api.callback(ownerChat, "private", ownerID, strings.Join(parts, ":"))
}

// callIndex returns the position of the first matching API call, or -1.
func callIndex(calls []apiCall, method string, match func(map[string]any) bool) int {
	for i, call := range calls {
		if call.Method == method && (match == nil || match(call.Params)) {
			return i
		}
	}
	return -1
}

// TestLayoutAC001LiveStatusMCPTool checks the live status line of an MCP tool
// and the child-agent prefix (REQ-TGL-001).
func TestLayoutAC001LiveStatusMCPTool(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishText(backend, session, "one")
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		publishToolResult(backend, session, "c1")
		publishText(backend, session, "two")
		publishToolCall(backend, session, "c2", "read_file", `{"path":"notes.md"}`)
		publishToolResult(backend, session, "c2")
		publishText(backend, session, "three")
		publishToolCall(backend, session, "c3", "mcp__dbx__dbx_execute_query", `{"sql":"select 1"}`)
		time.Sleep(120 * time.Millisecond)
		publishToolResult(backend, session, "c3")
		backend.obs.Publish(core.LiveFrame{SessionID: "CHILD", Agent: "researcher", Kind: "tool_call", CallID: "cc1", Name: "read_file", Input: []byte(`{"path":"x.md"}`)})
		time.Sleep(80 * time.Millisecond)
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "run")
	live := api.waitCall("editMessageText", textHas("⏳ dbx_execute_query · step 3"))
	text, _ := live.Params["text"].(string)
	first, rest, _ := strings.Cut(text, "\n")
	if first != "⏳ dbx_execute_query · step 3" {
		t.Fatalf("first line = %q", first)
	}
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(line, "🔧") {
			t.Fatalf("live message has a tool line: %q", text)
		}
	}
	child := api.waitCall("editMessageText", textHas("⏳ researcher · read_file · step 3"))
	if first, _, _ := strings.Cut(child.Params["text"].(string), "\n"); first != "⏳ researcher · read_file · step 3" {
		t.Fatalf("child status = %q", first)
	}
}

// TestLayoutAC002LiveWritingStatus checks the live status while text streams.
func TestLayoutAC002LiveWritingStatus(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishText(backend, session, "Hello **there**")
		time.Sleep(100 * time.Millisecond)
		return TurnResult{Text: "Hello **there**", StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "hi")
	live := api.waitCall("editMessageText", textHas("✍️ step 1"))
	lines := strings.Split(live.Params["text"].(string), "\n")
	if len(lines) < 3 || lines[0] != "✍️ step 1" || lines[1] != "" || lines[2] != "Hello <b>there</b>" {
		t.Fatalf("live text = %q", live.Params["text"])
	}
}

// TestLayoutAC003OneBubblePerTurn checks that one bubble serves every approval
// of a turn.
func TestLayoutAC003OneBubblePerTurn(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	tools := []string{"mcp__dbx__dbx_list_tables", "mcp__dbx__dbx_describe", "mcp__dbx__dbx_execute_query"}
	choices := []string{"once", "session", "reject"}
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		for i, tool := range tools {
			callID := fmt.Sprintf("c%d", i+1)
			publishToolCall(backend, session, callID, tool, `{}`)
			time.Sleep(30 * time.Millisecond)
			ch.Approve(ctx, core.ApprovalRequest{SessionID: session, CallID: callID, ToolName: tool})
			publishToolResult(backend, session, callID)
		}
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "run")
	first := api.waitCall("sendMessage", textHas("🔐 Allow "+shortTool(tools[0])+":"))
	pressApproval(t, api, first, choices[0])
	second := api.waitCall("editMessageText", textHas("🔐 Allow "+shortTool(tools[1])+":"))
	pressApproval(t, api, second, choices[1])
	third := api.waitCall("editMessageText", textHas("🔐 Allow "+shortTool(tools[2])+":"))
	pressApproval(t, api, third, choices[2])

	api.waitCall("editMessageText", textIs("🔐 3 approvals: 2 allowed, 1 rejected"))
	api.waitCall("editMessageText", textIs("✓ allowed once · "+shortTool(tools[0])))
	api.waitCall("editMessageText", textIs("✓ allowed for session · "+shortTool(tools[1])))
	api.waitCall("editMessageText", textIs("⊘ rejected · "+shortTool(tools[2])))
	if n := api.count("sendMessage", textHas("🔐 Allow")); n != 1 {
		t.Fatalf("bubble sends = %d\n%s", n, api.dump())
	}
	if n := api.count("editMessageText", textHas("🔐 Allow")); n != 2 {
		t.Fatalf("bubble request edits = %d\n%s", n, api.dump())
	}
	api.waitCall("deleteMessage", nil)
}

// TestLayoutAC004ConcurrentApprovals checks FIFO promotion and both outcomes.
func TestLayoutAC004ConcurrentApprovals(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	results := make(chan core.ApprovalOutcome, 2)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		publishToolCall(backend, session, "c2", "read_file", `{"path":"notes.md"}`)
		time.Sleep(30 * time.Millisecond)
		requests := []core.ApprovalRequest{
			{SessionID: session, CallID: "c1", ToolName: "bash"},
			{SessionID: session, CallID: "c2", ToolName: "read_file"},
		}
		var wg sync.WaitGroup
		for _, req := range requests {
			wg.Add(1)
			go func(req core.ApprovalRequest) {
				defer wg.Done()
				results <- ch.Approve(ctx, req)
			}(req)
		}
		wg.Wait()
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "run")
	first := api.waitCall("sendMessage", textHas("🔐 Allow"))
	firstText := first.Params["text"].(string)
	otherTool := "read_file"
	if strings.Contains(firstText, "read_file") {
		otherTool = "bash"
	}
	pressApproval(t, api, first, "once")
	second := api.waitCall("editMessageText", textHas("🔐 Allow "+otherTool))
	pressApproval(t, api, second, "reject")

	got := map[core.ApprovalOutcome]int{}
	for i := 0; i < 2; i++ {
		got[<-results]++
	}
	if got[core.OutcomeAllowedOnce] != 1 || got[core.OutcomeRejected] != 1 {
		t.Fatalf("outcomes = %v", got)
	}
	api.waitCall("editMessageText", textIs("🔐 2 approvals: 1 allowed, 1 rejected"))
	api.waitCall("deleteMessage", nil)
}

// TestLayoutAC005ApprovalTimeoutPromotesNext checks expiry and promotion.
func TestLayoutAC005ApprovalTimeoutPromotesNext(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	results := make(chan core.ApprovalOutcome, 2)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		publishToolCall(backend, session, "c2", "read_file", `{"path":"notes.md"}`)
		time.Sleep(30 * time.Millisecond)
		requests := []core.ApprovalRequest{
			{SessionID: session, CallID: "c1", ToolName: "bash"},
			{SessionID: session, CallID: "c2", ToolName: "read_file"},
		}
		var wg sync.WaitGroup
		for _, req := range requests {
			wg.Add(1)
			go func(req core.ApprovalRequest) {
				defer wg.Done()
				results <- ch.Approve(ctx, req)
			}(req)
		}
		wg.Wait()
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)
	ch.ApprovalTimeout = 60 * time.Millisecond

	api.text(ownerChat, "private", ownerID, "run")
	first := api.waitCall("sendMessage", textHas("🔐 Allow"))
	firstText := first.Params["text"].(string)
	otherTool := "read_file"
	if strings.Contains(firstText, "read_file") {
		otherTool = "bash"
	}
	api.waitCall("editMessageText", textHas("🔐 Allow "+otherTool))
	for i := 0; i < 2; i++ {
		if got := <-results; got != core.OutcomeUnavailable {
			t.Fatalf("outcome = %q", got)
		}
	}
	api.waitCall("editMessageText", textIs("🔐 2 approvals: 2 rejected"))
	if n := api.count("editMessageText", textHas("⊘ approval expired · ")); n != 2 {
		t.Fatalf("expiry edits = %d\n%s", n, api.dump())
	}
	api.waitCall("deleteMessage", nil)
}

// TestLayoutAC005ApprovalCancelled checks a cancelled request.
func TestLayoutAC005ApprovalCancelled(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	results := make(chan core.ApprovalOutcome, 1)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		time.Sleep(30 * time.Millisecond)
		approvalCtx, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		results <- ch.Approve(approvalCtx, core.ApprovalRequest{SessionID: session, CallID: "c1", ToolName: "bash"})
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "run")
	api.waitCall("sendMessage", textHas("🔐 Allow bash"))
	if got := <-results; got != core.OutcomeCancelled {
		t.Fatalf("outcome = %q", got)
	}
	api.waitCall("editMessageText", textIs("⊘ cancelled · bash"))
	api.waitCall("editMessageText", textIs("🔐 1 approval: 1 rejected"))
	api.waitCall("deleteMessage", nil)
}

// TestLayoutAC006SummaryAndFooter checks the closing layout of a full turn.
func TestLayoutAC006SummaryAndFooter(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	tools := []string{"mcp__dbx__dbx_list_tables", "mcp__dbx__dbx_describe", "mcp__dbx__dbx_execute_query"}
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		for i, tool := range tools {
			callID := fmt.Sprintf("c%d", i+1)
			publishToolCall(backend, session, callID, tool, `{}`)
			time.Sleep(30 * time.Millisecond)
			ch.Approve(ctx, core.ApprovalRequest{SessionID: session, CallID: callID, ToolName: tool})
			publishToolResult(backend, session, callID)
		}
		publishToolCall(backend, session, "c4", "bash", `{"command":"ls"}`)
		publishToolResult(backend, session, "c4")
		for i := 0; i < 8; i++ {
			publishToolResult(backend, session, "c4")
			publishText(backend, session, fmt.Sprintf("answer %d\n\n", i+1))
		}
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "go")
	first := api.waitCall("sendMessage", textHas("🔐 Allow "+shortTool(tools[0])+":"))
	pressApproval(t, api, first, "once")
	second := api.waitCall("editMessageText", textHas("🔐 Allow "+shortTool(tools[1])+":"))
	pressApproval(t, api, second, "session")
	third := api.waitCall("editMessageText", textHas("🔐 Allow "+shortTool(tools[2])+":"))
	pressApproval(t, api, third, "once")

	api.waitCall("editMessageText", textIs("🔐 3 approvals: 3 allowed"))
	answer := api.waitCall("sendMessage", textHas("answer 1"))
	if text := answer.Params["text"].(string); strings.Contains(text, "⚙️") {
		t.Fatalf("final answer has footer: %q", text)
	}
	if reply := answer.Params["reply_parameters"]; reply != nil {
		t.Fatalf("answer has reply target: %v", reply)
	}
	footer := api.waitCall("sendMessage", textIs("⚙️ 12 steps · 4 tool calls"))
	if chat := footer.Params["chat_id"].(float64); chat != float64(ownerChat) {
		t.Fatalf("footer chat = %v", chat)
	}
	if footer.Params["reply_parameters"] != nil || footer.Params["reply_markup"] != nil {
		t.Fatalf("footer mismatch vs answer: %v", footer.Params)
	}
	del := api.waitCall("deleteMessage", nil)
	live := api.waitCall("sendMessage", textIs("…"))
	if del.Params["message_id"].(float64) != float64(live.MessageID) {
		t.Fatalf("deleted message %v, live message %d", del.Params["message_id"], live.MessageID)
	}
	if n := api.count("editMessageText", textHas("⚙️")); n != 0 {
		t.Fatalf("footer in an edit = %d\n%s", n, api.dump())
	}

	calls := api.snapshot()
	summaryIndex := callIndex(calls, "editMessageText", textIs("🔐 3 approvals: 3 allowed"))
	answerIndex := callIndex(calls, "sendMessage", textHas("answer 1"))
	footerIndex := callIndex(calls, "sendMessage", textIs("⚙️ 12 steps · 4 tool calls"))
	deleteIndex := callIndex(calls, "deleteMessage", nil)
	if summaryIndex < 0 || answerIndex < 0 || footerIndex < 0 || deleteIndex < 0 ||
		summaryIndex >= answerIndex || answerIndex >= footerIndex {
		t.Fatalf("summary=%d answer=%d footer=%d delete=%d\n%s", summaryIndex, answerIndex, footerIndex, deleteIndex, api.dump())
	}
}

// TestLayoutAC007NoBubbleEditsLive checks the in-place final layout.
func TestLayoutAC007NoBubbleEditsLive(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishText(backend, session, "hello")
		return TurnResult{Text: "hello", StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "hi")
	api.waitCall("editMessageText", textIs("hello"))
	if n := api.count("sendMessage", textIs("hello")); n != 0 {
		t.Fatalf("new answer messages = %d\n%s", n, api.dump())
	}
	if n := api.count("deleteMessage", nil); n != 0 {
		t.Fatalf("deletes = %d\n%s", n, api.dump())
	}
	if n := api.count("sendMessage", textHas("⚙️")); n != 0 {
		t.Fatalf("footer sends = %d\n%s", n, api.dump())
	}
	if n := api.count("editMessageText", textHas("⚙️")); n != 0 {
		t.Fatalf("footer in an edit = %d\n%s", n, api.dump())
	}
}

// TestLayoutAC008SingularFooter checks the one-step one-call footer.
func TestLayoutAC008SingularFooter(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "hi")
	final := api.waitCall("editMessageText", textIs("done"))
	if text := final.Params["text"].(string); strings.Contains(text, "⚙️") {
		t.Fatalf("final answer has footer: %q", text)
	}
	footer := api.waitCall("sendMessage", textIs("⚙️ 1 step · 1 tool call"))
	if footer.Params["reply_parameters"] != nil || footer.Params["reply_markup"] != nil {
		t.Fatalf("footer mismatch vs answer: %v", footer.Params)
	}
	if n := api.count("editMessageText", textHas("⚙️")); n != 0 {
		t.Fatalf("footer in an edit = %d\n%s", n, api.dump())
	}
}

// TestLayoutFooterAfterSplitAnswer checks that the footer follows the last chunk
// of an answer split over several messages (REQ-TGL-003).
func TestLayoutFooterAfterSplitAnswer(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	long := strings.Repeat("word ", 1000)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		publishToolResult(backend, session, "c1")
		publishText(backend, session, long)
		return TurnResult{Text: long, StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "hi")
	api.waitCall("sendMessage", textIs("⚙️ 2 steps · 1 tool call"))

	calls := api.snapshot()
	lastAnswer := -1
	for i, call := range calls {
		if call.Method != "sendMessage" {
			continue
		}
		text, _ := call.Params["text"].(string)
		if strings.Contains(text, "word") && !strings.Contains(text, "⚙️") {
			lastAnswer = i
		}
	}
	footerIndex := callIndex(calls, "sendMessage", textIs("⚙️ 2 steps · 1 tool call"))
	if lastAnswer < 0 || footerIndex < lastAnswer {
		t.Fatalf("last answer=%d footer=%d\n%s", lastAnswer, footerIndex, api.dump())
	}
	if n := api.count("editMessageText", textHas("⚙️")); n != 0 {
		t.Fatalf("footer in an edit = %d\n%s", n, api.dump())
	}
	if n := api.count("sendMessage", textIs("⚙️ 2 steps · 1 tool call")); n != 1 {
		t.Fatalf("footer sends = %d\n%s", n, api.dump())
	}
}

// TestLayoutAC009DeleteFailure checks that a failed delete is not shown.
func TestLayoutAC009DeleteFailure(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		time.Sleep(30 * time.Millisecond)
		ch.Approve(ctx, core.ApprovalRequest{SessionID: session, CallID: "c1", ToolName: "bash"})
		publishToolResult(backend, session, "c1")
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)
	api.failNext("deleteMessage", 500, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)

	api.text(ownerChat, "private", ownerID, "run")
	bubble := api.waitCall("sendMessage", textHas("🔐 Allow bash"))
	pressApproval(t, api, bubble, "once")
	answer := api.waitCall("sendMessage", textHas("done"))
	if text := answer.Params["text"].(string); strings.Contains(text, "⚙️") {
		t.Fatalf("final answer has footer: %q", text)
	}
	footer := api.waitCall("sendMessage", textIs("⚙️ 1 step · 1 tool call"))
	if footer.Params["reply_parameters"] != nil || footer.Params["reply_markup"] != nil {
		t.Fatalf("footer mismatch vs answer: %v", footer.Params)
	}
	if n := api.count("deleteMessage", nil); n != 1 {
		t.Fatalf("deletes = %d\n%s", n, api.dump())
	}
	for _, call := range api.snapshot() {
		text, _ := call.Params["text"].(string)
		if strings.Contains(text, "Internal Server Error") || strings.Contains(text, "deleteMessage") {
			t.Fatalf("delete failure shown in %s: %v", call.Method, text)
		}
	}
}

// TestLayoutFinalSendFallsBackToLive checks the failure path of the final send.
func TestLayoutFinalSendFallsBackToLive(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		publishToolCall(backend, session, "c1", "bash", `{"command":"ls"}`)
		time.Sleep(30 * time.Millisecond)
		ch.Approve(ctx, core.ApprovalRequest{SessionID: session, CallID: "c1", ToolName: "bash"})
		publishToolResult(backend, session, "c1")
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "run")
	bubble := api.waitCall("sendMessage", textHas("🔐 Allow bash"))
	api.failNext("sendMessage", 500, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	pressApproval(t, api, bubble, "once")

	api.waitCall("editMessageText", textIs("🔐 1 approval: 1 allowed"))
	live := api.waitCall("sendMessage", textIs("…"))
	final := api.waitCall("editMessageText", textIs("done"))
	if final.Params["message_id"].(float64) != float64(live.MessageID) {
		t.Fatalf("fallback edited message %v, live message %d", final.Params["message_id"], live.MessageID)
	}
	if text := final.Params["text"].(string); strings.Contains(text, "⚙️") {
		t.Fatalf("fallback answer has footer: %q", text)
	}
	footer := api.waitCall("sendMessage", textIs("⚙️ 1 step · 1 tool call"))
	if footer.Params["reply_parameters"] != nil || footer.Params["reply_markup"] != nil {
		t.Fatalf("footer mismatch vs answer: %v", footer.Params)
	}
	if n := api.count("deleteMessage", nil); n != 0 {
		t.Fatalf("deletes = %d\n%s", n, api.dump())
	}
}

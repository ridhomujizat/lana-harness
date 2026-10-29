package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestParseConfig(t *testing.T) {
	routes := []core.RouteConfig{{Channel: "telegram", Agent: "a", Match: map[string]any{"chat_id": int64(5)}}, {Channel: "telegram", Agent: "b", Match: map[string]any{"chat_id": int64(5)}}}
	cfg, err := ParseConfig(map[string]any{"owner_ids": []any{int64(7)}}, routes)
	if err != nil || cfg.TokenEnv != "TELEGRAM_BOT_TOKEN" || !cfg.OwnerIDs[7] || cfg.Routes[5] != "a" || cfg.APIBase != DefaultAPIBase {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	for _, raw := range []map[string]any{{"nope": 1}, {"owner_ids": "x"}, {"api_base": "ftp://x"}, {"api_base": "https://x/"}, {"token_env": ""}} {
		if _, err := ParseConfig(raw, routes); err == nil {
			t.Fatalf("%v accepted", raw)
		}
	}
	if _, err := ParseConfig(nil, []core.RouteConfig{{Channel: "telegram", Agent: "a"}}); err == nil {
		t.Fatal("route without chat_id accepted")
	}
	if _, err := ParseConfig(nil, nil); err == nil || !strings.Contains(err.Error(), `no [[routes]] entry for channel "telegram"`) {
		t.Fatalf("no routes err = %v", err)
	}
}

func TestStartRegistersCommandsAndRejectsBadToken(t *testing.T) {
	api := newFakeAPI(t)
	startChannel(t, api, newFakeBackend())
	call := api.waitCall("setMyCommands", nil)
	if cmds, _ := call.Params["commands"].([]any); len(cmds) != 9 {
		t.Fatalf("commands = %v", call.Params["commands"])
	}

	bad := newFakeAPI(t)
	bad.failNext("getMe", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	cfg, _ := ParseConfig(map[string]any{"api_base": bad.srv.URL}, []core.RouteConfig{{Channel: "telegram", Agent: "a", Match: map[string]any{"chat_id": int64(1)}}})
	if _, err := New(cfg, "x", newFakeBackend()).Start(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestPrivateChatTurn(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: "Checking"})
		time.Sleep(50 * time.Millisecond)
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_call", CallID: "c1", Name: "bash", Input: []byte(`{"command":"df -h"}`)})
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_result", CallID: "c1", Status: "success"})
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: "Disk is **fine**."})
		return TurnResult{Text: "Disk is **fine**.", StopReason: "end_turn", InputTokens: 10, OutputTokens: 5}, nil
	}
	startChannel(t, api, backend)
	api.text(ownerChat, "private", ownerID, "check disk")
	api.waitCall("sendMessage", textIs("…"))
	api.waitCall("sendChatAction", nil)
	final := api.waitCall("editMessageText", textHas("Disk is <b>fine</b>."))
	if text := final.Params["text"].(string); !strings.Contains(text, "🔧 bash df -h ✓") || !strings.HasPrefix(text, "🔧") || final.Params["parse_mode"] != "HTML" {
		t.Fatalf("final = %v", final.Params)
	}
	if got := backend.promptList(); len(got) != 1 || got[0] != "check disk" {
		t.Fatalf("prompts = %v", got)
	}
}

func TestStrangerIgnored(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)
	api.text(222, "private", 222, "hi")
	api.text(ownerChat, "private", ownerID, "hello")
	api.waitCall("editMessageText", nil)
	for _, call := range api.snapshot() {
		if id, ok := call.Params["chat_id"].(float64); ok && int64(id) == 222 {
			t.Fatalf("bot wrote to stranger: %v", call)
		}
	}
	if got := backend.promptList(); len(got) != 1 {
		t.Fatalf("prompts = %v", got)
	}
}

func TestGroupMentionOnly(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)
	api.text(groupChat, "supergroup", memberID, "just chatting")
	api.text(groupChat, "supergroup", memberID, "@Lana_Bot how are you")
	api.waitCall("editMessageText", nil)
	if got := backend.promptList(); len(got) != 1 || got[0] != "Budi: how are you" {
		t.Fatalf("prompts = %v", got)
	}
}

func TestInboxDuringTurnAndCancel(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		select {
		case <-backend.cancelCh:
			return TurnResult{StopReason: "cancelled"}, nil
		case <-ctx.Done():
			return TurnResult{StopReason: "cancelled"}, nil
		}
	}
	startChannel(t, api, backend)
	api.text(ownerChat, "private", ownerID, "long task")
	api.waitCall("sendMessage", textIs("…"))
	api.text(ownerChat, "private", ownerID, "also this")
	api.waitCall("setMessageReaction", nil)
	backend.mu.Lock()
	sent := append([]string(nil), backend.sent...)
	backend.mu.Unlock()
	if len(sent) != 1 || sent[0] != "also this" {
		t.Fatalf("inbox = %v", sent)
	}
	api.text(ownerChat, "private", ownerID, "/cancel")
	api.waitCall("editMessageText", textHas("⊘ cancelled"))
	api.text(ownerChat, "private", ownerID, "/cancel")
	api.waitCall("sendMessage", textIs("· nothing to cancel"))
}

func TestNonTextAndCommands(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)
	api.push(Update{Message: &Message{MessageID: 5, From: &User{ID: ownerID}, Chat: Chat{ID: ownerChat, Type: "private"}, Photo: []any{map[string]any{"file_id": "x"}}}})
	api.waitCall("sendMessage", textIs(textOnlyReply))

	api.text(ownerChat, "private", ownerID, "/help")
	api.waitCall("sendMessage", textIs("I'm helper. Commands: /new /agent /memory /mcp /skills /compact /cancel /status"))
	api.text(ownerChat, "private", ownerID, "/agent")
	kb := api.waitCall("sendMessage", textIs("Current agent: helper"))
	if !strings.Contains(api.dump(), `"callback_data":"g:coder"`) {
		t.Fatalf("agent keyboard missing: %v", kb.Params)
	}
	api.callback(ownerChat, "private", ownerID, "g:coder")
	api.waitCall("sendMessage", textIs("· agent: coder"))
	api.text(ownerChat, "private", ownerID, "/status")
	api.waitCall("sendMessage", textHas("agent coder · model mock:coder · policy ask · session S1 · 3 turns"))
	api.text(ownerChat, "private", ownerID, "/agent ghost")
	api.waitCall("sendMessage", textIs(`✗ unknown agent "ghost" (available: coder, helper)`))
	api.text(ownerChat, "private", ownerID, "/memory")
	api.waitCall("sendMessage", textHas("· user 11/2000 characters, 1 entries\n  1. Name: Ridho\n· memory 0/4000 characters, 0 entries"))
	api.text(ownerChat, "private", ownerID, "/mcp")
	api.waitCall("sendMessage", textIs("· github running, 26 tools"))
	api.text(ownerChat, "private", ownerID, "/skills")
	api.waitCall("sendMessage", textIs("· demo (global): Demo skill"))
	api.text(ownerChat, "private", ownerID, "/compact")
	api.waitCall("sendMessage", textIs("· compaction 100 → 40 token"))
	api.text(ownerChat, "private", ownerID, "/new")
	api.waitCall("sendMessage", textIs("· new session S2"))
	api.text(ownerChat, "private", ownerID, "/nope")
	api.waitCall("sendMessage", textIs("· unknown command: /nope"))
	api.text(groupChat, "supergroup", memberID, "/new")
	api.waitCall("sendMessage", textIs("✗ only the owner can use /new"))
	api.text(groupChat, "supergroup", memberID, "/new@other_bot")
	time.Sleep(100 * time.Millisecond)
	if n := api.count("sendMessage", textIs("✗ only the owner can use /new")); n != 1 {
		t.Fatalf("command for another bot was handled (%d)", n)
	}
}

func TestApprovalFlow(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	var ch *Channel
	outcomes := make(chan core.ApprovalOutcome, 3)
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "tool_call", CallID: "c1", Name: "bash", Input: []byte(`{"command":"rm -rf tmp"}`)})
		time.Sleep(30 * time.Millisecond)
		outcomes <- ch.Approve(ctx, core.ApprovalRequest{SessionID: session, CallID: "c1", ToolName: "bash"})
		return TurnResult{Text: "done", StopReason: "end_turn"}, nil
	}
	ch, _, _ = startChannel(t, api, backend)

	api.text(groupChat, "supergroup", ownerID, "@lana_bot clean up")
	ask := api.waitCall("sendMessage", textIs("Allow bash: rm -rf tmp?"))
	data := ask.Params["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"].(string)
	id := strings.Split(data, ":")[1]
	api.callback(groupChat, "supergroup", memberID, "a:"+id+":once")
	api.waitCall("answerCallbackQuery", textIs("only the owner can approve"))
	api.callback(groupChat, "supergroup", ownerID, "a:"+id+":once")
	if got := <-outcomes; got != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q", got)
	}
	api.waitCall("editMessageText", textIs("Allow bash: rm -rf tmp?\n✓ allowed once"))
	api.callback(groupChat, "supergroup", ownerID, "a:"+id+":once")
	api.waitCall("answerCallbackQuery", textIs("this request is no longer active"))

	api.text(ownerChat, "private", ownerID, "again")
	if got := <-outcomes; got != core.OutcomeRejected {
		t.Fatalf("timeout outcome = %q", got)
	}
	api.waitCall("editMessageText", textHas("⊘ approval expired"))
}

func TestApproveUnknownSession(t *testing.T) {
	cfg, _ := ParseConfig(nil, []core.RouteConfig{{Channel: "telegram", Agent: "a", Match: map[string]any{"chat_id": int64(1)}}})
	if got := New(cfg, "x", newFakeBackend()).Approve(context.Background(), core.ApprovalRequest{SessionID: "nope"}); got != core.OutcomeUnavailable {
		t.Fatalf("outcome = %q", got)
	}
}

func TestPollBackoffOffsetAndUnauthorized(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	api.failNext("getUpdates", 502, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	api.failNext("getUpdates", 502, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	_, _, done := startChannel(t, api, backend)
	api.text(ownerChat, "private", ownerID, "one")
	api.text(ownerChat, "private", ownerID, "two")
	api.text(ownerChat, "private", ownerID, "three")
	api.waitCall("editMessageText", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		api.mu.Lock()
		last := api.offsets[len(api.offsets)-1]
		api.mu.Unlock()
		if last == 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	api.mu.Lock()
	last := api.offsets[len(api.offsets)-1]
	api.mu.Unlock()
	if last != 4 {
		t.Fatalf("offset = %d, want 4", last)
	}
	api.failNext("getUpdates", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on 401")
	}
}

func TestLongAnswerSplit(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	para := strings.Repeat("x", 3000)
	answer := para + "\n\n" + para + "\n\n" + para
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: answer})
		return TurnResult{Text: answer, StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)
	api.text(ownerChat, "private", ownerID, "write a lot")
	api.waitCall("editMessageText", textIs(para))
	deadline := time.Now().Add(2 * time.Second)
	for api.count("sendMessage", textIs(para)) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := api.count("sendMessage", textIs(para)); n != 2 {
		t.Fatalf("overflow messages = %d\n%s", n, api.dump())
	}
}

func TestErrorAndParseFallback(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		return TurnResult{StopReason: "error"}, errors.New("provider down")
	}
	startChannel(t, api, backend)
	api.failNext("editMessageText", 400, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities"}`)
	api.text(ownerChat, "private", ownerID, "hi")
	api.waitCall("editMessageText", func(p map[string]any) bool {
		_, html := p["parse_mode"]
		return p["text"] == "✗ provider down" && !html
	})
}

func TestStreamingEditsAreThrottled(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		for i := 0; i < 30; i++ {
			backend.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: "w "})
			time.Sleep(5 * time.Millisecond)
		}
		return TurnResult{StopReason: "end_turn"}, nil
	}
	startChannel(t, api, backend)
	api.text(ownerChat, "private", ownerID, "stream")
	api.waitCall("editMessageText", textHas(strings.TrimSpace(strings.Repeat("w ", 30))))
	// 150 ms of streaming with a 20 ms interval allows at most ~8 edits plus the final one.
	if n := api.count("editMessageText", nil); n > 10 {
		t.Fatalf("edits = %d", n)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// poolLLM is a test registry that knows Antigravity accounts and their pool,
// as the router does (llmcall.AntigravityAccounts, llmcall.PoolAccounts).
type poolLLM struct {
	*testLLM
	agy  map[string]core.ProviderConfig
	pool []string
}

func (p poolLLM) AntigravityAccount(name string) (core.ProviderConfig, bool) {
	cfg, ok := p.agy[name]
	if name == "antigravity" {
		cfg.Kind = core.AntigravityKind
		return cfg, true
	}
	return cfg, ok
}

func (p poolLLM) PoolAccounts(runtime string) []string {
	if runtime == "antigravity" {
		return p.pool
	}
	return nil
}

// agyScript answers per account: an account in fail fails before any output
// with that class; any other answers "reply from <account>".
type agyScript struct {
	mu   sync.Mutex
	fail map[string]string
}

func (s *agyScript) setFail(account, class string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail == nil {
		s.fail = map[string]string{}
	}
	if class == "" {
		delete(s.fail, account)
		return
	}
	s.fail[account] = class
}

func (s *agyScript) run(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
	account := turn.Config.Account
	s.mu.Lock()
	class := s.fail[account]
	s.mu.Unlock()
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeSessionMeta, ClaudeSessionID: "acp-" + account, Model: turn.Config.Model, Tier: "pro"})
	if class != "" {
		msg := "Antigravity account " + account + " failed: " + class
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: msg})
		return &core.RuntimeEarlyError{Class: class, Message: msg}
	}
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "reply from " + account})
	turn.Emit(core.RuntimeEvent{Kind: core.RuntimeUsage, Usage: core.Usage{InputTokens: 3, OutputTokens: 2}})
	return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
}

// agyHarness is a harness with a scripted Antigravity runtime and, when pool
// is not nil, a registry that knows the accounts and the pool.
func agyHarness(t *testing.T, script mock.Script, agents map[string]core.AgentConfig, accounts map[string]core.ProviderConfig, pool []string) (*harness, *fakeRuntime, *agyScript, *[]ExternalUsage) {
	t.Helper()
	h := newHarness(t, script, agents)
	s := &agyScript{}
	rt := newFakeRuntime(s.run)
	h.reg.SetRuntime("antigravity", rt)
	if accounts != nil || pool != nil {
		h.reg.llm = poolLLM{testLLM: h.llm, agy: accounts, pool: pool}
	}
	var mu sync.Mutex
	usages := &[]ExternalUsage{}
	h.reg.SetExternalHooks(ExternalHooks{Usage: func(u ExternalUsage) { mu.Lock(); *usages = append(*usages, u); mu.Unlock() }})
	return h, rt, s, usages
}

func attempts(t *testing.T, events []core.SessionEvent) []core.AssistantAttempt {
	t.Helper()
	var out []core.AssistantAttempt
	for _, e := range events {
		if e.Kind == core.KindAssistantAttempt {
			out = append(out, payloadOf[core.AssistantAttempt](t, e))
		}
	}
	return out
}

// AC-AGY-011: Antigravity reports not signed in before any output, so the
// turn writes assistant/attempt and finishes in lana's loop with the next
// target.
func TestAntigravityEarlyAuthFallsBack(t *testing.T) {
	h, rt, s, usages := agyHarness(t, mock.Script{Responses: []mock.Response{textResponse("deepseek answer")}},
		map[string]core.AgentConfig{"assistant": {Model: "antigravity/gemini-4-neon-high", Fallback: []string{"mock:m"}}}, nil, nil)
	s.setFail("antigravity", "auth")
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "deepseek answer" || len(rt.recorded()) != 1 {
		t.Fatalf("result = %+v, runtime turns %d", result, len(rt.recorded()))
	}
	events := h.events(t)
	got := attempts(t, events)
	if len(got) != 1 || got[0].Provider != "antigravity" || got[0].Model != "gemini-4-neon-high" || got[0].Error.Class != "auth" {
		t.Fatalf("attempts = %+v", got)
	}
	if k := kindCounts(events); k[core.KindTurnStart] != 1 || k[core.KindTurnEnd] != 1 || k[core.KindUserMessage] != 1 {
		t.Fatalf("kinds = %v", k)
	}
	if len(*usages) != 1 || (*usages)[0].Runtime != "antigravity" || (*usages)[0].Provider != "antigravity" || (*usages)[0].Outcome != "auth" {
		t.Fatalf("usages = %+v", *usages)
	}
	// An account that failed before output stores no runtime session.
	for _, e := range events {
		if _, ok := runtimeMeta(e); ok {
			t.Fatalf("a failed attempt stored a runtime session: %s", e.Payload)
		}
	}
}

// REQ-AGY-001, REQ-AGY-002: a named account runs with its own HOME and
// binary; the built-in one with the daemon's HOME; Claude keys stay out; the
// session meta and the record name the runtime and the account.
func TestAntigravityAccountConfig(t *testing.T) {
	h, rt, _, usages := agyHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "agy-work/gemini-4-neon-high", ReasoningEffort: "high", Binary: "/agent/claude", Auth: core.RuntimeAuthAPIKey},
	}, map[string]core.ProviderConfig{
		"agy-work":   {Kind: core.AntigravityKind, Binary: "/opt/agy"},
		"agy-custom": {Kind: core.AntigravityKind, Home: "$LANA_HOME/homes/custom"},
	}, nil)
	ctx := context.Background()
	if r := h.run(t, "one"); r.Text != "reply from agy-work" {
		t.Fatalf("result = %+v", r)
	}
	if err := h.reg.SetModel(ctx, h.info.ID, "agy-custom/default"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "two")
	if err := h.reg.SetModel(ctx, h.info.ID, "antigravity/gemini-3.8-flash-low"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "three")
	if err := h.reg.SetModel(ctx, h.info.ID, "antigravity/-x"); err == nil {
		t.Fatal("a flag-like antigravity model was accepted")
	}
	turns := rt.recorded()
	if len(turns) != 3 {
		t.Fatalf("turns = %d", len(turns))
	}
	work := turns[0].Config
	if work.Account != "agy-work" || work.Home != filepath.Join(h.reg.home, "antigravity", "agy-work") || work.Binary != "/opt/agy" ||
		work.Model != "gemini-4-neon-high" || work.Effort != "high" || work.Auth != "" || work.APIKeyEnv != "" || work.ConfigDir != "" {
		t.Fatalf("agy-work config = %+v", work)
	}
	if strings.Contains(work.AppendSystemPrompt, "mcp__lana__") {
		t.Fatalf("prompt names Claude's MCP prefix:\n%s", work.AppendSystemPrompt)
	}
	if c := turns[1].Config; c.Home != filepath.Join(h.reg.home, "homes", "custom") || c.Model != "" {
		t.Fatalf("agy-custom config = %+v", c)
	}
	if b := turns[2].Config; b.Account != "antigravity" || b.Home != "" || b.Binary != "" {
		t.Fatalf("built-in config = %+v", b)
	}
	var metas []core.SystemMessage
	for _, e := range h.events(t) {
		if m, ok := runtimeMeta(e); ok {
			metas = append(metas, m)
		}
	}
	if len(metas) != 3 || metas[0].Runtime != "antigravity" || metas[0].Account != "agy-work" || metas[2].Account != "" {
		t.Fatalf("metas = %+v", metas)
	}
	if len(*usages) != 3 || (*usages)[0].Runtime != "antigravity" || (*usages)[0].Provider != "agy-work" || (*usages)[2].Provider != "antigravity" {
		t.Fatalf("usages = %+v", *usages)
	}
}

// REQ-AGY-004: a turn resumes only the runtime session of the same runtime and
// account; a turn on another runtime or account in between starts a new one.
func TestAntigravityResumeSameRuntimeAndAccount(t *testing.T) {
	h, rt, _, _ := agyHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high"},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, nil)
	claude := newFakeRuntime(helloScript)
	h.reg.SetRuntime("claude-code", claude)
	ctx := context.Background()
	h.run(t, "one")
	h.run(t, "two")
	if err := h.reg.SetModel(ctx, h.info.ID, "claude-code/opus"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "three")
	if err := h.reg.SetModel(ctx, h.info.ID, "antigravity/gemini-4-neon-high"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "four")
	h.run(t, "five")
	if err := h.reg.SetModel(ctx, h.info.ID, "agy-2/gemini-4-neon-high"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "six")

	turns := rt.recorded()
	if len(turns) != 5 {
		t.Fatalf("antigravity turns = %d", len(turns))
	}
	want := []string{"", "acp-antigravity", "", "acp-antigravity", ""}
	for i, w := range want {
		if turns[i].ResumeID != w {
			t.Fatalf("turn %d resume = %q, want %q", i, turns[i].ResumeID, w)
		}
	}
	if c := claude.recorded(); len(c) != 1 || c[0].ResumeID != "" {
		t.Fatalf("claude turns = %+v", c)
	}
	// The turn after Claude starts fresh with the conversation, moved from
	// the Claude account.
	if first := turns[2].Input[0].Text; !strings.HasPrefix(first, "Conversation so far (moved from claude-code):\n") ||
		!strings.Contains(first, "Owner: three") || !strings.Contains(first, "Assistant: hello from claude") {
		t.Fatalf("context block = %q", first)
	}
	if strings.Contains(turns[2].Input[0].Text, "Owner: four") {
		t.Fatal("the context block holds the current message")
	}
	// A resumed turn gets no context block.
	if strings.HasPrefix(turns[1].Input[0].Text, "Conversation so far") || strings.HasPrefix(turns[3].Input[0].Text, "Conversation so far") {
		t.Fatal("a resumed turn got a context block")
	}
	if first := turns[4].Input[0].Text; !strings.HasPrefix(first, "Conversation so far (moved from antigravity):\n") {
		t.Fatalf("account switch block = %q", first)
	}
}

// REQ-PAF-002: the first external turn of a session that only had native
// turns gets "Conversation so far:".
func TestExternalAfterNativeGetsConversation(t *testing.T) {
	h, rt, _, _ := agyHarness(t, mock.Script{Responses: []mock.Response{textResponse("native one")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}}, nil, nil)
	h.run(t, "one")
	if err := h.reg.SetModel(context.Background(), h.info.ID, "antigravity/gemini-4-neon-high"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "two")
	turns := rt.recorded()
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	if got := turns[0].Input[0].Text; got != "Conversation so far:\nOwner: one\nAssistant: native one" {
		t.Fatalf("block = %q", got)
	}
	if ownerText(t, turns[0]) != "two" {
		t.Fatalf("owner text = %q", ownerText(t, turns[0]))
	}
}

// AC-PAF-001: a pooled address moves from an account out of quota to the next
// account of the pool; the attempt is kept, the record names the account that
// answered, and the failed account rests.
func TestPoolFailsOverToNextAccount(t *testing.T) {
	h, rt, s, usages := agyHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high"},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, []string{"antigravity", "agy-2"})
	s.setFail("antigravity", core.ErrorClassQuota)
	before := time.Now()
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "reply from agy-2" {
		t.Fatalf("result = %+v", result)
	}
	turns := rt.recorded()
	if len(turns) != 2 || turns[0].Config.Account != "antigravity" || turns[1].Config.Account != "agy-2" || turns[1].Config.Model != "gemini-4-neon-high" {
		t.Fatalf("turns = %+v", turns)
	}
	events := h.events(t)
	got := attempts(t, events)
	if len(got) != 1 || got[0].Provider != "antigravity" || got[0].Error.Class != core.ErrorClassQuota {
		t.Fatalf("attempts = %+v", got)
	}
	if k := kindCounts(events); k[core.KindUserMessage] != 1 || k[core.KindTurnEnd] != 1 || k[core.KindStepStart] != 1 {
		t.Fatalf("kinds = %v", k)
	}
	u := *usages
	if len(u) != 2 || u[0].Provider != "antigravity" || u[0].Outcome != core.ErrorClassQuota || u[1].Provider != "agy-2" || u[1].Outcome != "ok" ||
		u[1].Requested != "antigravity/gemini-4-neon-high" {
		t.Fatalf("usages = %+v", u)
	}
	cool := h.reg.AccountCooldowns()
	until, ok := cool["antigravity"]
	if !ok || until.Before(before.Add(limitCooldown-time.Minute)) || until.After(time.Now().Add(limitCooldown)) {
		t.Fatalf("cooldowns = %v", cool)
	}
	if _, ok := cool["agy-2"]; ok {
		t.Fatalf("the working account rests: %v", cool)
	}
}

// AC-PAF-002: a pinned account address never moves to another account of the
// pool; it goes to the next chain target.
func TestPinnedAccountDoesNotPool(t *testing.T) {
	h, rt, s, _ := agyHarness(t, mock.Script{Responses: []mock.Response{textResponse("native answer")}}, map[string]core.AgentConfig{
		"assistant": {Model: "agy-2/gemini-4-neon-high", Fallback: []string{"mock:m"}},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, []string{"antigravity", "agy-2"})
	s.setFail("agy-2", core.ErrorClassQuota)
	result := h.run(t, "hi")
	if result.Text != "native answer" {
		t.Fatalf("result = %+v", result)
	}
	if turns := rt.recorded(); len(turns) != 1 || turns[0].Config.Account != "agy-2" {
		t.Fatalf("turns = %+v", turns)
	}
}

// AC-PAF-003, AC-PAF-004: after two turns on antigravity, a failover to agy-2
// starts a new session there with the earlier turns in front of the message;
// the next turn starts at agy-2 and resumes its session.
func TestPoolContextBlockAndStickyAccount(t *testing.T) {
	h, rt, s, _ := agyHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high"},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, []string{"antigravity", "agy-2"})
	h.run(t, "first question")
	h.run(t, "second question")
	s.setFail("antigravity", core.ErrorClassQuota)
	if r := h.run(t, "third question"); r.Text != "reply from agy-2" {
		t.Fatalf("failover = %+v", r)
	}
	turns := rt.recorded()
	if len(turns) != 4 || turns[1].ResumeID != "acp-antigravity" || turns[2].Config.Account != "antigravity" || turns[2].ResumeID != "acp-antigravity" {
		t.Fatalf("turns = %+v", turns)
	}
	moved := turns[3]
	if moved.Config.Account != "agy-2" || moved.ResumeID != "" {
		t.Fatalf("moved turn = %+v", moved)
	}
	block := moved.Input[0].Text
	for _, want := range []string{
		"Conversation so far (moved from antigravity):\n",
		"Owner: first question\nAssistant: reply from antigravity\nOwner: second question\nAssistant: reply from antigravity",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("block has no %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "third question") || ownerText(t, moved) != "third question" {
		t.Fatalf("block = %q", block)
	}

	// AC-PAF-004: with antigravity rested no more, the next turn still starts
	// at the sticky agy-2 and resumes its session.
	s.setFail("antigravity", "")
	h.reg.cooldownMu.Lock()
	h.reg.cooldowns = nil
	h.reg.cooldownMu.Unlock()
	if r := h.run(t, "fourth question"); r.Text != "reply from agy-2" {
		t.Fatalf("sticky = %+v", r)
	}
	turns = rt.recorded()
	if len(turns) != 5 || turns[4].Config.Account != "agy-2" || turns[4].ResumeID != "acp-agy-2" {
		t.Fatalf("sticky turn = %+v", turns[len(turns)-1])
	}
	if strings.HasPrefix(turns[4].Input[0].Text, "Conversation so far") {
		t.Fatal("a resumed turn got a context block")
	}
}

// REQ-PAF-004: an account that rests is skipped; when every account rests the
// pool is tried in order anyway, then the next chain target runs.
func TestPoolCooldowns(t *testing.T) {
	h, rt, s, _ := agyHarness(t, mock.Script{Responses: []mock.Response{textResponse("native answer")}}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high", Fallback: []string{"mock:m"}},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, []string{"antigravity", "agy-2"})
	h.reg.coolAccount("antigravity", time.Now().Add(time.Hour))
	if r := h.run(t, "one"); r.Text != "reply from agy-2" {
		t.Fatalf("result = %+v", r)
	}
	if turns := rt.recorded(); len(turns) != 1 || turns[0].Config.Account != "agy-2" {
		t.Fatalf("a resting account ran: %+v", turns)
	}

	// Every account rests and fails: both are tried in pool order (agy-2 is
	// sticky now), then lana's loop answers.
	s.setFail("antigravity", "rate_limit")
	s.setFail("agy-2", "auth")
	h.reg.coolAccount("agy-2", time.Now().Add(time.Hour))
	before := time.Now()
	if r := h.run(t, "two"); r.Text != "native answer" {
		t.Fatalf("result = %+v", r)
	}
	turns := rt.recorded()
	if len(turns) != 3 || turns[1].Config.Account != "agy-2" || turns[2].Config.Account != "antigravity" {
		t.Fatalf("turns = %+v", turns)
	}
	got := attempts(t, h.events(t))
	if len(got) != 2 || got[0].Provider != "agy-2" || got[1].Provider != "antigravity" {
		t.Fatalf("attempts = %+v", got)
	}
	// A later reset time wins over a shorter cooldown.
	if until := h.reg.AccountCooldowns()["agy-2"]; until.Before(before.Add(50 * time.Minute)) {
		t.Fatalf("agy-2 until = %v", until)
	}
}

// REQ-PAF-001: an early failure that is not auth or a limit stops the pool and
// goes to the next chain target.
func TestPoolStopsOnOtherFailure(t *testing.T) {
	h, rt, s, _ := agyHarness(t, mock.Script{Responses: []mock.Response{textResponse("native answer")}}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high", Fallback: []string{"mock:m"}},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, []string{"antigravity", "agy-2"})
	s.setFail("antigravity", core.ErrorClassNotFound)
	if r := h.run(t, "one"); r.Text != "native answer" {
		t.Fatalf("result = %+v", r)
	}
	if turns := rt.recorded(); len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	if len(h.reg.AccountCooldowns()) != 0 {
		t.Fatalf("cooldowns = %v", h.reg.AccountCooldowns())
	}
}

// REQ-AGY-008: a scheduled run uses an Antigravity account only with its
// allow_scheduled; a pool keeps the accounts that allow it.
func TestScheduledRunAntigravity(t *testing.T) {
	h, rt, _, _ := agyHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high", AllowScheduled: true},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind}}, nil)
	defer h.reg.MarkScheduled(h.info.ID)()
	r := h.run(t, "scheduled")
	if r.Err == nil || r.Err.Message != "antigravity is not allowed for scheduled runs of agent 'assistant'" || len(rt.recorded()) != 0 {
		t.Fatalf("result = %+v", r)
	}

	h2, rt2, _, _ := agyHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "antigravity/gemini-4-neon-high"},
	}, map[string]core.ProviderConfig{"agy-2": {Kind: core.AntigravityKind, AllowScheduled: true}}, []string{"antigravity", "agy-2"})
	defer h2.reg.MarkScheduled(h2.info.ID)()
	if r := h2.run(t, "scheduled"); r.Text != "reply from agy-2" {
		t.Fatalf("result = %+v", r)
	}
	if turns := rt2.recorded(); len(turns) != 1 || turns[0].Config.Account != "agy-2" {
		t.Fatalf("turns = %+v", turns)
	}
}

// The Antigravity prompt head names lana's tools as the lana MCP server's
// tools, not with Claude's prefix.
func TestAntigravityToolNote(t *testing.T) {
	if got := externalToolNote("antigravity", []string{"memory_write", "agent_list"}); !strings.Contains(got, "MCP tools named mcp_lana_<tool>: agent_list, memory_write") ||
		strings.Contains(got, "mcp__lana__") {
		t.Fatalf("note = %q", got)
	}
	if got := externalToolNote("claude-code", []string{"memory_write"}); !strings.Contains(got, "mcp__lana__<tool>") {
		t.Fatalf("claude note = %q", got)
	}
	if externalToolNote("antigravity", nil) != "" {
		t.Fatal("empty tool set has a note")
	}
}

// REQ-PAF-002: the transcript keeps the newest turns within the limit,
// dropping the oldest whole turns, and leaves injected blocks, runtime notes,
// and the current turn out.
func TestTranscriptTail(t *testing.T) {
	ev := func(turn, kind string, payload any) core.SessionEvent {
		raw, _ := json.Marshal(payload)
		return core.SessionEvent{TurnID: turn, Kind: kind, Payload: raw}
	}
	text := func(s string) []core.ContentBlock { return []core.ContentBlock{{Kind: core.BlockText, Text: s}} }
	events := []core.SessionEvent{
		ev("t1", core.KindUserMessage, core.UserMessage{Content: append(text("<context>x</context>"), text("old question")...), ContextBlocks: 1}),
		ev("t1", core.KindAssistantMessage, core.AssistantMessage{Content: append(text("old answer"), core.ContentBlock{Kind: core.BlockToolUse, Name: "bash"})}),
		ev("t2", core.KindUserMessage, core.UserMessage{Content: text("new question")}),
		ev("t2", core.KindUserMessage, core.UserMessage{Content: text("runtime note"), Runtime: true}),
		ev("t2", core.KindAssistantMessage, core.AssistantMessage{Content: []core.ContentBlock{{Kind: core.BlockThinking, Text: "hmm"}, {Kind: core.BlockText, Text: "new answer"}}}),
		ev("t3", core.KindUserMessage, core.UserMessage{Content: text("current")}),
	}
	full := transcriptTail(events, "t3", 1000)
	if full != "Owner: old question\nAssistant: old answer\nOwner: new question\nAssistant: new answer" {
		t.Fatalf("full = %q", full)
	}
	if got := transcriptTail(events, "t3", 50); got != "Owner: new question\nAssistant: new answer" {
		t.Fatalf("tail = %q", got)
	}
	if got := transcriptTail(events, "t3", 10); got != "new answer" {
		t.Fatalf("cut = %q", got)
	}
	if transcriptTail(events[5:], "t3", 100) != "" {
		t.Fatal("the current turn is in the transcript")
	}
}

func TestMCPToolNameAgyPrefix(t *testing.T) {
	for in, want := range map[string]string{"mcp__lana__memory_write": "memory_write", "mcp_lana_memory_write": "memory_write"} {
		if got, ok := mcpToolName(in); !ok || got != want {
			t.Fatalf("mcpToolName(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"run_command", "mcp_lana_", "mcp__lana__"} {
		if _, ok := mcpToolName(in); ok {
			t.Fatalf("mcpToolName(%q) matched", in)
		}
	}
}

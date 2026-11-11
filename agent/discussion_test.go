package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

func newDiscussionHarness(t *testing.T, script mock.Script, team core.TeamConfig) *harness {
	t.Helper()
	h := newSubagentHarness(t, script, map[string]core.AgentConfig{
		"lana":  {Provider: "mock", Model: "lead-model"},
		"karin": {Provider: "mock", Model: "m", Description: "Jobs"},
		"dana":  {Provider: "mock", Model: "m", Description: "Finance"},
	})
	for _, tool := range tools.NewDiscussTools() {
		if err := h.tools.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	team.Lead = "lana"
	team.Members = []string{"karin", "dana"}
	if team.Mode == "" {
		team.Mode = core.TeamModeLead
	}
	h.reg.config.Teams = map[string]core.TeamConfig{"career": team}
	h.reg.home = t.TempDir()
	newSubagentParent(t, h, "career")
	return h
}

func discussionEvents(t *testing.T, h *harness) (started []core.DiscussionStartedPayload, msgs []core.DiscussionMessagePayload, ended []core.DiscussionEndedPayload) {
	t.Helper()
	for _, e := range h.events(t) {
		switch e.Kind {
		case core.KindDiscussionStarted:
			var p core.DiscussionStartedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatalf("payload %s: %v", e.Payload, err)
			}
			started = append(started, p)
		case core.KindDiscussionMessage:
			var p core.DiscussionMessagePayload
			_ = json.Unmarshal(e.Payload, &p)
			msgs = append(msgs, p)
		case core.KindDiscussionEnded:
			var p core.DiscussionEndedPayload
			_ = json.Unmarshal(e.Payload, &p)
			ended = append(ended, p)
		}
	}
	return
}

func TestDiscussionFlow(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "discuss_start", `{"topic":"Ambil tawaran?","opening":"Saya ajak Karin dan Dana.","members":[{"agent":"karin","angle":"karier"},{"agent":"dana","angle":"uang"}]}`),
		toolResponse("c2", "discuss_ask", `{"agent":"karin","message":"@Karin, layak?"}`),
		textResponse("Layak, persentil 60."),
		toolResponse("c3", "discuss_ask", `{"agent":"dana","message":"@Dana, setuju?","reply_to":"karin","quote":"Layak"}`),
		textResponse("PASS: setuju dengan Karin"),
		toolResponse("c4", "discuss_end", `{"reason":"agreed","conclusion":"Ambil.","agreed":[{"text":"Gaji wajar","agent":"karin"}],"parts":{"karin":"Cek pasar"},"notes":[{"kind":"decision","text":"Ambil tawaran"}]}`),
		textResponse("Kesimpulannya: ambil."),
	}}, core.TeamConfig{})
	result := h.run(t, "Tokopedia nawarin 28 jt, ambil ga?")
	if result.Text != "Kesimpulannya: ambil." || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	started, msgs, ended := discussionEvents(t, h)
	if len(started) != 1 || started[0].Topic != "Ambil tawaran?" || len(started[0].Members) != 2 || started[0].Rounds != 3 || started[0].Lead != "lana" {
		t.Fatalf("started=%+v", started)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages=%+v", msgs)
	}
	if msgs[0].Role != core.DiscussionRoleLead || msgs[0].Agent != "karin" || msgs[0].Text != "@Karin, layak?" || msgs[0].Round != 1 {
		t.Fatalf("ask=%+v", msgs[0])
	}
	if msgs[1].Role != core.DiscussionRoleMember || msgs[1].Agent != "karin" || msgs[1].Text != "Layak, persentil 60." || msgs[1].ChildSessionID == "" {
		t.Fatalf("reply=%+v", msgs[1])
	}
	if !msgs[3].Passed || msgs[3].Text != "setuju dengan Karin" || msgs[3].ReplyTo != "karin" || msgs[3].Quote != "Layak" {
		t.Fatalf("pass=%+v", msgs[3])
	}
	if len(ended) != 1 || ended[0].Reason != "agreed" || ended[0].NotesSaved != 1 || ended[0].Rounds != 1 || ended[0].Parts["karin"] != "Cek pasar" {
		t.Fatalf("ended=%+v", ended)
	}
	notes, err := h.reg.RoomMemory().List("career")
	if err != nil || len(notes) != 1 || notes[0].Kind != "decision" || notes[0].Agent != "lana" {
		t.Fatalf("notes=%+v err=%v", notes, err)
	}
	if h.reg.disc.get(h.info.ID) != nil {
		t.Fatal("discussion still open after discuss_end")
	}
	// The member got the briefing; its child session holds the topic.
	children := subagentChildren(t, h, h.info.ID)
	if len(children) != 2 {
		t.Fatalf("children=%+v", children)
	}
}

func TestDiscussionRoundLimitAndNoExtra(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "discuss_start", `{"topic":"T","opening":"O","members":[{"agent":"karin"}]}`),
		toolResponse("c2", "discuss_ask", `{"agent":"karin","message":"@Karin 1"}`),
		textResponse("satu"),
		toolResponse("c3", "discuss_ask", `{"agent":"karin","message":"@Karin 2"}`),
		toolResponse("c4", "discuss_more", `{"message":"Satu ronde lagi","reason":"x"}`),
		toolResponse("c5", "discuss_end", `{"reason":"limit","conclusion":"C"}`),
		textResponse("done"),
	}}, core.TeamConfig{Rounds: 1, ExtraRounds: core.TeamExtraNever})
	h.run(t, "hi")
	if p := toolResultPayload(t, h, "c3"); p.Status != core.ToolStatusError || !strings.Contains(p.Output, "round limit reached") {
		t.Fatalf("second ask=%+v", p)
	}
	if p := toolResultPayload(t, h, "c4"); p.Status != core.ToolStatusError || !strings.Contains(p.Output, "extra rounds are off") {
		t.Fatalf("more=%+v", p)
	}
}

func discussionUnit(t *testing.T, team core.TeamConfig) (*harness, *discussionRunner, core.ToolCtx) {
	t.Helper()
	h := newDiscussionHarness(t, mock.Script{}, team)
	cfg := h.reg.config.Teams["career"]
	d := &discussionRunner{reg: h.reg, team: "career", lead: "lana", cfg: cfg, allowed: cfg.Members}
	tc := core.ToolCtx{SessionID: h.info.ID, TurnID: "t1", CallID: "c1"}
	out, err := d.Start(context.Background(), tc, core.DiscussStart{Topic: "T", Opening: "O", Members: []core.DiscussionMember{{Agent: "karin"}, {Agent: "dana"}}})
	if err != nil || out.Status != core.ToolStatusSuccess {
		t.Fatalf("start=%+v err=%v", out, err)
	}
	return h, d, tc
}

func TestDiscussionExtraOnceThenAsk(t *testing.T) {
	_, d, tc := discussionUnit(t, core.TeamConfig{})
	out, _ := d.More(context.Background(), tc, core.DiscussMore{Message: "Tambah satu", Reason: "data"})
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "limit now 4") {
		t.Fatalf("first more=%+v", out)
	}
	// The second extra round needs the owner; this turn has no question service.
	out, _ = d.More(context.Background(), tc, core.DiscussMore{Message: "Lagi", Reason: "data"})
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "cannot be asked") {
		t.Fatalf("second more=%+v", out)
	}
}

type answerQuestions struct{ label string }

func (a answerQuestions) Ask(context.Context, core.QuestionRequest) (core.QuestionOutcome, error) {
	return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: []core.QuestionAnswer{{Selected: []string{a.label}}}}, nil
}

func TestDiscussionExtraAskOwner(t *testing.T) {
	_, d, tc := discussionUnit(t, core.TeamConfig{ExtraRounds: core.TeamExtraAsk})
	tc.Questions = answerQuestions{label: "Sum up now"}
	out, _ := d.More(context.Background(), tc, core.DiscussMore{Message: "Tambah?", Reason: "x"})
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "summary now") {
		t.Fatalf("declined=%+v", out)
	}
	tc.Questions = answerQuestions{label: "Add 1 round"}
	out, _ = d.More(context.Background(), tc, core.DiscussMore{Message: "Tambah?", Reason: "x"})
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "limit now 4") {
		t.Fatalf("granted=%+v", out)
	}
}

func TestDiscussionHardCap(t *testing.T) {
	_, d, tc := discussionUnit(t, core.TeamConfig{Rounds: 5})
	out, _ := d.More(context.Background(), tc, core.DiscussMore{Message: "m", Reason: "r"})
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "at most 5 rounds") {
		t.Fatalf("more=%+v", out)
	}
}

func TestDiscussionStop(t *testing.T) {
	h, d, tc := discussionUnit(t, core.TeamConfig{})
	ok, err := h.reg.StopDiscussion(context.Background(), h.info.ID, core.DiscussionDirect)
	if !ok || err != nil {
		t.Fatalf("stop=%v err=%v", ok, err)
	}
	out, _ := d.Ask(context.Background(), tc, core.DiscussAsk{Agent: "karin", Message: "@Karin"})
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "direct answer") {
		t.Fatalf("ask after stop=%+v", out)
	}
	out, _ = d.End(context.Background(), tc, core.DiscussEnd{Reason: "agreed", Conclusion: "C"})
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("end=%+v", out)
	}
	_, msgs, ended := discussionEvents(t, h)
	if len(msgs) != 1 || msgs[0].Role != core.DiscussionRoleOwner || msgs[0].Stop != core.DiscussionDirect {
		t.Fatalf("stop message=%+v", msgs)
	}
	if len(ended) != 1 || ended[0].Reason != "direct" {
		t.Fatalf("ended=%+v", ended)
	}
	if ok, _ := h.reg.StopDiscussion(context.Background(), h.info.ID, core.DiscussionStop); ok {
		t.Fatal("stop after end reported an open discussion")
	}
}

func TestDiscussionStartChecks(t *testing.T) {
	_, d, tc := discussionUnit(t, core.TeamConfig{})
	out, _ := d.Start(context.Background(), tc, core.DiscussStart{Topic: "T", Opening: "O", Members: []core.DiscussionMember{{Agent: "karin"}}})
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "already open") {
		t.Fatalf("second start=%+v", out)
	}
	tc.SessionID = "other"
	out, _ = d.Start(context.Background(), tc, core.DiscussStart{Topic: "T", Opening: "O", Members: []core.DiscussionMember{{Agent: "opi"}}})
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "not a member") {
		t.Fatalf("stranger=%+v", out)
	}
}

func TestDiscussionParsePass(t *testing.T) {
	for in, want := range map[string]bool{
		"PASS: setuju":          true,
		"pass":                  true,
		"Pass - nothing new":    true,
		"Passing the bonus on…": false,
		"Saya setuju":           false,
	} {
		if got, _ := parsePass(in); got != want {
			t.Errorf("parsePass(%q)=%v want %v", in, got, want)
		}
	}
}

func TestDiscussionLeadPrompt(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, core.TeamConfig{Discuss: core.TeamDiscussAsk, Rounds: 2, Description: "Keputusan karier"})
	if _, err := h.reg.RoomMemory().Add("career", "fact", "Target nego 31 jt", "dana", "d_x", ""); err != nil {
		t.Fatal(err)
	}
	var names []string
	h.hooks.LLMStream.Use(func(ctx context.Context, in core.LLMStreamInput, next core.Next[core.LLMStreamInput, core.ChatStream]) (core.ChatStream, error) {
		for _, spec := range in.Request.Tools {
			names = append(names, spec.Name)
		}
		return next(ctx, in)
	})
	h.run(t, "hi")
	var system core.SystemMessage
	for _, e := range h.events(t) {
		if e.Kind == core.KindSystemMessage {
			_ = json.Unmarshal(e.Payload, &system)
		}
	}
	for _, want := range []string{`## Room "career"`, "Purpose: Keputusan karier", "only when the owner asks", "round limit is 2", "Target nego 31 jt"} {
		if !strings.Contains(system.Text, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, system.Text)
		}
	}
	for _, want := range DiscussTools {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Fatalf("tool %s not offered: %v", want, names)
		}
	}
}

// fakeKnowledge answers Recall and the domain checks the discussion uses.
type fakeKnowledge struct {
	core.Knowledge
	core.KnowledgeDomains
	hits map[string][]core.KnowledgeHit
}

func (f *fakeKnowledge) Recall(_ context.Context, _ string, _ int, scopes []string, _ string, _ func(string) bool) ([]core.KnowledgeHit, error) {
	return f.hits[scopes[0]], nil
}
func (f *fakeKnowledge) HasDomain(agent string) bool { return agent == "karin" || agent == "dana" }
func (f *fakeKnowledge) Pages(_ context.Context, scope, _ string, _ int) ([]core.KnowledgePageInfo, int, error) {
	if scope == "karin" {
		return []core.KnowledgePageInfo{{Title: "gaji-pasar"}, {Title: "offer-2026"}}, 48, nil
	}
	return nil, 0, nil
}

func TestDiscussionKnowledgeAware(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "discuss_start", `{"topic":"Ambil tawaran?","opening":"O","members":[{"agent":"karin","angle":"pasar"}]}`),
		toolResponse("c2", "discuss_ask", `{"agent":"karin","message":"@Karin?"}`),
		textResponse("Layak di persentil 60.\nNew point: bonus tahunan hilang.\nNotes: gaji-pasar, offer-2026"),
		toolResponse("c3", "discuss_end", `{"reason":"agreed","conclusion":"Ambil."}`),
		textResponse("ok"),
	}}, core.TeamConfig{})
	h.reg.knowledge = &fakeKnowledge{hits: map[string][]core.KnowledgeHit{
		"karin": {{Path: "agents/karin/wiki/gaji-pasar.md", Title: "gaji-pasar"}, {Path: "agents/karin/wiki/offer-2026.md", Title: "offer-2026"}},
	}}
	h.run(t, "ambil ga?")
	started, msgs, _ := discussionEvents(t, h)
	if len(started) != 1 || started[0].Members[0].Notes != 2 || len(started[0].Skipped) != 1 || started[0].Skipped[0].Agent != "dana" {
		t.Fatalf("started=%+v", started)
	}
	if p := toolResultPayload(t, h, "c1"); !strings.Contains(p.Output, "karin has 2 matching notes") {
		t.Fatalf("start output=%q", p.Output)
	}
	reply := msgs[1]
	if !reply.NewPoint || strings.Join(reply.Notes, ",") != "gaji-pasar,offer-2026" || strings.Contains(reply.Text, "Notes:") || strings.Contains(reply.Text, "New point:") {
		t.Fatalf("reply=%+v", reply)
	}
	// The lead's roster carries what each member knows.
	var system core.SystemMessage
	for _, e := range h.events(t) {
		if e.Kind == core.KindSystemMessage {
			_ = json.Unmarshal(e.Payload, &system)
		}
	}
	if !strings.Contains(system.Text, "karin: Jobs (knows: 48 pages · gaji-pasar, offer-2026)") {
		t.Fatalf("roster:\n%s", system.Text)
	}
}

func TestDiscussionNotesLine(t *testing.T) {
	text, notes := splitNotesLine("Jawab.\n**Notes:** a, `b`")
	if text != "Jawab." || strings.Join(notes, "|") != "a|b" {
		t.Fatalf("text=%q notes=%v", text, notes)
	}
	if text, notes := splitNotesLine("Tidak ada catatan di akhir."); text != "Tidak ada catatan di akhir." || notes != nil {
		t.Fatalf("plain=%q %v", text, notes)
	}
}

func TestTaskPromptBuildsOnTeammates(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{}, core.TeamConfig{})
	h.reg.knowledge = &fakeKnowledge{hits: map[string][]core.KnowledgeHit{
		"karin": {{Path: "agents/karin/wiki/csv-guide.md", Title: "csv-guide"}},
	}}
	ctx := context.Background()
	child, err := h.store.Create(ctx, core.NewSession{Agent: "dana", Channel: "library", Workspace: h.ws, ParentSessionID: h.info.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Append(ctx, child.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "tc", Payload: core.TurnStart{V: core.PayloadVersion, TurnID: "tc"}},
		{Kind: core.KindAssistantMessage, TurnID: "tc", Payload: core.AssistantMessage{V: core.PayloadVersion, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "Pakai encoding/csv."}}, StopReason: "end_turn"}},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &subagentRunner{reg: h.reg, allowed: []string{"karin", "dana"}}
	got := runner.taskPrompt(ctx, h.info.ID, core.SpawnRequest{Agent: "karin", Prompt: "Implement export CSV", Context: []string{child.ID, "unknown"}, WithNotes: true})
	for _, want := range []string{"Implement export CSV", "### From dana\nPakai encoding/csv.", "- csv-guide (agents/karin/wiki/csv-guide.md)", "Notes: <page titles"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, got)
		}
	}
}

func TestToolCallFacts(t *testing.T) {
	name, note, file := toolCallFacts(json.RawMessage(`{"name":"mcp__lana__knowledge_read","input":{"ref":"agents/karin/wiki/csv-guide.md"}}`))
	if name != "knowledge_read" || note != "csv-guide" || file != "" {
		t.Fatalf("read = %q %q %q", name, note, file)
	}
	name, _, file = toolCallFacts(json.RawMessage(`{"name":"Edit","input":{"file_path":"invoice/export.go"}}`))
	if name != "Edit" || file != "invoice/export.go" {
		t.Fatalf("edit = %q %q", name, file)
	}
}

func TestRosterCapabilities(t *testing.T) {
	got := core.AgentCapabilities(core.AgentConfig{Model: "claude-code/sonnet", Tools: []string{"bash", "write_file", "web_fetch"}, MCP: []string{"broker"}})
	if strings.Join(got, ",") != "Claude Code,web,files,shell,knowledge,broker" {
		t.Fatalf("capabilities = %v", got)
	}
}

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func groupMessages(t *testing.T, h *harness) []core.AssistantMessage {
	t.Helper()
	var out []core.AssistantMessage
	for _, e := range h.events(t) {
		if e.Kind != core.KindAssistantMessage {
			continue
		}
		var p core.AssistantMessage
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func text(m core.AssistantMessage) string {
	var parts []string
	for _, b := range m.Content {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "")
}

// Everyone answers a message that names no one, in order, lead first; a
// teammate with nothing to add passes.
func TestGroupEveryoneAnswers(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("Saya koordinir. @karin cek gaji pasar."),
		textResponse("Gaji pasar 28–40 jt."),
		textResponse("PASS"),
	}}, core.TeamConfig{Mode: core.TeamModeGroup})
	result := h.run(t, "Tawaran 28 jt, gimana?")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	msgs := groupMessages(t, h)
	if len(msgs) != 3 {
		t.Fatalf("messages=%+v", msgs)
	}
	if msgs[0].SourceName != "lana" || msgs[1].SourceName != "karin" || msgs[2].SourceName != "dana" || !msgs[2].Passed {
		t.Fatalf("speakers=%+v", msgs)
	}
	if text(msgs[1]) != "Gaji pasar 28–40 jt." || msgs[0].Source != GroupSource {
		t.Fatalf("karin=%+v", msgs[1])
	}
	// Each agent ran in its own session, briefed on the room.
	children := subagentChildren(t, h, h.info.ID)
	if len(children) != 3 {
		t.Fatalf("children=%+v", children)
	}
}

// A message that @mentions one agent goes only to it; its own @mention hands
// over to a teammate, who sees what was said.
func TestGroupMentionsRoute(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("Sisa bersih 5,5 jt. @karin tolong cek angkanya."),
		textResponse("Sudah saya cek, cocok."),
	}}, core.TeamConfig{Mode: core.TeamModeGroup})
	h.run(t, "@dana hitung sisa bersihnya")
	msgs := groupMessages(t, h)
	if len(msgs) != 2 || msgs[0].SourceName != "dana" || msgs[1].SourceName != "karin" {
		t.Fatalf("messages=%+v", msgs)
	}
	var karin core.SessionInfo
	for _, c := range subagentChildren(t, h, h.info.ID) {
		if c.Agent == "karin" {
			karin = c
		}
	}
	events, err := h.store.ChainEvents(t.Context(), karin.ID)
	if err != nil {
		t.Fatal(err)
	}
	var briefed bool
	for _, e := range events {
		if e.Kind == core.KindUserMessage && strings.Contains(string(e.Payload), "Dana: Sisa bersih 5,5 jt") && strings.Contains(string(e.Payload), "Owner: @dana hitung") {
			briefed = true
		}
	}
	if !briefed {
		t.Fatal("karin did not see the conversation")
	}
}

// A lead-mode team keeps the lead's turn (no group replies).
func TestGroupLeadModeUnchanged(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{Responses: []mock.Response{textResponse("lead answer")}}, core.TeamConfig{Mode: core.TeamModeLead})
	result := h.run(t, "hi")
	if result.Text != "lead answer" {
		t.Fatalf("result=%+v", result)
	}
}

func TestMentionsIn(t *testing.T) {
	h := newDiscussionHarness(t, mock.Script{}, core.TeamConfig{})
	got := h.reg.mentionsIn("@Karin dan @dana, juga @nobody", []string{"lana", "karin", "dana"})
	if strings.Join(got, ",") != "karin,dana" {
		t.Fatalf("mentions=%v", got)
	}
}

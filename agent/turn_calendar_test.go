package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// calendarTurnTools runs one turn of an agent and returns the tool names the
// provider was offered. registerCalendar registers a calendar tool first.
func calendarTurnTools(t *testing.T, cfg core.AgentConfig, registerCalendar, child bool) []string {
	t.Helper()
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		map[string]core.AgentConfig{"assistant": cfg})
	if registerCalendar {
		if err := h.tools.Register(fakeMCPTool{name: CalendarTool}); err != nil {
			t.Fatal(err)
		}
	}
	if child {
		session, err := h.store.Create(context.Background(), core.NewSession{
			Agent: "assistant", Channel: "library", Workspace: h.ws, ParentSessionID: h.info.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		h.info = session
	}
	recording := &recordingProvider{}
	h.llm.providers["mock"] = recording
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	return toolSpecNames(recording.last.Tools)
}

// REQ-CAL-009: every agent is offered the calendar tool without listing it,
// unless disabled_tools removes it.
func TestTurnOffersCalendarTool(t *testing.T) {
	base := core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"read_file"}}

	t.Run("registered and not listed", func(t *testing.T) {
		names := calendarTurnTools(t, base, true, false)
		if !slices.Contains(names, CalendarTool) {
			t.Fatalf("offered tools=%v, calendar missing", names)
		}
		if !slices.Contains(names, "read_file") {
			t.Fatalf("offered tools=%v, read_file missing", names)
		}
		if !slices.IsSorted(names) {
			t.Fatalf("offered tools=%v, not sorted", names)
		}
	})

	t.Run("child session", func(t *testing.T) {
		if names := calendarTurnTools(t, base, true, true); !slices.Contains(names, CalendarTool) {
			t.Fatalf("offered tools=%v, a child session should get calendar", names)
		}
	})

	t.Run("not registered", func(t *testing.T) {
		if names := calendarTurnTools(t, base, false, false); slices.Contains(names, CalendarTool) {
			t.Fatalf("offered tools=%v, unregistered calendar offered", names)
		}
	})

	t.Run("disabled_tools", func(t *testing.T) {
		cfg := base
		cfg.DisabledTools = []string{CalendarTool}
		if names := calendarTurnTools(t, cfg, true, false); slices.Contains(names, CalendarTool) {
			t.Fatalf("offered tools=%v, calendar should be disabled", names)
		}
	})

	t.Run("listed is not duplicated", func(t *testing.T) {
		cfg := base
		cfg.Tools = []string{"read_file", CalendarTool}
		names := calendarTurnTools(t, cfg, true, false)
		count := 0
		for _, name := range names {
			if name == CalendarTool {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("offered tools=%v, calendar offered %d times, want 1", names, count)
		}
	})
}

// TestAutoOfferedToolsCalendar covers the shared helper the dashboard catalog
// reads: calendar is auto-offered only when registered, root or child.
func TestAutoOfferedToolsCalendar(t *testing.T) {
	if got := AutoOfferedTools(AutoOfferState{CalendarRegistered: true}); !slices.Equal(got, []string{CalendarTool}) {
		t.Fatalf("AutoOfferedTools = %v, want [calendar]", got)
	}
	if got := AutoOfferedTools(AutoOfferState{Root: true}); slices.Contains(got, CalendarTool) {
		t.Fatalf("AutoOfferedTools = %v, calendar without registration", got)
	}
}

// TestAutoOfferedToolsSendFile covers the shared helper: send_file is
// auto-offered only when the registry has it, after the calendar tool
// (REQ-AGF-002).
func TestAutoOfferedToolsSendFile(t *testing.T) {
	if got := AutoOfferedTools(AutoOfferState{SendFileRegistered: true}); !slices.Equal(got, []string{SendFileTool}) {
		t.Fatalf("AutoOfferedTools = %v, want [send_file]", got)
	}
	if got := AutoOfferedTools(AutoOfferState{Root: true}); slices.Contains(got, SendFileTool) {
		t.Fatalf("AutoOfferedTools = %v, send_file without registration", got)
	}
	both := AutoOfferedTools(AutoOfferState{CalendarRegistered: true, SendFileRegistered: true})
	if !slices.Equal(both, []string{CalendarTool, SendFileTool}) {
		t.Fatalf("AutoOfferedTools = %v, want [calendar send_file]", both)
	}
}

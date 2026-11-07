package main

// The Telegram side of the calendar (spec #35, design §Wiring, API-CAL-017):
// the owner sender of calendar messages and notices, and the /agenda command.
// cmd/lana wires them into the channel in daemon.go.

import (
	"context"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/channels/telegram"
)

// calendarTelegramSender returns the plain owner message sender of the
// calendar: send_message actions, missed and failed notices and the daily
// agenda go to every Telegram owner private chat (REQ-CAL-006, REQ-CAL-007,
// REQ-CAL-011).
func calendarTelegramSender(ch *telegram.Channel) func(ctx context.Context, text string) error {
	return telegramWorkflowNotifier{ch: ch}.SendOwner
}

// withAgendaCommand puts /agenda in front of the workflow command hook, so a
// workflow with a chat command named "agenda" never swallows it (API-CAL-017).
func withAgendaCommand(rt *lana.Runtime, hooks telegram.WorkflowHooks, now func() time.Time) telegram.WorkflowHooks {
	next := hooks.Command
	hooks.Command = func(ctx context.Context, text string) (string, bool) {
		if isAgendaCommand(text) {
			return agendaReply(ctx, rt, now())
		}
		if next == nil {
			return "", false
		}
		return next(ctx, text)
	}
	return hooks
}

// agendaReply renders today's agenda for /agenda.
func agendaReply(ctx context.Context, rt *lana.Runtime, now time.Time) (string, bool) {
	if rt.Calendar() == nil {
		return "· the calendar is not available", true
	}
	text, err := rt.Calendar().Agenda(ctx, now)
	if err != nil {
		return "✗ " + err.Error(), true
	}
	return text, true
}

// isAgendaCommand reports whether text is "/agenda", also "/agenda@bot" and
// with trailing words.
func isAgendaCommand(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	word := fields[0]
	if i := strings.IndexByte(word, '@'); i >= 0 {
		word = word[:i]
	}
	return strings.EqualFold(word, "/agenda")
}

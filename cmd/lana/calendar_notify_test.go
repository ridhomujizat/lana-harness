package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/internal/calendar"
	"github.com/ridhomujizat/lana-harness/web"
)

// The Google link satisfies the web port of API-CAL-012 … API-CAL-014.
var _ web.CalendarGoogle = (*calendar.Google)(nil)

// API-CAL-017: /agenda is answered with today's agenda before the workflow
// command hook, and every other command still reaches the workflow hook.
func TestAgendaCommandRouting(t *testing.T) {
	rt := newChatRuntime(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := rt.Calendar().CreateItem(ctx, calendar.Item{
		Kind: calendar.KindTask, Title: "Pay electricity", Date: now.In(rt.Calendar().Location(ctx)).Format("2006-01-02"),
	}, "agent:Dana"); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	var forwarded []string
	hooks := withAgendaCommand(rt, telegram.WorkflowHooks{
		Command: func(_ context.Context, text string) (string, bool) {
			forwarded = append(forwarded, text)
			return "workflow reply", true
		},
	}, func() time.Time { return now })

	for _, text := range []string{"/agenda", "/agenda@lana_bot", "/AGENDA please"} {
		reply, handled := hooks.Command(ctx, text)
		if !handled || !strings.Contains(reply, "Pay electricity (added by Dana)") {
			t.Fatalf("%s: reply=%q handled=%v", text, reply, handled)
		}
	}
	if len(forwarded) != 0 {
		t.Fatalf("/agenda reached the workflow hook: %v", forwarded)
	}

	reply, handled := hooks.Command(ctx, "/research cats")
	if !handled || reply != "workflow reply" || len(forwarded) != 1 || forwarded[0] != "/research cats" {
		t.Fatalf("workflow command: reply=%q handled=%v forwarded=%v", reply, handled, forwarded)
	}

	// Without a workflow hook other commands stay unhandled.
	bare := withAgendaCommand(rt, telegram.WorkflowHooks{}, time.Now)
	if _, handled := bare.Command(ctx, "/agendas"); handled {
		t.Fatal("/agendas was handled")
	}
}

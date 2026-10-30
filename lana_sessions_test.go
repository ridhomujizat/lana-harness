package lana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestListAllSessionsEveryChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	rt := newTestRuntime(t, srv.URL, nil)
	ctx := context.Background()

	channels := []string{"cli", "telegram", "web"}
	for _, channel := range channels {
		if _, err := rt.CreateChannelSession(ctx, channel, "address-"+channel, "assistant"); err != nil {
			t.Fatalf("CreateChannelSession(%s): %v", channel, err)
		}
		// Keep the updated_at values distinct so the paging order is defined.
		time.Sleep(2 * time.Millisecond)
	}

	all, err := rt.ListAllSessions(ctx, core.SessionFilter{})
	if err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}
	if len(all) != len(channels) {
		t.Fatalf("ListAllSessions returned %d sessions, want %d", len(all), len(channels))
	}
	seen := map[string]bool{}
	for _, info := range all {
		seen[info.Channel] = true
	}
	for _, channel := range channels {
		if !seen[channel] {
			t.Fatalf("ListAllSessions did not return channel %q: %+v", channel, all)
		}
	}
	if all[0].Channel != "web" {
		t.Fatalf("first session channel = %q, want the newest channel web", all[0].Channel)
	}

	webOnly, err := rt.ListAllSessions(ctx, core.SessionFilter{Channel: "web"})
	if err != nil {
		t.Fatalf("ListAllSessions(web): %v", err)
	}
	if len(webOnly) != 1 || webOnly[0].Channel != "web" {
		t.Fatalf("web sessions = %+v", webOnly)
	}

	limited, err := rt.ListAllSessions(ctx, core.SessionFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListAllSessions(limit): %v", err)
	}
	if len(limited) != 2 || limited[0].Channel != "web" || limited[1].Channel != "telegram" {
		t.Fatalf("limited sessions = %+v", limited)
	}
}

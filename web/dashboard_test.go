package web

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/stats"
)

// fakeDashboardBackend adds the dashboard port to the in-memory backend.
type fakeDashboardBackend struct {
	*fakeWebBackend
	dashboard stats.Dashboard
	err       error
	gotRange  string
	gotAgent  string
}

func (b *fakeDashboardBackend) Dashboard(_ context.Context, rangeName, agent string) (stats.Dashboard, error) {
	b.gotRange = rangeName
	b.gotAgent = agent
	dashboard := b.dashboard
	dashboard.Agent = agent
	return dashboard, b.err
}

func sampleDashboard() stats.Dashboard {
	return stats.Dashboard{
		Range:           "7d",
		Agents:          []string{"assistant", "lana"},
		From:            10,
		To:              20,
		BucketMS:        86_400_000,
		DaemonStartedAt: 5,
		Now:             stats.Now{SessionsRunning: 2, SessionsWaitingInput: 1},
		Summary:         stats.Summary{InputTokens: 7, Turns: 3},
		Series:          []stats.SeriesPoint{{Start: 10}},
		ByModel:         []stats.ModelRow{{Provider: "deepseek", Model: "deepseek-chat"}},
		ByAgent:         []stats.AgentRow{{Agent: "assistant"}},
		BySource:        []stats.SourceRow{{Source: "turn"}},
		Tools:           []stats.ToolRow{{Name: "read_file"}},
		TurnReasons:     []stats.ReasonRow{{Reason: "end_turn"}},
		TurnDuration:    stats.Duration{AvgMS: 1, P95MS: 2},
		Errors:          []stats.ErrorRow{{At: 1, Source: "tool", Message: "read_file: boom"}},
	}
}

// TestDashboardShapeCoversACDSH002 covers the API-DSH-001 shape of
// GET /v1/dashboard?range=7d, including the non-null lists.
func TestDashboardShapeCoversACDSH002(t *testing.T) {
	backend := &fakeDashboardBackend{fakeWebBackend: newFakeWebBackend(), dashboard: sampleDashboard()}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard?range=7d", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.gotRange != "7d" {
		t.Fatalf("backend range = %q, want 7d", backend.gotRange)
	}
	var got stats.Dashboard
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(got, sampleDashboard()) {
		t.Fatalf("dashboard = %+v, want %+v", got, sampleDashboard())
	}
	for _, key := range []string{`"agent":"`, `"agents":["assistant","lana"]`, `"series":[`, `"by_model":[`, `"by_agent":[`, `"by_source":[`, `"tools":[`, `"turn_reasons":[`, `"errors":[`} {
		if !strings.Contains(rr.Body.String(), key) {
			t.Fatalf("body has no %s: %s", key, rr.Body.String())
		}
	}
}

// TestDashboardDefaultsRange covers the 24h default when range is omitted.
func TestDashboardDefaultsRange(t *testing.T) {
	backend := &fakeDashboardBackend{fakeWebBackend: newFakeWebBackend(), dashboard: sampleDashboard()}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.gotRange != defaultDashboardRange {
		t.Fatalf("backend range = %q, want %q", backend.gotRange, defaultDashboardRange)
	}
}

// TestDashboardPassesAgent covers API-DSH-001 r2: the agent query value
// reaches the backend and is echoed in the payload; an absent value means all
// agents.
func TestDashboardPassesAgent(t *testing.T) {
	backend := &fakeDashboardBackend{fakeWebBackend: newFakeWebBackend(), dashboard: sampleDashboard()}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard?range=7d&agent=lana", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.gotAgent != "lana" {
		t.Fatalf("backend agent = %q, want lana", backend.gotAgent)
	}
	var got stats.Dashboard
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.Agent != "lana" {
		t.Fatalf("agent = %q, want lana", got.Agent)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard?range=7d", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.gotAgent != "" {
		t.Fatalf("backend agent = %q, want empty", backend.gotAgent)
	}
}

// TestDashboardLongAgentIs400 covers API-DSH-001 r2: an agent filter over 128
// bytes is invalid_request and never reaches the backend.
func TestDashboardLongAgentIs400(t *testing.T) {
	backend := &fakeDashboardBackend{fakeWebBackend: newFakeWebBackend(), dashboard: sampleDashboard()}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard?agent="+strings.Repeat("a", 129), "", cookie, ""))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	if backend.gotAgent != "" {
		t.Fatalf("backend was called with agent %q", backend.gotAgent)
	}

	// 128 bytes is the inclusive limit.
	agent := strings.Repeat("a", 128)
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard?agent="+agent, "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.gotAgent != agent {
		t.Fatalf("backend agent = %d bytes, want the 128-byte value", len(backend.gotAgent))
	}
}

// TestDashboardBadRangeIs400 covers API-DSH-001: an unknown range is
// invalid_request and never reaches the backend.
func TestDashboardBadRangeIs400(t *testing.T) {
	backend := &fakeDashboardBackend{fakeWebBackend: newFakeWebBackend(), dashboard: sampleDashboard()}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	for _, name := range []string{"1h", "week", "365d", "24H"} {
		rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard?range="+name, "", cookie, ""))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
	if backend.gotRange != "" {
		t.Fatalf("backend was called with range %q", backend.gotRange)
	}
}

// TestDashboardRequiresAuth covers the auth middleware on the route.
func TestDashboardRequiresAuth(t *testing.T) {
	backend := &fakeDashboardBackend{fakeWebBackend: newFakeWebBackend()}
	s, _ := newSessionTestServer(t, backend)

	rr := serveAuth(s, authRequest(http.MethodGet, "/v1/dashboard", ""))
	assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
}

// TestDashboardWithoutPortIs500 covers a Backend that does not implement the
// optional DashboardBackend port.
func TestDashboardWithoutPortIs500(t *testing.T) {
	s, cookie, _ := newLoggedInSessionServer(t, newFakeWebBackend())

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/dashboard", "", cookie, ""))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

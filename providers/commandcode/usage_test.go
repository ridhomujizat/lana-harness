package commandcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// usageServer answers the Command Code usage endpoints with the given
// bodies; a path missing from bodies answers 500. It records each request
// URI and the Authorization header.
type usageServer struct {
	mu      sync.Mutex
	uris    map[string]string
	auth    []string
	bodies  map[string]string
	statusF func(path string) int
}

func newUsageServer(t *testing.T, bodies map[string]string) (*usageServer, *httptest.Server) {
	t.Helper()
	us := &usageServer{uris: map[string]string{}, bodies: bodies}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		us.mu.Lock()
		us.uris[r.URL.Path] = r.URL.RequestURI()
		us.auth = append(us.auth, r.Header.Get("Authorization"))
		statusF := us.statusF
		us.mu.Unlock()
		if statusF != nil {
			if status := statusF(r.URL.Path); status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"bad key ` + testKey + `"}`))
				return
			}
		}
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return us, srv
}

func (us *usageServer) uri(path string) string {
	us.mu.Lock()
	defer us.mu.Unlock()
	return us.uris[path]
}

const (
	fiveHourReset = int64(1795000000000)
	weeklyReset   = int64(1795500000000)
)

func fullUsageBodies() map[string]string {
	return map[string]string{
		whoamiPath: `{"org":{"id":"org_42","login":"acme"},"user":{"userName":"ridho"},"orgLimits":[]}`,
		creditsPath: `{"credits":{"monthlyCredits":12.5,"purchasedCredits":"5","freeCredits":2.5,"planId":"individual_pro_v1_monthly"},
			"windowLimits":{"limited":true,
				"fiveHour":{"used":3,"cap":4,"resetAt":1795000000000},
				"weekly":{"used":"20","cap":"20","resetAt":"1795500000000"}},
			"sandboxMinutes":null}`,
		subscriptionsPath: `{"data":{"planId":"individual_pro_v1_monthly","status":"active",
			"currentPeriodStart":"2026-11-01T00:00:00.000Z","currentPeriodEnd":"2026-12-01T00:00:00.000Z"}}`,
		usageSummaryPath: `{"totalCost":67.5,"totalCount":120,"periodBasis":"billing-period"}`,
	}
}

func TestFetchBalanceHappyPath(t *testing.T) {
	us, srv := newUsageServer(t, fullUsageBodies())
	u, err := newTestAdapter(t, srv.URL).FetchBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := us.uri(whoamiPath); got != "/alpha/whoami?limits=1" {
		t.Errorf("whoami = %q", got)
	}
	if got := us.uri(creditsPath); got != "/alpha/billing/credits?orgId=org_42" {
		t.Errorf("credits = %q", got)
	}
	if got := us.uri(subscriptionsPath); got != "/alpha/billing/subscriptions?orgId=org_42" {
		t.Errorf("subscriptions = %q", got)
	}
	if got := us.uri(usageSummaryPath); got != "/alpha/usage/summary?orgId=org_42&since=2026-11-01T00%3A00%3A00.000Z" {
		t.Errorf("summary = %q", got)
	}
	for _, auth := range us.auth {
		if auth != "Bearer "+testKey {
			t.Errorf("Authorization = %q", auth)
		}
	}
	if len(us.auth) != 4 {
		t.Errorf("requests = %d, want 4", len(us.auth))
	}

	// Pro v1 is $80 a month: pool = max(80, 12.5) + 5 + 2.5 = 87.5; left 20.
	c := u.Credit
	if c == nil || c.LimitUSD == nil || c.RemainingUSD == nil {
		t.Fatalf("credit = %+v", c)
	}
	if *c.LimitUSD != 87.5 || *c.RemainingUSD != 20 || c.UsedUSD != 67.5 {
		t.Errorf("credit = limit %v remaining %v used %v", *c.LimitUSD, *c.RemainingUSD, c.UsedUSD)
	}
	if u.Balance != nil || u.Rate != nil {
		t.Errorf("balance/rate set: %+v", u)
	}

	if len(u.Windows) != 2 {
		t.Fatalf("windows = %+v", u.Windows)
	}
	five, week := u.Windows[0], u.Windows[1]
	if five.Type != "five_hour" || five.Status != "allowed" || five.Utilization == nil || *five.Utilization != 0.75 {
		t.Errorf("five_hour = %+v", five)
	}
	if five.ResetsAt == nil || !five.ResetsAt.Equal(time.UnixMilli(fiveHourReset)) {
		t.Errorf("five_hour resets = %v", five.ResetsAt)
	}
	if week.Type != "seven_day" || week.Status != "rejected" || week.Utilization == nil || *week.Utilization != 1 {
		t.Errorf("seven_day = %+v", week)
	}
	if week.ResetsAt == nil || !week.ResetsAt.Equal(time.UnixMilli(weeklyReset)) {
		t.Errorf("seven_day resets = %v", week.ResetsAt)
	}

	p := u.Plan
	if p == nil || p.Name != "Pro" || p.Status != "active" {
		t.Fatalf("plan = %+v", p)
	}
	if p.RenewsAt == nil || !p.RenewsAt.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("renews = %v", p.RenewsAt)
	}
}

func TestFetchBalanceWithoutOrgID(t *testing.T) {
	bodies := fullUsageBodies()
	bodies[whoamiPath] = `{"org":{"id":null},"user":{"userName":"ridho"}}`
	bodies[subscriptionsPath] = `{"data":{"planId":"custom-plan","status":"trialing"}}`
	us, srv := newUsageServer(t, bodies)
	u, err := newTestAdapter(t, srv.URL).FetchBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		creditsPath:       "/alpha/billing/credits",
		subscriptionsPath: "/alpha/billing/subscriptions",
		usageSummaryPath:  "/alpha/usage/summary",
	} {
		if got := us.uri(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	// Not active: pool = spent + remaining = 67.5 + 20.
	if c := u.Credit; c == nil || *c.LimitUSD != 87.5 || *c.RemainingUSD != 20 {
		t.Errorf("credit = %+v", c)
	}
	// An unknown plan id is shown as is.
	if u.Plan == nil || u.Plan.Name != "custom-plan" || u.Plan.Status != "trialing" || u.Plan.RenewsAt != nil {
		t.Errorf("plan = %+v", u.Plan)
	}
}

func TestFetchBalanceSummaryFailureKeepsCredits(t *testing.T) {
	bodies := fullUsageBodies()
	delete(bodies, usageSummaryPath)
	delete(bodies, subscriptionsPath)
	_, srv := newUsageServer(t, bodies)
	u, err := newTestAdapter(t, srv.URL).FetchBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// No subscription: pool = 0 spent + 20 left; plan from the credits.
	if c := u.Credit; c == nil || *c.LimitUSD != 20 || *c.RemainingUSD != 20 || c.UsedUSD != 0 {
		t.Errorf("credit = %+v", c)
	}
	if u.Plan == nil || u.Plan.Name != "Pro" || u.Plan.Status != "" {
		t.Errorf("plan = %+v", u.Plan)
	}
	if len(u.Windows) != 2 {
		t.Errorf("windows = %+v", u.Windows)
	}
}

func TestFetchBalanceKeyRejected(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		us, srv := newUsageServer(t, fullUsageBodies())
		us.statusF = func(string) int { return status }
		_, err := newTestAdapter(t, srv.URL).FetchBalance(context.Background())
		if err == nil {
			t.Fatalf("%d: no error", status)
		}
		want := "Command Code key rejected (" + strconv.Itoa(status) + ")"
		if err.Error() != want {
			t.Errorf("%d: error = %q, want %q", status, err.Error(), want)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("error holds the key: %q", err.Error())
		}
	}
}

func TestFetchBalanceCreditsFailureFails(t *testing.T) {
	bodies := fullUsageBodies()
	delete(bodies, creditsPath)
	_, srv := newUsageServer(t, bodies)
	_, err := newTestAdapter(t, srv.URL).FetchBalance(context.Background())
	if err == nil || err.Error() != "Command Code usage: HTTP 500" {
		t.Errorf("error = %v", err)
	}
}

func TestFetchBalanceWindowLimitsNull(t *testing.T) {
	bodies := fullUsageBodies()
	bodies[creditsPath] = `{"credits":{"monthlyCredits":0,"purchasedCredits":0,"freeCredits":0,"planId":"individual_go"},"windowLimits":null}`
	bodies[subscriptionsPath] = `{"data":{"planId":"individual_go","status":"active","currentPeriodStart":"2026-11-01T00:00:00Z"}}`
	bodies[usageSummaryPath] = `{"totalCost":11}`
	_, srv := newUsageServer(t, bodies)
	u, err := newTestAdapter(t, srv.URL).FetchBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The windows clear: a non-nil empty slice.
	if u.Windows == nil || len(u.Windows) != 0 {
		t.Errorf("windows = %#v", u.Windows)
	}
	// Go is $10 a month, all of it spent.
	if c := u.Credit; c == nil || *c.LimitUSD != 10 || *c.RemainingUSD != 0 || c.UsedUSD != 10 {
		t.Errorf("credit = %+v", c)
	}
	if u.Plan == nil || u.Plan.Name != "Go" {
		t.Errorf("plan = %+v", u.Plan)
	}
}

func TestPlanPrefixLongestMatch(t *testing.T) {
	for id, want := range map[string]string{
		"individual_pro":            "individual-pro",
		"INDIVIDUAL_PRO_V1_MONTHLY": "individual-pro-v1",
		"individual-goat-yearly":    "individual-goat",
		"individual_go_v1":          "individual-go-v1",
		"teams_pro_annual":          "teams-pro",
		"enterprise":                "",
		"":                          "",
	} {
		if got := planPrefix(id); got != want {
			t.Errorf("planPrefix(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestUsageUpdateEmpty(t *testing.T) {
	u := usageUpdate(usageCredits{}, usageSubscription{}, usageSummary{})
	if u.Credit != nil || u.Plan != nil || u.Windows == nil || len(u.Windows) != 0 {
		t.Errorf("update = %+v", u)
	}
}

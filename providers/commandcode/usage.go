package commandcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/router"
)

// The usage endpoints the official CLI reads for `/usage` (spec 79).
const (
	whoamiPath        = "/alpha/whoami"
	creditsPath       = "/alpha/billing/credits"
	subscriptionsPath = "/alpha/billing/subscriptions"
	usageSummaryPath  = "/alpha/usage/summary"
)

// planCredits are the monthly USD credits of each plan, keyed by the plan id
// prefix, as the official CLI knows them (REQ-CCU-002).
var planCredits = map[string]float64{
	"individual-go":       10,
	"individual-go-v1":    10,
	"individual-goat":     70,
	"individual-pro":      30,
	"individual-pro-v1":   80,
	"individual-provider": 15,
	"individual-max":      150,
	"individual-ultra":    300,
	"teams-pro":           40,
}

// planNames are the display names of the plans of planCredits.
var planNames = map[string]string{
	"individual-go":       "Go",
	"individual-go-v1":    "Go",
	"individual-goat":     "GOAT",
	"individual-pro":      "Pro",
	"individual-pro-v1":   "Pro",
	"individual-provider": "Provider",
	"individual-max":      "Max",
	"individual-ultra":    "Ultra",
	"teams-pro":           "Teams Pro",
}

// planPrefixes are the keys of planCredits, longest first.
var planPrefixes = func() []string {
	out := make([]string, 0, len(planCredits))
	for k := range planCredits {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}()

// planPrefix returns the known plan prefix of planID ("" when unknown): the
// longest prefix of the id lowercased with "_" read as "-".
func planPrefix(planID string) string {
	id := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(planID)), "_", "-")
	if id == "" {
		return ""
	}
	for _, prefix := range planPrefixes {
		if strings.HasPrefix(id, prefix) {
			return prefix
		}
	}
	return ""
}

// flexNumber is a JSON number that may also come as a numeric string.
type flexNumber struct {
	value float64
	set   bool
}

func (n *flexNumber) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "null" {
		return nil
	}
	if unquoted, err := strconv.Unquote(raw); err == nil {
		raw = strings.TrimSpace(unquoted)
	}
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		// A value the fetch cannot read counts as not reported.
		return nil
	}
	n.value, n.set = v, true
	return nil
}

// flexString is a JSON string that may also come as a number (an org id).
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = flexString(strings.TrimSpace(text))
		return nil
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		*s = flexString(raw)
	}
	return nil
}

// flexTime is a time given as epoch milliseconds (a number or a numeric
// string) or as an RFC 3339 string.
type flexTime struct {
	at  time.Time
	set bool
}

func (t *flexTime) UnmarshalJSON(data []byte) error {
	var n flexNumber
	if err := n.UnmarshalJSON(data); err == nil && n.set {
		if n.value > 0 {
			t.at, t.set = time.UnixMilli(int64(n.value)).UTC(), true
		}
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return nil
	}
	if at, err := time.Parse(time.RFC3339, strings.TrimSpace(text)); err == nil {
		t.at, t.set = at.UTC(), true
	}
	return nil
}

func (t flexTime) ptr() *time.Time {
	if !t.set {
		return nil
	}
	at := t.at
	return &at
}

type usageWhoami struct {
	Org *struct {
		ID flexString `json:"id"`
	} `json:"org"`
}

type usageWindow struct {
	Used    flexNumber `json:"used"`
	Cap     flexNumber `json:"cap"`
	ResetAt flexTime   `json:"resetAt"`
}

type usageCredits struct {
	Credits *struct {
		MonthlyCredits   flexNumber `json:"monthlyCredits"`
		PurchasedCredits flexNumber `json:"purchasedCredits"`
		FreeCredits      flexNumber `json:"freeCredits"`
		PlanID           string     `json:"planId"`
	} `json:"credits"`
	WindowLimits *struct {
		FiveHour *usageWindow `json:"fiveHour"`
		Weekly   *usageWindow `json:"weekly"`
	} `json:"windowLimits"`
}

type usageSubscription struct {
	Data *struct {
		PlanID             string   `json:"planId"`
		Status             string   `json:"status"`
		CurrentPeriodStart string   `json:"currentPeriodStart"`
		CurrentPeriodEnd   flexTime `json:"currentPeriodEnd"`
	} `json:"data"`
}

type usageSummary struct {
	TotalCost flexNumber `json:"totalCost"`
}

// FetchBalance reads the Command Code plan usage the way the official CLI's
// `/usage` does (REQ-CCU-001): whoami for the org, then the credits and the
// subscription, then the spend summary of the billing period. A failed
// whoami or credits read fails the fetch; a failed subscription or summary
// read leaves its part out.
func (a *Adapter) FetchBalance(ctx context.Context) (router.LimitUpdate, error) {
	var who usageWhoami
	if err := a.getUsage(ctx, whoamiPath, url.Values{"limits": {"1"}}, &who); err != nil {
		return router.LimitUpdate{}, err
	}
	org := url.Values{}
	if who.Org != nil && who.Org.ID != "" {
		org.Set("orgId", string(who.Org.ID))
	}

	var (
		credits    usageCredits
		sub        usageSubscription
		creditsErr error
		subErr     error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		subErr = a.getUsage(ctx, subscriptionsPath, org, &sub)
	}()
	creditsErr = a.getUsage(ctx, creditsPath, org, &credits)
	<-done
	if creditsErr != nil {
		return router.LimitUpdate{}, creditsErr
	}
	if subErr != nil {
		sub = usageSubscription{}
	}

	summaryQuery := url.Values{}
	for k, v := range org {
		summaryQuery[k] = v
	}
	if sub.Data != nil && strings.TrimSpace(sub.Data.CurrentPeriodStart) != "" {
		summaryQuery.Set("since", strings.TrimSpace(sub.Data.CurrentPeriodStart))
	}
	var summary usageSummary
	if err := a.getUsage(ctx, usageSummaryPath, summaryQuery, &summary); err != nil {
		summary = usageSummary{}
	}

	return usageUpdate(credits, sub, summary), nil
}

// usageUpdate maps the usage answers to a limit update (REQ-CCU-002..004),
// with the CLI's math: the pool is the plan's monthly credits (an active
// plan) or the spend plus what is left.
func usageUpdate(credits usageCredits, sub usageSubscription, summary usageSummary) router.LimitUpdate {
	var monthly, purchased, free float64
	creditPlanID := ""
	if c := credits.Credits; c != nil {
		monthly = max(0, c.MonthlyCredits.value)
		purchased = max(0, c.PurchasedCredits.value)
		free = max(0, c.FreeCredits.value)
		creditPlanID = c.PlanID
	}
	remaining := monthly + purchased + free
	spent := max(0, summary.TotalCost.value)

	planID, status := "", ""
	var renewsAt *time.Time
	if d := sub.Data; d != nil {
		planID = strings.TrimSpace(d.PlanID)
		status = strings.ToLower(strings.TrimSpace(d.Status))
		renewsAt = d.CurrentPeriodEnd.ptr()
	}
	prefix := planPrefix(planID)

	pool := spent + remaining
	if status == "active" && prefix != "" {
		pool = max(planCredits[prefix], monthly) + purchased + free
	}

	out := router.LimitUpdate{Windows: []router.Window{}}
	if pool > 0 {
		limit, left := pool, remaining
		out.Credit = &router.Credit{LimitUSD: &limit, UsedUSD: max(0, pool-remaining), RemainingUSD: &left}
	}
	if wl := credits.WindowLimits; wl != nil {
		if w, ok := usageWindowOf("five_hour", wl.FiveHour); ok {
			out.Windows = append(out.Windows, w)
		}
		if w, ok := usageWindowOf("seven_day", wl.Weekly); ok {
			out.Windows = append(out.Windows, w)
		}
	}

	if planID == "" {
		planID = strings.TrimSpace(creditPlanID)
		prefix = planPrefix(planID)
	}
	if planID != "" {
		name := planID
		if prefix != "" {
			name = planNames[prefix]
		}
		out.Plan = &router.Plan{Name: name, Status: status, RenewsAt: renewsAt}
	}
	return out
}

// usageWindowOf maps one Command Code window; a window without a cap is
// left out.
func usageWindowOf(kind string, w *usageWindow) (router.Window, bool) {
	if w == nil || !w.Cap.set || w.Cap.value <= 0 {
		return router.Window{}, false
	}
	used := max(0, w.Used.value)
	fraction := used / w.Cap.value
	status := "allowed"
	if used >= w.Cap.value {
		status = "rejected"
	}
	return router.Window{Type: kind, Status: status, Utilization: &fraction, ResetsAt: w.ResetAt.ptr()}, true
}

// usageError is a usage fetch error the Limits card shows as is.
type usageError string

func (e usageError) Error() string { return string(e) }

// getUsage GETs one usage endpoint and decodes its JSON answer into out.
// Errors are short and never hold the key.
func (a *Adapter) getUsage(ctx context.Context, path string, query url.Values, out any) error {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req, key, err := a.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return usageError("Command Code usage: " + redact(err.Error(), key))
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return usageError(fmt.Sprintf("Command Code key rejected (%d)", resp.StatusCode))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return usageError(fmt.Sprintf("Command Code usage: HTTP %d", resp.StatusCode))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return usageError("Command Code usage: invalid response from " + strings.SplitN(path, "?", 2)[0])
	}
	return nil
}

var _ router.BalanceFetcher = (*Adapter)(nil)

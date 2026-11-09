package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/routerdb"
)

func costPtr(v float64) *float64 { return &v }

func routerCall(at time.Time, provider, model, source, outcome string, in, out, cached int64, cost *float64, ttft int64) routerdb.Call {
	return routerdb.Call{
		At: at, Source: source, Agent: "lana", SessionID: "s1", Requested: provider + "/" + model,
		Provider: provider, Model: model, Attempt: 1, Outcome: outcome,
		InputTokens: in, OutputTokens: out, CachedTokens: cached, CostUSD: cost, TTFTMs: ttft, DurationMs: ttft * 2,
	}
}

func TestRouterMigrationTables(t *testing.T) {
	s, _ := openTest(t)
	if n := countRows(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('router_calls', 'router_keys', 'router_kv')"); n != 3 {
		t.Fatalf("router tables=%d, want 3", n)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('router_calls_at', 'router_calls_provider_at')"); n != 2 {
		t.Fatalf("router indexes=%d, want 2", n)
	}
}

func TestRouterCallsInsertAndList(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	var calls []routerdb.Call
	for i := range 7 {
		provider, outcome, source := "deepseek", "ok", "turn"
		if i%2 == 1 {
			provider = "openai"
		}
		if i%3 == 2 {
			outcome = "rate_limit"
		}
		if i == 6 {
			source = "proxy"
		}
		c := routerCall(base.Add(time.Duration(i)*time.Minute), provider, "m", source, outcome, 10, 5, 2, costPtr(0.5), 100)
		calls = append(calls, c)
	}
	calls[0].CostUSD = nil
	calls[0].Error = "boom"
	calls[0].TurnID = "t1"
	if err := s.InsertRouterCalls(ctx, calls[:4]); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertRouterCalls(ctx, calls[4:]); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertRouterCalls(ctx, nil); err != nil {
		t.Fatal(err)
	}

	// Pagination: newest first, 3 per page.
	var ids []int64
	before := int64(0)
	for range 5 {
		page, err := s.ListRouterCalls(ctx, routerdb.CallFilter{Limit: 3, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range page.Calls {
			ids = append(ids, c.ID)
		}
		if page.NextBefore == 0 {
			break
		}
		before = page.NextBefore
	}
	if len(ids) != 7 {
		t.Fatalf("ids=%v, want 7", ids)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] >= ids[i-1] {
			t.Fatalf("not newest first: %v", ids)
		}
	}

	// The oldest row round-trips every column.
	page, err := s.ListRouterCalls(ctx, routerdb.CallFilter{Limit: 1, Before: ids[5]})
	if err != nil {
		t.Fatal(err)
	}
	got := page.Calls[0]
	if page.NextBefore != 0 || got.ID != ids[6] || !got.At.Equal(base) || got.CostUSD != nil || got.Error != "boom" ||
		got.TurnID != "t1" || got.InputTokens != 10 || got.OutputTokens != 5 || got.CachedTokens != 2 ||
		got.TTFTMs != 100 || got.DurationMs != 200 || got.Requested != "deepseek/m" || got.Agent != "lana" || got.SessionID != "s1" {
		t.Fatalf("round trip: %+v next=%d", got, page.NextBefore)
	}
	if got.At.Location() != time.UTC {
		t.Fatalf("at not UTC: %v", got.At)
	}

	cases := []struct {
		name   string
		filter routerdb.CallFilter
		want   int
	}{
		{"provider", routerdb.CallFilter{Provider: "openai"}, 3},
		{"ok", routerdb.CallFilter{Outcome: "ok"}, 5},
		{"error", routerdb.CallFilter{Outcome: "error"}, 2},
		{"source", routerdb.CallFilter{Source: "proxy"}, 1},
		{"combined", routerdb.CallFilter{Provider: "deepseek", Outcome: "error"}, 1},
	}
	for _, tc := range cases {
		page, err := s.ListRouterCalls(ctx, tc.filter)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Calls) != tc.want {
			t.Fatalf("%s: %d calls, want %d", tc.name, len(page.Calls), tc.want)
		}
	}
	if _, err := s.ListRouterCalls(ctx, routerdb.CallFilter{Outcome: "bad"}); !errors.Is(err, routerdb.ErrInvalidFilter) {
		t.Fatalf("bad outcome err=%v", err)
	}
}

func TestRouterCallsLimitCap(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	calls := make([]routerdb.Call, 250)
	now := time.Now().UTC()
	for i := range calls {
		calls[i] = routerCall(now, "p", "m", "turn", "ok", 1, 1, 0, nil, 1)
	}
	if err := s.InsertRouterCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListRouterCalls(ctx, routerdb.CallFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != routerdb.MaxCallsLimit || page.NextBefore == 0 {
		t.Fatalf("calls=%d next=%d", len(page.Calls), page.NextBefore)
	}
	page, err = s.ListRouterCalls(ctx, routerdb.CallFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != routerdb.DefaultCallsLimit {
		t.Fatalf("default page=%d", len(page.Calls))
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// AC-RTR-015: totals equal the sum of the records in range and each provider
// row matches its own records.
func TestRouterUsageAggregation(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Add(-7 * 24 * time.Hour)
	inRange := []routerdb.Call{
		routerCall(now.Add(-1*time.Hour), "deepseek", "deepseek-flash", "turn", "ok", 1000, 200, 400, costPtr(0.01), 100),
		routerCall(now.Add(-2*time.Hour), "deepseek", "deepseek-flash", "turn", "ok", 500, 100, 0, costPtr(0.02), 300),
		routerCall(now.Add(-3*time.Hour), "deepseek", "deepseek-pro", "compaction", "rate_limit", 0, 0, 0, costPtr(0), 0),
		routerCall(now.Add(-26*time.Hour), "openai", "gpt-5", "turn", "ok", 300, 30, 10, costPtr(0.5), 200),
		routerCall(now.Add(-50*time.Hour), "openai", "gpt-5", "proxy", "ok", 100, 10, 0, nil, 400),
	}
	outOfRange := []routerdb.Call{
		routerCall(now.Add(-8*24*time.Hour), "deepseek", "deepseek-flash", "turn", "ok", 9999, 9999, 9999, costPtr(99), 9999),
	}
	if err := s.InsertRouterCalls(ctx, append(append([]routerdb.Call{}, inRange...), outOfRange...)); err != nil {
		t.Fatal(err)
	}
	u, err := s.RouterUsage(ctx, since, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sum := func(calls []routerdb.Call) routerdb.Stats {
		var st routerdb.Stats
		for _, c := range calls {
			st.Calls++
			if c.Outcome != "ok" {
				st.Errors++
			}
			st.InputTokens += c.InputTokens
			st.OutputTokens += c.OutputTokens
			st.CachedTokens += c.CachedTokens
			if c.CostUSD != nil {
				st.CostUSD += *c.CostUSD
			}
		}
		return st
	}
	check := func(name string, got, want routerdb.Stats) {
		t.Helper()
		if got.Calls != want.Calls || got.Errors != want.Errors || got.InputTokens != want.InputTokens ||
			got.OutputTokens != want.OutputTokens || got.CachedTokens != want.CachedTokens || !near(got.CostUSD, want.CostUSD) {
			t.Fatalf("%s: got %+v, want %+v", name, got, want)
		}
	}
	check("totals", u.Totals, sum(inRange))
	if !u.Totals.CostPartial {
		t.Fatal("totals: cost_partial should be true (an ok openai row has no price)")
	}
	// ok TTFTs: 100, 300, 200, 400 → sorted 100 200 300 400; p50 = 200, p95 = 400.
	if u.Totals.TTFTP50Ms != 200 || u.Totals.TTFTP95Ms != 400 {
		t.Fatalf("totals ttft p50=%d p95=%d", u.Totals.TTFTP50Ms, u.Totals.TTFTP95Ms)
	}

	if len(u.ByProvider) != 2 || u.ByProvider[0].Provider != "deepseek" || u.ByProvider[1].Provider != "openai" {
		t.Fatalf("by_provider=%+v", u.ByProvider)
	}
	check("deepseek", u.ByProvider[0].Stats, sum(inRange[:3]))
	check("openai", u.ByProvider[1].Stats, sum(inRange[3:]))
	if u.ByProvider[0].CostPartial {
		t.Fatal("deepseek: an errored row with no cost must not make cost partial")
	}
	if !u.ByProvider[1].CostPartial {
		t.Fatal("openai: cost_partial should be true")
	}
	if u.ByProvider[0].TTFTP50Ms != 100 || u.ByProvider[0].TTFTP95Ms != 300 {
		t.Fatalf("deepseek ttft p50=%d p95=%d", u.ByProvider[0].TTFTP50Ms, u.ByProvider[0].TTFTP95Ms)
	}

	if len(u.ByModel) != 3 || u.ByModel[0].Address != "deepseek/deepseek-flash" || u.ByModel[0].Calls != 2 {
		t.Fatalf("by_model=%+v", u.ByModel)
	}
	check("deepseek-flash", u.ByModel[0].Stats, sum(inRange[:2]))
	if len(u.BySource) != 3 || u.BySource[0].Source != "turn" || u.BySource[0].Calls != 3 {
		t.Fatalf("by_source=%+v", u.BySource)
	}

	// Daily series: UTC-aligned, every day from since to now, sums to totals.
	if len(u.Series) < 8 {
		t.Fatalf("series has %d buckets, want at least 8", len(u.Series))
	}
	var series routerdb.Stats
	for i, b := range u.Series {
		if b.T.Location() != time.UTC || !b.T.Equal(b.T.Truncate(24*time.Hour)) || b.T.Hour() != 0 {
			t.Fatalf("bucket %d not UTC-midnight aligned: %v", i, b.T)
		}
		if i > 0 && b.T.Sub(u.Series[i-1].T) != 24*time.Hour {
			t.Fatalf("bucket %d gap: %v", i, b.T)
		}
		series.Calls += b.Calls
		series.Errors += b.Errors
		series.InputTokens += b.InputTokens
		series.OutputTokens += b.OutputTokens
		series.CachedTokens += b.CachedTokens
		series.CostUSD += b.CostUSD
	}
	check("series", series, sum(inRange))

	// Hourly series over 24h only sees the last day.
	u, err = s.RouterUsage(ctx, now.Add(-24*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	check("24h", u.Totals, sum(inRange[:3]))
	if n := len(u.Series); n != 25 {
		t.Fatalf("hourly buckets=%d, want 25", n)
	}
	for _, b := range u.Series {
		if !b.T.Equal(b.T.Truncate(time.Hour)) {
			t.Fatalf("hour bucket not aligned: %v", b.T)
		}
	}
}

func TestRouterUsageEmpty(t *testing.T) {
	s, _ := openTest(t)
	u, err := s.RouterUsage(context.Background(), time.Now().Add(-24*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u.Totals.Calls != 0 || u.Totals.CostPartial || u.Totals.TTFTP50Ms != 0 || len(u.ByProvider) != 0 || u.ByProvider == nil {
		t.Fatalf("empty usage: %+v", u)
	}
}

func TestRouterPrune(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC()
	calls := []routerdb.Call{
		routerCall(now.Add(-31*24*time.Hour), "p", "m", "turn", "ok", 1, 1, 0, nil, 1),
		routerCall(now.Add(-30*24*time.Hour-time.Minute), "p", "m", "turn", "ok", 1, 1, 0, nil, 1),
		routerCall(now.Add(-29*24*time.Hour), "p", "m", "turn", "ok", 1, 1, 0, nil, 1),
		routerCall(now, "p", "m", "turn", "ok", 1, 1, 0, nil, 1),
	}
	if err := s.InsertRouterCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneRouterCalls(ctx, now.Add(-routerdb.Retention))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned=%d, want 2", n)
	}
	if left := countRows(t, s, "SELECT COUNT(*) FROM router_calls"); left != 2 {
		t.Fatalf("left=%d, want 2", left)
	}
}

// AC-RTR-022 (store side): a revoked key no longer looks up.
func TestRouterKeysLifecycle(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	created := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	a := routerdb.Key{ID: "k_a", Name: "cursor", Prefix: "lrk_ab12", Hash: "hash-a", CreatedAt: created}
	b := routerdb.Key{ID: "k_b", Name: "cli", Prefix: "lrk_cd34", Hash: "hash-b", CreatedAt: created.Add(time.Minute)}
	for _, k := range []routerdb.Key{a, b} {
		if err := s.CreateRouterKey(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateRouterKey(ctx, routerdb.Key{ID: "k_c", Name: "dup", Prefix: "x", Hash: "hash-a"}); err == nil {
		t.Fatal("duplicate hash accepted")
	}
	keys, err := s.ListRouterKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != "k_a" || keys[1].ID != "k_b" || !keys[0].CreatedAt.Equal(created) || keys[0].LastUsedAt != nil {
		t.Fatalf("keys=%+v", keys)
	}

	got, ok, err := s.LookupRouterKeyByHash(ctx, "hash-a")
	if err != nil || !ok || got.ID != "k_a" || got.Name != "cursor" || got.Prefix != "lrk_ab12" {
		t.Fatalf("lookup a: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.LookupRouterKeyByHash(ctx, "nope"); err != nil || ok {
		t.Fatalf("unknown hash: ok=%v err=%v", ok, err)
	}

	used := created.Add(time.Hour)
	if err := s.TouchRouterKey(ctx, "k_a", used); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchRouterKey(ctx, "missing", used); !errors.Is(err, routerdb.ErrKeyNotFound) {
		t.Fatalf("touch missing err=%v", err)
	}
	got, _, _ = s.LookupRouterKeyByHash(ctx, "hash-a")
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Fatalf("last_used_at=%v", got.LastUsedAt)
	}

	if err := s.RevokeRouterKey(ctx, "k_a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LookupRouterKeyByHash(ctx, "hash-a"); err != nil || ok {
		t.Fatalf("revoked key still looks up: ok=%v err=%v", ok, err)
	}
	keys, err = s.ListRouterKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].ID != "k_b" {
		t.Fatalf("keys after revoke=%+v", keys)
	}
	for _, id := range []string{"k_a", "missing"} {
		err := s.RevokeRouterKey(ctx, id)
		if !errors.Is(err, routerdb.ErrKeyNotFound) || !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("revoke %s err=%v", id, err)
		}
	}
}

func TestRouterKV(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, ok, err := s.GetRouterKV(ctx, "limits"); err != nil || ok {
		t.Fatalf("unset: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{`[{"provider":"a"}]`, `[]`} {
		if err := s.PutRouterKV(ctx, "limits", v); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.GetRouterKV(ctx, "limits")
		if err != nil || !ok || got != v {
			t.Fatalf("get=%q ok=%v err=%v, want %q", got, ok, err, v)
		}
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM router_kv WHERE updated_at > 0"); n != 1 {
		t.Fatalf("kv rows=%d", n)
	}
}

func TestRouterStoreClosed(t *testing.T) {
	s, _ := openTest(t)
	_ = s.Close()
	ctx := context.Background()
	if err := s.InsertRouterCalls(ctx, []routerdb.Call{{}}); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("insert err=%v", err)
	}
	if _, err := s.ListRouterCalls(ctx, routerdb.CallFilter{}); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("list err=%v", err)
	}
	if _, _, err := s.LookupRouterKeyByHash(ctx, "h"); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("lookup err=%v", err)
	}
}

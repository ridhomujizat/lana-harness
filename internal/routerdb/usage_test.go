package routerdb

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	cases := []struct {
		in   []int64
		q    float64
		want int64
	}{
		{nil, 0.5, 0},
		{[]int64{7}, 0.5, 7},
		{[]int64{7}, 0.95, 7},
		{[]int64{1, 2, 3, 4}, 0.5, 2},
		{[]int64{1, 2, 3, 4}, 0.95, 4},
		{[]int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, 0.95, 19},
	}
	for _, tc := range cases {
		if got := Percentile(tc.in, tc.q); got != tc.want {
			t.Fatalf("Percentile(%v, %v)=%d, want %d", tc.in, tc.q, got, tc.want)
		}
	}
}

func TestAggregateSeriesBounds(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 30, 0, 0, time.UTC)
	since := now.Add(-3 * time.Hour)
	calls := []Call{
		{At: since.Add(-time.Minute), Provider: "p", Model: "m", Outcome: OutcomeOK, InputTokens: 100},
		{At: since, Provider: "p", Model: "m", Outcome: OutcomeOK, InputTokens: 1},
		{At: now.Add(48 * time.Hour), Provider: "p", Model: "m", Outcome: "server", InputTokens: 2},
	}
	u := Aggregate(calls, since, now, time.Hour)
	if u.Totals.Calls != 2 || u.Totals.InputTokens != 3 || u.Totals.Errors != 1 {
		t.Fatalf("totals=%+v", u.Totals)
	}
	if len(u.Series) != 4 || !u.Series[0].T.Equal(time.Date(2026, 11, 8, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("series=%+v", u.Series)
	}
	if u.Series[0].InputTokens != 1 || u.Series[3].InputTokens != 2 || u.Series[3].Errors != 1 {
		t.Fatalf("series=%+v", u.Series)
	}
}

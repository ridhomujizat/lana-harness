package routerdb

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// statsAcc accumulates Stats and the TTFT samples of ok records.
type statsAcc struct {
	Stats
	ttft []int64
}

func (a *statsAcc) add(c Call) {
	a.Calls++
	a.InputTokens += c.InputTokens
	a.OutputTokens += c.OutputTokens
	a.CachedTokens += c.CachedTokens
	if c.CostUSD != nil {
		a.CostUSD += *c.CostUSD
	}
	if c.Outcome != OutcomeOK {
		a.Errors++
		return
	}
	if c.CostUSD == nil {
		a.CostPartial = true
	}
	a.ttft = append(a.ttft, c.TTFTMs)
}

func (a *statsAcc) result() Stats {
	s := a.Stats
	if len(a.ttft) > 0 {
		slices.Sort(a.ttft)
		s.TTFTP50Ms = Percentile(a.ttft, 0.50)
		s.TTFTP95Ms = Percentile(a.ttft, 0.95)
	}
	return s
}

// Percentile returns the nearest-rank q-quantile (0 < q <= 1) of sorted, or
// 0 when sorted is empty.
func Percentile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(q*float64(len(sorted)))) - 1
	rank = max(0, min(rank, len(sorted)-1))
	return sorted[rank]
}

// Aggregate builds the usage overview of calls with since <= at. The series
// has one bucket per bucket duration (UTC-aligned) from since through now,
// including empty ones; a zero bucket means one hour. Records after now count
// in the last bucket.
func Aggregate(calls []Call, since, now time.Time, bucket time.Duration) Usage {
	if bucket <= 0 {
		bucket = time.Hour
	}
	since, now = since.UTC(), now.UTC()
	u := Usage{Since: since, Bucket: bucket}
	var total statsAcc
	providers := map[string]*statsAcc{}
	modelsBy := map[[2]string]*statsAcc{}
	sources := map[string]*statsAcc{}
	first := since.Truncate(bucket)
	last := now.Truncate(bucket)
	n := max(int(last.Sub(first)/bucket)+1, 1)
	u.Series = make([]Bucket, n)
	for i := range u.Series {
		u.Series[i].T = first.Add(time.Duration(i) * bucket)
	}
	acc := func(m map[string]*statsAcc, key string) *statsAcc {
		a := m[key]
		if a == nil {
			a = &statsAcc{}
			m[key] = a
		}
		return a
	}
	for _, c := range calls {
		if c.At.Before(since) {
			continue
		}
		total.add(c)
		acc(providers, c.Provider).add(c)
		acc(sources, c.Source).add(c)
		mk := [2]string{c.Provider, c.Model}
		if modelsBy[mk] == nil {
			modelsBy[mk] = &statsAcc{}
		}
		modelsBy[mk].add(c)
		// A record after now (clock skew) counts in the last bucket, so
		// the series always sums to the totals.
		i := min(int(c.At.UTC().Truncate(bucket).Sub(first)/bucket), len(u.Series)-1)
		b := &u.Series[i]
		b.Calls++
		if c.Outcome != OutcomeOK {
			b.Errors++
		}
		b.InputTokens += c.InputTokens
		b.OutputTokens += c.OutputTokens
		b.CachedTokens += c.CachedTokens
		if c.CostUSD != nil {
			b.CostUSD += *c.CostUSD
		}
	}
	u.Totals = total.result()
	u.ByProvider = make([]ProviderStats, 0, len(providers))
	for name, a := range providers {
		u.ByProvider = append(u.ByProvider, ProviderStats{Provider: name, Stats: a.result()})
	}
	slices.SortFunc(u.ByProvider, func(a, b ProviderStats) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Provider, b.Provider))
	})
	u.ByModel = make([]ModelStats, 0, len(modelsBy))
	for k, a := range modelsBy {
		u.ByModel = append(u.ByModel, ModelStats{Address: k[0] + "/" + k[1], Provider: k[0], Model: k[1], Stats: a.result()})
	}
	slices.SortFunc(u.ByModel, func(a, b ModelStats) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Address, b.Address))
	})
	u.BySource = make([]SourceStats, 0, len(sources))
	for name, a := range sources {
		u.BySource = append(u.BySource, SourceStats{Source: name, Stats: a.result()})
	}
	slices.SortFunc(u.BySource, func(a, b SourceStats) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Source, b.Source))
	})
	return u
}

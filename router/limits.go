package router

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	// balanceRefreshEvery is the shortest gap between two balance fetches of
	// one provider (REQ-RTR-012).
	balanceRefreshEvery = 5 * time.Minute
	// limitSaveDebounce delays persisting limit snapshots so a burst of
	// header updates is written once.
	limitSaveDebounce = 10 * time.Second
)

// RateLimit is one limit a provider reported in its rate-limit headers.
type RateLimit struct {
	Limit     int64      `json:"limit"`
	Remaining int64      `json:"remaining"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

// RateLimits are the rate limits of a key. A nil field was not reported.
type RateLimits struct {
	Requests     *RateLimit `json:"requests"`
	Tokens       *RateLimit `json:"tokens"`
	InputTokens  *RateLimit `json:"input_tokens"`
	OutputTokens *RateLimit `json:"output_tokens"`
}

// Credit is a credit limit such as OpenRouter's key limit. LimitUSD and
// RemainingUSD are nil for a key without a limit.
type Credit struct {
	LimitUSD     *float64 `json:"limit_usd"`
	UsedUSD      float64  `json:"used_usd"`
	RemainingUSD *float64 `json:"remaining_usd"`
}

// Balance is an account balance such as DeepSeek's.
type Balance struct {
	Currency string  `json:"currency"`
	Total    float64 `json:"total"`
	Granted  float64 `json:"granted"`
	ToppedUp float64 `json:"topped_up"`
}

// Window is a usage window of a plan, such as the Claude Agent five-hour
// window (spec 38).
type Window struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	// Utilization is the used fraction, 0..1 (0.42 = 42 %). Nil means the
	// runtime did not report it (JSON null).
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

// LimitSnapshot is the latest limits one provider reported (API-RTR-013).
type LimitSnapshot struct {
	Provider  string      `json:"provider"`
	Kind      string      `json:"kind"`
	UpdatedAt time.Time   `json:"updated_at"`
	Error     string      `json:"error"`
	Rate      *RateLimits `json:"rate"`
	Credit    *Credit     `json:"credit"`
	Balance   *Balance    `json:"balance"`
	Windows   []Window    `json:"windows"`
}

// LimitUpdate is a partial snapshot merged into the stored one: a nil field
// leaves the stored value, a set field replaces it. Rate merges per limit.
type LimitUpdate struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind,omitempty"`
	// At is the time of the values; zero means now.
	At     time.Time   `json:"at,omitempty"`
	Rate   *RateLimits `json:"rate,omitempty"`
	Credit *Credit     `json:"credit,omitempty"`
	// Balance replaces the stored balance.
	Balance *Balance `json:"balance,omitempty"`
	// Windows replaces the stored windows when non-nil.
	Windows []Window `json:"windows,omitempty"`
	// Error sets the error when non-nil; a pointer to "" clears it.
	Error *string `json:"error,omitempty"`
}

// LimitStore keeps limit snapshots across restarts.
type LimitStore interface {
	Load() []LimitSnapshot
	Save([]LimitSnapshot)
}

type limitState struct {
	mu        sync.Mutex
	store     LimitStore
	snaps     map[string]LimitSnapshot
	lastFetch map[string]time.Time
	dirty     bool
	timer     *time.Timer
	closed    bool
	debounce  time.Duration
}

func (l *limitState) init(store LimitStore) {
	l.store = store
	l.snaps = map[string]LimitSnapshot{}
	l.lastFetch = map[string]time.Time{}
	l.debounce = limitSaveDebounce
	if store == nil {
		return
	}
	for _, snap := range store.Load() {
		if snap.Provider != "" {
			l.snaps[snap.Provider] = snap
		}
	}
}

// attach sets the store after init and loads its snapshots for providers
// that have not reported since boot.
func (l *limitState) attach(store LimitStore) {
	loaded := store.Load()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = store
	reported := len(l.snaps) > 0
	for _, snap := range loaded {
		if _, ok := l.snaps[snap.Provider]; !ok && snap.Provider != "" {
			l.snaps[snap.Provider] = snap
		}
	}
	// Values reported before the store was attached are saved too.
	if reported {
		l.scheduleSaveLocked()
	}
}

// UpdateLimits merges u into the provider's snapshot. Adapters call it
// through Deps.Limits; external sources such as the Claude Agent runtime
// call it directly.
func (r *Router) UpdateLimits(u LimitUpdate) {
	if u.Provider == "" {
		return
	}
	at := u.At
	if at.IsZero() {
		at = r.now()
	}
	l := &r.limits
	l.mu.Lock()
	defer l.mu.Unlock()
	snap := l.snaps[u.Provider]
	snap.Provider = u.Provider
	if u.Kind != "" {
		snap.Kind = u.Kind
	}
	snap.UpdatedAt = at.UTC()
	if u.Rate != nil {
		// Copy on write: snapshots handed out share the old pointers.
		rate := RateLimits{}
		if snap.Rate != nil {
			rate = *snap.Rate
		}
		if u.Rate.Requests != nil {
			rate.Requests = u.Rate.Requests
		}
		if u.Rate.Tokens != nil {
			rate.Tokens = u.Rate.Tokens
		}
		if u.Rate.InputTokens != nil {
			rate.InputTokens = u.Rate.InputTokens
		}
		if u.Rate.OutputTokens != nil {
			rate.OutputTokens = u.Rate.OutputTokens
		}
		snap.Rate = &rate
	}
	if u.Credit != nil {
		snap.Credit = u.Credit
	}
	if u.Balance != nil {
		snap.Balance = u.Balance
	}
	if u.Windows != nil {
		snap.Windows = append([]Window(nil), u.Windows...)
	}
	if u.Error != nil {
		snap.Error = *u.Error
	}
	l.snaps[u.Provider] = snap
	l.scheduleSaveLocked()
}

func (l *limitState) scheduleSaveLocked() {
	if l.store == nil || l.closed {
		return
	}
	l.dirty = true
	if l.timer == nil {
		l.timer = time.AfterFunc(l.debounce, l.flush)
	}
}

func (l *limitState) flush() {
	l.mu.Lock()
	l.timer = nil
	if !l.dirty || l.store == nil {
		l.mu.Unlock()
		return
	}
	l.dirty = false
	out := l.sortedLocked()
	l.mu.Unlock()
	l.store.Save(out)
}

func (l *limitState) close() {
	l.mu.Lock()
	l.closed = true
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.mu.Unlock()
	l.flush()
}

func (l *limitState) sortedLocked() []LimitSnapshot {
	out := make([]LimitSnapshot, 0, len(l.snaps))
	for _, name := range sortedKeys(l.snaps) {
		out = append(out, l.snaps[name])
	}
	return out
}

// Limits returns one snapshot per configured provider, in name order, then
// the snapshots of other sources (such as the Claude Agent runtime). A
// provider that reported nothing has an empty snapshot. With refresh, the
// balances and credits are fetched first, at most once per 5 minutes per
// provider.
func (r *Router) Limits(ctx context.Context, refresh bool) []LimitSnapshot {
	snap := r.snap.Load()
	if refresh {
		r.refreshBalances(ctx, snap)
	}
	l := &r.limits
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LimitSnapshot, 0, len(snap.providers)+len(l.snaps))
	for _, name := range sortedKeys(snap.providers) {
		s, ok := l.snaps[name]
		if !ok {
			s = LimitSnapshot{Provider: name}
		}
		s.Kind = snap.providers[name].kind
		out = append(out, s)
	}
	var extra []string
	for name := range l.snaps {
		if _, ok := snap.providers[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		out = append(out, l.snaps[name])
	}
	return out
}

func (r *Router) refreshBalances(ctx context.Context, snap *snapshot) {
	now := r.now()
	var wg sync.WaitGroup
	for _, name := range sortedKeys(snap.providers) {
		entry := snap.providers[name]
		fetcher, ok := balanceFetcherOf(entry)
		if !ok {
			continue
		}
		r.limits.mu.Lock()
		last, fetched := r.limits.lastFetch[name]
		if fetched && now.Sub(last) < balanceRefreshEvery {
			r.limits.mu.Unlock()
			continue
		}
		r.limits.lastFetch[name] = now
		r.limits.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := fetcher.FetchBalance(ctx)
			if err != nil {
				msg := truncate(Redact(err.Error(), snap.apiKey(r.opts.secrets, name)), maxRecordError)
				r.UpdateLimits(LimitUpdate{Provider: name, Kind: entry.kind, Error: &msg})
				return
			}
			u.Provider = name
			if u.Kind == "" {
				u.Kind = entry.kind
			}
			noError := ""
			u.Error = &noError
			r.UpdateLimits(u)
		}()
	}
	wg.Wait()
}

func balanceFetcherOf(entry *providerEntry) (BalanceFetcher, bool) {
	if f, ok := entry.adapter.(BalanceFetcher); ok {
		return f, true
	}
	f, ok := entry.raw.(BalanceFetcher)
	return f, ok
}

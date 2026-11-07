package lana

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/routerdb"
	"github.com/ridhomujizat/lana-harness/router"
	"github.com/ridhomujizat/lana-harness/store"
)

// fakeCallStore records InsertRouterCalls batches. When gate is non-nil each
// insert waits for a value from it.
type fakeCallStore struct {
	mu      sync.Mutex
	batches [][]routerdb.Call
	gate    chan struct{}
	entered chan struct{}
	pruned  []time.Time
}

func (f *fakeCallStore) InsertRouterCalls(_ context.Context, calls []routerdb.Call) error {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, append([]routerdb.Call(nil), calls...))
	return nil
}

func (f *fakeCallStore) ListRouterCalls(context.Context, routerdb.CallFilter) (routerdb.CallPage, error) {
	return routerdb.CallPage{}, nil
}

func (f *fakeCallStore) RouterUsage(context.Context, time.Time, time.Duration) (routerdb.Usage, error) {
	return routerdb.Usage{}, nil
}

func (f *fakeCallStore) PruneRouterCalls(_ context.Context, olderThan time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruned = append(f.pruned, olderThan)
	return 0, nil
}

func (f *fakeCallStore) snapshot() (batches [][]routerdb.Call, total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.batches {
		total += len(b)
	}
	return append([][]routerdb.Call(nil), f.batches...), total
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testRecord(i int) router.CallRecord {
	cost := 0.25
	return router.CallRecord{
		At: time.Date(2026, 11, 8, 0, 0, i, 0, time.UTC), Source: "turn", Agent: "lana", SessionID: "s", TurnID: "t",
		Requested: "coding", Provider: "deepseek", Model: "deepseek-flash", Attempt: 1, Outcome: "ok",
		InputTokens: 1000, OutputTokens: 200, CachedTokens: 400, CostUSD: &cost, TTFTMs: 90, DurationMs: 300, Error: "",
	}
}

func TestRouterRecorderBatchesBySize(t *testing.T) {
	st := &fakeCallStore{}
	rec := newRouterRecorder(st, routerRecorderOptions{batchSize: 4, flushEvery: time.Hour})
	defer rec.Close()
	for i := range 8 {
		rec.Record(testRecord(i))
	}
	waitFor(t, "two full batches", func() bool { _, n := st.snapshot(); return n == 8 })
	batches, _ := st.snapshot()
	if len(batches) != 2 || len(batches[0]) != 4 || len(batches[1]) != 4 {
		t.Fatalf("batches=%d", len(batches))
	}
	got := batches[0][0]
	want := testRecord(0)
	if !got.At.Equal(want.At) || got.Requested != "coding" || got.Provider != "deepseek" || got.InputTokens != 1000 ||
		got.CachedTokens != 400 || got.CostUSD == nil || *got.CostUSD != 0.25 || got.TTFTMs != 90 || got.TurnID != "t" {
		t.Fatalf("row=%+v", got)
	}
	if batches[1][3].At.Second() != 7 {
		t.Fatalf("order lost: %+v", batches[1][3])
	}
}

func TestRouterRecorderFlushesOnTimer(t *testing.T) {
	st := &fakeCallStore{}
	rec := newRouterRecorder(st, routerRecorderOptions{batchSize: 64, flushEvery: 20 * time.Millisecond})
	defer rec.Close()
	rec.Record(testRecord(1))
	rec.Record(testRecord(2))
	waitFor(t, "timer flush", func() bool { _, n := st.snapshot(); return n == 2 })
}

func TestRouterRecorderCloseFlushes(t *testing.T) {
	st := &fakeCallStore{}
	rec := newRouterRecorder(st, routerRecorderOptions{batchSize: 64, flushEvery: time.Hour})
	for i := range 10 {
		rec.Record(testRecord(i))
	}
	rec.Close()
	if _, n := st.snapshot(); n != 10 {
		t.Fatalf("flushed %d, want 10", n)
	}
	rec.Close() // idempotent
	rec.Record(testRecord(11))
	if rec.Dropped() != 1 {
		t.Fatalf("record after close dropped=%d, want 1", rec.Dropped())
	}
}

// countingHandler counts slog records at warn level.
type countingHandler struct{ warns atomic.Int64 }

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		h.warns.Add(1)
	}
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }

func TestRouterRecorderDropsWhenFull(t *testing.T) {
	st := &fakeCallStore{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	logs := &countingHandler{}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC).UnixNano())
	rec := newRouterRecorder(st, routerRecorderOptions{
		capacity: 4, batchSize: 1, flushEvery: time.Hour, logger: slog.New(logs),
		now: func() time.Time { return time.Unix(0, clock.Load()) },
	})
	// The first record is taken by the writer, which then blocks in Insert.
	rec.Record(testRecord(0))
	<-st.entered
	// Four fill the buffer; the next three are dropped without blocking.
	done := make(chan struct{})
	go func() {
		for i := 1; i <= 7; i++ {
			rec.Record(testRecord(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked on a full buffer")
	}
	if rec.Dropped() != 3 {
		t.Fatalf("dropped=%d, want 3", rec.Dropped())
	}
	if logs.warns.Load() != 1 {
		t.Fatalf("warns=%d, want 1 (rate-limited)", logs.warns.Load())
	}
	clock.Add(int64(time.Minute))
	rec.Record(testRecord(8))
	if rec.Dropped() != 4 || logs.warns.Load() != 2 {
		t.Fatalf("after a minute: dropped=%d warns=%d", rec.Dropped(), logs.warns.Load())
	}
	// Unblock the writer: Close writes the buffered records.
	close(st.gate)
	rec.Close()
	if _, n := st.snapshot(); n != 5 {
		t.Fatalf("stored %d, want 5", n)
	}
}

func openRouterTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "lana.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRouterRecorderOverSQLite(t *testing.T) {
	st := openRouterTestStore(t)
	rec := newRouterRecorder(st, routerRecorderOptions{})
	for i := range 70 {
		rec.Record(testRecord(i))
	}
	rec.Close()
	page, err := st.ListRouterCalls(context.Background(), routerdb.CallFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != 70 || !page.Calls[0].At.Equal(testRecord(69).At) {
		t.Fatalf("stored %d calls", len(page.Calls))
	}
}

func TestRouterLimitStoreRoundTrip(t *testing.T) {
	st := openRouterTestStore(t)
	ls := newRouterLimitStore(st)
	if got := ls.Load(); got != nil {
		t.Fatalf("empty load=%+v", got)
	}
	reset := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	limit := 10.0
	snaps := []router.LimitSnapshot{
		{Provider: "deepseek", Kind: "deepseek", UpdatedAt: reset, Balance: &router.Balance{Currency: "USD", Total: 4.5}},
		{Provider: "openrouter", Kind: "openrouter", UpdatedAt: reset, Error: "timeout",
			Credit: &router.Credit{LimitUSD: &limit, UsedUSD: 2},
			Rate:   &router.RateLimits{Requests: &router.RateLimit{Limit: 60, Remaining: 59, ResetAt: &reset}}},
	}
	ls.Save(snaps)
	got := ls.Load()
	if len(got) != 2 || got[0].Balance == nil || got[0].Balance.Total != 4.5 || got[1].Credit == nil ||
		*got[1].Credit.LimitUSD != 10 || got[1].Rate.Requests.Remaining != 59 || !got[1].Rate.Requests.ResetAt.Equal(reset) ||
		got[1].Error != "timeout" || !got[0].UpdatedAt.Equal(reset) {
		t.Fatalf("round trip=%+v", got)
	}
	ls.Save(nil)
	if got := ls.Load(); len(got) != 0 {
		t.Fatalf("after clear=%+v", got)
	}
	if err := st.PutRouterKV(context.Background(), routerdb.KVLimits, "{bad"); err != nil {
		t.Fatal(err)
	}
	if got := ls.Load(); got != nil {
		t.Fatalf("bad json load=%+v", got)
	}
}

func TestPruneRouterCallsRetention(t *testing.T) {
	st := &fakeCallStore{}
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	if _, err := pruneRouterCalls(context.Background(), st, now); err != nil {
		t.Fatal(err)
	}
	if len(st.pruned) != 1 || !st.pruned[0].Equal(now.Add(-30*24*time.Hour)) {
		t.Fatalf("pruned=%v", st.pruned)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runRouterPrune(ctx, stop, st, 10*time.Millisecond, func() time.Time { return now })
		close(done)
	}()
	waitFor(t, "repeated prune", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return len(st.pruned) >= 3
	})
	cancel()
	<-done
}

func TestProxyKey(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		key, prefix, err := GenerateProxyKey()
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != 47 || !strings.HasPrefix(key, "lrk_") || prefix != key[:8] {
			t.Fatalf("key=%q prefix=%q", key, prefix)
		}
		for _, r := range key[4:] {
			if !strings.ContainsRune(proxyKeyAlphabet, r) {
				t.Fatalf("non-base62 char %q in %q", r, key)
			}
		}
		if seen[key] {
			t.Fatalf("duplicate key %q", key)
		}
		seen[key] = true
	}
	h := HashProxyKey("lrk_abc")
	if len(h) != 64 || h != HashProxyKey("lrk_abc") || h == HashProxyKey("lrk_abd") {
		t.Fatalf("hash=%q", h)
	}
	// sha256("abc") known vector.
	if HashProxyKey("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("hash is not hex SHA-256")
	}
}

// AC-RTR-022 (store side, end to end through the helpers): a generated key
// looks up by its hash until revoked.
func TestProxyKeyRevokedLookupFails(t *testing.T) {
	st := openRouterTestStore(t)
	ctx := context.Background()
	key, prefix, err := GenerateProxyKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRouterKey(ctx, routerdb.Key{ID: "k_1", Name: "cursor", Prefix: prefix, Hash: HashProxyKey(key)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.LookupRouterKeyByHash(ctx, HashProxyKey(key)); err != nil || !ok {
		t.Fatalf("live lookup ok=%v err=%v", ok, err)
	}
	if err := st.RevokeRouterKey(ctx, "k_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.LookupRouterKeyByHash(ctx, HashProxyKey(key)); err != nil || ok {
		t.Fatalf("revoked lookup ok=%v err=%v", ok, err)
	}
	if err := st.RevokeRouterKey(ctx, "k_1"); !errors.Is(err, routerdb.ErrKeyNotFound) {
		t.Fatalf("second revoke err=%v", err)
	}
}

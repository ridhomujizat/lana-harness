package lana

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/routerdb"
	"github.com/ridhomujizat/lana-harness/router"
)

// Router persistence glue (spec 37, design "store/sqlite" and Persistence):
// an async call recorder and a limit store over the session store, the
// 30-day call prune, and proxy-key helpers.

const (
	// routerRecorderCap is the record buffer; Record drops when it is full.
	routerRecorderCap = 1024
	// routerRecorderBatch is the largest batch written in one transaction.
	routerRecorderBatch = 64
	// routerRecorderFlush is the longest a record waits in a partial batch.
	routerRecorderFlush = 500 * time.Millisecond
	// routerDropWarnEvery rate-limits the "records dropped" warning.
	routerDropWarnEvery = time.Minute
	// routerPruneEvery is the interval of the call-record prune.
	routerPruneEvery = time.Hour
	// routerStoreTimeout bounds one store write of the adapters.
	routerStoreTimeout = 10 * time.Second
)

// routerRecorder is the async router.Recorder over a routerdb.CallStore: a
// buffered channel drained by one writer goroutine that inserts batches.
// Record never blocks; it drops and counts when the buffer is full.
type routerRecorder struct {
	store      routerdb.CallStore
	logger     *slog.Logger
	batchSize  int
	flushEvery time.Duration
	now        func() time.Time

	// mu guards ch against a send after Close closed it: Record holds the
	// read lock, Close the write lock.
	mu       sync.RWMutex
	ch       chan router.CallRecord
	closed   bool
	done     chan struct{}
	dropped  atomic.Int64
	lastWarn atomic.Int64
}

var _ router.Recorder = (*routerRecorder)(nil)

// routerRecorderOptions tunes a recorder; zero fields take the defaults.
type routerRecorderOptions struct {
	capacity   int
	batchSize  int
	flushEvery time.Duration
	logger     *slog.Logger
	now        func() time.Time
}

// newRouterRecorder starts the async call recorder over st. Close flushes
// the buffered records and stops the writer.
func newRouterRecorder(st routerdb.CallStore, opts routerRecorderOptions) *routerRecorder {
	if opts.capacity <= 0 {
		opts.capacity = routerRecorderCap
	}
	if opts.batchSize <= 0 {
		opts.batchSize = routerRecorderBatch
	}
	if opts.flushEvery <= 0 {
		opts.flushEvery = routerRecorderFlush
	}
	if opts.logger == nil {
		opts.logger = slog.Default()
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	r := &routerRecorder{
		store: st, logger: opts.logger, batchSize: opts.batchSize, flushEvery: opts.flushEvery, now: opts.now,
		ch: make(chan router.CallRecord, opts.capacity), done: make(chan struct{}),
	}
	go r.run()
	return r
}

// Record queues rec for the writer. It never blocks: a full buffer or a
// closed recorder drops the record and counts it.
func (r *routerRecorder) Record(rec router.CallRecord) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		r.drop()
		return
	}
	select {
	case r.ch <- rec:
		r.mu.RUnlock()
	default:
		r.mu.RUnlock()
		r.drop()
	}
}

// drop counts a dropped record and warns at most once per minute.
func (r *routerRecorder) drop() {
	total := r.dropped.Add(1)
	now := r.now().UnixNano()
	last := r.lastWarn.Load()
	if last != 0 && now-last < int64(routerDropWarnEvery) {
		return
	}
	if r.lastWarn.CompareAndSwap(last, now) {
		r.logger.Warn("router: call records dropped, the recorder is full", "dropped_total", total)
	}
}

// Dropped returns how many records were dropped since the recorder started.
func (r *routerRecorder) Dropped() int64 { return r.dropped.Load() }

// Close stops accepting records, writes the buffered ones, and waits for the
// writer. It is safe to call more than once.
func (r *routerRecorder) Close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.ch)
	}
	r.mu.Unlock()
	<-r.done
}

func (r *routerRecorder) run() {
	defer close(r.done)
	ticker := time.NewTicker(r.flushEvery)
	defer ticker.Stop()
	batch := make([]routerdb.Call, 0, r.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
		err := r.store.InsertRouterCalls(ctx, batch)
		cancel()
		if err != nil {
			r.logger.Warn("router: write call records", "records", len(batch), "error", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case rec, ok := <-r.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, routerCallRow(rec))
			if len(batch) >= r.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// routerCallRow converts a router record to its store row.
func routerCallRow(rec router.CallRecord) routerdb.Call {
	var cost *float64
	if rec.CostUSD != nil {
		v := *rec.CostUSD
		cost = &v
	}
	return routerdb.Call{
		At: rec.At, Source: rec.Source, Agent: rec.Agent, SessionID: rec.SessionID, TurnID: rec.TurnID,
		Requested: rec.Requested, Provider: rec.Provider, Model: rec.Model, Attempt: rec.Attempt, Outcome: rec.Outcome,
		InputTokens: rec.InputTokens, OutputTokens: rec.OutputTokens, CachedTokens: rec.CachedTokens,
		CostUSD: cost, TTFTMs: rec.TTFTMs, DurationMs: rec.DurationMs, Error: rec.Error,
	}
}

// routerLimitStore is the router.LimitStore over router_kv key "limits"
// (JSON). Errors are logged; Load returns nil on any failure.
type routerLimitStore struct {
	store  routerdb.KVStore
	logger *slog.Logger
}

var _ router.LimitStore = routerLimitStore{}

// newRouterLimitStore returns the limit store over st.
func newRouterLimitStore(st routerdb.KVStore) routerLimitStore {
	return routerLimitStore{store: st, logger: slog.Default()}
}

func (s routerLimitStore) Load() []router.LimitSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	value, ok, err := s.store.GetRouterKV(ctx, routerdb.KVLimits)
	if err != nil {
		s.logger.Warn("router: load limits", "error", err)
		return nil
	}
	if !ok || value == "" {
		return nil
	}
	var snaps []router.LimitSnapshot
	if err := json.Unmarshal([]byte(value), &snaps); err != nil {
		s.logger.Warn("router: decode limits", "error", err)
		return nil
	}
	return snaps
}

func (s routerLimitStore) Save(snaps []router.LimitSnapshot) {
	if snaps == nil {
		snaps = []router.LimitSnapshot{}
	}
	data, err := json.Marshal(snaps)
	if err != nil {
		s.logger.Warn("router: encode limits", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	if err := s.store.PutRouterKV(ctx, routerdb.KVLimits, string(data)); err != nil {
		s.logger.Warn("router: save limits", "error", err)
	}
}

// pruneRouterCalls deletes the call records older than the 30-day retention
// (REQ-RTR-009).
func pruneRouterCalls(ctx context.Context, st routerdb.CallStore, now time.Time) (int64, error) {
	return st.PruneRouterCalls(ctx, now.Add(-routerdb.Retention))
}

// runRouterPrune prunes call records now and then every interval until ctx
// or stop ends.
func runRouterPrune(ctx context.Context, stop <-chan struct{}, st routerdb.CallStore, every time.Duration, now func() time.Time) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, time.Minute)
		if n, err := pruneRouterCalls(pctx, st, now()); err != nil {
			if ctx.Err() == nil {
				slog.Warn("router: prune call records", "error", err)
			}
		} else if n > 0 {
			slog.Info("router: pruned call records", "deleted", n)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// attachRouterStores gives the router its call recorder and limit store over
// the session store when the store keeps router records (spec 37). The llm
// module boots before the store module, so they are attached after boot.
func (rt *Runtime) attachRouterStores() {
	rt.router, _ = rt.coreCtx.LLM.(*router.Router)
	if rt.router == nil {
		return
	}
	var rec router.Recorder
	if st, ok := rt.store.(routerdb.CallStore); ok {
		rt.routerRec = newRouterRecorder(st, routerRecorderOptions{})
		rec = rt.routerRec
	}
	var limits router.LimitStore
	if st, ok := rt.store.(routerdb.KVStore); ok {
		limits = newRouterLimitStore(st)
	}
	rt.router.AttachStores(rec, limits)
}

// closeRouterStores flushes the router's limit snapshots and the buffered
// call records. Close calls it after the turns stopped and before the store
// closes; records made later (a draining background pass) are dropped.
func (rt *Runtime) closeRouterStores() {
	if rt.router != nil {
		_ = rt.router.Close()
	}
	if rt.routerRec != nil {
		rt.routerRec.Close()
	}
}

// Router returns the LLM router, or nil when the llm service is not a
// router (spec 37, T-RTR-06).
func (rt *Runtime) Router() *router.Router { return rt.router }

// RouterStore returns the session store's router persistence, or nil when
// the store keeps no router records.
func (rt *Runtime) RouterStore() routerdb.Store {
	st, _ := rt.store.(routerdb.Store)
	return st
}

// StartRouterUpkeep starts the hourly prune of router call records older than
// 30 days (REQ-RTR-009) when the session store keeps router records. It is a
// no-op after the first call and stops with ctx or Close.
func (rt *Runtime) StartRouterUpkeep(ctx context.Context) {
	st, ok := rt.store.(routerdb.CallStore)
	if !ok || rt.closed.Load() {
		return
	}
	if ctx == nil {
		ctx = rt.ctx
	}
	rt.routerUpkeepOnce.Do(func() {
		rt.routerUpkeep.Add(1)
		go func() {
			defer rt.routerUpkeep.Done()
			runRouterPrune(ctx, rt.ctx.Done(), st, routerPruneEvery, time.Now)
		}()
	})
}

// proxyKeyAlphabet is the base62 alphabet of proxy keys.
const proxyKeyAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// proxyKeyLen is the number of random base62 characters after "lrk_".
const proxyKeyLen = 43

// GenerateProxyKey returns a new router proxy key, "lrk_" followed by 43
// base62 characters from crypto/rand, and its display prefix (the first 8
// characters, "lrk_" plus 4). Only HashProxyKey(key) is stored
// (REQ-RTR-016).
func GenerateProxyKey() (key, prefix string, err error) {
	buf := make([]byte, 0, 4+proxyKeyLen)
	buf = append(buf, "lrk_"...)
	limit := big.NewInt(int64(len(proxyKeyAlphabet)))
	for range proxyKeyLen {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", "", err
		}
		buf = append(buf, proxyKeyAlphabet[n.Int64()])
	}
	key = string(buf)
	return key, key[:8], nil
}

// HashProxyKey returns the hex SHA-256 of a proxy key, the form stored in
// router_keys.
func HashProxyKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

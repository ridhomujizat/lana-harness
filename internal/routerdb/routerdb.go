// Package routerdb holds the persistence types of the LLM router (spec 37):
// call records, proxy keys, and router key/value entries. The SQLite store
// implements Store; the root package adapts it to router.Recorder and
// router.LimitStore. Neither store nor web may import router (archtest), so the
// rows live here.
package routerdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// MaxCallsLimit is the largest page of call records (API-RTR-012).
const MaxCallsLimit = 200

// DefaultCallsLimit is the page size used when CallFilter.Limit is zero.
const DefaultCallsLimit = 50

// Retention is how long call records are kept (REQ-RTR-009).
const Retention = 30 * 24 * time.Hour

// OutcomeOK is the outcome of a successful attempt. Any other outcome is an
// error class.
const OutcomeOK = "ok"

// KVLimits is the router_kv key of the persisted limit snapshots.
const KVLimits = "limits"

// ErrKeyNotFound is returned when a proxy key does not exist or is revoked.
var ErrKeyNotFound = fmt.Errorf("router key not found: %w", core.ErrNotFound)

// ErrInvalidFilter is returned for a call filter with an unknown outcome.
var ErrInvalidFilter = errors.New("invalid router call filter")

// Call is one stored attempt of one LLM call (REQ-RTR-009, API-RTR-012).
type Call struct {
	ID           int64     `json:"id"`
	At           time.Time `json:"at"`
	Source       string    `json:"source"`
	Agent        string    `json:"agent"`
	SessionID    string    `json:"session_id"`
	TurnID       string    `json:"turn_id,omitempty"`
	Requested    string    `json:"requested"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	Attempt      int       `json:"attempt"`
	Outcome      string    `json:"outcome"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CachedTokens int64     `json:"cached_tokens"`
	// CostUSD is nil when the price was unknown.
	CostUSD    *float64 `json:"cost_usd"`
	TTFTMs     int64    `json:"ttft_ms"`
	DurationMs int64    `json:"duration_ms"`
	Error      string   `json:"error"`
}

// CallFilter selects a page of call records, newest first.
type CallFilter struct {
	// Limit is the page size: 0 means DefaultCallsLimit, values above
	// MaxCallsLimit are capped.
	Limit int
	// Before, when > 0, returns only records with id < Before.
	Before int64
	// Provider, when set, keeps only records served by that provider.
	Provider string
	// Outcome is "" (all), "ok", or "error" (every outcome other than ok).
	Outcome string
	// Source, when set, keeps only records of that source.
	Source string
}

// CallPage is a page of call records. NextBefore is the Before value of the
// next page, or 0 when there are no older records.
type CallPage struct {
	Calls      []Call `json:"calls"`
	NextBefore int64  `json:"next_before"`
}

// Stats are the aggregate figures of a set of call records (API-RTR-011).
type Stats struct {
	Calls        int64   `json:"calls"`
	Errors       int64   `json:"errors"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	// CostPartial is true when an ok record had no known price.
	CostPartial bool `json:"cost_partial"`
	// TTFTP50Ms and TTFTP95Ms are nearest-rank percentiles of the time to
	// first event over ok records; 0 when there are none.
	TTFTP50Ms int64 `json:"ttft_p50_ms"`
	TTFTP95Ms int64 `json:"ttft_p95_ms"`
}

// Bucket is one point of the usage series.
type Bucket struct {
	T            time.Time `json:"t"`
	Calls        int64     `json:"calls"`
	Errors       int64     `json:"errors"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CachedTokens int64     `json:"cached_tokens"`
	CostUSD      float64   `json:"cost_usd"`
}

// ProviderStats is the by_provider row of the usage overview.
type ProviderStats struct {
	Provider string `json:"provider"`
	Stats
}

// ModelStats is the by_model row of the usage overview. Address is
// provider/model.
type ModelStats struct {
	Address  string `json:"address"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Stats
}

// SourceStats is the by_source row of the usage overview.
type SourceStats struct {
	Source string `json:"source"`
	Stats
}

// Usage is the usage overview of the records since a time (API-RTR-011).
// Group rows are ordered by calls, most first, then by name.
type Usage struct {
	Since      time.Time       `json:"since"`
	Bucket     time.Duration   `json:"-"`
	Totals     Stats           `json:"totals"`
	Series     []Bucket        `json:"series"`
	ByProvider []ProviderStats `json:"by_provider"`
	ByModel    []ModelStats    `json:"by_model"`
	BySource   []SourceStats   `json:"by_source"`
}

// Key is a proxy key (REQ-RTR-016). Only the hash of the key is stored.
type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Hash       string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CallStore stores call records.
type CallStore interface {
	// InsertRouterCalls stores calls in one transaction; IDs are assigned by
	// the store and ignored on input.
	InsertRouterCalls(ctx context.Context, calls []Call) error
	ListRouterCalls(ctx context.Context, filter CallFilter) (CallPage, error)
	// RouterUsage aggregates the records with at >= since. The series has one
	// bucket per bucket duration, aligned in UTC, from since up to now,
	// including empty buckets.
	RouterUsage(ctx context.Context, since time.Time, bucket time.Duration) (Usage, error)
	// PruneRouterCalls deletes records older than olderThan and returns how
	// many it deleted.
	PruneRouterCalls(ctx context.Context, olderThan time.Time) (int64, error)
}

// KeyStore stores proxy keys.
type KeyStore interface {
	CreateRouterKey(ctx context.Context, key Key) error
	// ListRouterKeys returns the keys that are not revoked, oldest first.
	ListRouterKeys(ctx context.Context) ([]Key, error)
	// RevokeRouterKey revokes a key. A missing or already revoked key is
	// ErrKeyNotFound.
	RevokeRouterKey(ctx context.Context, id string) error
	// LookupRouterKeyByHash returns the live key with hash; ok is false for
	// an unknown or revoked key.
	LookupRouterKeyByHash(ctx context.Context, hash string) (Key, bool, error)
	// TouchRouterKey sets the key's last-used time. Callers throttle it.
	TouchRouterKey(ctx context.Context, id string, at time.Time) error
}

// KVStore stores router key/value entries such as the limit snapshots.
type KVStore interface {
	// GetRouterKV returns the value of key; ok is false when it is unset.
	GetRouterKV(ctx context.Context, key string) (string, bool, error)
	PutRouterKV(ctx context.Context, key, value string) error
}

// Store is every router persistence surface.
type Store interface {
	CallStore
	KeyStore
	KVStore
}

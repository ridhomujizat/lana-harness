package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/routerdb"
)

// Store implements routerdb.Store (spec 37, migration 0008).
var _ routerdb.Store = (*Store)(nil)

const routerCallColumns = "id, at, source, agent, session_id, turn_id, requested, provider, model, attempt, outcome, input_tokens, output_tokens, cached_tokens, cost_usd, ttft_ms, duration_ms, error"

const routerKeyColumns = "id, name, prefix, hash, created_at, last_used_at, revoked_at"

// msTime turns a stored Unix-millisecond value into a UTC time.
func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// nullMS turns a nullable Unix-millisecond column into a time pointer.
func nullMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := msTime(v.Int64)
	return &t
}

func scanRouterCall(row rowScanner) (routerdb.Call, error) {
	var c routerdb.Call
	var at int64
	var cost sql.NullFloat64
	if err := row.Scan(&c.ID, &at, &c.Source, &c.Agent, &c.SessionID, &c.TurnID, &c.Requested, &c.Provider, &c.Model,
		&c.Attempt, &c.Outcome, &c.InputTokens, &c.OutputTokens, &c.CachedTokens, &cost, &c.TTFTMs, &c.DurationMs, &c.Error); err != nil {
		return routerdb.Call{}, err
	}
	c.At = msTime(at)
	if cost.Valid {
		v := cost.Float64
		c.CostUSD = &v
	}
	return c, nil
}

func scanRouterKey(row rowScanner) (routerdb.Key, error) {
	var k routerdb.Key
	var created int64
	var used, revoked sql.NullInt64
	if err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.Hash, &created, &used, &revoked); err != nil {
		return routerdb.Key{}, err
	}
	k.CreatedAt = msTime(created)
	k.LastUsedAt = nullMS(used)
	k.RevokedAt = nullMS(revoked)
	return k, nil
}

// InsertRouterCalls stores call records in one transaction. Input IDs are
// ignored; SQLite assigns them in order.
func (s *Store) InsertRouterCalls(ctx context.Context, calls []routerdb.Call) error {
	if len(calls) == 0 {
		return s.assertOpen()
	}
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		stmt, err := conn.PrepareContext(ctx, `INSERT INTO router_calls (at, source, agent, session_id, turn_id, requested, provider, model, attempt, outcome,
			input_tokens, output_tokens, cached_tokens, cost_usd, ttft_ms, duration_ms, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, c := range calls {
			var cost any
			if c.CostUSD != nil {
				cost = *c.CostUSD
			}
			if _, err := stmt.ExecContext(ctx, c.At.UnixMilli(), c.Source, c.Agent, c.SessionID, c.TurnID, c.Requested, c.Provider, c.Model,
				c.Attempt, c.Outcome, c.InputTokens, c.OutputTokens, c.CachedTokens, cost, c.TTFTMs, c.DurationMs, c.Error); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListRouterCalls returns a page of call records, newest first (API-RTR-012).
func (s *Store) ListRouterCalls(ctx context.Context, filter routerdb.CallFilter) (routerdb.CallPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = routerdb.DefaultCallsLimit
	}
	limit = min(limit, routerdb.MaxCallsLimit)
	var where []string
	var args []any
	if filter.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, filter.Before)
	}
	if filter.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, filter.Provider)
	}
	if filter.Source != "" {
		where = append(where, "source = ?")
		args = append(args, filter.Source)
	}
	switch filter.Outcome {
	case "":
	case routerdb.OutcomeOK:
		where = append(where, "outcome = 'ok'")
	case "error":
		where = append(where, "outcome <> 'ok'")
	default:
		return routerdb.CallPage{}, fmt.Errorf("%w: outcome %q", routerdb.ErrInvalidFilter, filter.Outcome)
	}
	query := "SELECT " + routerCallColumns + " FROM router_calls"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	calls, err := queryCalendar(ctx, s, scanRouterCall, query, args...)
	if err != nil {
		return routerdb.CallPage{}, err
	}
	page := routerdb.CallPage{Calls: calls}
	if len(calls) > limit {
		page.Calls = calls[:limit]
		page.NextBefore = page.Calls[limit-1].ID
	}
	return page, nil
}

// RouterUsage aggregates the call records with at >= since (API-RTR-011).
// Sums and percentiles are computed in Go over the range.
func (s *Store) RouterUsage(ctx context.Context, since time.Time, bucket time.Duration) (routerdb.Usage, error) {
	scan := func(row rowScanner) (routerdb.Call, error) {
		var c routerdb.Call
		var at int64
		var cost sql.NullFloat64
		if err := row.Scan(&at, &c.Source, &c.Provider, &c.Model, &c.Outcome, &c.InputTokens, &c.OutputTokens, &c.CachedTokens, &cost, &c.TTFTMs); err != nil {
			return routerdb.Call{}, err
		}
		c.At = msTime(at)
		if cost.Valid {
			v := cost.Float64
			c.CostUSD = &v
		}
		return c, nil
	}
	calls, err := queryCalendar(ctx, s, scan,
		"SELECT at, source, provider, model, outcome, input_tokens, output_tokens, cached_tokens, cost_usd, ttft_ms FROM router_calls WHERE at >= ?",
		since.UnixMilli())
	if err != nil {
		return routerdb.Usage{}, err
	}
	return routerdb.Aggregate(calls, since, time.Now(), bucket), nil
}

// PruneRouterCalls deletes call records older than olderThan.
func (s *Store) PruneRouterCalls(ctx context.Context, olderThan time.Time) (int64, error) {
	var n int64
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "DELETE FROM router_calls WHERE at < ?", olderThan.UnixMilli())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// CreateRouterKey stores a new proxy key. CreatedAt defaults to now.
func (s *Store) CreateRouterKey(ctx context.Context, key routerdb.Key) error {
	if key.ID == "" || key.Hash == "" {
		return errors.New("router key needs an id and a hash")
	}
	created := key.CreatedAt.UnixMilli()
	if key.CreatedAt.IsZero() {
		created = nowMS()
	}
	var used, revoked any
	if key.LastUsedAt != nil {
		used = key.LastUsedAt.UnixMilli()
	}
	if key.RevokedAt != nil {
		revoked = key.RevokedAt.UnixMilli()
	}
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO router_keys ("+routerKeyColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)",
			key.ID, key.Name, key.Prefix, key.Hash, created, used, revoked)
		return err
	})
}

// ListRouterKeys returns the live proxy keys, oldest first.
func (s *Store) ListRouterKeys(ctx context.Context) ([]routerdb.Key, error) {
	return queryCalendar(ctx, s, scanRouterKey,
		"SELECT "+routerKeyColumns+" FROM router_keys WHERE revoked_at IS NULL ORDER BY created_at, id")
}

// RevokeRouterKey revokes a live proxy key; a missing or revoked key is
// routerdb.ErrKeyNotFound.
func (s *Store) RevokeRouterKey(ctx context.Context, id string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "UPDATE router_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", nowMS(), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return routerdb.ErrKeyNotFound
		}
		return nil
	})
}

// LookupRouterKeyByHash returns the live key with hash. The stored hash is
// compared in constant time after the indexed lookup.
func (s *Store) LookupRouterKeyByHash(ctx context.Context, hash string) (routerdb.Key, bool, error) {
	if err := s.assertOpen(); err != nil {
		return routerdb.Key{}, false, err
	}
	if hash == "" {
		return routerdb.Key{}, false, nil
	}
	k, err := scanRouterKey(s.db.QueryRowContext(ctx,
		"SELECT "+routerKeyColumns+" FROM router_keys WHERE hash = ? AND revoked_at IS NULL", hash))
	if errors.Is(err, sql.ErrNoRows) {
		return routerdb.Key{}, false, nil
	}
	if err != nil {
		return routerdb.Key{}, false, err
	}
	if subtle.ConstantTimeCompare([]byte(k.Hash), []byte(hash)) != 1 {
		return routerdb.Key{}, false, nil
	}
	return k, true, nil
}

// TouchRouterKey sets a key's last-used time. A missing key is
// routerdb.ErrKeyNotFound.
func (s *Store) TouchRouterKey(ctx context.Context, id string, at time.Time) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "UPDATE router_keys SET last_used_at = ? WHERE id = ?", at.UnixMilli(), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return routerdb.ErrKeyNotFound
		}
		return nil
	})
}

// GetRouterKV returns the value of a router_kv key.
func (s *Store) GetRouterKV(ctx context.Context, key string) (string, bool, error) {
	if err := s.assertOpen(); err != nil {
		return "", false, err
	}
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM router_kv WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// PutRouterKV sets a router_kv key.
func (s *Store) PutRouterKV(ctx context.Context, key, value string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx,
			"INSERT INTO router_kv (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
			key, value, nowMS())
		return err
	})
}

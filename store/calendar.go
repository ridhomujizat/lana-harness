package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

// Store implements calendar.Store (spec #35, migration 0007).
var _ calendar.Store = (*Store)(nil)

const calendarItemColumns = "id, kind, title, notes, start_ms, end_ms, all_day, date, end_date, time, repeat_json, assignee, color, state, action_json, google_linked, google_calendar, google_event, google_etag, google_sync, created_by, updated_by, created_at, updated_at, deleted_at"

const calendarOverrideColumns = "source, ref, occurrence, op, to_ms, author, at, fired_at"

const calendarFiringColumns = "item_id, occurrence, status, started_at, ended_at, session_id, run_workflow, run_number, error"

const calendarGoogleColumns = "calendar_id, event_id, etag, item_id, event_json, updated_ms"

// calendarRepeat is calendar.Repeat without its MarshalJSON, so a stored
// repeat reads back exactly as written (an empty Freq stays "", nil Days stay
// nil).
type calendarRepeat calendar.Repeat

// boolInt maps a bool to the INTEGER 0/1 SQLite stores.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// calendarItemArgs returns the column values of an item in
// calendarItemColumns order.
func calendarItemArgs(it calendar.Item) ([]any, error) {
	repeat, err := json.Marshal(calendarRepeat(it.Repeat))
	if err != nil {
		return nil, err
	}
	action := []byte("null")
	if it.Action != nil {
		if action, err = json.Marshal(it.Action); err != nil {
			return nil, err
		}
	}
	var g calendar.GoogleRef
	if it.Google != nil {
		g = *it.Google
	}
	return []any{
		it.ID, string(it.Kind), it.Title, it.Notes, it.Start, it.End, boolInt(it.AllDay),
		it.Date, it.EndDate, it.Time, string(repeat), it.Assignee, it.Color, it.State, string(action),
		boolInt(it.Google != nil), g.CalendarID, g.EventID, g.ETag, g.Sync,
		it.CreatedBy, it.UpdatedBy, it.CreatedAt, it.UpdatedAt, it.DeletedAt,
	}, nil
}

func scanCalendarItem(row rowScanner) (calendar.Item, error) {
	var it calendar.Item
	var kind, repeat, action string
	var allDay, linked int
	var g calendar.GoogleRef
	if err := row.Scan(&it.ID, &kind, &it.Title, &it.Notes, &it.Start, &it.End, &allDay,
		&it.Date, &it.EndDate, &it.Time, &repeat, &it.Assignee, &it.Color, &it.State, &action,
		&linked, &g.CalendarID, &g.EventID, &g.ETag, &g.Sync,
		&it.CreatedBy, &it.UpdatedBy, &it.CreatedAt, &it.UpdatedAt, &it.DeletedAt); err != nil {
		return calendar.Item{}, err
	}
	it.Kind = calendar.Kind(kind)
	it.AllDay = allDay != 0
	if repeat != "" {
		var r calendarRepeat
		if err := json.Unmarshal([]byte(repeat), &r); err != nil {
			return calendar.Item{}, err
		}
		it.Repeat = calendar.Repeat(r)
	}
	if action != "" && action != "null" {
		var a calendar.Action
		if err := json.Unmarshal([]byte(action), &a); err != nil {
			return calendar.Item{}, err
		}
		it.Action = &a
	}
	if linked != 0 {
		it.Google = &g
	}
	return it, nil
}

func scanCalendarOverride(row rowScanner) (calendar.Override, error) {
	var o calendar.Override
	err := row.Scan(&o.Source, &o.Ref, &o.Occurrence, &o.Op, &o.To, &o.By, &o.At, &o.FiredAt)
	return o, err
}

func scanCalendarFiring(row rowScanner) (calendar.Firing, error) {
	var f calendar.Firing
	err := row.Scan(&f.ItemID, &f.Occurrence, &f.Status, &f.StartedAt, &f.EndedAt, &f.SessionID, &f.RunWorkflow, &f.RunNumber, &f.Error)
	return f, err
}

func scanCalendarGoogle(row rowScanner) (calendar.GoogleEvent, error) {
	var e calendar.GoogleEvent
	err := row.Scan(&e.CalendarID, &e.EventID, &e.ETag, &e.ItemID, &e.JSON, &e.UpdatedMs)
	return e, err
}

// queryCalendar runs a read query and scans every row with scan. It returns an
// empty, non-nil slice when nothing matches, like the MemStore.
func queryCalendar[T any](ctx context.Context, s *Store, scan func(rowScanner) (T, error), query string, args ...any) ([]T, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// inClause returns " AND <column> IN (?, ...)" and appends values to args.
func inClause(column string, values []string, args []any) (string, []any) {
	placeholders := make([]string, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args = append(args, v)
	}
	return " AND " + column + " IN (" + strings.Join(placeholders, ", ") + ")", args
}

// CreateItem stores a new item; it fails when the id is taken, deleted or not.
func (s *Store) CreateItem(ctx context.Context, it calendar.Item) error {
	args, err := calendarItemArgs(it)
	if err != nil {
		return err
	}
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO calendar_items ("+calendarItemColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", args...)
		return err
	})
}

// GetItem returns one item, or calendar.ErrNotFound when it is missing or
// soft-deleted.
func (s *Store) GetItem(ctx context.Context, id string) (calendar.Item, error) {
	if err := s.assertOpen(); err != nil {
		return calendar.Item{}, err
	}
	it, err := scanCalendarItem(s.db.QueryRowContext(ctx, "SELECT "+calendarItemColumns+" FROM calendar_items WHERE id = ? AND deleted_at = 0", id))
	if errors.Is(err, sql.ErrNoRows) {
		return calendar.Item{}, calendar.ErrNotFound
	}
	if err != nil {
		return calendar.Item{}, err
	}
	return it, nil
}

// UpdateItem writes every field of an existing, not deleted item, or returns
// calendar.ErrNotFound.
func (s *Store) UpdateItem(ctx context.Context, it calendar.Item) error {
	args, err := calendarItemArgs(it)
	if err != nil {
		return err
	}
	// Move the id from the front of the column values to the WHERE clause.
	args = append(args[1:], it.ID)
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "UPDATE calendar_items SET kind = ?, title = ?, notes = ?, start_ms = ?, end_ms = ?, all_day = ?, date = ?, end_date = ?, time = ?, repeat_json = ?, assignee = ?, color = ?, state = ?, action_json = ?, google_linked = ?, google_calendar = ?, google_event = ?, google_etag = ?, google_sync = ?, created_by = ?, updated_by = ?, created_at = ?, updated_at = ?, deleted_at = ? WHERE id = ? AND deleted_at = 0", args...)
		if err != nil {
			return err
		}
		return calendarAffected(res)
	})
}

// DeleteItem soft-deletes an item at the given Unix ms (1 when at is 0), or
// returns calendar.ErrNotFound when it is missing or already deleted.
func (s *Store) DeleteItem(ctx context.Context, id string, at int64) error {
	if at == 0 {
		at = 1
	}
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "UPDATE calendar_items SET deleted_at = ? WHERE id = ? AND deleted_at = 0", at, id)
		if err != nil {
			return err
		}
		return calendarAffected(res)
	})
}

// calendarAffected maps an UPDATE that changed no row to calendar.ErrNotFound.
func calendarAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return calendar.ErrNotFound
	}
	return nil
}

// ListItems returns the items that are not deleted, by created_at then id.
func (s *Store) ListItems(ctx context.Context) ([]calendar.Item, error) {
	return queryCalendar(ctx, s, scanCalendarItem, "SELECT "+calendarItemColumns+" FROM calendar_items WHERE deleted_at = 0 ORDER BY created_at, id")
}

// ListDeletedItems returns the soft-deleted items that still have a Google
// link, by created_at then id (REQ-CAL-012).
func (s *Store) ListDeletedItems(ctx context.Context) ([]calendar.Item, error) {
	return queryCalendar(ctx, s, scanCalendarItem, "SELECT "+calendarItemColumns+" FROM calendar_items WHERE deleted_at != 0 AND google_linked = 1 ORDER BY created_at, id")
}

// PurgeItem removes a soft-deleted item row for good; its history lines stay.
// It returns calendar.ErrNotFound when the item is missing or not deleted.
func (s *Store) PurgeItem(ctx context.Context, id string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "DELETE FROM calendar_items WHERE id = ? AND deleted_at != 0", id)
		if err != nil {
			return err
		}
		return calendarAffected(res)
	})
}

// PutOverride upserts an override by (source, ref, occurrence) (REQ-CAL-004).
func (s *Store) PutOverride(ctx context.Context, o calendar.Override) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO calendar_overrides ("+calendarOverrideColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(source, ref, occurrence) DO UPDATE SET op = excluded.op, to_ms = excluded.to_ms, author = excluded.author, at = excluded.at, fired_at = excluded.fired_at",
			o.Source, o.Ref, o.Occurrence, o.Op, o.To, o.By, o.At, o.FiredAt)
		return err
	})
}

// DeleteOverride removes one override; a missing one is not an error.
func (s *Store) DeleteOverride(ctx context.Context, source, ref string, occurrence int64) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "DELETE FROM calendar_overrides WHERE source = ? AND ref = ? AND occurrence = ?", source, ref, occurrence)
		return err
	})
}

// ListOverrides returns every override by source, ref, occurrence.
func (s *Store) ListOverrides(ctx context.Context) ([]calendar.Override, error) {
	return queryCalendar(ctx, s, scanCalendarOverride, "SELECT "+calendarOverrideColumns+" FROM calendar_overrides ORDER BY source, ref, occurrence")
}

// InsertFiring stores a new firing. It returns false, and changes nothing,
// when (item_id, occurrence) exists, so an occurrence fires at most once
// (REQ-CAL-006).
func (s *Store) InsertFiring(ctx context.Context, f calendar.Firing) (bool, error) {
	inserted := false
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "INSERT INTO calendar_firings ("+calendarFiringColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(item_id, occurrence) DO NOTHING",
			f.ItemID, f.Occurrence, f.Status, f.StartedAt, f.EndedAt, f.SessionID, f.RunWorkflow, f.RunNumber, f.Error)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		inserted = n > 0
		return nil
	})
	return inserted, err
}

// UpdateFiring writes f only when the stored status is one of fromStatuses
// (any status when empty) and reports whether it wrote. The status check and
// the write are one UPDATE, so concurrent callers see exactly one winner. It
// returns calendar.ErrNotFound when the firing does not exist.
func (s *Store) UpdateFiring(ctx context.Context, f calendar.Firing, fromStatuses ...string) (bool, error) {
	updated := false
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		query := "UPDATE calendar_firings SET status = ?, started_at = ?, ended_at = ?, session_id = ?, run_workflow = ?, run_number = ?, error = ? WHERE item_id = ? AND occurrence = ?"
		args := []any{f.Status, f.StartedAt, f.EndedAt, f.SessionID, f.RunWorkflow, f.RunNumber, f.Error, f.ItemID, f.Occurrence}
		if len(fromStatuses) > 0 {
			var clause string
			clause, args = inClause("status", fromStatuses, args)
			query += clause
		}
		res, err := conn.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			updated = true
			return nil
		}
		var exists int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM calendar_firings WHERE item_id = ? AND occurrence = ?", f.ItemID, f.Occurrence).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return calendar.ErrNotFound
		}
		return nil
	})
	return updated, err
}

// GetFiring returns one firing, or calendar.ErrNotFound.
func (s *Store) GetFiring(ctx context.Context, itemID string, occurrence int64) (calendar.Firing, error) {
	if err := s.assertOpen(); err != nil {
		return calendar.Firing{}, err
	}
	f, err := scanCalendarFiring(s.db.QueryRowContext(ctx, "SELECT "+calendarFiringColumns+" FROM calendar_firings WHERE item_id = ? AND occurrence = ?", itemID, occurrence))
	if errors.Is(err, sql.ErrNoRows) {
		return calendar.Firing{}, calendar.ErrNotFound
	}
	if err != nil {
		return calendar.Firing{}, err
	}
	return f, nil
}

// ListFirings returns the firings matching the filter, newest occurrence
// first, then by item id.
func (s *Store) ListFirings(ctx context.Context, filter calendar.FiringFilter) ([]calendar.Firing, error) {
	query := "SELECT " + calendarFiringColumns + " FROM calendar_firings WHERE 1=1"
	args := []any{}
	if filter.ItemID != "" {
		query += " AND item_id = ?"
		args = append(args, filter.ItemID)
	}
	if len(filter.Statuses) > 0 {
		var clause string
		clause, args = inClause("status", filter.Statuses, args)
		query += clause
	}
	if filter.From != 0 {
		query += " AND occurrence >= ?"
		args = append(args, filter.From)
	}
	if filter.To != 0 {
		query += " AND occurrence < ?"
		args = append(args, filter.To)
	}
	query += " ORDER BY occurrence DESC, item_id"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	return queryCalendar(ctx, s, scanCalendarFiring, query, args...)
}

// SetHabitCheck upserts a habit check by (item_id, date).
func (s *Store) SetHabitCheck(ctx context.Context, c calendar.HabitCheck) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO calendar_habit_checks (item_id, date, author, at) VALUES (?, ?, ?, ?) ON CONFLICT(item_id, date) DO UPDATE SET author = excluded.author, at = excluded.at",
			c.ItemID, c.Date, c.By, c.At)
		return err
	})
}

// DeleteHabitCheck removes one habit check; a missing one is not an error.
func (s *Store) DeleteHabitCheck(ctx context.Context, itemID, date string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "DELETE FROM calendar_habit_checks WHERE item_id = ? AND date = ?", itemID, date)
		return err
	})
}

// ListHabitChecks returns the checks of one item ("" = all) by item, date.
func (s *Store) ListHabitChecks(ctx context.Context, itemID string) ([]calendar.HabitCheck, error) {
	scan := func(row rowScanner) (calendar.HabitCheck, error) {
		var c calendar.HabitCheck
		err := row.Scan(&c.ItemID, &c.Date, &c.By, &c.At)
		return c, err
	}
	query := "SELECT item_id, date, author, at FROM calendar_habit_checks"
	args := []any{}
	if itemID != "" {
		query += " WHERE item_id = ?"
		args = append(args, itemID)
	}
	return queryCalendar(ctx, s, scan, query+" ORDER BY item_id, date", args...)
}

// AppendHistory stores one history line.
func (s *Store) AppendHistory(ctx context.Context, h calendar.HistoryLine) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO calendar_history (item_id, at, author, summary, snapshot) VALUES (?, ?, ?, ?, ?)",
			h.ItemID, h.At, h.By, h.Summary, h.Snapshot)
		return err
	})
}

// ListHistory returns the last limit lines of one item ("" = all; limit 0 =
// all), newest first; lines with the same time are newest-appended first.
func (s *Store) ListHistory(ctx context.Context, itemID string, limit int) ([]calendar.HistoryLine, error) {
	scan := func(row rowScanner) (calendar.HistoryLine, error) {
		var h calendar.HistoryLine
		err := row.Scan(&h.ItemID, &h.At, &h.By, &h.Summary, &h.Snapshot)
		return h, err
	}
	query := "SELECT item_id, at, author, summary, snapshot FROM calendar_history"
	args := []any{}
	if itemID != "" {
		query += " WHERE item_id = ?"
		args = append(args, itemID)
	}
	query += " ORDER BY at DESC, id DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	return queryCalendar(ctx, s, scan, query, args...)
}

// GetKV returns one calendar setting value and whether it exists.
func (s *Store) GetKV(ctx context.Context, key string) (string, bool, error) {
	if err := s.assertOpen(); err != nil {
		return "", false, err
	}
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM calendar_kv WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetKV writes one calendar setting value.
func (s *Store) SetKV(ctx context.Context, key, value string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO calendar_kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
		return err
	})
}

// PutGoogleEvent upserts a cached Google event by (calendar_id, event_id).
func (s *Store) PutGoogleEvent(ctx context.Context, e calendar.GoogleEvent) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO calendar_google_events ("+calendarGoogleColumns+") VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(calendar_id, event_id) DO UPDATE SET etag = excluded.etag, item_id = excluded.item_id, event_json = excluded.event_json, updated_ms = excluded.updated_ms",
			e.CalendarID, e.EventID, e.ETag, e.ItemID, e.JSON, e.UpdatedMs)
		return err
	})
}

// DeleteGoogleEvent removes one cached event; a missing one is not an error.
func (s *Store) DeleteGoogleEvent(ctx context.Context, calendarID, eventID string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "DELETE FROM calendar_google_events WHERE calendar_id = ? AND event_id = ?", calendarID, eventID)
		return err
	})
}

// ListGoogleEvents returns every cached event by calendar_id, event_id.
func (s *Store) ListGoogleEvents(ctx context.Context) ([]calendar.GoogleEvent, error) {
	return queryCalendar(ctx, s, scanCalendarGoogle, "SELECT "+calendarGoogleColumns+" FROM calendar_google_events ORDER BY calendar_id, event_id")
}

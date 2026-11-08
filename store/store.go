// Package store provides the SQLite SessionStore.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/sqlitex"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var (
	ErrSessionNotFound       = fmt.Errorf("session not found: %w", core.ErrNotFound)
	ErrForkNotAtTurnBoundary = errors.New("fork is only allowed at turn/end")
	ErrTurnActive            = errors.New("a turn is still running")
	ErrLeaseNotHeld          = errors.New("session is not held by this process")
	ErrSessionHasChildren    = errors.New("session still has children")
	ErrSchemaTooNew          = errors.New("database schema is newer than the binary")
	ErrStoreClosed           = errors.New("store is closed")
	ErrSessionChanged        = errors.New("session changed during compaction")
	ErrNothingToCompact      = errors.New("nothing to compact")
	ErrInvalidPolicy         = errors.New("invalid policy")
)

const schemaVersion = 9

var validKinds = map[string]bool{
	core.KindSessionCreated: true, core.KindTurnStart: true, core.KindTurnEnd: true,
	core.KindStepStart: true, core.KindStepEnd: true, core.KindSystemMessage: true,
	core.KindUserMessage: true, core.KindAssistantMessage: true, core.KindAssistantAttempt: true,
	core.KindToolCall: true, core.KindToolResult: true, core.KindApprovalAsked: true,
	core.KindApprovalDecided: true, core.KindApprovalPolicy: true, core.KindModelSelection: true,
	core.KindQuestionAsked: true, core.KindQuestionAnswered: true,
	core.KindCompactionSummary: true, core.KindToolResultPruned: true,
	core.KindMemoryCaptured: true, core.KindKnowledgeCompiled: true,
}

// Store is the SQLite implementation of core.SessionStore.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	path   string
	closed atomic.Bool
	Hooks  *core.Hooks
}

// Open opens the database and runs migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("database path must be absolute: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
	}
	_ = os.Chmod(path, 0o600)
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sqlitex.Open(dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			_ = os.Chmod(path+suffix, 0o600)
		}
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return ErrSchemaTooNew
	}
	names := map[int]string{1: "session_log", 2: "session_leases", 3: "session_status_waiting_input", 4: "session_text", 5: "room_reads", 6: "workflows", 7: "calendar", 8: "router", 9: "session_title"}
	for v := version + 1; v <= schemaVersion; v++ {
		name := fmt.Sprintf("migrations/%04d_%s.sql", v, names[v])
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		if v == 3 {
			if err := s.migrateSessionsRebuild(ctx, v, string(data)); err != nil {
				return err
			}
			continue
		}
		if v == 4 {
			if err := migrateSessionText(ctx, s.db, v, string(data)); err != nil {
				return err
			}
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := execMigration(ctx, tx, v, string(data)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func execMigration(ctx context.Context, tx *sql.Tx, version int, stmt string) error {
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version))
	return err
}

// migrateSessionsRebuild applies a table rebuild that drops and recreates the
// sessions parent table. PRAGMA foreign_keys must be turned off on the
// connection before the transaction starts, since it is a no-op inside a
// transaction and DROP TABLE sessions would otherwise fail while child rows
// reference it. A dedicated connection keeps the pragma and the transaction
// together. foreign_key_check before COMMIT replaces the disabled enforcement.
func (s *Store) migrateSessionsRebuild(ctx context.Context, version int, stmt string) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	// The pragma is per-connection, so restore it before the connection returns
	// to the pool, even when the migration rolls back.
	defer func() {
		if _, restoreErr := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); restoreErr != nil && err == nil {
			err = fmt.Errorf("restore foreign_keys: %w", restoreErr)
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := execMigration(ctx, tx, version, stmt); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migrateSessionText applies migration 4: it creates the session_text table and
// its FTS5 index, then backfills the index from existing session_events in the
// same transaction. An error rolls the whole transaction back, so a failed
// backfill leaves the database at version 3 (REQ-SSR-003).
func migrateSessionText(ctx context.Context, db *sql.DB, version int, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := backfillSessionText(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// backfillSessionText indexes the conversation text of the events already in
// the log. It reads every user/message and assistant/message row and inserts
// one session_text row per event that yields conversation text (REQ-SSR-003).
func backfillSessionText(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT session_id, seq, kind, payload, created_at FROM session_events WHERE kind IN (?, ?)", core.KindUserMessage, core.KindAssistantMessage)
	if err != nil {
		return err
	}
	type pendingRow struct {
		sessionID string
		seq       int64
		role      string
		text      string
		createdAt int64
	}
	var pending []pendingRow
	for rows.Next() {
		var sessionID, kind string
		var seq, createdAt int64
		var payload []byte
		if err := rows.Scan(&sessionID, &seq, &kind, &payload, &createdAt); err != nil {
			_ = rows.Close()
			return err
		}
		role, text, ok := conversationText(kind, payload)
		if !ok {
			continue
		}
		pending = append(pending, pendingRow{sessionID: sessionID, seq: seq, role: role, text: text, createdAt: createdAt})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, row := range pending {
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_text (session_id, seq, role, created_at, text) VALUES (?, ?, ?, ?, ?)", row.sessionID, row.seq, row.role, row.createdAt, row.text); err != nil {
			return err
		}
	}
	return nil
}

// conversationText extracts the indexable conversation text of one event. It
// keeps only the text blocks of a user/message or assistant/message, hides
// runtime notes and the injected leading context blocks, and reports ok=false
// when nothing is left (REQ-SSR-001).
func conversationText(kind string, payload []byte) (role, text string, ok bool) {
	switch kind {
	case core.KindUserMessage:
		var msg core.UserMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return "", "", false
		}
		if msg.Runtime {
			return "", "", false
		}
		blocks := msg.Content
		if msg.ContextBlocks > 0 {
			if msg.ContextBlocks >= len(blocks) {
				blocks = nil
			} else {
				blocks = blocks[msg.ContextBlocks:]
			}
		}
		role, text = "user", joinTextBlocks(blocks)
	case core.KindAssistantMessage:
		var msg core.AssistantMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return "", "", false
		}
		role, text = "assistant", joinTextBlocks(msg.Content)
	default:
		return "", "", false
	}
	if strings.TrimSpace(text) == "" {
		return "", "", false
	}
	return role, text, true
}

// joinTextBlocks joins the text of the text blocks, ignoring every other kind.
func joinTextBlocks(blocks []core.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Kind == core.BlockText {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		return fmt.Errorf("foreign key violation in table %s referencing %s", table, parent)
	}
	return rows.Err()
}

// Close closes the store. It is safe to call more than once: only the first
// call closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.db.Close()
}

func (s *Store) assertOpen() error {
	if s.closed.Load() {
		return ErrStoreClosed
	}
	return nil
}

func nowMS() int64 { return time.Now().UnixMilli() }

func (s *Store) withWrite(ctx context.Context, fn func(*sql.Conn) error) error {
	return s.withWriteAfter(ctx, nil, fn)
}

// withWriteAfter commits fn's transaction like withWrite and then runs
// afterCommit while still holding the write lock. The callback only runs after
// a successful COMMIT, so a failed transaction never notifies observers, and
// because the lock is held across the commit and the callback the notification
// order matches the commit order. Append uses this to publish its events in
// seq order.
func (s *Store) withWriteAfter(ctx context.Context, afterCommit func(), fn func(*sql.Conn) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpen(); err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if err := fn(conn); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	if afterCommit != nil {
		afterCommit()
	}
	return nil
}

func marshalEvent(kind string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return NormalizePayload(kind, raw)
}

// NormalizePayload decodes a payload to the current version.
func NormalizePayload(kind string, raw json.RawMessage) (json.RawMessage, error) {
	if !validKinds[kind] {
		return nil, fmt.Errorf("unknown kind: %s", kind)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	v, ok := value["v"]
	if !ok {
		return nil, fmt.Errorf("payload has no v")
	}
	version, ok := v.(float64)
	if !ok {
		return nil, fmt.Errorf("v is not an integer")
	}
	switch int(version) {
	case 0:
		value["v"] = core.PayloadVersion
		return json.Marshal(value)
	case core.PayloadVersion:
		return raw, nil
	default:
		return nil, core.ErrUnknownPayloadVersion
	}
}

// Create creates a session and session/created.
func (s *Store) Create(ctx context.Context, in core.NewSession) (core.SessionInfo, error) {
	if in.Agent == "" || in.Channel == "" || in.Workspace == "" {
		return core.SessionInfo{}, errors.New("agent, channel, and workspace are required")
	}
	workspace := in.Workspace
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	sessionID := id.New()
	now := nowMS()
	firstSeq := int64(1)
	if in.InheritedSeq > 0 {
		firstSeq = in.InheritedSeq + 1
	}
	title, titleSource := in.Title, in.TitleSource
	if title == "" || !validTitleSource(titleSource) || titleSource == "" {
		title, titleSource = "", ""
	}
	info := core.SessionInfo{ID: sessionID, Agent: in.Agent, Channel: in.Channel, Address: in.Address, Workspace: workspace, Title: title, TitleSource: titleSource, Status: "idle", ParentSessionID: in.ParentSessionID, InheritedSeq: in.InheritedSeq, CreatedAt: now, UpdatedAt: now}
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		if in.ParentSessionID != "" {
			var count int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", in.ParentSessionID).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				return ErrSessionNotFound
			}
		}
		created := core.SessionCreated{V: core.PayloadVersion, Agent: in.Agent, Channel: in.Channel, Address: in.Address, Workspace: workspace, ParentSessionID: in.ParentSessionID, ParentCallID: in.ParentCallID, InheritedSeq: in.InheritedSeq}
		payload, err := json.Marshal(created)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO sessions (id, agent, channel, address, workspace, title, title_source, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'idle', NULLIF(?, ''), ?, ?, 0, ?, ?)", sessionID, in.Agent, in.Channel, in.Address, workspace, title, titleSource, in.ParentSessionID, in.InheritedSeq, firstSeq, now, now); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULL, ?)", sessionID, firstSeq, core.KindSessionCreated, string(payload), now); err != nil {
			return err
		}
		info.LastSeq = firstSeq
		return nil
	})
	if err != nil {
		return core.SessionInfo{}, err
	}
	return info, nil
}

// Info reads the session projection.
func (s *Store) Info(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	if err := s.assertOpen(); err != nil {
		return core.SessionInfo{}, err
	}
	return s.info(ctx, sessionID)
}

func (s *Store) info(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	var info core.SessionInfo
	var parent sql.NullString
	var inherited sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT id, agent, channel, address, workspace, title, title_source, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at FROM sessions WHERE id = ?", sessionID).
		Scan(&info.ID, &info.Agent, &info.Channel, &info.Address, &info.Workspace, &info.Title, &info.TitleSource, &info.Status, &parent, &inherited, &info.LastSeq, &info.TurnCount, &info.CreatedAt, &info.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return core.SessionInfo{}, ErrSessionNotFound
	}
	if err != nil {
		return core.SessionInfo{}, err
	}
	info.ParentSessionID = parent.String
	info.InheritedSeq = inherited.Int64
	return info, nil
}

// validTitleSource reports whether source is a stored title source (spec 40).
func validTitleSource(source string) bool {
	switch source {
	case "", core.TitleSourceAuto, core.TitleSourceOwner, core.TitleSourceSystem:
		return true
	}
	return false
}

// SetSessionTitle implements core.SessionTitleStore (spec 40, REQ-TTL-005).
// It changes only the title projection; updated_at stays, so a rename does
// not move the session in the list.
func (s *Store) SetSessionTitle(ctx context.Context, sessionID, title, source string, onlyIfUntitled bool) (bool, error) {
	if err := s.assertOpen(); err != nil {
		return false, err
	}
	if !validTitleSource(source) {
		return false, fmt.Errorf("invalid title source %q", source)
	}
	if title == "" {
		source = ""
	}
	changed := false
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		query := "UPDATE sessions SET title = ?, title_source = ? WHERE id = ?"
		if onlyIfUntitled {
			query += " AND title_source = ''"
		}
		res, err := conn.ExecContext(ctx, query, title, source, sessionID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			changed = true
			return nil
		}
		var count int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", sessionID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return ErrSessionNotFound
		}
		return nil
	})
	return changed, err
}

// Append writes a batch of events atomically.
func (s *Store) Append(ctx context.Context, sessionID string, drafts []core.EventDraft) ([]core.SessionEvent, error) {
	if len(drafts) == 0 {
		return nil, errors.New("drafts are empty")
	}
	var events []core.SessionEvent
	err := s.withWriteAfter(ctx, func() {
		if s.Hooks == nil || s.Hooks.SessionEvent == nil {
			return
		}
		for _, event := range events {
			s.Hooks.SessionEvent.Publish(event)
		}
	}, func(conn *sql.Conn) error {
		var status string
		var lastSeq int64
		err := conn.QueryRowContext(ctx, "SELECT status, last_seq FROM sessions WHERE id = ?", sessionID).Scan(&status, &lastSeq)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionNotFound
		}
		if err != nil {
			return err
		}
		now := nowMS()
		seq := lastSeq
		for _, draft := range drafts {
			if !validKinds[draft.Kind] {
				return fmt.Errorf("unknown kind: %s", draft.Kind)
			}
			if draft.Kind == core.KindTurnStart && (status == "running" || status == "waiting_input") {
				return ErrTurnActive
			}
			payload, err := marshalEvent(draft.Kind, draft.Payload)
			if err != nil {
				return err
			}
			seq++
			if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)", sessionID, seq, draft.Kind, string(payload), draft.TurnID, now); err != nil {
				return err
			}
			if role, text, ok := conversationText(draft.Kind, payload); ok {
				if _, err := conn.ExecContext(ctx, "INSERT INTO session_text (session_id, seq, role, created_at, text) VALUES (?, ?, ?, ?, ?)", sessionID, seq, role, now, text); err != nil {
					return err
				}
			}
			events = append(events, core.SessionEvent{SessionID: sessionID, Seq: seq, Kind: draft.Kind, Payload: payload, TurnID: draft.TurnID, CreatedAt: now})
			switch draft.Kind {
			case core.KindTurnStart:
				status = "running"
			case core.KindTurnEnd:
				status = "idle"
			case core.KindQuestionAsked:
				status = "waiting_input"
			case core.KindQuestionAnswered:
				status = "running"
			}
		}
		turnCount := 0
		for _, draft := range drafts {
			if draft.Kind == core.KindTurnStart {
				turnCount++
			}
		}
		if _, err := conn.ExecContext(ctx, "UPDATE sessions SET last_seq = ?, status = ?, turn_count = turn_count + ?, updated_at = ? WHERE id = ?", seq, status, turnCount, now, sessionID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// Events reads events from seq.
func (s *Store) Events(ctx context.Context, sessionID string, fromSeq int64) iter.Seq2[core.SessionEvent, error] {
	return func(yield func(core.SessionEvent, error) bool) {
		if err := s.assertOpen(); err != nil {
			yield(core.SessionEvent{}, err)
			return
		}
		if fromSeq < 1 {
			fromSeq = 1
		}
		if _, err := s.info(ctx, sessionID); err != nil {
			yield(core.SessionEvent{}, err)
			return
		}
		rows, err := s.db.QueryContext(ctx, "SELECT session_id, seq, kind, payload, COALESCE(turn_id, ''), created_at FROM session_events WHERE session_id = ? AND seq >= ? ORDER BY seq", sessionID, fromSeq)
		if err != nil {
			yield(core.SessionEvent{}, err)
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var event core.SessionEvent
			var payload string
			if err := rows.Scan(&event.SessionID, &event.Seq, &event.Kind, &payload, &event.TurnID, &event.CreatedAt); err != nil {
				yield(core.SessionEvent{}, err)
				return
			}
			event.Payload = json.RawMessage(payload)
			if !yield(event, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(core.SessionEvent{}, err)
		}
	}
}

// ScanEvents implements core.EventScanner. It reads events of the given kinds
// across every session, created at or after sinceMS, in (created_at,
// session_id, seq) order. A tool/result payload is reduced to call_id, status,
// and the first 300 characters of the output, so a large tool output is never
// loaded (design.md §2, REQ-DSH-001).
func (s *Store) ScanEvents(ctx context.Context, kinds []string, sinceMS int64) iter.Seq2[core.ScannedEvent, error] {
	return func(yield func(core.ScannedEvent, error) bool) {
		if err := s.assertOpen(); err != nil {
			yield(core.ScannedEvent{}, err)
			return
		}
		if len(kinds) == 0 {
			return
		}
		placeholders := make([]string, len(kinds))
		args := make([]any, 0, len(kinds)+1)
		for i, kind := range kinds {
			placeholders[i] = "?"
			args = append(args, kind)
		}
		args = append(args, sinceMS)
		query := "SELECT e.session_id, e.seq, e.kind," +
			" CASE WHEN e.kind = '" + core.KindToolResult + "'" +
			" THEN json_object('call_id', json_extract(e.payload, '$.call_id'), 'status', json_extract(e.payload, '$.status'), 'output', substr(json_extract(e.payload, '$.output'), 1, 300))" +
			" ELSE e.payload END," +
			" COALESCE(e.turn_id, ''), e.created_at, s.agent" +
			" FROM session_events e JOIN sessions s ON s.id = e.session_id" +
			" WHERE e.kind IN (" + strings.Join(placeholders, ", ") + ") AND e.created_at >= ?" +
			" ORDER BY e.created_at, e.session_id, e.seq"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			yield(core.ScannedEvent{}, err)
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var event core.ScannedEvent
			var payload string
			if err := rows.Scan(&event.SessionID, &event.Seq, &event.Kind, &payload, &event.TurnID, &event.CreatedAt, &event.Agent); err != nil {
				yield(core.ScannedEvent{}, err)
				return
			}
			event.Payload = json.RawMessage(payload)
			if !yield(event, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(core.ScannedEvent{}, err)
		}
	}
}

// CountAgentEvents implements core.AgentEventCounter (REQ-ROOM-002,
// REQ-ROOM-005). It counts the events of the given kinds in the sessions of
// agent created after sinceMS, joining session_events with sessions. With
// topLevelOnly only the agent's top-level sessions are counted. An empty kinds
// slice returns 0 without querying.
func (s *Store) CountAgentEvents(ctx context.Context, agent string, kinds []string, sinceMS int64, topLevelOnly bool) (int64, error) {
	if err := s.assertOpen(); err != nil {
		return 0, err
	}
	if len(kinds) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(kinds))
	args := make([]any, 0, len(kinds)+2)
	args = append(args, agent)
	for i, kind := range kinds {
		placeholders[i] = "?"
		args = append(args, kind)
	}
	args = append(args, sinceMS)
	query := "SELECT COUNT(*) FROM session_events e JOIN sessions s ON s.id = e.session_id" +
		" WHERE s.agent = ? AND e.kind IN (" + strings.Join(placeholders, ", ") + ") AND e.created_at > ?"
	if topLevelOnly {
		query += " AND (s.parent_session_id IS NULL OR s.parent_session_id = '')"
	}
	var count int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// RoomReadAt implements core.RoomReadStore (REQ-ROOM-005). It returns 0 when
// the room has no row.
func (s *Store) RoomReadAt(ctx context.Context, room string) (int64, error) {
	if err := s.assertOpen(); err != nil {
		return 0, err
	}
	var at int64
	err := s.db.QueryRowContext(ctx, "SELECT read_at FROM room_reads WHERE room = ?", room).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return at, nil
}

// SetRoomRead implements core.RoomReadStore (REQ-ROOM-005). It upserts the
// read time, which only moves forward, and returns the stored value.
func (s *Store) SetRoomRead(ctx context.Context, room string, at int64) (int64, error) {
	var stored int64
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "INSERT INTO room_reads (room, read_at) VALUES (?, ?) ON CONFLICT(room) DO UPDATE SET read_at = max(read_at, excluded.read_at)", room, at); err != nil {
			return err
		}
		return conn.QueryRowContext(ctx, "SELECT read_at FROM room_reads WHERE room = ?", room).Scan(&stored)
	})
	if err != nil {
		return 0, err
	}
	return stored, nil
}

func (s *Store) loadEvents(ctx context.Context, sessionID string) ([]core.SessionEvent, error) {
	var out []core.SessionEvent
	for event, err := range s.Events(ctx, sessionID, 1) {
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, nil
}

// ChainEvents returns the parent chain truncated at inherited_seq.
func (s *Store) ChainEvents(ctx context.Context, sessionID string) ([]core.SessionEvent, error) {
	info, err := s.info(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var chain []core.SessionEvent
	if info.ParentSessionID != "" {
		parent, err := s.ChainEvents(ctx, info.ParentSessionID)
		if err != nil {
			return nil, err
		}
		for _, event := range parent {
			if event.Seq <= info.InheritedSeq {
				chain = append(chain, event)
			}
		}
	}
	own, err := s.loadEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	chain = append(chain, own...)
	return chain, nil
}

// DeriveMessages uses core.DeriveMessages.
func (s *Store) DeriveMessages(ctx context.Context, sessionID string) ([]core.Message, error) {
	events, err := s.ChainEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return core.DeriveMessages(events)
}

// List returns the most recent sessions.
func (s *Store) List(ctx context.Context, f core.SessionFilter) ([]core.SessionInfo, error) {
	if f.Limit < 0 {
		return nil, errors.New("negative limit")
	}
	query := "SELECT id, agent, channel, address, workspace, title, title_source, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at FROM sessions WHERE 1=1"
	args := []any{}
	if f.Channel != "" {
		query += " AND channel = ?"
		args = append(args, f.Channel)
	}
	if f.Address != "" {
		query += " AND address = ?"
		args = append(args, f.Address)
	}
	if f.ParentSessionID != "" {
		query += " AND parent_session_id = ?"
		args = append(args, f.ParentSessionID)
	}
	if f.Agent != "" {
		query += " AND agent = ?"
		args = append(args, f.Agent)
	}
	if f.TopLevelOnly {
		query += " AND (parent_session_id IS NULL OR parent_session_id = '')"
	}
	if f.Before > 0 {
		query += " AND updated_at < ?"
		args = append(args, f.Before)
	}
	query += " ORDER BY updated_at DESC, created_at DESC, id DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []core.SessionInfo
	for rows.Next() {
		var info core.SessionInfo
		var parent sql.NullString
		var inherited sql.NullInt64
		if err := rows.Scan(&info.ID, &info.Agent, &info.Channel, &info.Address, &info.Workspace, &info.Title, &info.TitleSource, &info.Status, &parent, &inherited, &info.LastSeq, &info.TurnCount, &info.CreatedAt, &info.UpdatedAt); err != nil {
			return nil, err
		}
		info.ParentSessionID = parent.String
		info.InheritedSeq = inherited.Int64
		out = append(out, info)
	}
	return out, rows.Err()
}

// FindPrefix finds sessions by case-insensitive prefix.
func (s *Store) FindPrefix(ctx context.Context, prefix string) ([]core.SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM sessions WHERE UPPER(id) LIKE UPPER(?) || '%' ORDER BY id", prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var out []core.SessionInfo
	for _, id := range ids {
		info, err := s.info(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, rows.Err()
}

// Fork creates a child from the parent's turn/end.
func (s *Store) Fork(ctx context.Context, parentID string, atSeq int64) (core.SessionInfo, error) {
	parent, err := s.info(ctx, parentID)
	if err != nil {
		return core.SessionInfo{}, err
	}
	var kind string
	if err := s.db.QueryRowContext(ctx, "SELECT kind FROM session_events WHERE session_id = ? AND seq = ?", parentID, atSeq).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.SessionInfo{}, ErrForkNotAtTurnBoundary
		}
		return core.SessionInfo{}, err
	}
	if kind != core.KindTurnEnd {
		return core.SessionInfo{}, ErrForkNotAtTurnBoundary
	}
	childID := id.New()
	now := nowMS()
	firstSeq := atSeq + 1
	created := core.SessionCreated{V: core.PayloadVersion, Agent: parent.Agent, Channel: parent.Channel, Address: parent.Address, Workspace: parent.Workspace, ParentSessionID: parentID, InheritedSeq: atSeq}
	payload, _ := json.Marshal(created)
	err = s.withWrite(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "INSERT INTO sessions (id, agent, channel, address, workspace, title, title_source, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'idle', ?, ?, ?, 0, ?, ?)", childID, parent.Agent, parent.Channel, parent.Address, parent.Workspace, parent.Title, parent.TitleSource, parentID, atSeq, firstSeq, now, now); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULL, ?)", childID, firstSeq, core.KindSessionCreated, string(payload), now)
		return err
	})
	if err != nil {
		return core.SessionInfo{}, err
	}
	return s.info(ctx, childID)
}

// Open opens a session with repair and a lease requirement. The caller must
// hold the session lease. When the session is still "running" or
// "waiting_input" (its turn/end was lost, for example because the process died
// mid-turn), Open closes every orphaned approval, question and tool call with
// an "interrupted" outcome, appends an "interrupted" turn/end for the orphaned
// turn and sets the status back to idle. The caller must make sure no turn for
// the session is live, since Open cannot tell a live turn from a dead one.
func (s *Store) Open(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	if _, err := s.info(ctx, sessionID); err != nil {
		return core.SessionInfo{}, err
	}
	var held int
	pid, host := os.Getpid(), currentHost()
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_leases WHERE session_id = ? AND pid = ? AND host = ?", sessionID, pid, host).Scan(&held); err != nil {
		return core.SessionInfo{}, err
	}
	if held == 0 {
		return core.SessionInfo{}, ErrLeaseNotHeld
	}
	var repaired []core.SessionEvent
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		repaired = nil
		var status string
		var seq int64
		if err := conn.QueryRowContext(ctx, "SELECT status, last_seq FROM sessions WHERE id = ?", sessionID).Scan(&status, &seq); err != nil {
			return err
		}
		if status != "running" && status != "waiting_input" {
			return nil
		}
		events, err := s.loadEvents(ctx, sessionID)
		if err != nil {
			return err
		}
		now := nowMS()
		insert := func(kind, turnID string, payload any) error {
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			seq++
			if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)", sessionID, seq, kind, string(raw), turnID, now); err != nil {
				return err
			}
			repaired = append(repaired, core.SessionEvent{SessionID: sessionID, Seq: seq, Kind: kind, Payload: raw, TurnID: turnID, CreatedAt: now})
			return nil
		}
		// The orphaned turn is the last one started.
		var turnID string
		for _, event := range events {
			if event.Kind == core.KindTurnStart {
				turnID = event.TurnID
			}
		}
		// Orphaned approval.
		asked := map[string]string{}
		decided := map[string]bool{}
		for _, event := range events {
			if event.Kind == core.KindApprovalAsked {
				var p core.ApprovalAsked
				_ = json.Unmarshal(event.Payload, &p)
				asked[p.RequestID] = p.CallID
			}
			if event.Kind == core.KindApprovalDecided {
				var p core.ApprovalDecided
				_ = json.Unmarshal(event.Payload, &p)
				decided[p.RequestID] = true
			}
		}
		for requestID, callID := range asked {
			if decided[requestID] {
				continue
			}
			if err := insert(core.KindApprovalDecided, "", core.ApprovalDecided{V: 1, RequestID: requestID, CallID: callID, Outcome: core.OutcomeUnavailable, Reason: "interrupted"}); err != nil {
				return err
			}
		}
		// Orphaned question.
		questions := map[string]string{}
		questionAnswers := map[string]bool{}
		for _, event := range events {
			if event.Kind == core.KindQuestionAsked {
				var p core.QuestionAsked
				_ = json.Unmarshal(event.Payload, &p)
				questions[p.RequestID] = p.CallID
			}
			if event.Kind == core.KindQuestionAnswered {
				var p core.QuestionAnswered
				_ = json.Unmarshal(event.Payload, &p)
				questionAnswers[p.RequestID] = true
			}
		}
		for requestID, callID := range questions {
			if questionAnswers[requestID] {
				continue
			}
			if err := insert(core.KindQuestionAnswered, "", core.QuestionAnswered{V: 1, RequestID: requestID, CallID: callID, Outcome: core.QuestionStatusUnavailable, Responder: "none", Reason: core.QuestionReasonInterrupted}); err != nil {
				return err
			}
		}
		// Orphaned call.
		calls := map[string]string{}
		results := map[string]bool{}
		for _, event := range events {
			if event.Kind == core.KindToolCall {
				var p core.ToolCallPayload
				_ = json.Unmarshal(event.Payload, &p)
				calls[p.CallID] = event.TurnID
			}
			if event.Kind == core.KindToolResult {
				var p core.ToolResultPayload
				_ = json.Unmarshal(event.Payload, &p)
				results[p.CallID] = true
			}
		}
		for callID, callTurn := range calls {
			if results[callID] {
				continue
			}
			if err := insert(core.KindToolResult, callTurn, core.ToolResultPayload{V: 1, CallID: callID, Status: core.ToolStatusError, Output: "interrupted"}); err != nil {
				return err
			}
		}
		if err := insert(core.KindTurnEnd, turnID, core.TurnEnd{V: 1, TurnID: turnID, Reason: "interrupted"}); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, "UPDATE sessions SET status = 'idle', last_seq = ?, updated_at = ? WHERE id = ?", seq, now, sessionID)
		return err
	})
	if err != nil {
		return core.SessionInfo{}, err
	}
	if s.Hooks != nil && s.Hooks.SessionEvent != nil {
		for _, event := range repaired {
			s.Hooks.SessionEvent.Publish(event)
		}
	}
	return s.info(ctx, sessionID)
}

var pidAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func currentHost() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// LeaseHolder reports the PID of the process holding the session lease without
// acquiring it. It applies the same liveness rule as TryLease, so a stale row
// left by a dead process on this host is not reported as held.
func (s *Store) LeaseHolder(ctx context.Context, sessionID string) (int, bool, error) {
	if err := s.assertOpen(); err != nil {
		return 0, false, err
	}
	var heldPID int
	var heldHost string
	err := s.db.QueryRowContext(ctx, "SELECT pid, host FROM session_leases WHERE session_id = ?", sessionID).Scan(&heldPID, &heldHost)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	pid, host := os.Getpid(), currentHost()
	if heldHost != host || heldPID == pid || pidAlive(heldPID) {
		return heldPID, true, nil
	}
	return 0, false, nil
}

// TryLease acquires the session lease.
func (s *Store) TryLease(ctx context.Context, sessionID string) (func() error, error) {
	if _, err := s.info(ctx, sessionID); err != nil {
		return nil, err
	}
	pid, host := os.Getpid(), currentHost()
	var heldPID int
	var heldHost string
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		err := conn.QueryRowContext(ctx, "SELECT pid, host FROM session_leases WHERE session_id = ?", sessionID).Scan(&heldPID, &heldHost)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if heldHost != host || (heldPID != pid && pidAlive(heldPID)) || heldPID == pid {
				return &core.LeaseHeldError{SessionID: sessionID, PID: heldPID, Host: heldHost}
			}
		}
		_, err = conn.ExecContext(ctx, "INSERT INTO session_leases (session_id, pid, host, acquired_at) VALUES (?, ?, ?, ?) ON CONFLICT(session_id) DO UPDATE SET pid = excluded.pid, host = excluded.host, acquired_at = excluded.acquired_at", sessionID, pid, host, nowMS())
		return err
	})
	if err != nil {
		return nil, err
	}
	return func() error {
		return s.withWrite(context.Background(), func(conn *sql.Conn) error {
			_, err := conn.ExecContext(context.Background(), "DELETE FROM session_leases WHERE session_id = ? AND pid = ? AND host = ?", sessionID, pid, host)
			return err
		})
	}, nil
}

// Prune deletes a session that has no children.
func (s *Store) Prune(ctx context.Context, sessionID string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		var count int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE parent_session_id = ?", sessionID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrSessionHasChildren
		}
		var exists int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", sessionID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrSessionNotFound
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO session_prune_allow (session_id) VALUES (?)", sessionID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM session_text WHERE session_id = ?", sessionID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM session_events WHERE session_id = ?", sessionID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM session_leases WHERE session_id = ?", sessionID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", sessionID); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, "DELETE FROM session_prune_allow WHERE session_id = ?", sessionID)
		return err
	})
}

// RebuildProjections rebuilds the sessions table from events.
func (s *Store) RebuildProjections(ctx context.Context) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, "SELECT id FROM sessions")
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		_ = rows.Close()
		for _, sessionID := range ids {
			events, err := s.loadEvents(ctx, sessionID)
			if err != nil {
				return err
			}
			status := "idle"
			var lastSeq int64
			var turns int64
			for _, event := range events {
				lastSeq = event.Seq
				if event.Kind == core.KindTurnStart {
					status = "running"
					turns++
				}
				if event.Kind == core.KindTurnEnd {
					status = "idle"
				}
				if event.Kind == core.KindQuestionAsked {
					status = "waiting_input"
				}
				if event.Kind == core.KindQuestionAnswered {
					status = "running"
				}
			}
			if _, err := conn.ExecContext(ctx, "UPDATE sessions SET status = ?, last_seq = ?, turn_count = ? WHERE id = ?", status, lastSeq, turns, sessionID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SearchText finds sessions whose conversation text matches q (REQ-SSR-011,
// -012, -014, -015). Terms are matched as alternatives with BM25 ranking, the
// best-matching message of a session represents it, and only searchable
// sessions (roots and forks) are considered. In the caller's own session only
// messages at or below the latest compaction summary's upto_seq are returned.
func (s *Store) SearchText(ctx context.Context, q core.TextQuery) ([]core.TextHit, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	if len(q.Terms) == 0 {
		return nil, nil
	}
	upto := int64(0)
	if q.CallerSession != "" {
		var err error
		upto, err = s.callerUptoSeq(ctx, q.CallerSession)
		if err != nil {
			return nil, err
		}
	}
	query := "SELECT t.session_id, t.seq, t.role, t.created_at, t.text," +
		" snippet(session_text_fts, 0, '«', '»', '…', 24)," +
		" s.agent, s.channel, s.title, s.address, s.parent_session_id, s.inherited_seq" +
		" FROM session_text_fts" +
		" JOIN session_text t ON t.rowid = session_text_fts.rowid" +
		" JOIN sessions s ON s.id = t.session_id" +
		" WHERE session_text_fts MATCH ?" +
		" AND (s.parent_session_id IS NULL OR COALESCE(s.inherited_seq, 0) > 0)" +
		" AND (? = '' OR s.agent = ?)" +
		" ORDER BY bm25(session_text_fts)" +
		" LIMIT 300"
	rows, err := s.db.QueryContext(ctx, query, matchExpression(q.Terms), q.Agent, q.Agent)
	if err != nil {
		return nil, err
	}
	type match struct {
		row     core.TextRow
		snippet string
		session core.SessionInfo
	}
	var matches []match
	for rows.Next() {
		var m match
		var parent sql.NullString
		var inherited sql.NullInt64
		if err := rows.Scan(&m.row.SessionID, &m.row.Seq, &m.row.Role, &m.row.CreatedAt, &m.row.Text, &m.snippet,
			&m.session.Agent, &m.session.Channel, &m.session.Title, &m.session.Address, &parent, &inherited); err != nil {
			_ = rows.Close()
			return nil, err
		}
		m.session.ID = m.row.SessionID
		m.session.ParentSessionID = parent.String
		m.session.InheritedSeq = inherited.Int64
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	var hits []core.TextHit
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		if m.row.SessionID == q.CallerSession && (upto == 0 || m.row.Seq > upto) {
			continue
		}
		if seen[m.row.SessionID] {
			continue
		}
		seen[m.row.SessionID] = true
		hits = append(hits, core.TextHit{Session: m.session, Row: m.row, Snippet: m.snippet})
		if q.Limit > 0 && len(hits) >= q.Limit {
			break
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	aroundUpto := int64(0)
	if hits[0].Row.SessionID == q.CallerSession {
		aroundUpto = upto
	}
	before, err := s.textRowsBefore(ctx, hits[0].Row.SessionID, hits[0].Row.Seq, q.Context, aroundUpto)
	if err != nil {
		return nil, err
	}
	after, err := s.textRowsAfter(ctx, hits[0].Row.SessionID, hits[0].Row.Seq, q.Context, aroundUpto)
	if err != nil {
		return nil, err
	}
	around := make([]core.TextRow, 0, len(before)+1+len(after))
	around = append(around, before...)
	around = append(around, hits[0].Row)
	around = append(around, after...)
	hits[0].Around = around
	return hits, nil
}

// TextWindow returns the conversation text of one session around aroundSeq
// (REQ-SSR-013). sessionID is a full id or a unique prefix. The rows are in seq
// order, before and after aroundSeq.
func (s *Store) TextWindow(ctx context.Context, sessionID string, aroundSeq int64, radius int) (core.SessionInfo, []core.TextRow, error) {
	if err := s.assertOpen(); err != nil {
		return core.SessionInfo{}, nil, err
	}
	matches, err := s.FindPrefix(ctx, sessionID)
	if err != nil {
		return core.SessionInfo{}, nil, err
	}
	if len(matches) == 0 {
		return core.SessionInfo{}, nil, ErrSessionNotFound
	}
	if len(matches) > 1 {
		return core.SessionInfo{}, nil, errors.New("session id prefix is ambiguous")
	}
	info := matches[0]
	before, err := s.textRowsBefore(ctx, info.ID, aroundSeq, radius, 0)
	if err != nil {
		return core.SessionInfo{}, nil, err
	}
	after, err := s.textRowsAfter(ctx, info.ID, aroundSeq, radius, 0)
	if err != nil {
		return core.SessionInfo{}, nil, err
	}
	out := make([]core.TextRow, 0, len(before)+len(after))
	out = append(out, before...)
	out = append(out, after...)
	return info, out, nil
}

// TextHeads lists the searchable sessions (spec 29 definition: roots and forks)
// that have conversation text, with each session's newest text seq (REQ-KNU-001,
// REQ-KNU-003). The result is ordered by session id.
func (s *Store) TextHeads(ctx context.Context) ([]core.TextHead, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	query := "SELECT t.session_id, MAX(t.seq)," +
		" s.agent, s.channel, s.title, s.address, s.parent_session_id, s.inherited_seq" +
		" FROM session_text t" +
		" JOIN sessions s ON s.id = t.session_id" +
		" WHERE (s.parent_session_id IS NULL OR COALESCE(s.inherited_seq, 0) > 0)" +
		" GROUP BY t.session_id" +
		" ORDER BY t.session_id"
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []core.TextHead
	for rows.Next() {
		var head core.TextHead
		var parent sql.NullString
		var inherited sql.NullInt64
		if err := rows.Scan(&head.Session.ID, &head.MaxSeq,
			&head.Session.Agent, &head.Session.Channel, &head.Session.Title, &head.Session.Address, &parent, &inherited); err != nil {
			return nil, err
		}
		head.Session.ParentSessionID = parent.String
		head.Session.InheritedSeq = inherited.Int64
		out = append(out, head)
	}
	return out, rows.Err()
}

// TextRows returns the rows of sessionID with seq above afterSeq in seq order,
// stopping before the total text would pass maxRunes runes (REQ-KNU-001,
// REQ-KNU-003). The first row is always returned when any row exists, even when
// it alone is larger than maxRunes; maxRunes <= 0 returns only that first row.
func (s *Store) TextRows(ctx context.Context, sessionID string, afterSeq int64, maxRunes int) ([]core.TextRow, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	limit := maxRunes + 1
	if limit < 1 {
		limit = 1
	}
	rows, err := s.textRows(ctx, "SELECT session_id, seq, role, created_at, text FROM session_text WHERE session_id = ? AND seq > ? ORDER BY seq LIMIT ?", sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	var out []core.TextRow
	total := 0
	for _, row := range rows {
		n := utf8.RuneCountInString(row.Text)
		if len(out) > 0 && (maxRunes <= 0 || total+n > maxRunes) {
			break
		}
		out = append(out, row)
		total += n
	}
	return out, nil
}

// callerUptoSeq returns the upto_seq of the latest compaction summary of the
// session, or 0 when the session has no summary (REQ-SSR-015).
func (s *Store) callerUptoSeq(ctx context.Context, sessionID string) (int64, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM session_events WHERE session_id = ? AND kind = ? ORDER BY seq DESC LIMIT 1", sessionID, core.KindCompactionSummary).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var summary core.CompactionSummary
	if err := json.Unmarshal([]byte(payload), &summary); err != nil {
		return 0, err
	}
	return summary.UptoSeq, nil
}

// textRowsBefore returns up to limit session_text rows with seq below aroundSeq,
// in ascending seq order. A non-zero upto caps the caller's own session.
func (s *Store) textRowsBefore(ctx context.Context, sessionID string, aroundSeq int64, limit int, upto int64) ([]core.TextRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	query := "SELECT session_id, seq, role, created_at, text FROM session_text WHERE session_id = ? AND seq < ?"
	args := []any{sessionID, aroundSeq}
	if upto > 0 {
		query += " AND seq <= ?"
		args = append(args, upto)
	}
	query += " ORDER BY seq DESC LIMIT ?"
	args = append(args, limit)
	out, err := s.textRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// textRowsAfter returns up to limit session_text rows with seq above aroundSeq,
// in ascending seq order. A non-zero upto caps the caller's own session.
func (s *Store) textRowsAfter(ctx context.Context, sessionID string, aroundSeq int64, limit int, upto int64) ([]core.TextRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	query := "SELECT session_id, seq, role, created_at, text FROM session_text WHERE session_id = ? AND seq > ?"
	args := []any{sessionID, aroundSeq}
	if upto > 0 {
		query += " AND seq <= ?"
		args = append(args, upto)
	}
	query += " ORDER BY seq LIMIT ?"
	args = append(args, limit)
	return s.textRows(ctx, query, args...)
}

func (s *Store) textRows(ctx context.Context, query string, args ...any) ([]core.TextRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []core.TextRow
	for rows.Next() {
		var row core.TextRow
		if err := rows.Scan(&row.SessionID, &row.Seq, &row.Role, &row.CreatedAt, &row.Text); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// matchExpression turns terms into an FTS5 MATCH string. Every term is a quoted
// string, so FTS5 operators and syntax characters in a term are searched as
// literal words and can never break the query (REQ-SSR-011). An inner double
// quote is doubled.
func matchExpression(terms []string) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " OR ")
}

var _ core.SessionStore = (*Store)(nil)
var _ core.LeaseProbe = (*Store)(nil)
var _ core.EventScanner = (*Store)(nil)
var _ core.AgentEventCounter = (*Store)(nil)
var _ core.RoomReadStore = (*Store)(nil)
var _ core.SessionSearcher = (*Store)(nil)
var _ core.SessionTextReader = (*Store)(nil)

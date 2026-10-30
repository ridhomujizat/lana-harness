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
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var (
	ErrSessionNotFound       = errors.New("session not found")
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

const schemaVersion = 3

var validKinds = map[string]bool{
	core.KindSessionCreated: true, core.KindTurnStart: true, core.KindTurnEnd: true,
	core.KindStepStart: true, core.KindStepEnd: true, core.KindSystemMessage: true,
	core.KindUserMessage: true, core.KindAssistantMessage: true, core.KindAssistantAttempt: true,
	core.KindToolCall: true, core.KindToolResult: true, core.KindApprovalAsked: true,
	core.KindApprovalDecided: true, core.KindApprovalPolicy: true, core.KindModelSelection: true,
	core.KindQuestionAsked: true, core.KindQuestionAnswered: true,
	core.KindCompactionSummary: true,
}

// Store is the SQLite implementation of core.SessionStore.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	path   string
	closed bool
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
	db, err := sql.Open("sqlite", dsn)
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
	names := map[int]string{1: "session_log", 2: "session_leases", 3: "session_status_waiting_input"}
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

// Close closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

func (s *Store) assertOpen() error {
	if s.closed {
		return ErrStoreClosed
	}
	return nil
}

func nowMS() int64 { return time.Now().UnixMilli() }

func (s *Store) withWrite(ctx context.Context, fn func(*sql.Conn) error) error {
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
	info := core.SessionInfo{ID: sessionID, Agent: in.Agent, Channel: in.Channel, Address: in.Address, Workspace: workspace, Status: "idle", ParentSessionID: in.ParentSessionID, InheritedSeq: in.InheritedSeq, CreatedAt: now, UpdatedAt: now}
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
		if _, err := conn.ExecContext(ctx, "INSERT INTO sessions (id, agent, channel, address, workspace, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'idle', NULLIF(?, ''), ?, 1, 0, ?, ?)", sessionID, in.Agent, in.Channel, in.Address, workspace, in.ParentSessionID, in.InheritedSeq, now, now); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, 1, ?, ?, NULL, ?)", sessionID, core.KindSessionCreated, string(payload), now); err != nil {
			return err
		}
		info.LastSeq = 1
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
	err := s.db.QueryRowContext(ctx, "SELECT id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at FROM sessions WHERE id = ?", sessionID).
		Scan(&info.ID, &info.Agent, &info.Channel, &info.Address, &info.Workspace, &info.Title, &info.Status, &parent, &inherited, &info.LastSeq, &info.TurnCount, &info.CreatedAt, &info.UpdatedAt)
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

// Append writes a batch of events atomically.
func (s *Store) Append(ctx context.Context, sessionID string, drafts []core.EventDraft) ([]core.SessionEvent, error) {
	if len(drafts) == 0 {
		return nil, errors.New("drafts are empty")
	}
	var events []core.SessionEvent
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
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
	if s.Hooks != nil && s.Hooks.SessionEvent != nil {
		for _, event := range events {
			s.Hooks.SessionEvent.Publish(event)
		}
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
	query := "SELECT id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at FROM sessions WHERE 1=1"
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
	if f.TopLevelOnly {
		query += " AND (parent_session_id IS NULL OR parent_session_id = '')"
	}
	query += " ORDER BY updated_at DESC"
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
		if err := rows.Scan(&info.ID, &info.Agent, &info.Channel, &info.Address, &info.Workspace, &info.Title, &info.Status, &parent, &inherited, &info.LastSeq, &info.TurnCount, &info.CreatedAt, &info.UpdatedAt); err != nil {
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
	created := core.SessionCreated{V: core.PayloadVersion, Agent: parent.Agent, Channel: parent.Channel, Address: parent.Address, Workspace: parent.Workspace, ParentSessionID: parentID, InheritedSeq: atSeq}
	payload, _ := json.Marshal(created)
	err = s.withWrite(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "INSERT INTO sessions (id, agent, channel, address, workspace, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'idle', ?, ?, 1, 0, ?, ?)", childID, parent.Agent, parent.Channel, parent.Address, parent.Workspace, parentID, atSeq, now, now); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, 1, ?, ?, NULL, ?)", childID, core.KindSessionCreated, string(payload), now)
		return err
	})
	if err != nil {
		return core.SessionInfo{}, err
	}
	return s.info(ctx, childID)
}

// Open opens a session with repair and a lease requirement.
func (s *Store) Open(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	info, err := s.info(ctx, sessionID)
	if err != nil {
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
	err = s.withWrite(ctx, func(conn *sql.Conn) error {
		var status string
		if err := conn.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = ?", sessionID).Scan(&status); err != nil {
			return err
		}
		if status != "running" && status != "waiting_input" {
			return nil
		}
		events, err := s.loadEvents(ctx, sessionID)
		if err != nil {
			return err
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
		seq := info.LastSeq
		now := nowMS()
		for requestID, callID := range asked {
			if decided[requestID] {
				continue
			}
			seq++
			payload, _ := json.Marshal(core.ApprovalDecided{V: 1, RequestID: requestID, CallID: callID, Outcome: core.OutcomeUnavailable, Reason: "interrupted"})
			if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULL, ?)", sessionID, seq, core.KindApprovalDecided, string(payload), now); err != nil {
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
			seq++
			payload, _ := json.Marshal(core.QuestionAnswered{V: 1, RequestID: requestID, CallID: callID, Outcome: core.QuestionStatusUnavailable, Responder: "none", Reason: core.QuestionReasonInterrupted})
			if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULL, ?)", sessionID, seq, core.KindQuestionAnswered, string(payload), now); err != nil {
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
		for callID, turnID := range calls {
			if results[callID] {
				continue
			}
			seq++
			payload, _ := json.Marshal(core.ToolResultPayload{V: 1, CallID: callID, Status: core.ToolStatusError, Output: "interrupted"})
			if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)", sessionID, seq, core.KindToolResult, string(payload), turnID, now); err != nil {
				return err
			}
		}
		{
			seq++
			payload, _ := json.Marshal(core.TurnEnd{V: 1, TurnID: "", Reason: "interrupted"})
			if _, err := conn.ExecContext(ctx, "INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULL, ?)", sessionID, seq, core.KindTurnEnd, string(payload), now); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "UPDATE sessions SET status = 'idle', last_seq = ?, updated_at = ? WHERE id = ?", seq, now, sessionID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return core.SessionInfo{}, err
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

var _ core.SessionStore = (*Store)(nil)
var _ core.LeaseProbe = (*Store)(nil)
var _ = strings.TrimSpace

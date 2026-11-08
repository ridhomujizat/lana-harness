package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lana.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func createSession(t *testing.T, s *Store) core.SessionInfo {
	t.Helper()
	info, err := s.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// TestScanEventsCoversCoreEventScanner covers the cross-session scan of the
// dashboard: kind filtering, the sinceMS bound, the agent join, and the
// reduced tool/result payload (design.md §2).
func TestScanEventsCoversCoreEventScanner(t *testing.T) {
	s, _ := openTest(t)
	first, err := s.Create(context.Background(), core.NewSession{Agent: "alice", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(context.Background(), core.NewSession{Agent: "bob", Channel: "web", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	longOutput := strings.Repeat("x", 500) + `"quoted"`
	if _, err := s.Append(context.Background(), first.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindToolCall, TurnID: "t1", Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}},
		{Kind: core.KindToolResult, TurnID: "t1", Payload: core.ToolResultPayload{V: 1, CallID: "c1", Status: core.ToolStatusError, Output: longOutput}},
		{Kind: core.KindTurnEnd, TurnID: "t1", Payload: core.TurnEnd{V: 1, TurnID: "t1", Reason: "end_turn"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(context.Background(), second.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}},
	}); err != nil {
		t.Fatal(err)
	}

	kinds := []string{core.KindTurnStart, core.KindToolCall, core.KindToolResult, core.KindTurnEnd}
	var got []core.ScannedEvent
	for event, err := range s.ScanEvents(context.Background(), kinds, 0) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if len(got) != 5 {
		t.Fatalf("scanned %d events, want 5: %+v", len(got), got)
	}
	agents := map[string]string{}
	var result core.ScannedEvent
	for _, event := range got {
		if event.Kind == core.KindSessionCreated {
			t.Fatalf("session/created was scanned")
		}
		agents[event.SessionID] = event.Agent
		if event.Kind == core.KindToolResult {
			result = event
		}
	}
	if agents[first.ID] != "alice" || agents[second.ID] != "bob" {
		t.Fatalf("agents = %+v", agents)
	}

	var reduced struct {
		CallID string `json:"call_id"`
		Status string `json:"status"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result.Payload, &reduced); err != nil {
		t.Fatalf("reduced payload %q: %v", result.Payload, err)
	}
	if reduced.CallID != "c1" || reduced.Status != "error" {
		t.Fatalf("reduced = %+v", reduced)
	}
	if len([]rune(reduced.Output)) > 300 {
		t.Fatalf("reduced output = %d runes, want <= 300", len([]rune(reduced.Output)))
	}

	if count := scanCount(t, s.ScanEvents(context.Background(), nil, 0)); count != 0 {
		t.Fatalf("empty kinds scanned %d events", count)
	}
	if count := scanCount(t, s.ScanEvents(context.Background(), kinds, nowMS()+100000)); count != 0 {
		t.Fatalf("future bound scanned %d events", count)
	}
}

func scanCount(t *testing.T, seq iter.Seq2[core.ScannedEvent, error]) int {
	t.Helper()
	count := 0
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	return count
}

func TestOpenCreatesWALDatabase(t *testing.T) {
	_, path := openTest(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("wal file missing: %v", err)
	}
}

func TestOpenRelativePath(t *testing.T) {
	if _, err := Open(context.Background(), "relative.db"); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	s, path := openTest(t)
	_ = s.Close()
	again, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	var version int
	if err := again.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d", version)
	}
}

func TestSchemaTooNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lana.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err=%v", err)
	}
}

type sessionRow struct {
	id, agent, channel, address, workspace, title, status string
	parent                                                sql.NullString
	inherited                                             sql.NullInt64
	lastSeq, turnCount, createdAt, updatedAt              int64
}

func readSessionRow(t *testing.T, db *sql.DB, sessionID string) sessionRow {
	t.Helper()
	var row sessionRow
	err := db.QueryRow("SELECT id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at FROM sessions WHERE id = ?", sessionID).
		Scan(&row.id, &row.agent, &row.channel, &row.address, &row.workspace, &row.title, &row.status, &row.parent, &row.inherited, &row.lastSeq, &row.turnCount, &row.createdAt, &row.updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

type eventRow struct {
	sessionID string
	seq       int64
	kind      string
	payload   string
	turnID    string
	createdAt int64
}

func readEventRows(t *testing.T, db *sql.DB) []eventRow {
	t.Helper()
	rows, err := db.Query("SELECT session_id, seq, kind, payload, COALESCE(turn_id, ''), created_at FROM session_events ORDER BY session_id, seq")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []eventRow
	for rows.Next() {
		var row eventRow
		if err := rows.Scan(&row.sessionID, &row.seq, &row.kind, &row.payload, &row.turnID, &row.createdAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigrateV2ToV3SessionsWaitingInput(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lana.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, name := range []string{"migrations/0001_session_log.sql", "migrations/0002_session_leases.sql"} {
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	var oldCreateSQL string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'").Scan(&oldCreateSQL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(oldCreateSQL, "waiting_input") {
		t.Fatalf("v2 fixture is not the old schema: %s", oldCreateSQL)
	}

	const parentID, childID = "session-parent", "session-child"
	const insertSession = `INSERT INTO sessions (id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := db.Exec(insertSession, parentID, "agent", "library", "addr", "/work", "Parent", "idle", nil, nil, 2, 1, 1000, 1100); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insertSession, childID, "agent", "library", "addr", "/work", "Child", "running", parentID, 2, 3, 1, 1200, 1300); err != nil {
		t.Fatal(err)
	}
	events := []eventRow{
		{parentID, 1, core.KindSessionCreated, `{"v":1,"agent":"agent"}`, "", 1000},
		{parentID, 2, core.KindTurnEnd, `{"v":1,"turn_id":"t1","reason":"end_turn"}`, "t1", 1100},
		{childID, 1, core.KindSessionCreated, `{"v":1,"agent":"agent"}`, "", 1200},
		{childID, 2, core.KindTurnStart, `{"v":1,"turn_id":"t2"}`, "t2", 1250},
		{childID, 3, core.KindUserMessage, `{"v":1,"content":[]}`, "t2", 1300},
	}
	for _, event := range events {
		if _, err := db.Exec("INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)", event.sessionID, event.seq, event.kind, event.payload, event.turnID, event.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE sessions SET status = 'waiting_input' WHERE id = ?", childID); err == nil {
		t.Fatal("v2 fixture unexpectedly accepts waiting_input")
	}

	parentBefore := readSessionRow(t, db, parentID)
	childBefore := readSessionRow(t, db, childID)
	eventsBefore := readEventRows(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}

	parentAfter := readSessionRow(t, s.db, parentID)
	childAfter := readSessionRow(t, s.db, childID)
	if parentAfter != parentBefore {
		t.Fatalf("parent changed during migration: before=%+v after=%+v", parentBefore, parentAfter)
	}
	if childAfter != childBefore {
		t.Fatalf("child changed during migration: before=%+v after=%+v", childBefore, childAfter)
	}
	if childAfter.parent.String != parentID || !childAfter.parent.Valid {
		t.Fatalf("child parent=%+v, want %s", childAfter.parent, parentID)
	}
	if eventsAfter := readEventRows(t, s.db); !reflect.DeepEqual(eventsAfter, eventsBefore) {
		t.Fatalf("events changed during migration: before=%+v after=%+v", eventsBefore, eventsAfter)
	}

	var indexCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_sessions_channel_address_updated'").Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("idx_sessions_channel_address_updated count=%d", indexCount)
	}
	var createSQL string
	if err := s.db.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'").Scan(&createSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(createSQL, "'waiting_input'") {
		t.Fatalf("sessions CREATE lacks waiting_input: %s", createSQL)
	}
	if !strings.Contains(createSQL, "REFERENCES sessions (id) ON DELETE RESTRICT") {
		t.Fatalf("sessions self-reference was not preserved: %s", createSQL)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status = 'waiting_input' WHERE id = ?", childID); err != nil {
		t.Fatalf("waiting_input rejected after migration: %v", err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status = 'bogus' WHERE id = ?", childID); err == nil {
		t.Fatal("invalid status accepted after migration")
	}

	// The append-only triggers and the self-referencing foreign key must survive
	// the table rebuild.
	if _, err := s.db.Exec("UPDATE session_events SET kind = 'x' WHERE session_id = ?", childID); err == nil {
		t.Fatal("append-only trigger lost during migration")
	}
	if _, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", parentID); err == nil {
		t.Fatal("parent with child deleted: foreign key lost during migration")
	}
}

func TestRawUpdateDeleteRejected(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.db.Exec("UPDATE session_events SET kind = 'x' WHERE session_id = ?", info.ID); err == nil {
		t.Fatal("update accepted")
	}
	if _, err := s.db.Exec("DELETE FROM session_events WHERE session_id = ?", info.ID); err == nil {
		t.Fatal("delete accepted")
	}
}

func TestCreateWritesSessionCreated(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	events, err := s.loadEvents(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Seq != 1 || events[0].Kind != core.KindSessionCreated {
		t.Fatalf("events=%+v", events)
	}
	var payload core.SessionCreated
	_ = json.Unmarshal(events[0].Payload, &payload)
	if payload.Workspace == "" || payload.Channel != "library" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestAppendSeq(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	events, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("seq=%v", events)
	}
}

func TestAppendBatchAtomic(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	_, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: "unknown/kind", Payload: core.TurnStart{V: 1}},
	})
	if err == nil {
		t.Fatal("unknown kind accepted")
	}
	events, _ := s.loadEvents(context.Background(), info.ID)
	if len(events) != 1 {
		t.Fatalf("batch was not rolled back: %+v", events)
	}
}

func TestTurnStartRejectedWhileOpen(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}}}); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("err=%v", err)
	}
}

func TestConcurrentAppendNoBusy(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}}})
		}()
	}
	wg.Wait()
	events, _ := s.loadEvents(context.Background(), info.ID)
	if len(events) != 21 {
		t.Fatalf("events=%d", len(events))
	}
}

func TestTwoInstancesSeqUnique(t *testing.T) {
	s, path := openTest(t)
	info := createSession(t, s)
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}}})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = second.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}}})
		}()
	}
	wg.Wait()
	events, _ := s.loadEvents(context.Background(), info.ID)
	if len(events) != 21 {
		t.Fatalf("events=%d", len(events))
	}
}

func TestLease(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	release, err := s.TryLease(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TryLease(context.Background(), info.ID); err == nil {
		t.Fatal("second lease accepted")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(context.Background(), info.ID); !errors.Is(err, ErrLeaseNotHeld) {
		t.Fatalf("open without lease: %v", err)
	}
}

func TestLeaseDeadReplaced(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	old := pidAlive
	pidAlive = func(int) bool { return false }
	defer func() { pidAlive = old }()
	if _, err := s.db.Exec("INSERT INTO session_leases (session_id, pid, host, acquired_at) VALUES (?, 999999, ?, 1)", info.ID, currentHost()); err != nil {
		t.Fatal(err)
	}
	release, err := s.TryLease(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = release()
}

func TestResumeRepairOrphanCall(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	release, _ := s.TryLease(context.Background(), info.ID)
	defer func() { _ = release() }()
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindToolCall, TurnID: "t", Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	repaired, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != "idle" {
		t.Fatalf("status=%q", repaired.Status)
	}
	events, _ := s.loadEvents(context.Background(), info.ID)
	if events[len(events)-1].Kind != core.KindTurnEnd {
		t.Fatalf("last event=%s", events[len(events)-1].Kind)
	}
	before := len(events)
	if _, err := s.Open(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	events, _ = s.loadEvents(context.Background(), info.ID)
	if len(events) != before {
		t.Fatalf("repair is not idempotent: %d -> %d", before, len(events))
	}
}

func TestForkAtTurnEnd(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	events, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	at := events[1].Seq
	child, err := s.Fork(context.Background(), info.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentSessionID != info.ID || child.InheritedSeq != at {
		t.Fatalf("child=%+v", child)
	}
	if _, err := s.Fork(context.Background(), info.ID, events[0].Seq); !errors.Is(err, ErrForkNotAtTurnBoundary) {
		t.Fatalf("err=%v", err)
	}
}

func TestEventsFromSeq(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	for i := 0; i < 3; i++ {
		_, _ = s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}}})
	}
	var seqs []int64
	for event, err := range s.Events(context.Background(), info.ID, 3) {
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, event.Seq)
	}
	if len(seqs) != 2 || seqs[0] != 3 {
		t.Fatalf("seqs=%v", seqs)
	}
}

func TestListOrderAndLimit(t *testing.T) {
	s, _ := openTest(t)
	a := createSession(t, s)
	b := createSession(t, s)
	_, _ = s.Append(context.Background(), a.ID, []core.EventDraft{{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}}})
	// The append bumps updated_at, but both sessions can share the same
	// millisecond, so force a strict ordering for the assertion.
	if _, err := s.db.Exec("UPDATE sessions SET updated_at = updated_at + 1 WHERE id = ?", a.ID); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(context.Background(), core.SessionFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list=%+v b=%s", list, b.ID)
	}
	if _, err := s.List(context.Background(), core.SessionFilter{Limit: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestListFiltersByParentSessionID(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	parent := createSession(t, s)
	other := createSession(t, s)
	child, err := s.Create(ctx, core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir(), ParentSessionID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir(), ParentSessionID: other.ID}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, core.SessionFilter{ParentSessionID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != child.ID {
		t.Fatalf("list=%+v child=%s", list, child.ID)
	}
}

func TestListTopLevelOnly(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	root := createSession(t, s)
	child, err := s.Create(ctx, core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir(), ParentSessionID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, core.SessionFilter{TopLevelOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != root.ID {
		t.Fatalf("list=%+v root=%s child=%s", list, root.ID, child.ID)
	}
	all, err := s.List(ctx, core.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("zero-value filter returned %d sessions, want 2", len(all))
	}
}

func TestFindPrefixCaseInsensitive(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	matches, err := s.FindPrefix(context.Background(), strings.ToLower(info.ID[:8]))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != info.ID {
		t.Fatalf("matches=%+v", matches)
	}
	resolved, err := core.ResolvePrefix(matches, strings.ToLower(info.ID[:8]))
	if err != nil || resolved.ID != info.ID {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}

func TestPrune(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	if err := s.Prune(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Info(context.Background(), info.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPruneWithChildren(t *testing.T) {
	s, _ := openTest(t)
	parent := createSession(t, s)
	events, _ := s.Append(context.Background(), parent.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	if _, err := s.Fork(context.Background(), parent.ID, events[1].Seq); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(context.Background(), parent.ID); !errors.Is(err, ErrSessionHasChildren) {
		t.Fatalf("err=%v", err)
	}
}

func TestRebuildProjectionsIdentical(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	_, _ = s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	before, _ := s.Info(context.Background(), info.ID)
	if err := s.RebuildProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Info(context.Background(), info.ID)
	if before.LastSeq != after.LastSeq || before.TurnCount != after.TurnCount || before.Status != after.Status {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}

func TestUnknownPayloadVersion(t *testing.T) {
	if _, err := NormalizePayload(core.KindUserMessage, json.RawMessage(`{"v":99}`)); !errors.Is(err, core.ErrUnknownPayloadVersion) {
		t.Fatalf("err=%v", err)
	}
}

func TestOldPayloadMigratedInMemory(t *testing.T) {
	raw, err := NormalizePayload(core.KindUserMessage, json.RawMessage(`{"v":0,"content":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	if value["v"].(float64) != 1 {
		t.Fatalf("raw=%s", raw)
	}
}
func TestModuleStoreDefaultPath(t *testing.T) {
	home := t.TempDir()
	ctx, err := core.Boot(context.Background(), core.BootInput{Config: core.Config{Modules: []core.ModuleRow{{ID: "store", Module: "sqlite_store"}}}, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctx.Shutdown(context.Background()) }()
	if _, err := os.Stat(filepath.Join(home, "lana.db")); err != nil {
		t.Fatalf("default database not created: %v", err)
	}
}
func TestResumeRepairOrphanApproval(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	release, _ := s.TryLease(context.Background(), info.ID)
	defer func() { _ = release() }()
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindApprovalAsked, TurnID: "t", Payload: core.ApprovalAsked{V: 1, RequestID: "r1", CallID: "c1", Tool: "write_file", Risk: core.RiskWrite}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	events, _ := s.loadEvents(context.Background(), info.ID)
	var decided bool
	var turnEnd bool
	for _, event := range events {
		if event.Kind == core.KindApprovalDecided {
			decided = true
		}
		if event.Kind == core.KindTurnEnd {
			turnEnd = true
		}
	}
	if !decided || !turnEnd {
		t.Fatalf("approval repair incomplete")
	}
}

func TestResumeRepairOrphanQuestion(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	release, _ := s.TryLease(context.Background(), info.ID)
	defer func() { _ = release() }()
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindQuestionAsked, TurnID: "t", Payload: core.QuestionAsked{V: 1, RequestID: "q1", CallID: "c1", ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli()}},
	}); err != nil {
		t.Fatal(err)
	}
	open, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open.Status != "waiting_input" {
		t.Fatalf("status before open=%q", open.Status)
	}
	repaired, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != "idle" {
		t.Fatalf("status after open=%q", repaired.Status)
	}
	events, _ := s.loadEvents(context.Background(), info.ID)
	var answered *core.SessionEvent
	var turnEndSeq int64
	for i := range events {
		if events[i].Kind == core.KindQuestionAnswered {
			answered = &events[i]
		}
		if events[i].Kind == core.KindTurnEnd {
			turnEndSeq = events[i].Seq
		}
	}
	if answered == nil {
		t.Fatal("orphaned question was not closed")
	}
	if answered.Seq >= turnEndSeq {
		t.Fatalf("question/answered seq=%d is not before turn/end seq=%d", answered.Seq, turnEndSeq)
	}
	var payload core.QuestionAnswered
	if err := json.Unmarshal(answered.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RequestID != "q1" || payload.CallID != "c1" || payload.Outcome != core.QuestionStatusUnavailable || payload.Reason != core.QuestionReasonInterrupted || payload.Responder != "none" {
		t.Fatalf("payload=%+v", payload)
	}
	before := len(events)
	if _, err := s.Open(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	events, _ = s.loadEvents(context.Background(), info.ID)
	if len(events) != before {
		t.Fatalf("repair is not idempotent: %d -> %d", before, len(events))
	}
}

func TestProjectionQuestionWaitingThenRunning(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindQuestionAsked, TurnID: "t", Payload: core.QuestionAsked{V: 1, RequestID: "q1", CallID: "c1"}},
	}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_input" {
		t.Fatalf("status after ask=%q", waiting.Status)
	}
	if err := s.RebuildProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	waiting, err = s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_input" {
		t.Fatalf("projection after ask=%q", waiting.Status)
	}
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindQuestionAnswered, TurnID: "t", Payload: core.QuestionAnswered{V: 1, RequestID: "q1", CallID: "c1", Outcome: core.QuestionStatusAnswered, Responder: "web"}},
	}); err != nil {
		t.Fatal(err)
	}
	running, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != "running" {
		t.Fatalf("status after answer=%q", running.Status)
	}
	if err := s.RebuildProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	running, err = s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != "running" {
		t.Fatalf("projection after answer=%q", running.Status)
	}
}

func TestTurnStartRejectedWhileWaitingInput(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindQuestionAsked, TurnID: "t1", Payload: core.QuestionAsked{V: 1, RequestID: "q1", CallID: "c1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}}}); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("err=%v", err)
	}
	still, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != "waiting_input" {
		t.Fatalf("status after refused turn=%q", still.Status)
	}
}

func TestBroadcastAfterCommit(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	hooks := core.NewHooks()
	s.Hooks = hooks
	sub := hooks.SessionEvent.Subscribe()
	defer sub.Close()
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}}}); err != nil {
		t.Fatal(err)
	}
	received, err := sub.Recv(context.Background())
	if err != nil || received.Value.Kind != core.KindUserMessage {
		t.Fatalf("received=%+v err=%v", received, err)
	}
}

func TestNoBroadcastOnFailure(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	hooks := core.NewHooks()
	s.Hooks = hooks
	sub := hooks.SessionEvent.Subscribe()
	defer sub.Close()
	_, _ = s.Append(context.Background(), info.ID, []core.EventDraft{{Kind: "unknown/kind"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := sub.Recv(ctx); err == nil {
		t.Fatal("event sent even though append failed")
	}
}

func TestLeaseOtherHost(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.db.Exec("INSERT INTO session_leases (session_id, pid, host, acquired_at) VALUES (?, ?, 'other-host', 1)", info.ID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	_, err := s.TryLease(context.Background(), info.ID)
	var held *core.LeaseHeldError
	if !errors.As(err, &held) || held.Host != "other-host" {
		t.Fatalf("err=%v", err)
	}
}

// REQ-ATT-004: a tool/result event keeps its images through Append and derive.
func TestToolResultImagesSurviveStore(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	image := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("png-bytes"), Path: "attachments/a.png"}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
		}}},
		{Kind: core.KindToolResult, TurnID: "t", Payload: core.ToolResultPayload{V: 1, CallID: "c1", Status: core.ToolStatusSuccess, Output: "Image a.png (2x2).", Images: []core.ContentBlock{image}}},
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := s.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
	block := messages[1].Content[0]
	if block.Kind != core.BlockToolResult || len(block.Images) != 1 || !reflect.DeepEqual(block.Images[0], image) {
		t.Fatalf("block=%+v", block)
	}
}

// REQ-ATT-002: a user/message image block keeps its media type, data, and path.
func TestUserMessageImageSurvivesStore(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	image := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/jpeg", Data: []byte("jpeg-bytes"), Path: "attachments/2026-11-01/a.jpg"}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "look"},
			image,
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := s.DeriveMessages(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 2 {
		t.Fatalf("messages=%+v", messages)
	}
	got := messages[0].Content[1]
	if got.Kind != core.BlockImage || got.MediaType != image.MediaType || !bytes.Equal(got.Data, image.Data) || got.Path != image.Path {
		t.Fatalf("image block=%+v", got)
	}
}

func readSessionText(t *testing.T, db *sql.DB, sessionID string) []core.TextRow {
	t.Helper()
	rows, err := db.Query("SELECT session_id, seq, role, created_at, text FROM session_text WHERE session_id = ? ORDER BY seq", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []core.TextRow
	for rows.Next() {
		var row core.TextRow
		if err := rows.Scan(&row.SessionID, &row.Seq, &row.Role, &row.CreatedAt, &row.Text); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func appendUserText(t *testing.T, s *Store, sessionID, turnID, text string) core.SessionEvent {
	t.Helper()
	events, err := s.Append(context.Background(), sessionID, []core.EventDraft{{
		Kind: core.KindUserMessage, TurnID: turnID,
		Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return events[0]
}

// AC-SSR-001 (REQ-SSR-001, REQ-SSR-002): a user and an assistant message become
// searchable as they are appended; a runtime note, an injected context block,
// and a non-text block are not indexed; pruning removes the session's rows.
func TestSessionTextAppendIndexesConversationText(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Runtime: true, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "runtime note deploy"}}}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, ContextBlocks: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "context secret"},
			{Kind: core.BlockText, Text: "deploy staging"},
		}}},
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockThinking, Text: "thinking hard"},
			{Kind: core.BlockText, Text: "staging done"},
		}}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("x")},
		}}},
	}); err != nil {
		t.Fatal(err)
	}

	indexed := readSessionText(t, s.db, info.ID)
	if len(indexed) != 2 {
		t.Fatalf("indexed %d rows, want 2: %+v", len(indexed), indexed)
	}
	if indexed[0].Seq != 4 || indexed[0].Role != "user" || indexed[0].Text != "deploy staging" {
		t.Fatalf("first indexed row = %+v", indexed[0])
	}
	if indexed[1].Seq != 5 || indexed[1].Role != "assistant" || indexed[1].Text != "staging done" {
		t.Fatalf("second indexed row = %+v", indexed[1])
	}

	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"deploy"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != info.ID || hits[0].Row.Seq != 4 {
		t.Fatalf("hits=%+v", hits)
	}
	hidden, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"secret"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 0 {
		t.Fatalf("context block was indexed: %+v", hidden)
	}

	if err := s.Prune(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if rows := readSessionText(t, s.db, info.ID); len(rows) != 0 {
		t.Fatalf("prune left %d rows: %+v", len(rows), rows)
	}
}

// v3Database builds a schema-version-3 database the way the previous binary left
// it: migrations 0001-0003 applied and user_version set to 3.
func v3Database(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lana.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/0001_session_log.sql", "migrations/0002_session_leases.sql", "migrations/0003_session_status_waiting_input.sql"} {
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// AC-SSR-002 (REQ-SSR-003): a version-3 database with events migrates to
// version 4 and its old conversation text is found.
func TestMigrateV3ToV4BackfillsSessionText(t *testing.T) {
	ctx := context.Background()
	db, path := v3Database(t)
	const sessionID = "SESSIONV3"
	if _, err := db.Exec("INSERT INTO sessions (id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, 'lana', 'web', 'addr', '/work', 'Old', 'idle', NULL, NULL, 3, 1, 1000, 1200)", sessionID); err != nil {
		t.Fatal(err)
	}
	events := []eventRow{
		{sessionID, 1, core.KindSessionCreated, `{"v":1,"agent":"lana"}`, "", 1000},
		{sessionID, 2, core.KindUserMessage, `{"v":1,"content":[{"type":"text","text":"old deploy staging message"}]}`, "t1", 1100},
		{sessionID, 3, core.KindAssistantMessage, `{"v":1,"content":[{"type":"text","text":"old staging reply"}]}`, "t1", 1200},
	}
	for _, event := range events {
		if _, err := db.Exec("INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)", event.sessionID, event.seq, event.kind, event.payload, event.turnID, event.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}

	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"deploy"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != sessionID || hits[0].Row.Seq != 2 || hits[0].Row.Role != "user" {
		t.Fatalf("hits=%+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "deploy") {
		t.Fatalf("snippet=%q", hits[0].Snippet)
	}
	reply, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"reply"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply) != 1 || reply[0].Row.Role != "assistant" || reply[0].Row.Seq != 3 {
		t.Fatalf("reply=%+v", reply)
	}
}

// REQ-SSR-011: a message holding two query terms ranks above one holding a
// single term.
func TestSearchTextRanksMoreTermsFirst(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	both := createSession(t, s)
	one := createSession(t, s)
	appendUserText(t, s, both.ID, "t", "alpha deploy staging")
	appendUserText(t, s, one.ID, "t", "alpha deploy beta")

	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"deploy", "staging"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Session.ID != both.ID {
		t.Fatalf("hits=%+v both=%s", hits, both.ID)
	}
}

// REQ-SSR-014: a subagent session (parent set, no inherited_seq) is not
// searchable, while a fork (parent set, inherited_seq > 0) is.
func TestSearchTextSkipsSubagentsAndKeepsForks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	parent := createSession(t, s)
	sub, err := s.Create(ctx, core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir(), ParentSessionID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	appendUserText(t, s, sub.ID, "t", "subagent needle magnetic")
	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"magnetic"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("subagent session was searchable: %+v", hits)
	}
	// A NULL inherited_seq is excluded the same way.
	if _, err := s.db.Exec("UPDATE sessions SET inherited_seq = NULL WHERE id = ?", sub.ID); err != nil {
		t.Fatal(err)
	}
	hits, err = s.SearchText(ctx, core.TextQuery{Terms: []string{"magnetic"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("NULL inherited_seq session was searchable: %+v", hits)
	}

	events, err := s.Append(ctx, parent.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Fork(ctx, parent.ID, events[1].Seq)
	if err != nil {
		t.Fatal(err)
	}
	appendUserText(t, s, child.ID, "t", "fork needle radar")
	hits, err = s.SearchText(ctx, core.TextQuery{Terms: []string{"radar"}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != child.ID || hits[0].Session.InheritedSeq != events[1].Seq {
		t.Fatalf("fork hits=%+v child=%s", hits, child.ID)
	}
}

// REQ-SSR-015: in the caller's own session only messages at or below the latest
// compaction summary's upto_seq are returned; a session with no summary returns
// no hits for itself.
func TestSearchTextCallerUptoRule(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	caller := createSession(t, s)
	other := createSession(t, s)
	if _, err := s.Append(ctx, caller.ID, []core.EventDraft{
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "needle alpha"}}}},
		{Kind: core.KindCompactionSummary, Payload: core.CompactionSummary{V: 1, FromSeq: 1, UptoSeq: 2}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "needle beta"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	appendUserText(t, s, other.ID, "t", "needle gamma")

	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"needle"}, CallerSession: caller.ID, Limit: 10, Context: 0})
	if err != nil {
		t.Fatal(err)
	}
	var sawCaller, sawOther bool
	for _, hit := range hits {
		if hit.Session.ID == caller.ID {
			sawCaller = true
			if hit.Row.Seq > 2 {
				t.Fatalf("caller row above upto returned: %+v", hit.Row)
			}
		}
		if hit.Session.ID == other.ID {
			sawOther = true
		}
	}
	if !sawCaller || !sawOther {
		t.Fatalf("caller=%v other=%v hits=%+v", sawCaller, sawOther, hits)
	}

	none := createSession(t, s)
	appendUserText(t, s, none.ID, "t", "needle epsilon")
	hits, err = s.SearchText(ctx, core.TextQuery{Terms: []string{"needle"}, CallerSession: none.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		if hit.Session.ID == none.ID {
			t.Fatalf("session without compaction returned its own row: %+v", hit)
		}
	}
}

// REQ-SSR-012: the first hit carries up to Context rows on each side of the
// matching message, in seq order, the hit included.
func TestSearchTextFillsAroundForFirstHit(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	for _, text := range []string{"one", "two", "deploy needle", "three", "four"} {
		appendUserText(t, s, info.ID, "t", text)
	}
	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{"needle"}, Limit: 5, Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits=%+v", hits)
	}
	var seqs []int64
	for _, row := range hits[0].Around {
		seqs = append(seqs, row.Seq)
	}
	if !reflect.DeepEqual(seqs, []int64{3, 4, 5}) {
		t.Fatalf("around seqs=%v, want [3 4 5]", seqs)
	}
	if hits[0].Around[1].Text != "deploy needle" {
		t.Fatalf("hit row missing from around: %+v", hits[0].Around)
	}
}

// REQ-SSR-013: TextWindow returns rows in seq order around a seq, resolves a
// unique prefix, and reports unknown and ambiguous ids.
func TestTextWindowOrderAndPrefix(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		appendUserText(t, s, info.ID, "t", text)
	}
	_, rows, err := s.TextWindow(ctx, info.ID, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, row := range rows {
		seqs = append(seqs, row.Seq)
	}
	if !reflect.DeepEqual(seqs, []int64{2, 3, 5, 6}) {
		t.Fatalf("window seqs=%v, want [2 3 5 6]", seqs)
	}
	if rows[0].SessionID != info.ID || rows[0].Role != "user" {
		t.Fatalf("row=%+v", rows[0])
	}

	resolved, prefixRows, err := s.TextWindow(ctx, strings.ToLower(info.ID[:8]), 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != info.ID || len(prefixRows) != 2 {
		t.Fatalf("resolved=%s rows=%+v", resolved.ID, prefixRows)
	}

	if _, _, err := s.TextWindow(ctx, "NOSUCHPREFIX", 4, 1); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown err=%v", err)
	}

	const insertSession = "INSERT INTO sessions (id, agent, channel, address, workspace, title, status, parent_session_id, inherited_seq, last_seq, turn_count, created_at, updated_at) VALUES (?, 'a', 'library', 'addr', '/work', '', 'idle', NULL, NULL, 1, 0, 1000, 1000)"
	for _, id := range []string{"ZZTESTAAA000000000000000001", "ZZTESTAAA000000000000000002"} {
		if _, err := s.db.Exec(insertSession, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.TextWindow(ctx, "ZZTEST", 1, 1); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous err=%v", err)
	}
	if _, _, err := s.TextWindow(ctx, "ZZTESTAAA000000000000000001", 1, 1); err != nil {
		t.Fatalf("full id err=%v", err)
	}
}

// REQ-SSR-011: FTS syntax characters and operators in a term are searched as
// literal words and can never break the query.
func TestSearchTextMatchIsSafe(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	plain := createSession(t, s)
	operator := createSession(t, s)
	appendUserText(t, s, plain.ID, "t", "deploy staging")
	appendUserText(t, s, operator.ID, "t", "we or they")

	if hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{`"(*`}, Limit: 5}); err != nil || len(hits) != 0 {
		t.Fatalf("quote term err=%v hits=%+v", err, hits)
	}
	hits, err := s.SearchText(ctx, core.TextQuery{Terms: []string{`"(*`, "OR"}, Limit: 5})
	if err != nil {
		t.Fatalf("OR term err=%v", err)
	}
	if len(hits) != 1 || hits[0].Session.ID != operator.ID {
		t.Fatalf("OR was not searched as a literal word: %+v", hits)
	}
	hits, err = s.SearchText(ctx, core.TextQuery{Terms: []string{`"(*deploy`, "staging"}, Limit: 5})
	if err != nil {
		t.Fatalf("mixed term err=%v", err)
	}
	if len(hits) != 1 || hits[0].Session.ID != plain.ID {
		t.Fatalf("mixed hits=%+v", hits)
	}
}

// REQ-KNU-001, REQ-KNU-003: TextHeads lists searchable sessions that have
// conversation text with their newest text seq: the root and the fork are
// included, a subagent session is not, and the session info is filled.
func TestTextHeads(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	root, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "telegram", Address: "addr-1", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET title = ? WHERE id = ?", "Deploy staging", root.ID); err != nil {
		t.Fatal(err)
	}
	_ = appendUserText(t, s, root.ID, "t", "first")
	last := appendUserText(t, s, root.ID, "t", "second")

	fork, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "web", Address: "addr-2", Workspace: t.TempDir(), ParentSessionID: root.ID, InheritedSeq: last.Seq})
	if err != nil {
		t.Fatal(err)
	}
	forkRow := appendUserText(t, s, fork.ID, "t", "forked")

	sub, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "web", Workspace: t.TempDir(), ParentSessionID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	appendUserText(t, s, sub.ID, "t", "subagent text")

	heads, err := s.TextHeads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 2 {
		t.Fatalf("heads=%+v", heads)
	}
	byID := map[string]core.TextHead{}
	for _, head := range heads {
		byID[head.Session.ID] = head
	}
	rhead, ok := byID[root.ID]
	if !ok || rhead.MaxSeq != last.Seq {
		t.Fatalf("root head=%+v want MaxSeq=%d", rhead, last.Seq)
	}
	if rhead.Session.Agent != "lana" || rhead.Session.Channel != "telegram" || rhead.Session.Title != "Deploy staging" || rhead.Session.Address != "addr-1" {
		t.Fatalf("root session info=%+v", rhead.Session)
	}
	fhead, ok := byID[fork.ID]
	if !ok || fhead.MaxSeq != forkRow.Seq {
		t.Fatalf("fork head=%+v want MaxSeq=%d", fhead, forkRow.Seq)
	}
	if fhead.Session.ParentSessionID != root.ID || fhead.Session.InheritedSeq != last.Seq {
		t.Fatalf("fork session info=%+v", fhead.Session)
	}
	if _, ok := byID[sub.ID]; ok {
		t.Fatalf("subagent session was included: %+v", heads)
	}
}

// REQ-KNU-001, REQ-KNU-003: TextRows returns rows in seq order, afterSeq skips
// earlier rows, the rune cap stops before the row that would exceed it, and a
// first row larger than the cap is returned alone.
func TestTextRows(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	first := appendUserText(t, s, info.ID, "t", "alpha") // 5 runes
	second := appendUserText(t, s, info.ID, "t", "beta") // 4 runes
	third := appendUserText(t, s, info.ID, "t", "gamma") // 5 runes

	all, err := s.TextRows(ctx, info.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, row := range all {
		seqs = append(seqs, row.Seq)
	}
	if !reflect.DeepEqual(seqs, []int64{first.Seq, second.Seq, third.Seq}) {
		t.Fatalf("seqs=%v, want %v", seqs, []int64{first.Seq, second.Seq, third.Seq})
	}
	if all[0].SessionID != info.ID || all[0].Role != "user" || all[0].Text != "alpha" {
		t.Fatalf("row=%+v", all[0])
	}

	after, err := s.TextRows(ctx, info.ID, second.Seq, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Seq != third.Seq {
		t.Fatalf("after=%+v", after)
	}

	capped, err := s.TextRows(ctx, info.ID, 0, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 || capped[0].Seq != first.Seq || capped[1].Seq != second.Seq {
		t.Fatalf("capped=%+v", capped)
	}

	tight, err := s.TextRows(ctx, info.ID, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(tight) != 1 || tight[0].Seq != first.Seq {
		t.Fatalf("tight=%+v", tight)
	}

	one, err := s.TextRows(ctx, info.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Seq != first.Seq || one[0].Text != "alpha" {
		t.Fatalf("one=%+v", one)
	}

	none, err := s.TextRows(ctx, info.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 1 || none[0].Seq != first.Seq {
		t.Fatalf("none=%+v", none)
	}
}

// REQ-ROOM-002: the session list filters by agent exactly and pages by last
// update time, newest activity first.
func TestListFilterAgentAndBefore(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	dana, err := s.Create(ctx, core.NewSession{Agent: "dana", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	karin, err := s.Create(ctx, core.NewSession{Agent: "karin", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	lana, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	setTimes := func(id string, created, updated int64) {
		t.Helper()
		if _, err := s.db.Exec("UPDATE sessions SET created_at = ?, updated_at = ? WHERE id = ?", created, updated, id); err != nil {
			t.Fatal(err)
		}
	}
	// dana was created first but is the most recently active, so a created_at
	// filter would keep it in every page.
	setTimes(dana.ID, 100, 400)
	setTimes(karin.ID, 200, 200)
	setTimes(lana.ID, 300, 100)

	byAgent, err := s.List(ctx, core.SessionFilter{Agent: "dana"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAgent) != 1 || byAgent[0].ID != dana.ID {
		t.Fatalf("byAgent=%+v want dana=%s", byAgent, dana.ID)
	}

	byBefore, err := s.List(ctx, core.SessionFilter{Before: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(byBefore) != 2 || byBefore[0].ID != karin.ID || byBefore[1].ID != lana.ID {
		t.Fatalf("byBefore=%+v want karin=%s lana=%s", byBefore, karin.ID, lana.ID)
	}

	none, err := s.List(ctx, core.SessionFilter{Agent: "dana", Before: 400})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("agent with before returned %+v", none)
	}
}

// REQ-ROOM-002: ExcludeChannels drops sessions on the listed channels, and the
// Limit applies after the exclusion so a page still returns up to Limit kept
// sessions.
func TestListExcludeChannels(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	dana, err := s.Create(ctx, core.NewSession{Agent: "dana", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	karin, err := s.Create(ctx, core.NewSession{Agent: "karin", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "workflow", Address: "wf1", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	setTimes := func(id string, created, updated int64) {
		t.Helper()
		if _, err := s.db.Exec("UPDATE sessions SET created_at = ?, updated_at = ? WHERE id = ?", created, updated, id); err != nil {
			t.Fatal(err)
		}
	}
	// The workflow session is the most recently active, so it would lead every
	// page were it not excluded.
	setTimes(dana.ID, 100, 300)
	setTimes(karin.ID, 200, 200)
	setTimes(wf.ID, 300, 400)

	all, err := s.List(ctx, core.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != wf.ID {
		t.Fatalf("all=%+v want wf=%s first", all, wf.ID)
	}

	kept, err := s.List(ctx, core.SessionFilter{ExcludeChannels: []string{"workflow"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].ID != dana.ID || kept[1].ID != karin.ID {
		t.Fatalf("kept=%+v want dana=%s karin=%s", kept, dana.ID, karin.ID)
	}

	limited, err := s.List(ctx, core.SessionFilter{ExcludeChannels: []string{"workflow"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ID != dana.ID {
		t.Fatalf("limited=%+v want dana=%s", limited, dana.ID)
	}
}

// REQ-ROOM-002, REQ-ROOM-005: CountAgentEvents counts the events of one agent,
// counts top-level sessions only on request, filters by kind and by creation
// time, and returns 0 for an empty kinds slice without querying.
func TestCountAgentEvents(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	dana, err := s.Create(ctx, core.NewSession{Agent: "dana", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Create(ctx, core.NewSession{Agent: "dana", Channel: "web", Address: "owner", Workspace: t.TempDir(), ParentSessionID: dana.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, core.NewSession{Agent: "karin", Channel: "web", Address: "owner", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	events := []struct {
		session string
		kind    string
		at      int64
	}{
		{dana.ID, core.KindAssistantMessage, 100},
		{dana.ID, core.KindQuestionAsked, 200},
		{dana.ID, core.KindTurnEnd, 300},
		{child.ID, core.KindAssistantMessage, 150},
		{other.ID, core.KindAssistantMessage, 150},
	}
	// Create writes session/created at seq 1, so the fixture events start at 2.
	seq := map[string]int64{dana.ID: 1, child.ID: 1, other.ID: 1}
	for _, event := range events {
		seq[event.session]++
		if _, err := s.db.Exec("INSERT INTO session_events (session_id, seq, kind, payload, turn_id, created_at) VALUES (?, ?, ?, '{}', NULL, ?)", event.session, seq[event.session], event.kind, event.at); err != nil {
			t.Fatal(err)
		}
	}

	kinds := []string{core.KindAssistantMessage, core.KindQuestionAsked}
	cases := []struct {
		name  string
		agent string
		kinds []string
		since int64
		top   bool
		want  int64
	}{
		{"all sessions", "dana", kinds, 0, false, 3},
		{"top level only", "dana", kinds, 0, true, 2},
		{"kind filter", "dana", []string{core.KindAssistantMessage}, 0, false, 2},
		{"since filter", "dana", kinds, 120, false, 2},
		{"other agent", "karin", kinds, 0, false, 1},
		{"empty kinds", "dana", nil, 0, false, 0},
	}
	for _, tc := range cases {
		got, err := s.CountAgentEvents(ctx, tc.agent, tc.kinds, tc.since, tc.top)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// REQ-ROOM-005: a room read mark only moves forward, and an unknown room reads
// as 0.
func TestRoomReadsUpsertKeepsMax(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if at, err := s.RoomReadAt(ctx, "dana"); err != nil || at != 0 {
		t.Fatalf("unknown room read_at=%d err=%v, want 0", at, err)
	}
	stored, err := s.SetRoomRead(ctx, "dana", 1000)
	if err != nil || stored != 1000 {
		t.Fatalf("first set stored=%d err=%v, want 1000", stored, err)
	}
	stored, err = s.SetRoomRead(ctx, "dana", 500)
	if err != nil || stored != 1000 {
		t.Fatalf("older set stored=%d err=%v, want 1000", stored, err)
	}
	if at, err := s.RoomReadAt(ctx, "dana"); err != nil || at != 1000 {
		t.Fatalf("read_at=%d err=%v, want 1000", at, err)
	}
	stored, err = s.SetRoomRead(ctx, "dana", 2000)
	if err != nil || stored != 2000 {
		t.Fatalf("newer set stored=%d err=%v, want 2000", stored, err)
	}
	if at, err := s.RoomReadAt(ctx, "dana"); err != nil || at != 2000 {
		t.Fatalf("read_at=%d err=%v, want 2000", at, err)
	}
}

// REQ-ROOM-005: a fresh database has the room_reads table at the current schema
// version.
func TestMigrateFreshAddsRoomReads(t *testing.T) {
	s, _ := openTest(t)
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'room_reads'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("room_reads table count=%d, want 1", count)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}
}

// v4Database builds a schema-version-4 database the way the previous binary left
// it: migrations 0001-0004 applied and user_version set to 4.
func v4Database(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lana.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/0001_session_log.sql", "migrations/0002_session_leases.sql", "migrations/0003_session_status_waiting_input.sql", "migrations/0004_session_text.sql"} {
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// REQ-ROOM-005: a version-4 database migrates to version 5 and its room reads
// work.
func TestMigrateV4ToV5AddsRoomReads(t *testing.T) {
	ctx := context.Background()
	db, path := v4Database(t)
	var before int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'room_reads'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("v4 fixture already has a room_reads table")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}
	if stored, err := s.SetRoomRead(ctx, "dana", 5000); err != nil || stored != 5000 {
		t.Fatalf("SetRoomRead stored=%d err=%v, want 5000", stored, err)
	}
	if at, err := s.RoomReadAt(ctx, "dana"); err != nil || at != 5000 {
		t.Fatalf("RoomReadAt=%d err=%v, want 5000", at, err)
	}
}

// Bug 1: the child of a fork continues the parent's seq space, so the child's
// own events never collide with the inherited range and a compaction summary
// that reached into that range no longer drops them from the model history.
func TestForkSeqsAreMonotonicAndDeriveKeepsChildMessages(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	parent := createSession(t, s)
	events, err := s.Append(ctx, parent.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "parent turn"}}}},
		{Kind: core.KindCompactionSummary, Payload: core.CompactionSummary{V: 1, FromSeq: 1, UptoSeq: 3, RetainedFromSeq: 1, Summary: "older turns"}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	at := events[len(events)-1].Seq
	child, err := s.Fork(ctx, parent.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	if child.InheritedSeq != at || child.LastSeq != at+1 {
		t.Fatalf("child=%+v, want InheritedSeq=%d LastSeq=%d", child, at, at+1)
	}
	childEvents, err := s.Append(ctx, child.ID, []core.EventDraft{
		{Kind: core.KindUserMessage, TurnID: "c", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "child one"}}}},
		{Kind: core.KindUserMessage, TurnID: "c", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "child two"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range childEvents {
		if event.Seq <= at {
			t.Fatalf("child event seq=%d is not after the fork seq %d", event.Seq, at)
		}
	}
	chain, err := s.ChainEvents(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].Seq <= chain[i-1].Seq {
			t.Fatalf("chain is not seq-ordered at %d: %d then %d", i, chain[i-1].Seq, chain[i].Seq)
		}
	}
	messages, err := s.DeriveMessages(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	texts := messageTexts(messages)
	for _, want := range []string{"older turns", "child one", "child two"} {
		if !containsText(texts, want) {
			t.Fatalf("derived messages %v missing %q", texts, want)
		}
	}
}

// Bug 1: Create with an inherited seq starts the new session after it.
func TestCreateWithInheritedSeqStartsAfterInherited(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	parent := createSession(t, s)
	child, err := s.Create(ctx, core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir(), ParentSessionID: parent.ID, InheritedSeq: 40})
	if err != nil {
		t.Fatal(err)
	}
	if child.InheritedSeq != 40 || child.LastSeq != 41 {
		t.Fatalf("child=%+v, want LastSeq=41", child)
	}
	var createdSeq int64
	for event, err := range s.Events(ctx, child.ID, 1) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == core.KindSessionCreated {
			createdSeq = event.Seq
		}
	}
	if createdSeq != 41 {
		t.Fatalf("session/created seq=%d, want 41", createdSeq)
	}
	if next := appendUserText(t, s, child.ID, "c", "hello"); next.Seq != 42 {
		t.Fatalf("first child append seq=%d, want 42", next.Seq)
	}
}

// Bug 4: the store's not-found sentinel wraps core.ErrNotFound so callers that
// map core.ErrNotFound to 404 see a not-found instead of a 500.
func TestErrSessionNotFoundMatchesCoreNotFound(t *testing.T) {
	if !errors.Is(ErrSessionNotFound, core.ErrNotFound) {
		t.Fatalf("ErrSessionNotFound does not wrap core.ErrNotFound")
	}
	s, _ := openTest(t)
	if _, err := s.Info(context.Background(), "missing-session-id"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Info on a missing session err=%v, want core.ErrNotFound", err)
	}
}

func messageTexts(messages []core.Message) []string {
	var out []string
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Kind == core.BlockText {
				out = append(out, block.Text)
			}
		}
	}
	return out
}

func containsText(texts []string, want string) bool {
	for _, text := range texts {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

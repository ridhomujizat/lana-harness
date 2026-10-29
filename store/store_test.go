package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

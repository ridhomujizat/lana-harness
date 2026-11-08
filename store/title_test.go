package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// v8Database builds a schema-version-8 database the way the previous binary
// left it: migrations 0001-0008 applied and user_version set to 8.
func v8Database(t *testing.T) (*sql.DB, string) {
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
	for _, name := range []string{"migrations/0001_session_log.sql", "migrations/0002_session_leases.sql", "migrations/0003_session_status_waiting_input.sql", "migrations/0004_session_text.sql", "migrations/0005_room_reads.sql", "migrations/0006_workflows.sql", "migrations/0007_calendar.sql", "migrations/0008_router.sql"} {
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
	if _, err := db.Exec("PRAGMA user_version = 8"); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// Spec 40: a version-8 database migrates to version 9. Sessions keep their
// rows, an existing title becomes an automatic one, and an untitled session
// has no title source.
func TestMigrateV8ToV9SessionTitle(t *testing.T) {
	ctx := context.Background()
	db, path := v8Database(t)
	for _, row := range []struct{ id, title string }{{"s-titled", "Old title"}, {"s-untitled", ""}} {
		if _, err := db.Exec("INSERT INTO sessions (id, agent, channel, address, workspace, title, status, last_seq, created_at, updated_at) VALUES (?, 'lana', 'web', 'owner', '/tmp', ?, 'idle', 1, 1, 1)", row.id, row.title); err != nil {
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
	if version != schemaVersion || schemaVersion != 9 {
		t.Fatalf("version=%d schemaVersion=%d, want 9", version, schemaVersion)
	}
	titled, err := s.Info(ctx, "s-titled")
	if err != nil {
		t.Fatal(err)
	}
	if titled.Title != "Old title" || titled.TitleSource != core.TitleSourceAuto {
		t.Fatalf("titled = %q/%q", titled.Title, titled.TitleSource)
	}
	untitled, err := s.Info(ctx, "s-untitled")
	if err != nil {
		t.Fatal(err)
	}
	if untitled.Title != "" || untitled.TitleSource != "" {
		t.Fatalf("untitled = %q/%q", untitled.Title, untitled.TitleSource)
	}
	// The CHECK constraint refuses an unknown source.
	if _, err := s.db.Exec("UPDATE sessions SET title_source = 'bogus' WHERE id = 's-titled'"); err == nil {
		t.Fatal("unknown title_source accepted")
	}
}

// Spec 40: SetSessionTitle writes the title projection, onlyIfUntitled keeps
// an existing title, updated_at stays, and List and Info read the source.
func TestSetSessionTitle(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "web", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "" || info.TitleSource != "" {
		t.Fatalf("new session titled: %+v", info)
	}
	changed, err := s.SetSessionTitle(ctx, info.ID, "First", core.TitleSourceAuto, true)
	if err != nil || !changed {
		t.Fatalf("first write changed=%v err=%v", changed, err)
	}
	changed, err = s.SetSessionTitle(ctx, info.ID, "Second", core.TitleSourceAuto, true)
	if err != nil || changed {
		t.Fatalf("onlyIfUntitled overwrote: changed=%v err=%v", changed, err)
	}
	if _, err := s.SetSessionTitle(ctx, info.ID, "Mine", core.TitleSourceOwner, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Info(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Mine" || got.TitleSource != core.TitleSourceOwner || got.UpdatedAt != info.UpdatedAt {
		t.Fatalf("after owner rename: %+v", got)
	}
	list, err := s.List(ctx, core.SessionFilter{})
	if err != nil || len(list) != 1 || list[0].TitleSource != core.TitleSourceOwner {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	// An empty title clears the source too.
	if _, err := s.SetSessionTitle(ctx, info.ID, "", core.TitleSourceOwner, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Info(ctx, info.ID); got.Title != "" || got.TitleSource != "" {
		t.Fatalf("after clear: %q/%q", got.Title, got.TitleSource)
	}
	if _, err := s.SetSessionTitle(ctx, "missing", "x", core.TitleSourceOwner, false); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := s.SetSessionTitle(ctx, info.ID, "x", "bogus", false); err == nil {
		t.Fatal("bogus source accepted")
	}
}

// REQ-TTL-007: a session created with a fixed title keeps it with source
// system, and a fork copies its parent's title.
func TestCreateWithTitleAndFork(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "workflow", Workspace: t.TempDir(), Title: "Workflow · Digest", TitleSource: core.TitleSourceSystem})
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Workflow · Digest" || info.TitleSource != core.TitleSourceSystem {
		t.Fatalf("create = %+v", info)
	}
	read, err := s.Info(ctx, info.ID)
	if err != nil || read.Title != info.Title || read.TitleSource != core.TitleSourceSystem {
		t.Fatalf("info = %+v err=%v", read, err)
	}
	events, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := s.Fork(ctx, info.ID, events[len(events)-1].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Title != info.Title || fork.TitleSource != core.TitleSourceSystem {
		t.Fatalf("fork = %q/%q", fork.Title, fork.TitleSource)
	}
	// A title without a source is ignored.
	plain, err := s.Create(ctx, core.NewSession{Agent: "lana", Channel: "web", Workspace: t.TempDir(), Title: "ignored"})
	if err != nil || plain.Title != "" || plain.TitleSource != "" {
		t.Fatalf("plain = %+v err=%v", plain, err)
	}
}

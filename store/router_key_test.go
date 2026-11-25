package store

import (
	"context"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/routerdb"
)

// Spec 75 REQ-PAF-005: migration 0012 adds a nullable key column to
// router_calls; a call keeps the secret name of its key, and a call without
// one stores NULL and reads back "".
func TestRouterCallKeyColumn(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if n := countRows(t, s, "SELECT COUNT(*) FROM pragma_table_info('router_calls') WHERE name = 'key' AND \"notnull\" = 0"); n != 1 {
		t.Fatalf("nullable key column=%d, want 1", n)
	}
	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	withKey := routerCall(base, "ds", "deepseek-flash", "turn", "ok", 1, 1, 0, nil, 10)
	withKey.Key = "K2"
	noKey := routerCall(base.Add(time.Minute), "ds", "deepseek-flash", "turn", "ok", 1, 1, 0, nil, 10)
	if err := s.InsertRouterCalls(ctx, []routerdb.Call{withKey, noKey}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM router_calls WHERE key IS NULL"); n != 1 {
		t.Fatalf("NULL keys=%d, want 1", n)
	}
	page, err := s.ListRouterCalls(ctx, routerdb.CallFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != 2 || page.Calls[0].Key != "" || page.Calls[1].Key != "K2" {
		t.Fatalf("calls=%+v, want keys \"\" then K2", page.Calls)
	}
}

// A version-11 database gains the key column; old rows read key "".
func TestMigrateV11ToV12RouterCallKey(t *testing.T) {
	ctx := context.Background()
	db, path := v8Database(t)
	for _, name := range []string{"migrations/0009_session_title.sql", "migrations/0010_workflow_node_via.sql", "migrations/0011_workflow_candidates.sql"} {
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 11"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO router_calls (at, provider, model) VALUES (1, 'ds', 'm')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion || schemaVersion != 12 {
		t.Fatalf("version=%d schemaVersion=%d, want 12", version, schemaVersion)
	}
	page, err := s.ListRouterCalls(ctx, routerdb.CallFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != 1 || page.Calls[0].Key != "" {
		t.Fatalf("calls=%+v, want one old row with key \"\"", page.Calls)
	}
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

func sampleCalendarItem(id string, createdAt int64) calendar.Item {
	return calendar.Item{
		ID:        id,
		Kind:      calendar.KindHabit,
		Title:     "Walk " + id,
		Notes:     "around the park",
		Start:     1000,
		End:       2000,
		AllDay:    true,
		Date:      "2026-11-01",
		EndDate:   "2026-11-02",
		Time:      "07:30",
		Repeat:    calendar.Repeat{Freq: calendar.FreqWeekly, Days: []string{"mon", "thu"}, Until: "2026-12-31"},
		Assignee:  calendar.Owner,
		Color:     "green",
		State:     calendar.StateOpen,
		Action:    &calendar.Action{Type: calendar.ActionRunWorkflow, WorkflowID: "wf", Input: json.RawMessage(`{"x":1}`)},
		Google:    &calendar.GoogleRef{CalendarID: "primary", EventID: "ev1", ETag: `"e1"`, Sync: calendar.SyncSynced},
		CreatedBy: calendar.Owner,
		UpdatedBy: "lana",
		CreatedAt: createdAt,
		UpdatedAt: createdAt + 1,
	}
}

func TestCalendarItemsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a := sampleCalendarItem("ci_a", 2)
	b := calendar.Item{ID: "ci_b", Kind: calendar.KindNote, Title: "B", Date: "2026-11-01", CreatedAt: 1}
	for _, it := range []calendar.Item{a, b} {
		if err := s.CreateItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateItem(ctx, a); err == nil {
		t.Fatal("duplicate create succeeded")
	}
	got, err := s.GetItem(ctx, "ci_a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, a) {
		t.Fatalf("item round trip\n got %+v\nwant %+v", got, a)
	}
	// An item without action, Google link or repeat reads back with nil
	// pointers and a zero repeat.
	gotB, err := s.GetItem(ctx, "ci_b")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotB, b) {
		t.Fatalf("plain item round trip\n got %+v\nwant %+v", gotB, b)
	}
	if _, err := s.GetItem(ctx, "nope"); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}

	list, err := s.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "ci_b" || list[1].ID != "ci_a" {
		t.Fatalf("list order %+v", list)
	}

	got.Title = "A2"
	got.Action = nil
	got.Google = nil
	got.Repeat = calendar.Repeat{}
	if err := s.UpdateItem(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetItem(ctx, "ci_a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("update round trip\n got %+v\nwant %+v", again, got)
	}
	if err := s.UpdateItem(ctx, calendar.Item{ID: "nope"}); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestCalendarItemSoftDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a := sampleCalendarItem("ci_a", 1)
	if err := s.CreateItem(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateItem(ctx, sampleCalendarItem("ci_b", 2)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem(ctx, "ci_a", 99); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(ctx, "ci_a"); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err := s.DeleteItem(ctx, "ci_a", 100); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if err := s.DeleteItem(ctx, "nope", 100); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if err := s.UpdateItem(ctx, a); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("update deleted: %v", err)
	}
	list, err := s.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "ci_b" {
		t.Fatalf("list after delete %+v", list)
	}
	// The row stays with its delete time, and the id stays taken.
	if n := countRows(t, s, "SELECT COUNT(*) FROM calendar_items WHERE id = 'ci_a' AND deleted_at = 99"); n != 1 {
		t.Fatalf("soft-deleted row count=%d", n)
	}
	if err := s.CreateItem(ctx, a); err == nil {
		t.Fatal("create over a deleted id succeeded")
	}
	// A zero delete time still marks the item deleted.
	if err := s.DeleteItem(ctx, "ci_b", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(ctx, "ci_b"); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("get deleted at 0: %v", err)
	}
	if list, _ := s.ListItems(ctx); list == nil || len(list) != 0 {
		t.Fatalf("empty list %#v", list)
	}
}

func TestCalendarOverrides(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	o := calendar.Override{Source: calendar.OverrideWorkflow, Ref: "wf/n", Occurrence: 10, Op: calendar.OpMove, To: 20, By: calendar.Owner, At: 5}
	if err := s.PutOverride(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.Op, o.To, o.FiredAt = calendar.OpSkip, 0, 33
	if err := s.PutOverride(ctx, o); err != nil {
		t.Fatal(err)
	}
	item := calendar.Override{Source: calendar.OverrideItem, Ref: "ci_a", Occurrence: 5, Op: calendar.OpSkip, By: "lana", At: 6}
	if err := s.PutOverride(ctx, item); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []calendar.Override{item, o}; !reflect.DeepEqual(list, want) {
		t.Fatalf("overrides\n got %+v\nwant %+v", list, want)
	}
	if err := s.DeleteOverride(ctx, calendar.OverrideWorkflow, "wf/n", 10); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOverride(ctx, calendar.OverrideWorkflow, "wf/n", 10); err != nil {
		t.Fatalf("delete missing override: %v", err)
	}
	if list, _ := s.ListOverrides(ctx); len(list) != 1 || list[0] != item {
		t.Fatalf("overrides after delete %+v", list)
	}
}

func TestCalendarFirings(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	f := calendar.Firing{ItemID: "ci_a", Occurrence: 100, Status: calendar.FiringScheduled}
	ok, err := s.InsertFiring(ctx, f)
	if err != nil || !ok {
		t.Fatalf("insert ok=%v err=%v", ok, err)
	}
	dup := f
	dup.Status = calendar.FiringDone
	ok, err = s.InsertFiring(ctx, dup)
	if err != nil || ok {
		t.Fatalf("second insert ok=%v err=%v", ok, err)
	}
	if got, err := s.GetFiring(ctx, "ci_a", 100); err != nil || got != f {
		t.Fatalf("insert changed an existing firing: %+v %v", got, err)
	}

	running := calendar.Firing{ItemID: "ci_a", Occurrence: 100, Status: calendar.FiringRunning, StartedAt: 7, SessionID: "s1", RunWorkflow: "wf", RunNumber: 3}
	ok, err = s.UpdateFiring(ctx, running, calendar.FiringScheduled, calendar.FiringMissed)
	if err != nil || !ok {
		t.Fatalf("cas scheduled->running ok=%v err=%v", ok, err)
	}
	ok, err = s.UpdateFiring(ctx, running, calendar.FiringScheduled)
	if err != nil || ok {
		t.Fatalf("cas from wrong status ok=%v err=%v", ok, err)
	}
	done := running
	done.Status, done.EndedAt, done.Error = calendar.FiringFailed, 9, "boom"
	ok, err = s.UpdateFiring(ctx, done)
	if err != nil || !ok {
		t.Fatalf("unconditional update ok=%v err=%v", ok, err)
	}
	got, err := s.GetFiring(ctx, "ci_a", 100)
	if err != nil || got != done {
		t.Fatalf("firing round trip %+v %v", got, err)
	}
	if _, err := s.UpdateFiring(ctx, calendar.Firing{ItemID: "ci_a", Occurrence: 1}); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if _, err := s.UpdateFiring(ctx, calendar.Firing{ItemID: "ci_a", Occurrence: 1}, calendar.FiringScheduled); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("cas missing: %v", err)
	}
	if _, err := s.GetFiring(ctx, "ci_a", 1); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
}

func TestCalendarListFirings(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	seed := []calendar.Firing{
		{ItemID: "ci_a", Occurrence: 10, Status: calendar.FiringDone},
		{ItemID: "ci_a", Occurrence: 20, Status: calendar.FiringMissed},
		{ItemID: "ci_b", Occurrence: 20, Status: calendar.FiringDone},
		{ItemID: "ci_b", Occurrence: 30, Status: calendar.FiringScheduled},
	}
	for _, f := range seed {
		if _, err := s.InsertFiring(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	keys := func(list []calendar.Firing) []string {
		out := []string{}
		for _, f := range list {
			out = append(out, f.ItemID+"@"+f.Status)
		}
		return out
	}
	cases := []struct {
		name   string
		filter calendar.FiringFilter
		want   []string
	}{
		{"all", calendar.FiringFilter{}, []string{"ci_b@scheduled", "ci_a@missed", "ci_b@done", "ci_a@done"}},
		{"item", calendar.FiringFilter{ItemID: "ci_a"}, []string{"ci_a@missed", "ci_a@done"}},
		{"statuses", calendar.FiringFilter{Statuses: []string{calendar.FiringDone, calendar.FiringMissed}}, []string{"ci_a@missed", "ci_b@done", "ci_a@done"}},
		{"range", calendar.FiringFilter{From: 20, To: 30}, []string{"ci_a@missed", "ci_b@done"}},
		{"limit", calendar.FiringFilter{Limit: 2}, []string{"ci_b@scheduled", "ci_a@missed"}},
		{"none", calendar.FiringFilter{ItemID: "nope"}, []string{}},
	}
	for _, tc := range cases {
		list, err := s.ListFirings(ctx, tc.filter)
		if err != nil {
			t.Fatal(err)
		}
		if list == nil {
			t.Fatalf("%s: nil list", tc.name)
		}
		if got := keys(list); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// REQ-CAL-006: concurrent compare-and-set calls on one firing have exactly
// one winner, and concurrent inserts store the firing once.
func TestCalendarFiringConcurrentCAS(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	const workers = 16
	var inserted atomic.Int32
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.InsertFiring(ctx, calendar.Firing{ItemID: "ci_a", Occurrence: 100, Status: calendar.FiringScheduled, SessionID: string(rune('a' + i))})
			if err != nil {
				t.Error(err)
			}
			if ok {
				inserted.Add(1)
			}
		}()
	}
	wg.Wait()
	if inserted.Load() != 1 {
		t.Fatalf("inserts won=%d, want 1", inserted.Load())
	}

	var won atomic.Int32
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := calendar.Firing{ItemID: "ci_a", Occurrence: 100, Status: calendar.FiringRunning, StartedAt: int64(i + 1)}
			ok, err := s.UpdateFiring(ctx, f, calendar.FiringScheduled)
			if err != nil {
				t.Error(err)
			}
			if ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("cas winners=%d, want 1", won.Load())
	}
	got, err := s.GetFiring(ctx, "ci_a", 100)
	if err != nil || got.Status != calendar.FiringRunning {
		t.Fatalf("after race %+v %v", got, err)
	}
}

func TestCalendarHabitChecks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	checks := []calendar.HabitCheck{
		{ItemID: "ci_b", Date: "2026-11-01", By: calendar.Owner, At: 1},
		{ItemID: "ci_a", Date: "2026-11-02", By: calendar.Owner, At: 2},
		{ItemID: "ci_a", Date: "2026-11-01", By: "lana", At: 3},
	}
	for _, c := range checks {
		if err := s.SetHabitCheck(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	upsert := calendar.HabitCheck{ItemID: "ci_a", Date: "2026-11-01", By: calendar.Owner, At: 9}
	if err := s.SetHabitCheck(ctx, upsert); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListHabitChecks(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []calendar.HabitCheck{upsert, checks[1], checks[0]}; !reflect.DeepEqual(all, want) {
		t.Fatalf("all checks\n got %+v\nwant %+v", all, want)
	}
	one, err := s.ListHabitChecks(ctx, "ci_b")
	if err != nil || len(one) != 1 || one[0] != checks[0] {
		t.Fatalf("checks of ci_b %+v %v", one, err)
	}
	if err := s.DeleteHabitCheck(ctx, "ci_a", "2026-11-01"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHabitCheck(ctx, "ci_a", "2026-11-01"); err != nil {
		t.Fatalf("delete missing check: %v", err)
	}
	if list, _ := s.ListHabitChecks(ctx, "ci_a"); len(list) != 1 || list[0] != checks[1] {
		t.Fatalf("checks after delete %+v", list)
	}
	if list, _ := s.ListHabitChecks(ctx, "nope"); list == nil || len(list) != 0 {
		t.Fatalf("checks of missing item %#v", list)
	}
}

func TestCalendarHistory(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	lines := []calendar.HistoryLine{
		{ItemID: "ci_a", At: 10, By: calendar.Owner, Summary: "created"},
		{ItemID: "ci_b", At: 15, By: "lana", Summary: "created b"},
		{ItemID: "ci_a", At: 20, By: "lana", Summary: "moved", Snapshot: `{"title":"x"}`},
		{ItemID: "ci_a", At: 20, By: calendar.Owner, Summary: "renamed"},
		{ItemID: "ci_a", At: 5, By: calendar.Owner, Summary: "late append, older time"},
	}
	for _, h := range lines {
		if err := s.AppendHistory(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	summaries := func(list []calendar.HistoryLine) []string {
		out := []string{}
		for _, h := range list {
			out = append(out, h.Summary)
		}
		return out
	}
	all, err := s.ListHistory(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"renamed", "moved", "created b", "created", "late append, older time"}; !reflect.DeepEqual(summaries(all), want) {
		t.Fatalf("all history %v", summaries(all))
	}
	if all[1] != lines[2] {
		t.Fatalf("history round trip %+v", all[1])
	}
	a, err := s.ListHistory(ctx, "ci_a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"renamed", "moved"}; !reflect.DeepEqual(summaries(a), want) {
		t.Fatalf("ci_a history %v", summaries(a))
	}
	if list, _ := s.ListHistory(ctx, "nope", 0); list == nil || len(list) != 0 {
		t.Fatalf("history of missing item %#v", list)
	}
}

func TestCalendarKV(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if v, ok, err := s.GetKV(ctx, "settings"); err != nil || ok || v != "" {
		t.Fatalf("missing key v=%q ok=%v err=%v", v, ok, err)
	}
	if err := s.SetKV(ctx, "settings", `{"week_start":1}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKV(ctx, "settings", `{"week_start":0}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKV(ctx, "empty", ""); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.GetKV(ctx, "settings"); err != nil || !ok || v != `{"week_start":0}` {
		t.Fatalf("settings v=%q ok=%v err=%v", v, ok, err)
	}
	if v, ok, err := s.GetKV(ctx, "empty"); err != nil || !ok || v != "" {
		t.Fatalf("empty v=%q ok=%v err=%v", v, ok, err)
	}
}

func TestCalendarGoogleEvents(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	e1 := calendar.GoogleEvent{CalendarID: "work", EventID: "e1", ETag: `"1"`, JSON: `{"id":"e1"}`, UpdatedMs: 10}
	e2 := calendar.GoogleEvent{CalendarID: "primary", EventID: "e9", ETag: `"2"`, ItemID: "ci_a", JSON: `{"id":"e9"}`, UpdatedMs: 11}
	for _, e := range []calendar.GoogleEvent{e1, e2} {
		if err := s.PutGoogleEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	e1.ETag, e1.ItemID, e1.JSON, e1.UpdatedMs = `"3"`, "ci_b", `{"id":"e1","summary":"x"}`, 12
	if err := s.PutGoogleEvent(ctx, e1); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListGoogleEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []calendar.GoogleEvent{e2, e1}; !reflect.DeepEqual(list, want) {
		t.Fatalf("google events\n got %+v\nwant %+v", list, want)
	}
	if err := s.DeleteGoogleEvent(ctx, "work", "e1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGoogleEvent(ctx, "work", "e1"); err != nil {
		t.Fatalf("delete missing event: %v", err)
	}
	if list, _ := s.ListGoogleEvents(ctx); len(list) != 1 || list[0] != e2 {
		t.Fatalf("google events after delete %+v", list)
	}
}

// The calendar tables survive a reopen.
func TestCalendarPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	a := sampleCalendarItem("ci_a", 1)
	if err := s.CreateItem(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKV(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	if got, err := again.GetItem(ctx, "ci_a"); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("item after reopen %+v %v", got, err)
	}
	if v, ok, _ := again.GetKV(ctx, "k"); !ok || v != "v" {
		t.Fatalf("kv after reopen %q %v", v, ok)
	}
}

func TestCalendarClosedStore(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	_ = s.Close()
	if _, err := s.ListItems(ctx); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("list on closed store: %v", err)
	}
	if err := s.SetKV(ctx, "k", "v"); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("write on closed store: %v", err)
	}
}

// v6Database builds a schema-version-6 database the way the previous binary
// left it: migrations 0001-0006 applied and user_version set to 6.
func v6Database(t *testing.T) (*sql.DB, string) {
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
	for _, name := range []string{"migrations/0001_session_log.sql", "migrations/0002_session_leases.sql", "migrations/0003_session_status_waiting_input.sql", "migrations/0004_session_text.sql", "migrations/0005_room_reads.sql", "migrations/0006_workflows.sql"} {
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
	if _, err := db.Exec("PRAGMA user_version = 6"); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// A version-6 database with a workflow migrates to version 7, keeps the
// workflow, and its calendar tables work afterwards.
func TestMigrateV6ToV7Calendar(t *testing.T) {
	ctx := context.Background()
	db, path := v6Database(t)
	if _, err := db.Exec("INSERT INTO workflows (id, name, guardian, status, version, created_at, updated_at) VALUES ('wf-old', 'Old', 'lana', 'draft', 1, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'calendar_%'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("v6 fixture already has %d calendar tables", before)
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
	if version != 7 {
		t.Fatalf("version=%d, want 7", version)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'calendar_%'"); n != 7 {
		t.Fatalf("calendar tables=%d, want 7", n)
	}
	if _, err := s.GetWorkflow(ctx, "wf-old"); err != nil {
		t.Fatalf("workflow lost in migration: %v", err)
	}
	if err := s.CreateItem(ctx, sampleCalendarItem("ci_a", 1)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.InsertFiring(ctx, calendar.Firing{ItemID: "ci_a", Occurrence: 1, Status: calendar.FiringScheduled}); err != nil || !ok {
		t.Fatalf("insert firing after migrate ok=%v err=%v", ok, err)
	}
}

func TestCalendarDeletedItemsAndPurge(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	linked := sampleCalendarItem("ci_a", 1)
	plain := sampleCalendarItem("ci_b", 2)
	plain.Google = nil
	live := sampleCalendarItem("ci_c", 3)
	for _, it := range []calendar.Item{linked, plain, live} {
		if err := s.CreateItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeItem(ctx, "ci_c"); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("purge live item: %v", err)
	}
	for _, id := range []string{"ci_a", "ci_b"} {
		if err := s.DeleteItem(ctx, id, 50); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListDeletedItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "ci_a" || got[0].Google == nil || got[0].Google.EventID != "ev1" || got[0].DeletedAt != 50 {
		t.Fatalf("deleted items %+v", got)
	}
	if err := s.PurgeItem(ctx, "ci_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeItem(ctx, "ci_a"); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("purge twice: %v", err)
	}
	if got, _ := s.ListDeletedItems(ctx); len(got) != 0 {
		t.Fatalf("deleted items after purge %+v", got)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM calendar_items WHERE id = 'ci_a'"); n != 0 {
		t.Fatalf("purged row count=%d", n)
	}
	if _, err := s.GetItem(ctx, "ci_c"); err != nil {
		t.Fatalf("live item gone: %v", err)
	}
}

package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMemStoreItems(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	a := Item{ID: "ci_a", Kind: KindHabit, Title: "A", Date: "2026-11-01", CreatedAt: 2,
		Repeat: Repeat{Freq: FreqWeekly, Days: []string{"mon"}},
		Action: &Action{Type: ActionRunWorkflow, WorkflowID: "wf", Input: json.RawMessage(`{"x":1}`)}}
	b := Item{ID: "ci_b", Kind: KindNote, Title: "B", Date: "2026-11-01", CreatedAt: 1}
	for _, it := range []Item{a, b} {
		if err := s.CreateItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateItem(ctx, a); err == nil {
		t.Fatal("duplicate create succeeded")
	}
	// Mutating the caller's or a returned value never reaches the store.
	a.Repeat.Days[0] = "tue"
	got, err := s.GetItem(ctx, "ci_a")
	if err != nil || got.Repeat.Days[0] != "mon" {
		t.Fatalf("get %v %+v", err, got)
	}
	got.Action.Input[1] = 'Z'
	got.Action.WorkflowID = "changed"
	again, _ := s.GetItem(ctx, "ci_a")
	if again.Action.WorkflowID != "wf" || string(again.Action.Input) != `{"x":1}` {
		t.Fatalf("store mutated through a copy: %+v", again.Action)
	}
	list, _ := s.ListItems(ctx)
	if len(list) != 2 || list[0].ID != "ci_b" || list[1].ID != "ci_a" {
		t.Fatalf("list order %+v", list)
	}
	again.Title = "A2"
	if err := s.UpdateItem(ctx, again); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateItem(ctx, Item{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if err := s.DeleteItem(ctx, "ci_a", 99); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(ctx, "ci_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err := s.DeleteItem(ctx, "ci_a", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if err := s.UpdateItem(ctx, again); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update deleted: %v", err)
	}
	if list, _ := s.ListItems(ctx); len(list) != 1 {
		t.Fatalf("list after delete %+v", list)
	}
}

func TestMemStoreOverrides(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	o := Override{Source: OverrideWorkflow, Ref: "wf/n", Occurrence: 10, Op: OpMove, To: 20, By: Owner}
	_ = s.PutOverride(ctx, o)
	o.Op, o.To = OpSkip, 0
	_ = s.PutOverride(ctx, o)
	_ = s.PutOverride(ctx, Override{Source: OverrideItem, Ref: "ci_a", Occurrence: 5, Op: OpSkip})
	list, _ := s.ListOverrides(ctx)
	if len(list) != 2 || list[0].Source != OverrideItem || list[1].Op != OpSkip {
		t.Fatalf("overrides %+v", list)
	}
	_ = s.DeleteOverride(ctx, OverrideWorkflow, "wf/n", 10)
	if err := s.DeleteOverride(ctx, OverrideWorkflow, "wf/n", 10); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if list, _ := s.ListOverrides(ctx); len(list) != 1 {
		t.Fatalf("after delete %+v", list)
	}
}

// REQ-CAL-006: a firing is inserted once and moves state only by CAS.
func TestMemStoreFiringInsertOnceAndCAS(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	f := Firing{ItemID: "ci_a", Occurrence: 100, Status: FiringScheduled}
	if ok, err := s.InsertFiring(ctx, f); !ok || err != nil {
		t.Fatalf("first insert %v %v", ok, err)
	}
	dup := f
	dup.Status = FiringMissed
	if ok, err := s.InsertFiring(ctx, dup); ok || err != nil {
		t.Fatalf("second insert %v %v", ok, err)
	}
	if got, _ := s.GetFiring(ctx, "ci_a", 100); got.Status != FiringScheduled {
		t.Fatalf("insert overwrote: %+v", got)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			running := f
			running.Status = FiringRunning
			ok, err := s.UpdateFiring(ctx, running, FiringScheduled, FiringMissed)
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("CAS winners %d, want 1", wins.Load())
	}
	done := f
	done.Status, done.EndedAt = FiringDone, 5
	if ok, _ := s.UpdateFiring(ctx, done, FiringRunning); !ok {
		t.Fatal("running → done refused")
	}
	if ok, _ := s.UpdateFiring(ctx, f); !ok {
		t.Fatal("unconditional update refused")
	}
	if _, err := s.UpdateFiring(ctx, Firing{ItemID: "x", Occurrence: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if _, err := s.GetFiring(ctx, "x", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
}

func TestMemStoreListFirings(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	for _, f := range []Firing{
		{ItemID: "a", Occurrence: 1, Status: FiringDone},
		{ItemID: "a", Occurrence: 3, Status: FiringMissed},
		{ItemID: "b", Occurrence: 2, Status: FiringMissed},
		{ItemID: "b", Occurrence: 4, Status: FiringFailed},
	} {
		_, _ = s.InsertFiring(ctx, f)
	}
	all, _ := s.ListFirings(ctx, FiringFilter{})
	if len(all) != 4 || all[0].Occurrence != 4 || all[3].Occurrence != 1 {
		t.Fatalf("all %+v", all)
	}
	missed, _ := s.ListFirings(ctx, FiringFilter{Statuses: []string{FiringMissed}})
	if len(missed) != 2 {
		t.Fatalf("missed %+v", missed)
	}
	a, _ := s.ListFirings(ctx, FiringFilter{ItemID: "a", Limit: 1})
	if len(a) != 1 || a[0].Occurrence != 3 {
		t.Fatalf("item a %+v", a)
	}
	ranged, _ := s.ListFirings(ctx, FiringFilter{From: 2, To: 4})
	if len(ranged) != 2 || ranged[0].Occurrence != 3 || ranged[1].Occurrence != 2 {
		t.Fatalf("ranged %+v", ranged)
	}
}

func TestMemStoreChecksHistoryKVGoogle(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	_ = s.SetHabitCheck(ctx, HabitCheck{ItemID: "h", Date: "2026-11-02", By: Owner})
	_ = s.SetHabitCheck(ctx, HabitCheck{ItemID: "h", Date: "2026-11-01", By: Owner})
	_ = s.SetHabitCheck(ctx, HabitCheck{ItemID: "h", Date: "2026-11-01", By: "agent:Dana"})
	_ = s.SetHabitCheck(ctx, HabitCheck{ItemID: "g", Date: "2026-11-01"})
	checks, _ := s.ListHabitChecks(ctx, "h")
	if len(checks) != 2 || checks[0].Date != "2026-11-01" || checks[0].By != "agent:Dana" {
		t.Fatalf("checks %+v", checks)
	}
	_ = s.DeleteHabitCheck(ctx, "h", "2026-11-01")
	_ = s.DeleteHabitCheck(ctx, "none", "2026-11-01")
	if all, _ := s.ListHabitChecks(ctx, ""); len(all) != 2 || all[0].ItemID != "g" {
		t.Fatalf("all checks %+v", all)
	}

	for i, sum := range []string{"created", "moved", "renamed"} {
		_ = s.AppendHistory(ctx, HistoryLine{ItemID: "h", At: int64(i + 1), By: Owner, Summary: sum})
	}
	_ = s.AppendHistory(ctx, HistoryLine{ItemID: "x", At: 9, Summary: "other"})
	hist, _ := s.ListHistory(ctx, "h", 2)
	if len(hist) != 2 || hist[0].Summary != "renamed" || hist[1].Summary != "moved" {
		t.Fatalf("history %+v", hist)
	}

	if _, ok, _ := s.GetKV(ctx, "k"); ok {
		t.Fatal("missing key found")
	}
	_ = s.SetKV(ctx, "k", "v")
	if v, ok, _ := s.GetKV(ctx, "k"); !ok || v != "v" {
		t.Fatalf("kv %q %v", v, ok)
	}

	_ = s.PutGoogleEvent(ctx, GoogleEvent{CalendarID: "p", EventID: "2", ETag: "e1"})
	_ = s.PutGoogleEvent(ctx, GoogleEvent{CalendarID: "p", EventID: "1"})
	_ = s.PutGoogleEvent(ctx, GoogleEvent{CalendarID: "p", EventID: "2", ETag: "e2"})
	evs, _ := s.ListGoogleEvents(ctx)
	if len(evs) != 2 || evs[0].EventID != "1" || evs[1].ETag != "e2" {
		t.Fatalf("google %+v", evs)
	}
	_ = s.DeleteGoogleEvent(ctx, "p", "1")
	if evs, _ := s.ListGoogleEvents(ctx); len(evs) != 1 {
		t.Fatalf("google after delete %+v", evs)
	}
}

func TestMemStoreDeletedItemsAndPurge(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	linked := Item{ID: "ci_a", Kind: KindEvent, Title: "A", CreatedAt: 1,
		Google: &GoogleRef{CalendarID: "primary", EventID: "ev1", Sync: SyncPending}}
	plain := Item{ID: "ci_b", Kind: KindEvent, Title: "B", CreatedAt: 2}
	live := Item{ID: "ci_c", Kind: KindEvent, Title: "C", CreatedAt: 3, Google: &GoogleRef{CalendarID: "primary"}}
	for _, it := range []Item{linked, plain, live} {
		if err := s.CreateItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeItem(ctx, "ci_c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge live: %v", err)
	}
	_ = s.DeleteItem(ctx, "ci_a", 5)
	_ = s.DeleteItem(ctx, "ci_b", 5)
	got, err := s.ListDeletedItems(ctx)
	if err != nil || len(got) != 1 || got[0].ID != "ci_a" || got[0].Google.EventID != "ev1" {
		t.Fatalf("deleted %v %+v", err, got)
	}
	got[0].Google.EventID = "changed"
	if again, _ := s.ListDeletedItems(ctx); again[0].Google.EventID != "ev1" {
		t.Fatal("store mutated through a copy")
	}
	if err := s.PurgeItem(ctx, "ci_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeItem(ctx, "ci_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge twice: %v", err)
	}
	if got, _ := s.ListDeletedItems(ctx); len(got) != 0 {
		t.Fatalf("after purge %+v", got)
	}
}

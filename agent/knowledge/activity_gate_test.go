package knowledge

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// shortenGate shortens the background-gate timings for one test and restores
// them after it. The fake clock drives the comparisons, so any consistent value
// works.
func shortenGate(t *testing.T) {
	t.Helper()
	prevWindow, prevStale, prevCool, prevPoll := quietWindow, busyStale, journalCooldown, gatePollInterval
	quietWindow = 2 * time.Second
	busyStale = 5 * time.Second
	journalCooldown = 3 * time.Second
	gatePollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		quietWindow, busyStale, journalCooldown, gatePollInterval = prevWindow, prevStale, prevCool, prevPoll
	})
}

// waitBackgroundIdle waits until no background journal pass is in flight.
func waitBackgroundIdle(t *testing.T, svc *Service) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !svc.upkeep.bgJournal.Load() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background journal pass did not finish")
}

// waitProviderCalls waits until the provider has been called at least n times.
func waitProviderCalls(t *testing.T, p *fakeProvider, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p.count() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider calls = %d, want at least %d", p.count(), n)
}

// REQ-KBG-020 (AC-KBG-006): the gate is quiet until a session turns busy, a
// stale busy mark counts as a lost turn/end, and a turn/end quiets it only
// after the quiet window.
func TestActivityQuietWindow(t *testing.T) {
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	clock := &fakeClock{t: base}
	act := newActivity(clock.now)

	if !act.quiet(base) {
		t.Fatal("a fresh gate is not quiet")
	}
	// An event from a session that is not busy is ignored.
	act.observe(core.SessionEvent{SessionID: "ghost", Kind: core.KindStepStart})
	if !act.quiet(base) {
		t.Fatal("an event from a non-busy session made the gate busy")
	}

	act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnStart})
	if act.quiet(base) {
		t.Fatal("a busy session is quiet")
	}
	// A stale busy mark counts as a lost turn/end (REQ-KBG-020).
	if !act.quiet(base.Add(busyStale)) {
		t.Fatal("a stale busy mark is not quiet")
	}

	// After turn/end the gate is quiet only once the window passed.
	act.observe(core.SessionEvent{SessionID: "s2", Kind: core.KindTurnStart})
	clock.set(base.Add(busyStale + time.Minute))
	act.observe(core.SessionEvent{SessionID: "s2", Kind: core.KindTurnEnd})
	end := clock.now()
	if act.quiet(end) {
		t.Fatal("quiet within the window after turn/end")
	}
	if act.quiet(end.Add(quietWindow - time.Nanosecond)) {
		t.Fatal("quiet just before the window ended")
	}
	if !act.quiet(end.Add(quietWindow)) {
		t.Fatal("not quiet after the window")
	}
}

// REQ-KBG-030, REQ-KBG-031: a preemptible pass is cancelled by turn/start, not
// by turn/end, and release cancels and unregisters it, safe to call twice.
func TestActivityPreemptible(t *testing.T) {
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	act := newActivity(func() time.Time { return base })

	ctx, release := act.preemptible(context.Background())
	act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnEnd})
	if err := ctx.Err(); err != nil {
		t.Fatalf("turn/end cancelled a preemptible pass: %v", err)
	}
	act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnStart})
	if err := ctx.Err(); err == nil {
		t.Fatal("turn/start did not cancel a preemptible pass")
	}
	release()
	release()

	ctx2, release2 := act.preemptible(context.Background())
	release2()
	if err := ctx2.Err(); err == nil {
		t.Fatal("release did not cancel the pass")
	}
}

// REQ-KBG-040 (AC-KBG-006): consolidationDue keeps the schedule rule and also
// needs the quiet window.
func TestConsolidationDueQuiet(t *testing.T) {
	shortenGate(t)
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	at := time.Date(2026, 11, 1, 3, 0, 0, 0, time.Local)
	env.clock.set(at)
	old := at.Add(-24 * time.Hour)
	env.svc.setUpkeepValue(upkeepKeyConsolidationAt, strconv.FormatInt(old.UnixMilli(), 10))

	if !env.svc.consolidationDue(at) {
		t.Fatal("consolidation should be due with no activity")
	}

	env.svc.act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnStart})
	if env.svc.consolidationDue(at) {
		t.Fatal("consolidation is due while a session is busy")
	}

	env.svc.act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnEnd})
	if env.svc.consolidationDue(at) {
		t.Fatal("consolidation is due within the quiet window")
	}

	env.clock.set(at.Add(quietWindow))
	if !env.svc.consolidationDue(env.clock.now()) {
		t.Fatal("consolidation is not due after the quiet window")
	}
}

// AC-KBG-005 (REQ-KBG-021, REQ-KBG-022): the background journal does not run
// before the quiet window, runs after it, respects the 30-minute cooldown, and
// runs again once the cooldown passed.
func TestBackgroundJournalQuietAndCooldown(t *testing.T) {
	shortenGate(t)
	base := time.Now()
	clock := &fakeClock{t: base}
	provider := &fakeProvider{text: `{"notes": ["a note"]}`}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	env.svc.now = clock.now
	env.store.setRows(twoTurnRows(env.info, base))

	// A busy session blocks the pass.
	env.svc.act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnStart})
	env.svc.backgroundJournal(context.Background())
	waitBackgroundIdle(t, env.svc)
	if provider.count() != 0 {
		t.Fatalf("ran while busy: %d calls", provider.count())
	}

	// Within the quiet window after turn/end, still nothing.
	env.svc.act.observe(core.SessionEvent{SessionID: "s1", Kind: core.KindTurnEnd})
	env.svc.backgroundJournal(context.Background())
	waitBackgroundIdle(t, env.svc)
	if provider.count() != 0 {
		t.Fatalf("ran within the quiet window: %d calls", provider.count())
	}

	// After the window it runs and journals.
	clock.set(base.Add(quietWindow))
	env.svc.backgroundJournal(context.Background())
	waitBackgroundIdle(t, env.svc)
	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("journal mark = %d, want 4", mark)
	}

	// New text within the cooldown does not run.
	env.store.setRows(append(twoTurnRows(env.info, base), core.TextRow{
		SessionID: env.info.ID, Seq: 5, Role: "user", CreatedAt: base.UnixMilli(), Text: "another owner line",
	}))
	clock.set(base.Add(quietWindow + journalCooldown - time.Second))
	if !env.svc.act.quiet(env.svc.now()) {
		t.Fatal("gate should be quiet during the cooldown phase")
	}
	env.svc.backgroundJournal(context.Background())
	waitBackgroundIdle(t, env.svc)
	if provider.count() != 1 {
		t.Fatalf("ran within the cooldown: %d calls", provider.count())
	}
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("mark moved within the cooldown: %d", mark)
	}

	// After the cooldown it runs again.
	clock.set(base.Add(quietWindow + journalCooldown))
	env.svc.backgroundJournal(context.Background())
	waitBackgroundIdle(t, env.svc)
	if provider.count() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.count())
	}
	if mark := env.journalMark(t); mark != 5 {
		t.Fatalf("journal mark = %d, want 5", mark)
	}
}

// AC-KBG-005, REQ-KBG-031: a turn/start during a blocked pass cancels it; the
// mark stays, it is not counted as failed, and the next quiet window journals
// it.
func TestBackgroundJournalPreemptedByTurnStart(t *testing.T) {
	shortenGate(t)
	base := time.Now()
	clock := &fakeClock{t: base}
	gate := make(chan struct{})
	provider := &fakeProvider{text: `{"notes": ["a note"]}`, gate: gate}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	env.svc.now = clock.now
	env.store.setRows(twoTurnRows(env.info, base))

	clock.set(base.Add(quietWindow))
	env.svc.backgroundJournal(context.Background())
	waitProviderCalls(t, provider, 1)

	// A turn/start on another session cancels the running pass.
	env.svc.act.observe(core.SessionEvent{SessionID: "s-other", Kind: core.KindTurnStart})
	waitBackgroundIdle(t, env.svc)
	close(gate)

	if mark := env.journalMark(t); mark != 0 {
		t.Fatalf("mark = %d, want 0 after a preempted pass", mark)
	}
	if failures := env.svc.journalFailureStatus(); len(failures) != 0 {
		t.Fatalf("journal_failed = %+v, want empty", failures)
	}

	// After the next quiet window it journals and the mark moves.
	env.svc.act.observe(core.SessionEvent{SessionID: "s-other", Kind: core.KindTurnEnd})
	clock.set(base.Add(quietWindow + quietWindow))
	env.svc.backgroundJournal(context.Background())
	waitBackgroundIdle(t, env.svc)
	if mark := env.journalMark(t); mark != 4 {
		t.Fatalf("mark = %d, want 4 after the next window", mark)
	}
	if provider.count() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.count())
	}
}

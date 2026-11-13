package knowledge

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// excerptOf returns the excerpt text of one journal request, so a test can route
// a reply to a session.
func excerptOf(req core.ChatRequest) string {
	if len(req.Messages) == 0 || len(req.Messages[0].Content) == 0 {
		return ""
	}
	return req.Messages[0].Content[0].Text
}

// journalRows is one owner turn and one reply of a session carrying a marker, so
// the excerpt identifies the session.
func journalRows(sessionID, marker string, at time.Time) []core.TextRow {
	return []core.TextRow{
		{SessionID: sessionID, Seq: 1, Role: "user", CreatedAt: at.UnixMilli(), Text: "owner " + marker},
		{SessionID: sessionID, Seq: 2, Role: "assistant", CreatedAt: at.UnixMilli(), Text: "agent " + marker},
	}
}

// newExtraSession creates one more session in a journal env.
func newExtraSession(t *testing.T, env *journalEnv, agent string) core.SessionInfo {
	t.Helper()
	info, err := env.store.Create(context.Background(), core.NewSession{Agent: agent, Channel: "telegram", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// AC-KBG-004 (REQ-KBG-011): the journal lines of a pass with one written, one
// failed, and one preempted session; a clean pass journals only; at most 10
// failed lines and errors cut to 120 runes.
func TestFormatJournalLines(t *testing.T) {
	counts := journalPassCounts{
		written:   1,
		failed:    1,
		preempted: 1,
		failures:  []journalFailure{{sessionID: "s-b", err: "unexpected end of JSON input"}},
	}
	got := formatJournalLines(counts)
	want := []string{
		"- journaled 1 sessions",
		"- journal failed: 1 sessions",
		"  - s-b: unexpected end of JSON input",
		"- journal preempted: 1 sessions (queued again)",
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lines[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	clean := formatJournalLines(journalPassCounts{written: 2, empty: 1, skipped: 4})
	if len(clean) != 1 || clean[0] != "- journaled 3 sessions" {
		t.Fatalf("clean lines = %q", clean)
	}
	if lines := formatJournalLines(journalPassCounts{skipped: 2}); len(lines) != 0 {
		t.Fatalf("skipped-only lines = %q, want none", lines)
	}

	var many journalPassCounts
	many.failed = 12
	for i := 0; i < 12; i++ {
		many.failures = append(many.failures, journalFailure{sessionID: "s", err: strings.Repeat("x", 200)})
	}
	lines := formatJournalLines(many)
	if len(lines) != 1+journalFailedLogMax {
		t.Fatalf("failed lines = %d, want %d: %q", len(lines), 1+journalFailedLogMax, lines)
	}
	if r := []rune(strings.TrimPrefix(lines[1], "  - s: ")); len(r) != journalFailRunes {
		t.Fatalf("error runes = %d, want %d", len(r), journalFailRunes)
	}
}

// AC-KBG-004, REQ-KBG-011, REQ-KBG-012: one written session and one whose reply
// does not parse yields the journaled and failed log lines, a failed-session
// line, and a JournalFailed entry that a later success clears.
func TestJournalPassesCountFailures(t *testing.T) {
	const good = `{"notes": ["a note"]}`
	var fixed atomic.Bool
	provider := &fakeProvider{route: func(req core.ChatRequest) string {
		if strings.Contains(excerptOf(req), "bravo") && !fixed.Load() {
			return "not json at all"
		}
		return good
	}}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	at := time.Now()
	a := newExtraSession(t, env, "assistant")
	b := newExtraSession(t, env, "assistant")
	env.store.setRows(append(journalRows(a.ID, "alpha", at), journalRows(b.ID, "bravo", at)...))

	counts, err := env.svc.journalPasses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts.written != 1 || counts.failed != 1 {
		t.Fatalf("counts = %+v, want written 1 failed 1", counts)
	}
	if len(counts.failures) != 1 || counts.failures[0].sessionID != b.ID || counts.failures[0].err == "" {
		t.Fatalf("failures = %+v", counts.failures)
	}
	joined := strings.Join(formatJournalLines(counts), "\n")
	if !strings.Contains(joined, "- journaled 1 sessions") || !strings.Contains(joined, "- journal failed: 1 sessions") {
		t.Fatalf("log lines = %q", joined)
	}
	if !strings.Contains(joined, "  - "+b.ID+": ") {
		t.Fatalf("log lines missing session line: %q", joined)
	}

	status, err := env.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.JournalFailed) != 1 || status.JournalFailed[0].SessionID != b.ID || status.JournalFailed[0].Failures != 1 {
		t.Fatalf("journal_failed = %+v, want only %s with 1 failure", status.JournalFailed, b.ID)
	}

	// A later successful pass of the failed session clears the entry.
	fixed.Store(true)
	counts, err = env.svc.journalPasses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts.written != 1 || counts.failed != 0 {
		t.Fatalf("counts after fix = %+v, want written 1 failed 0", counts)
	}
	status, err = env.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.JournalFailed == nil {
		t.Fatal("journal_failed is nil, want []")
	}
	if len(status.JournalFailed) != 0 {
		t.Fatalf("journal_failed after success = %+v, want empty", status.JournalFailed)
	}
}

// REQ-KBG-010, REQ-KBG-031: a cancellation with a cancelled pass context maps to
// journalPreempted, leaves the mark, and records no failure.
func TestJournalSessionPreempted(t *testing.T) {
	provider := &fakeProvider{err: context.Canceled}
	env := newJournalEnv(t, provider, core.KnowledgeConfig{})
	env.store.setRows(twoTurnRows(env.info, time.Now()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, errText := env.svc.journalSession(ctx, env.info)
	if outcome != journalPreempted || errText != "" {
		t.Fatalf("outcome = %v err = %q, want journalPreempted", outcome, errText)
	}
	if mark := env.journalMark(t); mark != 0 {
		t.Fatalf("mark = %d, want 0", mark)
	}
	status, err := env.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.JournalFailed) != 0 {
		t.Fatalf("journal_failed = %+v, want empty", status.JournalFailed)
	}
}

// REQ-KBG-012: consecutive failures count up, a preempted or skipped outcome
// leaves the entry, and a written outcome clears it.
func TestJournalFailureMemory(t *testing.T) {
	env := newJournalEnv(t, &fakeProvider{text: `{"notes": ["a"]}`}, core.KnowledgeConfig{})
	svc := env.svc

	svc.recordJournalOutcome("s1", journalFailed, "boom one")
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.JournalFailed) != 1 || status.JournalFailed[0].SessionID != "s1" ||
		status.JournalFailed[0].Failures != 1 || status.JournalFailed[0].Error != "boom one" {
		t.Fatalf("journal_failed = %+v", status.JournalFailed)
	}

	svc.recordJournalOutcome("s1", journalPreempted, "")
	svc.recordJournalOutcome("s1", journalSkipped, "")
	status, err = svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.JournalFailed) != 1 || status.JournalFailed[0].Failures != 1 {
		t.Fatalf("journal_failed after preempted/skipped = %+v", status.JournalFailed)
	}

	svc.recordJournalOutcome("s1", journalFailed, "boom two")
	status, err = svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.JournalFailed) != 1 || status.JournalFailed[0].Failures != 2 || status.JournalFailed[0].Error != "boom two" {
		t.Fatalf("journal_failed after second failure = %+v", status.JournalFailed)
	}

	svc.recordJournalOutcome("s1", journalWritten, "")
	status, err = svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.JournalFailed == nil {
		t.Fatal("journal_failed is nil, want []")
	}
	if len(status.JournalFailed) != 0 {
		t.Fatalf("journal_failed after success = %+v, want empty", status.JournalFailed)
	}
}

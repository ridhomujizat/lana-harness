package notify

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *fakeClock               { return &fakeClock{t: time.UnixMilli(1_700_000_000_000)} }
func ids(events []Event) (out []string) {
	for _, e := range events {
		out = append(out, e.ID)
	}
	return out
}
func reply(session, turn string) Event { return Reply("coder", "Fix bug", session, turn, "Done.") }

func TestBusPublishAssignsIDsAndFansOut(t *testing.T) {
	clock := newClock()
	bus := NewBus(WithClock(clock.now))
	_, events, cancel := bus.Subscribe("")
	defer cancel()

	first, ok := bus.Publish(reply("s1", "t1"))
	if !ok || first.ID == "" || first.At != clock.t.UnixMilli() {
		t.Fatalf("first publish = %+v, %v", first, ok)
	}
	second, _ := bus.Publish(reply("s1", "t2"))
	if second.ID == first.ID {
		t.Fatalf("ids repeat: %s", first.ID)
	}
	if got := <-events; got.ID != first.ID {
		t.Fatalf("subscriber got %s, want %s", got.ID, first.ID)
	}
	if got := <-events; got.ID != second.ID || got.Thread != "s1" {
		t.Fatalf("subscriber got %+v", got)
	}
	if bus.LastID() != second.ID {
		t.Fatalf("LastID = %s", bus.LastID())
	}
}

func TestBusDedupesByKey(t *testing.T) {
	bus := NewBus()
	if _, ok := bus.Publish(reply("s1", "t1")); !ok {
		t.Fatal("first publish dropped")
	}
	if _, ok := bus.Publish(reply("s1", "t1")); ok {
		t.Fatal("repeated key published twice")
	}
}

func TestBusResumeAfterID(t *testing.T) {
	clock := newClock()
	bus := NewBus(WithClock(clock.now))
	a, _ := bus.Publish(reply("s1", "a"))
	b, _ := bus.Publish(reply("s1", "b"))
	c, _ := bus.Publish(reply("s1", "c"))

	replay, _, cancel := bus.Subscribe(a.ID)
	cancel()
	if got := ids(replay); len(got) != 2 || got[0] != b.ID || got[1] != c.ID {
		t.Fatalf("resume after a = %v", got)
	}
	replay, _, cancel = bus.Subscribe("")
	cancel()
	if len(replay) != 0 {
		t.Fatalf("a new subscriber replays %v", ids(replay))
	}
	replay, _, cancel = bus.Subscribe("otherboot-7")
	cancel()
	if len(replay) != 3 {
		t.Fatalf("an id of another process replays %d events, want all 3", len(replay))
	}
	replay, _, cancel = bus.Subscribe(c.ID)
	cancel()
	if len(replay) != 0 {
		t.Fatalf("resume after the newest replays %v", ids(replay))
	}
}

func TestBusRingBufferLimits(t *testing.T) {
	clock := newClock()
	bus := NewBus(WithClock(clock.now), WithLimits(3, time.Hour))
	first, _ := bus.Publish(reply("s", "0"))
	for i := 1; i <= 4; i++ {
		bus.Publish(reply("s", string(rune('0'+i))))
	}
	replay, _, cancel := bus.Subscribe(first.ID)
	cancel()
	if len(replay) != 3 {
		t.Fatalf("buffer keeps %d events, want 3", len(replay))
	}

	clock.add(61 * time.Minute)
	bus.Publish(reply("s", "late"))
	replay, _, cancel = bus.Subscribe(first.ID)
	cancel()
	if len(replay) != 1 {
		t.Fatalf("events older than an hour are resumed: %d", len(replay))
	}
}

func TestBusSuppressesReplyOfVisibleSession(t *testing.T) {
	clock := newClock()
	bus := NewBus(WithClock(clock.now))
	bus.SetVisible("s1", true)
	if _, ok := bus.Publish(reply("s1", "t1")); ok {
		t.Fatal("reply of a visible session was published")
	}
	if _, ok := bus.Publish(reply("s2", "t1")); !ok {
		t.Fatal("reply of another session was dropped")
	}
	// Approvals of a visible session still notify.
	if _, ok := bus.Publish(ApprovalAsked("ap1", "coder", "bash", "ls", ApprovalTarget{SessionID: "s1"})); !ok {
		t.Fatal("approval of a visible session was dropped")
	}
	clock.add(PresenceTTL + time.Second)
	if _, ok := bus.Publish(reply("s1", "t2")); !ok {
		t.Fatal("presence did not expire")
	}
	bus.SetVisible("s1", true)
	bus.SetVisible("s1", false)
	if bus.Visible("s1") {
		t.Fatal("hidden report kept the session visible")
	}
}

func TestBusSlowSubscriberDoesNotBlock(t *testing.T) {
	bus := NewBus()
	_, _, cancel := bus.Subscribe("")
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer*3; i++ {
			bus.Publish(Reply("a", "", "s", strings.Repeat("x", i+1), "hi"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a full subscriber")
	}
}

func TestExcerpt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello **world**", "Hello world"},
		{"# Title\n\nSome *emphasis* and _more_.", "Title: Some emphasis and more."},
		{"## Done!\nAll set", "Done! All set"},
		{"text\n# Last heading", "text Last heading"},
		{"See [the docs](https://x.dev/a) and ![logo](a.png).", "See the docs and logo."},
		{"Run:\n```go\nfmt.Println(1)\n```\nthen `go test`.", "Run: [code] then go test."},
		{"- one\n- two\n1. three\n> quoted", "one two three quoted"},
		{"| a | b |\n|---|---|\n| 1 | 2 |", "a · b 1 · 2"},
		{"keep snake_case_names and 2*3*4", "keep snake_case_names and 2*3*4"},
		{"unclosed\n```\ncode", "unclosed [code]"},
		{"<https://lana.dev> <b>bold</b>", "https://lana.dev bold"},
	}
	for _, c := range cases {
		if got := Excerpt(c.in, 0); got != c.want {
			t.Errorf("Excerpt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExcerptCutsAtWordWithEllipsis(t *testing.T) {
	text := strings.Repeat("word ", 60)
	got := Excerpt(text, 140)
	if n := utf8.RuneCountInString(got); n > 140 {
		t.Fatalf("excerpt has %d runes", n)
	}
	if !strings.HasSuffix(got, "word…") {
		t.Fatalf("excerpt does not end at a word: %q", got)
	}
	if got := Cut("short", 140); got != "short" {
		t.Fatalf("Cut short = %q", got)
	}
	if got := Cut(strings.Repeat("é", 200), 10); utf8.RuneCountInString(got) != 10 || !strings.HasSuffix(got, Ellipsis) {
		t.Fatalf("Cut runes = %q", got)
	}
}

func TestEventMapping(t *testing.T) {
	r := Reply("coder", "", "s1", "t1", "**Done** — see [PR](http://x)\n\n```\nlog\n```")
	if r.Title != "coder · New chat" || r.Body != "Done — see PR [code]" || r.URL != "#/chats/coder" || r.Thread != "s1" {
		t.Fatalf("reply = %+v", r)
	}

	a := ApprovalAsked("ap1", "worker", "bash", strings.Repeat("x", 300), ApprovalTarget{SessionID: "child", RootID: "root"})
	if a.Title != "Approval needed · worker" || !a.TimeSensitive || a.ApprovalID != "ap1" || a.URL != "#/sessions/root" || a.Thread != "root" {
		t.Fatalf("approval = %+v", a)
	}
	if n := utf8.RuneCountInString(a.Body); n > ApprovalMax || !strings.HasPrefix(a.Body, "bash: xxx") {
		t.Fatalf("approval body = %q (%d)", a.Body, n)
	}
	wa := ApprovalAsked("ap2", "w", "write_file", "a.txt", ApprovalTarget{SessionID: "ws", WorkflowID: "wf1"})
	if wa.URL != "#/workflows/wf1" || wa.Thread != "workflow:wf1" || wa.WorkflowID != "wf1" {
		t.Fatalf("workflow approval = %+v", wa)
	}
	d := ApprovalDecided("ap1", "allowed_once", ApprovalTarget{SessionID: "s1"})
	if !d.Kind.Control() || d.ApprovalID != "ap1" || d.Outcome != "allowed_once" {
		t.Fatalf("decided = %+v", d)
	}

	q := QuestionAsked("q1", "coder", "Which branch?", 2, ApprovalTarget{SessionID: "s1"})
	if q.Title != "Question from coder" || q.Body != "Which branch? (+2 more)" || q.QuestionID != "q1" {
		t.Fatalf("question = %+v", q)
	}

	run := WorkflowRun{WorkflowID: "wf1", WorkflowName: "Daily", RunID: "r9", Number: 9, Trigger: "schedule", StartedAt: 1000, EndedAt: 1000 + 185_000, Summary: "**3** items", Error: "boom\nstack"}
	if e := WorkflowStarted(run); e.Title != "Workflow started · Daily" || e.Body != "Trigger: schedule" || !e.Silent || e.URL != "#/workflows/wf1/runs/9" || e.Thread != "workflow:wf1:r9" {
		t.Fatalf("started = %+v", e)
	}
	if e := WorkflowFinished(run); e.Title != "Workflow done · Daily · 3m 05s" || e.Body != "3 items" || e.Silent {
		t.Fatalf("finished = %+v", e)
	}
	if e := WorkflowFailed(run); e.Title != "Workflow failed · Daily" || e.Body != "boom stack" || e.Silent {
		t.Fatalf("failed = %+v", e)
	}
	w := WorkflowWaiting(run, "d1", "Send the mail?", WorkflowApprovalID("wf1", "d1"))
	if w.Title != "Approval needed · Daily" || w.ApprovalID != "wf:wf1:d1" || !w.TimeSensitive {
		t.Fatalf("waiting = %+v", w)
	}
	if wf, dec, ok := ParseWorkflowApprovalID(w.ApprovalID); !ok || wf != "wf1" || dec != "d1" {
		t.Fatalf("parse = %s %s %v", wf, dec, ok)
	}
	if _, _, ok := ParseWorkflowApprovalID("abc"); ok {
		t.Fatal("a broker id parsed as a workflow id")
	}
}

func TestChatURL(t *testing.T) {
	cases := []struct{ agent, want string }{
		{"coder", "#/chats/coder"},
		{"", "#/chats"},
		{"   ", "#/chats"},
		{"my agent/one", "#/chats/my%20agent%2Fone"},
	}
	for _, c := range cases {
		if got := ChatURL(c.agent); got != c.want {
			t.Errorf("ChatURL(%q) = %q, want %q", c.agent, got, c.want)
		}
	}
	if got := Reply("my agent/one", "t", "s", "turn", "x").URL; got != "#/chats/my%20agent%2Fone" {
		t.Errorf("reply URL = %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                             "<1s",
		45 * time.Second:              "45s",
		3*time.Minute + 5*time.Second: "3m 05s",
		time.Hour + 2*time.Minute + 9*time.Second: "1h 02m",
	} {
		if got := FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

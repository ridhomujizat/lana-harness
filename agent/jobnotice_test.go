package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestJobNoticeText covers the exact REQ-JBN-002 text and its bounds.
func TestJobNoticeText(t *testing.T) {
	n := core.JobNotice{ID: "bash-1", Command: "npm run dev", Status: "exited", ExitCode: 1, Tail: "boom"}
	got := JobNoticeText(n, "4m")
	want := "<task-notification>\n" +
		"job: bash-1\n" +
		"command: npm run dev\n" +
		"status: exited 1\n" +
		"runtime: 4m\n" +
		"output tail:\n" +
		"boom\n" +
		"</task-notification>\n" +
		"Background job bash-1 ended. Read more with job_output if needed, then tell the owner what happened and whether anything must be restarted."
	if got != want {
		t.Fatalf("JobNoticeText = %q, want %q", got, want)
	}

	if killed := JobNoticeText(core.JobNotice{ID: "bash-2", Status: "killed"}, "12s"); !strings.Contains(killed, "status: killed\n") {
		t.Fatalf("killed status missing: %q", killed)
	}

	// The command is cut to its first 200 runes, rune-safe.
	long := strings.Repeat("x", 250)
	out := JobNoticeText(core.JobNotice{ID: "j", Command: long, Status: "exited"}, "1s")
	if !strings.Contains(out, "command: "+strings.Repeat("x", 200)+"\n") {
		t.Fatal("command is not cut to 200 runes")
	}
	multi := strings.Repeat("é", 250)
	out = JobNoticeText(core.JobNotice{ID: "j", Command: multi, Status: "exited"}, "1s")
	if !utf8.ValidString(out) || strings.Contains(out, "Ã") {
		t.Fatalf("multi-byte command is not rune-safe: %q", out)
	}

	// The tail keeps only its last 2000 runes.
	tail := strings.Repeat("y", 2500)
	out = JobNoticeText(core.JobNotice{ID: "j", Status: "exited", Tail: tail}, "1s")
	if !strings.Contains(out, strings.Repeat("y", 2000)) || strings.Contains(out, strings.Repeat("y", 2001)) {
		t.Fatal("tail is not cut to its last 2000 runes")
	}
}

// TestJobNoticeTextOwnerStop covers REQ-JBN-002: a kill from the web Stop
// button says the owner stopped it; any other kill stays plain.
func TestJobNoticeTextOwnerStop(t *testing.T) {
	owner := core.JobNotice{ID: "bash-1", Status: "killed", StoppedBy: "owner"}
	got := JobNoticeText(owner, "1m")
	if line := "status: killed (stopped by the owner from the web)\n"; !strings.Contains(got, line) {
		t.Fatalf("owner stop status missing: %q", got)
	}

	plain := JobNoticeText(core.JobNotice{ID: "bash-2", Status: "killed"}, "1m")
	if !strings.Contains(plain, "status: killed\n") || strings.Contains(plain, "stopped by the owner") {
		t.Fatalf("plain kill status=%q", plain)
	}
}

// TestSendJobNoticeIdleStartsTurn covers REQ-JBN-003 and REQ-JBN-004: a notice
// for an idle session starts a turn whose user/message is a runtime note
// carrying the job payload.
func TestSendJobNoticeIdleStartsTurn(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("noted")}}, nil)
	notice := core.JobNotice{ID: "bash-1", Command: "npm test", Status: "exited", ExitCode: 1, Tail: "3 failing"}
	text := JobNoticeText(notice, "4m")
	if err := h.reg.SendJobNotice(h.info.ID, notice, text); err != nil {
		t.Fatal(err)
	}
	waitForIdle(t, h)

	var msg core.UserMessage
	found := false
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		if err := json.Unmarshal(event.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("the notice did not produce a user/message")
	}
	if !msg.Runtime {
		t.Fatalf("the notice is not a runtime note: %+v", msg)
	}
	if msg.Job == nil || msg.Job.ID != "bash-1" || msg.Job.Command != "npm test" || msg.Job.Status != "exited" || msg.Job.ExitCode != 1 || msg.Job.Tail != "3 failing" {
		t.Fatalf("notice job payload=%+v", msg.Job)
	}
	if !strings.Contains(userText(msg), "<task-notification>") || !strings.Contains(userText(msg), "Background job bash-1 ended.") {
		t.Fatalf("the model did not see the notice text: %q", userText(msg))
	}
}

// TestSendJobNoticeBusyTakesAtNextStep covers REQ-JBN-003: a notice for a busy
// session is queued and taken at the running turn's next step, like an owner
// message sent mid-turn. It is stored as a runtime note with the job payload.
func TestSendJobNoticeBusyTakesAtNextStep(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}}, nil)
	notice := core.JobNotice{ID: "bash-2", Command: "npm run dev", Status: "killed", Tail: "stopped"}
	text := JobNoticeText(notice, "2m")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	h.hooks.AgentPreStep.Use(func(ctx context.Context, in core.PreStepInput, next core.Next[core.PreStepInput, core.PreStepDecision]) (core.PreStepDecision, error) {
		once.Do(func() {
			entered <- struct{}{}
			<-release
		})
		return next(ctx, in)
	})
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := h.reg.SendJobNotice(h.info.ID, notice, text); err != nil {
		t.Fatal(err)
	}
	close(release)
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}

	var users, notices int
	var job core.UserMessage
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		users++
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Job != nil {
			notices++
			job = payload
		}
	}
	if users != 2 || notices != 1 {
		t.Fatalf("users=%d notices=%d events=%v", users, notices, kinds(h.events(t)))
	}
	if !job.Runtime || job.Job.ID != "bash-2" || job.Job.Status != "killed" {
		t.Fatalf("notice payload=%+v", job)
	}
}

// TestSendJobNoticeDropped covers REQ-JBN-005: a notice for a missing session
// or a closed registry returns nil and does nothing.
func TestSendJobNoticeDropped(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	notice := core.JobNotice{ID: "bash-3", Status: "exited"}
	if err := h.reg.SendJobNotice("missing-session", notice, "text"); err != nil {
		t.Fatalf("missing session: %v", err)
	}
	if err := h.reg.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.reg.SendJobNotice(h.info.ID, notice, "text"); err != nil {
		t.Fatalf("closed registry: %v", err)
	}
}

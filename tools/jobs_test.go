package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeJobService drives the job tools without a real process.
type fakeJobService struct {
	startJob core.JobView
	startErr error

	list []core.JobView

	readText string
	dropped  int64
	readJob  core.JobView
	readErr  error

	tail    string
	tailErr error

	waitJob core.JobView

	killJob core.JobView
	killErr error

	waitCalled  bool
	waitTimeout time.Duration
}

func (f *fakeJobService) Start(sessionID, workspace, command string) (core.JobView, error) {
	return f.startJob, f.startErr
}

func (f *fakeJobService) List(sessionID string) []core.JobView { return f.list }

func (f *fakeJobService) ReadNew(sessionID, id string, max int) (string, int64, core.JobView, error) {
	return f.readText, f.dropped, f.readJob, f.readErr
}

func (f *fakeJobService) Tail(sessionID, id string, max int) (string, error) {
	return f.tail, f.tailErr
}

func (f *fakeJobService) Wait(ctx context.Context, sessionID, id string, d time.Duration) core.JobView {
	f.waitCalled = true
	f.waitTimeout = d
	return f.waitJob
}

func (f *fakeJobService) Kill(sessionID, id string) (core.JobView, error) {
	return f.killJob, f.killErr
}

func callJobTool(t *testing.T, tool core.Tool, jobs core.JobService, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), core.ToolCtx{SessionID: "s1", Jobs: jobs}, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestJobOutputFormatsOutputAndStatus(t *testing.T) {
	fake := &fakeJobService{
		readText: "hello\nworld\n",
		readJob:  core.JobView{ID: "bash-1", Status: core.JobStatusRunning},
	}
	out := callJobTool(t, NewJobOutput(), fake, `{"job_id":"bash-1"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	want := "hello\nworld\n[status: running]"
	if out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
}

func TestJobOutputDroppedNotice(t *testing.T) {
	fake := &fakeJobService{
		dropped:  100,
		readText: "tail\n",
		readJob:  core.JobView{ID: "bash-1", Status: core.JobStatusKilled},
	}
	out := callJobTool(t, NewJobOutput(), fake, `{"job_id":"bash-1"}`)
	want := "[dropped 100 bytes]\ntail\n[status: killed]"
	if out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
}

func TestJobOutputEmptyBody(t *testing.T) {
	fake := &fakeJobService{readJob: core.JobView{ID: "bash-1", Status: core.JobStatusRunning}}
	out := callJobTool(t, NewJobOutput(), fake, `{"job_id":"bash-1"}`)
	if out.Output != "[status: running]" {
		t.Fatalf("output=%q", out.Output)
	}
}

func TestJobOutputWaitExited(t *testing.T) {
	fake := &fakeJobService{
		waitJob:  core.JobView{ID: "bash-1", Status: core.JobStatusExited, ExitCode: 0},
		readText: "done\n",
		readJob:  core.JobView{ID: "bash-1", Status: core.JobStatusExited, ExitCode: 0},
	}
	out := callJobTool(t, NewJobOutput(), fake, `{"job_id":"bash-1","wait":true}`)
	if !fake.waitCalled || fake.waitTimeout != 30*time.Second {
		t.Fatalf("wait called=%v timeout=%v", fake.waitCalled, fake.waitTimeout)
	}
	if !strings.HasSuffix(out.Output, "[status: exited 0]") {
		t.Fatalf("output=%q", out.Output)
	}
}

func TestJobOutputWaitCustomTimeout(t *testing.T) {
	fake := &fakeJobService{waitJob: core.JobView{ID: "bash-1", Status: core.JobStatusRunning}, readJob: core.JobView{ID: "bash-1", Status: core.JobStatusRunning}}
	callJobTool(t, NewJobOutput(), fake, `{"job_id":"bash-1","wait":true,"timeout_secs":120}`)
	if fake.waitTimeout != 120*time.Second {
		t.Fatalf("timeout=%v want 120s", fake.waitTimeout)
	}
}

func TestJobOutputTimeoutBounds(t *testing.T) {
	out := callJobTool(t, NewJobOutput(), &fakeJobService{}, `{"job_id":"bash-1","timeout_secs":601}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "between 1 and 600") {
		t.Fatalf("out=%+v", out)
	}
}

func TestJobOutputWaitUnknown(t *testing.T) {
	out := callJobTool(t, NewJobOutput(), &fakeJobService{}, `{"job_id":"bash-ghost","wait":true}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "unknown job bash-ghost") {
		t.Fatalf("out=%+v", out)
	}
}

func TestJobOutputCrossSessionRefused(t *testing.T) {
	fake := &fakeJobService{readErr: errors.New("unknown job")}
	out := callJobTool(t, NewJobOutput(), fake, `{"job_id":"bash-1"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "unknown job") {
		t.Fatalf("out=%+v", out)
	}
}

func TestJobListFormat(t *testing.T) {
	now := time.Now()
	long := strings.Repeat("x", 130)
	fake := &fakeJobService{list: []core.JobView{
		{ID: "bash-1", Status: core.JobStatusRunning, PID: 10, StartedAt: now.Add(-4 * time.Minute), Command: "npm run dev"},
		{ID: "bash-2", Status: core.JobStatusExited, ExitCode: 3, PID: 11, StartedAt: now.Add(-2 * time.Second), EndedAt: now, Command: long},
	}}
	out := callJobTool(t, NewJobList(), fake, `{}`)
	lines := strings.Split(out.Output, "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d output=%q", len(lines), out.Output)
	}
	if lines[0] != "bash-1 running pid=10 4m — npm run dev" {
		t.Fatalf("line0=%q", lines[0])
	}
	wantSecond := "bash-2 exited 3 pid=11 2s — " + strings.Repeat("x", 120)
	if lines[1] != wantSecond {
		t.Fatalf("line1=%q want %q", lines[1], wantSecond)
	}
}

func TestJobListEmpty(t *testing.T) {
	out := callJobTool(t, NewJobList(), &fakeJobService{}, `{}`)
	if out.Output != "no background jobs" {
		t.Fatalf("output=%q", out.Output)
	}
}

func TestJobKill(t *testing.T) {
	fake := &fakeJobService{killJob: core.JobView{ID: "bash-1", Status: core.JobStatusKilled}}
	out := callJobTool(t, NewJobKill(), fake, `{"job_id":"bash-1"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "job bash-1 killed" {
		t.Fatalf("out=%+v", out)
	}
}

func TestJobKillUnknown(t *testing.T) {
	fake := &fakeJobService{killErr: errors.New("unknown job")}
	out := callJobTool(t, NewJobKill(), fake, `{"job_id":"bash-ghost"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "unknown job") {
		t.Fatalf("out=%+v", out)
	}
}

func TestJobToolsWithoutRegistry(t *testing.T) {
	for _, tool := range []core.Tool{NewJobOutput(), NewJobList(), NewJobKill()} {
		out, err := tool.Call(context.Background(), core.ToolCtx{SessionID: "s1"}, json.RawMessage(`{"job_id":"bash-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "not available") {
			t.Fatalf("%s out=%+v", tool.Spec().Name, out)
		}
	}
}

func TestJobToolRisk(t *testing.T) {
	if got := NewJobOutput().Risk(nil); got != core.RiskRead {
		t.Fatalf("job_output risk=%q want read", got)
	}
	if got := NewJobList().Risk(nil); got != core.RiskRead {
		t.Fatalf("job_list risk=%q want read", got)
	}
	// job_kill carries the bash risk class (REQ-JOB-015).
	if got := NewJobKill().Risk(nil); got != NewBash(nil).Risk(nil) {
		t.Fatalf("job_kill risk=%q want %q", got, NewBash(nil).Risk(nil))
	}
}

package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/jobs"
	"github.com/ridhomujizat/lana-harness/tools"
)

// TestAddCompanionTools covers REQ-JOB-015: the job tools are offered with
// bash, dropped without it, and removed by disabled_tools.
func TestAddCompanionTools(t *testing.T) {
	cases := []struct {
		name     string
		tools    []string
		disabled []string
		want     []string
	}{
		{
			name:  "with bash",
			tools: []string{"read_file", "bash"},
			want:  []string{"read_file", "bash", "job_kill", "job_list", "job_output"},
		},
		{
			name:  "without bash",
			tools: []string{"read_file", "web_fetch"},
			want:  []string{"read_file", "web_fetch"},
		},
		{
			name:  "listed job tool without bash is dropped",
			tools: []string{"job_output", "read_file"},
			want:  []string{"read_file"},
		},
		{
			name:     "disabled job tool",
			tools:    []string{"bash"},
			disabled: []string{"job_kill"},
			want:     []string{"bash", "job_list", "job_output"},
		},
		{
			name:     "disabled bash drops the job tools",
			tools:    []string{"bash"},
			disabled: []string{"bash"},
			want:     []string{"bash"},
		},
		{
			name:  "already listed job tool is not duplicated",
			tools: []string{"bash", "job_output"},
			want:  []string{"bash", "job_output", "job_kill", "job_list"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := slices.Clone(tc.tools)
			got := addCompanionTools(input, tc.disabled, func(string) bool { return true })
			if !slices.Equal(got, tc.want) {
				t.Fatalf("addCompanionTools = %v, want %v", got, tc.want)
			}
			if !slices.Equal(input, tc.tools) {
				t.Fatalf("addCompanionTools mutated the input: %v", input)
			}
			if containsString(tc.disabled, "bash") && hasAnyJobTool(got) {
				t.Fatalf("agent without bash got job tools: %v", got)
			}
		})
	}
}

// TestAddCompanionToolsSkipsUnregistered covers a registry that does not have
// the job tools: none is offered (REQ-JOB-015).
func TestAddCompanionToolsSkipsUnregistered(t *testing.T) {
	got := addCompanionTools([]string{"read_file", "bash"}, nil, func(string) bool { return false })
	want := []string{"read_file", "bash"}
	if !slices.Equal(got, want) {
		t.Fatalf("addCompanionTools = %v, want %v", got, want)
	}
}

func hasAnyJobTool(list []string) bool {
	for _, item := range list {
		if isCompanionTool(item) {
			return true
		}
	}
	return false
}

// TestBackgroundJobTools covers the registry round-trip through the tools:
// start, tail, wait for exit, list, and kill (REQ-JOB-010..014).
func TestBackgroundJobTools(t *testing.T) {
	reg := jobs.New(t.TempDir())
	t.Cleanup(reg.Shutdown)
	svc := NewJobService(reg)
	ws := t.TempDir()
	tc := core.ToolCtx{SessionID: "s1", Workspace: ws, Jobs: svc}

	bash := tools.NewBash(&tools.Guard{Workspace: ws})
	out, err := bash.Call(context.Background(), tc, json.RawMessage(`{"command":"echo ready; sleep 30","run_in_background":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("start out=%+v", out)
	}
	if !strings.Contains(out.Output, "started job bash-") || !strings.Contains(out.Output, "[status: running]") || !strings.Contains(out.Output, "ready") {
		t.Fatalf("start output=%q", out.Output)
	}

	var runningID string
	for _, job := range reg.List("s1") {
		if job.Status == jobs.StatusRunning {
			runningID = job.ID
		}
	}
	if runningID == "" {
		t.Fatal("no running job registered")
	}

	// job_output with wait: true returns when a short job exits.
	short, err := reg.Start("s1", ws, "sleep 0.2; exit 0")
	if err != nil {
		t.Fatal(err)
	}
	out, err = tools.NewJobOutput().Call(context.Background(), tc, json.RawMessage(`{"job_id":"`+short.ID+`","wait":true,"timeout_secs":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimRight(out.Output, "\n"), "[status: exited 0]") {
		t.Fatalf("job_output wait output=%q", out.Output)
	}

	// job_list shows the running job.
	out, err = tools.NewJobList().Call(context.Background(), tc, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "running") {
		t.Fatalf("job_list output=%q", out.Output)
	}

	// job_kill stops the running job.
	out, err = tools.NewJobKill().Call(context.Background(), tc, json.RawMessage(`{"job_id":"`+runningID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "job "+runningID+" killed") {
		t.Fatalf("job_kill output=%q", out.Output)
	}
}

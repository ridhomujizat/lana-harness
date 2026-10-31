package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	jobOutputMaxChars    = 16000
	jobListCommandMax    = 120
	jobOutputDefaultWait = 30
	jobOutputMaxWait     = 600
	jobOutputDescription = "Reads new output from a background job started with bash run_in_background. Returns the output written since the previous job_output of that job, then a [status: running], [status: exited <code>], or [status: killed] line. With wait: true it waits up to timeout_secs (default 30, max 600) for the job to end."
	jobListDescription   = "Lists this session's background jobs, one per line: id, status, pid, age, and the command. Ends with no background jobs when the session has none."
	jobKillDescription   = "Stops a background job: SIGTERM to its process group, then SIGKILL after 5 seconds. Returns the final status."
)

var (
	jobOutputSpec = json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","minLength":1,"description":"Job id returned by bash run_in_background."},"wait":{"type":"boolean","description":"Wait up to timeout_secs for the job to end. Defaults to false."},"timeout_secs":{"type":"integer","minimum":1,"maximum":600,"description":"Longest time to wait in seconds (1-600). Defaults to 30."}},"required":["job_id"],"additionalProperties":false}`)
	jobListSpec   = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	jobKillSpec   = json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","minLength":1,"description":"Job id to stop."}},"required":["job_id"],"additionalProperties":false}`)
)

type jobOutputTool struct{}

// NewJobOutput creates the job_output tool.
func NewJobOutput() core.Tool { return &jobOutputTool{} }

func (t *jobOutputTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "job_output", Description: jobOutputDescription, InputSchema: jobOutputSpec}
}
func (t *jobOutputTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *jobOutputTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	if tc.Jobs == nil {
		return errorOutput("job_output is not available in this runtime")
	}
	var in struct {
		JobID       string `json:"job_id"`
		Wait        bool   `json:"wait"`
		TimeoutSecs int    `json:"timeout_secs"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.JobID == "" {
		return inputErrorOutput(fmt.Errorf("job_id is required"))
	}
	if in.TimeoutSecs == 0 {
		in.TimeoutSecs = jobOutputDefaultWait
	}
	if in.TimeoutSecs < 1 || in.TimeoutSecs > jobOutputMaxWait {
		return errorOutput(fmt.Sprintf("timeout_secs must be between 1 and %d", jobOutputMaxWait))
	}
	if in.Wait {
		if job := tc.Jobs.Wait(ctx, tc.SessionID, in.JobID, time.Duration(in.TimeoutSecs)*time.Second); job.ID == "" {
			return errorOutput("unknown job " + in.JobID)
		}
	}
	text, dropped, job, err := tc.Jobs.ReadNew(tc.SessionID, in.JobID, jobOutputMaxChars)
	if err != nil {
		return errorOutput(err.Error())
	}
	lines := make([]string, 0, 3)
	if dropped > 0 {
		lines = append(lines, fmt.Sprintf("[dropped %d bytes]", dropped))
	}
	if body := strings.TrimRight(text, "\n"); body != "" {
		lines = append(lines, body)
	}
	lines = append(lines, jobStatusLabel(job))
	return successOutput(strings.Join(lines, "\n"))
}

type jobListTool struct{}

// NewJobList creates the job_list tool.
func NewJobList() core.Tool { return &jobListTool{} }

func (t *jobListTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "job_list", Description: jobListDescription, InputSchema: jobListSpec}
}
func (t *jobListTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *jobListTool) Call(_ context.Context, tc core.ToolCtx, _ json.RawMessage) (core.ToolOutput, error) {
	if tc.Jobs == nil {
		return errorOutput("job_list is not available in this runtime")
	}
	list := tc.Jobs.List(tc.SessionID)
	if len(list) == 0 {
		return successOutput("no background jobs")
	}
	lines := make([]string, 0, len(list))
	for _, job := range list {
		lines = append(lines, fmt.Sprintf("%s %s pid=%d %s — %s", job.ID, jobStatusWord(job), job.PID, jobAgeLabel(job), cutJobRunes(job.Command, jobListCommandMax)))
	}
	return successOutput(strings.Join(lines, "\n"))
}

type jobKillTool struct{}

// NewJobKill creates the job_kill tool.
func NewJobKill() core.Tool { return &jobKillTool{} }

func (t *jobKillTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "job_kill", Description: jobKillDescription, InputSchema: jobKillSpec}
}

// Risk is the bash risk class: job_kill runs a process signal (REQ-JOB-015).
func (t *jobKillTool) Risk(json.RawMessage) core.Risk { return core.RiskExec }

func (t *jobKillTool) Call(_ context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	if tc.Jobs == nil {
		return errorOutput("job_kill is not available in this runtime")
	}
	var in struct {
		JobID string `json:"job_id"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if in.JobID == "" {
		return inputErrorOutput(fmt.Errorf("job_id is required"))
	}
	job, err := tc.Jobs.Kill(tc.SessionID, in.JobID)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(fmt.Sprintf("job %s %s", job.ID, jobStatusWord(job)))
}

// jobStatusWord renders the status column of job_list and job_kill.
func jobStatusWord(job core.JobView) string {
	switch job.Status {
	case core.JobStatusExited:
		return fmt.Sprintf("exited %d", job.ExitCode)
	case core.JobStatusKilled:
		return "killed"
	default:
		return "running"
	}
}

// jobAgeLabel renders how long the job has run: since it started while running,
// or its total runtime once ended.
func jobAgeLabel(job core.JobView) string {
	end := time.Now()
	if !job.EndedAt.IsZero() {
		end = job.EndedAt
	}
	return ageLabel(end.Sub(job.StartedAt))
}

func ageLabel(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func cutJobRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

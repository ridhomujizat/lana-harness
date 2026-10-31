package main

import (
	"context"
	"errors"
	"testing"

	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

// TestWebBackendJobs covers the JobBackend adapter of REQ-JOB-020: the job
// mapping, the millisecond timestamps, and the ErrUnknownJob mapping.
func TestWebBackendJobs(t *testing.T) {
	rt, home := webRuntime(t, mock.Script{})
	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)
	backend := newWebBackend(rt, home, leases, context.Background())

	started, err := rt.Jobs().Start("s1", home, "sleep 30")
	if err != nil {
		t.Fatalf("start job: %v", err)
	}
	t.Cleanup(func() { _, _ = rt.Jobs().Kill("s1", started.ID) })

	jobs, err := backend.SessionJobs(context.Background(), "s1")
	if err != nil {
		t.Fatalf("SessionJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != started.ID {
		t.Fatalf("jobs = %+v", jobs)
	}
	if jobs[0].Status != "running" || jobs[0].EndedAt != nil {
		t.Fatalf("job = %+v", jobs[0])
	}
	if jobs[0].StartedAt != started.StartedAt.UnixMilli() {
		t.Fatalf("started_at = %d, want %d", jobs[0].StartedAt, started.StartedAt.UnixMilli())
	}

	stopped, err := backend.StopJob(context.Background(), "s1", started.ID)
	if err != nil {
		t.Fatalf("StopJob: %v", err)
	}
	if stopped.Status != "killed" || stopped.EndedAt == nil {
		t.Fatalf("stopped = %+v", stopped)
	}

	if _, err := backend.StopJob(context.Background(), "s1", "bash-missing"); !errors.Is(err, web.ErrJobNotFound) {
		t.Fatalf("unknown stop err = %v, want ErrJobNotFound", err)
	}
}

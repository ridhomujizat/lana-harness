package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeJobBackend adds the background-job port to the in-memory backend.
type fakeJobBackend struct {
	*fakeWebBackend
	jobs []Job
}

func (b *fakeJobBackend) SessionJobs(_ context.Context, sessionID string) ([]Job, error) {
	if sessionID != "s1" {
		return nil, nil
	}
	return append([]Job(nil), b.jobs...), nil
}

func (b *fakeJobBackend) StopJob(_ context.Context, sessionID, jobID string) (Job, error) {
	if sessionID != "s1" {
		return Job{}, ErrJobNotFound
	}
	for _, job := range b.jobs {
		if job.ID == jobID {
			ended := int64(9000)
			stopped := job
			stopped.Status = "killed"
			stopped.EndedAt = &ended
			return stopped, nil
		}
	}
	return Job{}, ErrJobNotFound
}

// TestSessionJobsListCoversREQJOB020 covers the list shape of REQ-JOB-020:
// newest first, ended_at null while running, and a tail capped at 4000
// characters without splitting a rune.
func TestSessionJobsListCoversREQJOB020(t *testing.T) {
	ended := int64(2500)
	backend := &fakeJobBackend{
		fakeWebBackend: newFakeWebBackend(),
		jobs: []Job{
			{ID: "bash-old", Command: "npm run dev", Status: "exited", ExitCode: 1, PID: 42, StartedAt: 1000, EndedAt: &ended},
			{ID: "bash-new", Command: "sleep 1000", Status: "running", PID: 43, StartedAt: 3000, Tail: strings.Repeat("é", 5000)},
			{ID: "bash-mid", Command: "npm test", Status: "exited", PID: 44, StartedAt: 2000},
		},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/s1/jobs", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got []Job
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got) != 3 {
		t.Fatalf("jobs = %d, want 3", len(got))
	}
	for i, id := range []string{"bash-new", "bash-mid", "bash-old"} {
		if got[i].ID != id {
			t.Fatalf("job %d = %q, want %q", i, got[i].ID, id)
		}
	}
	if got[0].EndedAt != nil {
		t.Fatalf("running job ended_at = %v, want null", *got[0].EndedAt)
	}
	if got[2].EndedAt == nil || *got[2].EndedAt != ended {
		t.Fatalf("exited job ended_at = %v, want %d", got[2].EndedAt, ended)
	}
	if tail := got[0].Tail; tail != strings.Repeat("é", tailMaxChars) {
		t.Fatalf("tail runes = %d, valid = %t", utf8.RuneCountInString(tail), utf8.ValidString(tail))
	}
	if !utf8.ValidString(got[0].Tail) {
		t.Fatal("tail is not valid UTF-8")
	}
}

// TestSessionJobsEmptyIsArray covers the empty-list contract: a bare JSON
// array, never null.
func TestSessionJobsEmptyIsArray(t *testing.T) {
	backend := &fakeJobBackend{fakeWebBackend: newFakeWebBackend()}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/s1/jobs", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want []", body)
	}
}

// TestSessionJobsStopCoversREQJOB020 covers the stop response: the job after
// the kill, in the same shape as one list element.
func TestSessionJobsStopCoversREQJOB020(t *testing.T) {
	backend := &fakeJobBackend{
		fakeWebBackend: newFakeWebBackend(),
		jobs: []Job{
			{ID: "bash-run", Command: "sleep 1000", Status: "running", PID: 43, StartedAt: 3000},
		},
	}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s1/jobs/bash-run/stop", "", cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got Job
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if got.ID != "bash-run" || got.Status != "killed" || got.EndedAt == nil {
		t.Fatalf("stopped = %+v", got)
	}
}

// TestSessionJobsStopUnknownIs404 covers the unknown-id refusal.
func TestSessionJobsStopUnknownIs404(t *testing.T) {
	backend := &fakeJobBackend{fakeWebBackend: newFakeWebBackend()}
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/s1/jobs/bash-missing/stop", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// TestSessionJobsRequiresAuth covers the auth middleware on both routes.
func TestSessionJobsRequiresAuth(t *testing.T) {
	backend := &fakeJobBackend{fakeWebBackend: newFakeWebBackend()}
	s, _ := newSessionTestServer(t, backend)

	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/sessions/s1/jobs"},
		{http.MethodPost, "/v1/sessions/s1/jobs/bash-x/stop"},
	} {
		rr := serveAuth(s, authRequest(target.method, target.path, ""))
		assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	}
}

// TestSessionJobsWithoutPortIs500 covers a Backend that does not implement the
// optional JobBackend port.
func TestSessionJobsWithoutPortIs500(t *testing.T) {
	s, cookie, _ := newLoggedInSessionServer(t, newFakeWebBackend())

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/s1/jobs", "", cookie, ""))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

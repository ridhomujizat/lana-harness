package web

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"unicode/utf8"
)

// ErrJobNotFound is returned by a JobBackend for an unknown job id or a job of
// another session (REQ-JOB-020).
var ErrJobNotFound = errors.New("job not found")

// tailMaxChars is the maximum number of tail characters the jobs endpoints
// return (REQ-JOB-020).
const tailMaxChars = 4000

// Job is one background job as the browser sees it. EndedAt is null while the
// job runs; StartedAt and EndedAt are epoch milliseconds, the unit the session
// routes use.
type Job struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exit_code"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"started_at"`
	EndedAt   *int64 `json:"ended_at"`
	Tail      string `json:"tail"`
}

// JobBackend is the optional port of the background-job endpoints. A Backend
// that also implements it installs the port in SetBackend. It is not part of
// the Backend interface so other implementations stay unchanged.
type JobBackend interface {
	// SessionJobs returns the session's jobs, oldest first. Tail is at most a
	// few thousand bytes; the handler trims it to tailMaxChars characters.
	SessionJobs(ctx context.Context, sessionID string) ([]Job, error)
	// StopJob kills the job and returns it after the kill. An unknown job id
	// or one of another session returns ErrJobNotFound.
	StopJob(ctx context.Context, sessionID, jobID string) (Job, error)
}

// registerJobRoutes registers REQ-JOB-020 through Handle so the auth, Host,
// Origin, and CSRF checks apply exactly as for the other session endpoints.
func (s *Server) registerJobRoutes() {
	s.Handle("GET /v1/sessions/{id}/jobs", http.HandlerFunc(s.handleListJobs))
	s.Handle("POST /v1/sessions/{id}/jobs/{job}/stop", http.HandlerFunc(s.handleStopJob))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	backend := s.jobBackendOrError(w)
	if backend == nil {
		return
	}
	jobs, err := backend.SessionJobs(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeJobError(w, "list jobs", err)
		return
	}
	// REQ-JOB-020: newest first. List returns oldest first.
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].StartedAt > jobs[j].StartedAt
	})
	if jobs == nil {
		jobs = []Job{}
	}
	for i := range jobs {
		jobs[i].Tail = trimTail(jobs[i].Tail)
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleStopJob(w http.ResponseWriter, r *http.Request) {
	backend := s.jobBackendOrError(w)
	if backend == nil {
		return
	}
	job, err := backend.StopJob(r.Context(), r.PathValue("id"), r.PathValue("job"))
	if err != nil {
		s.writeJobError(w, "stop job", err)
		return
	}
	job.Tail = trimTail(job.Tail)
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) jobBackendOrError(w http.ResponseWriter) JobBackend {
	if s.jobBackend == nil {
		s.logger.Error("web: job backend is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return nil
	}
	return s.jobBackend
}

func (s *Server) writeJobError(w http.ResponseWriter, operation string, err error) {
	if errors.Is(err, ErrJobNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// trimTail keeps the last tailMaxChars runes and drops a leading partial rune,
// so the value is valid UTF-8 and fits the contract (REQ-JOB-020).
func trimTail(text string) string {
	if utf8.RuneCountInString(text) > tailMaxChars {
		runes := []rune(text)
		text = string(runes[len(runes)-tailMaxChars:])
	}
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r != utf8.RuneError || size > 1 {
			break
		}
		text = text[size:]
	}
	return text
}

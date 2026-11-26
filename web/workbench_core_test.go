package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// fakeWorkbenchCore adds the spec 84 core control to the fake backend.
type fakeWorkbenchCore struct {
	*fakeWorkbench
	mu         sync.Mutex
	info       WorkbenchCore
	restartErr error
	restarts   int
}

func (f *fakeWorkbenchCore) CoreInfo(context.Context) (WorkbenchCore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, nil
}

func (f *fakeWorkbenchCore) RestartCore(context.Context) (WorkbenchCore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	if f.restartErr != nil {
		return WorkbenchCore{}, f.restartErr
	}
	return f.info, nil
}

// Spec 84 REQ-WBR-002: outdated is a running core with another build, or one
// that sends no version.
func TestNewWorkbenchCoreOutdated(t *testing.T) {
	for _, tc := range []struct {
		name            string
		running         bool
		version, commit string
		want            bool
	}{
		{"not running", false, "", "", false},
		{"older core without a version", true, "", "", true},
		{"other version", true, "0.6.0-beta.13", "abc", true},
		{"other commit", true, "0.6.0-beta.14", "def", true},
		{"same build", true, "0.6.0-beta.14", "abc", false},
	} {
		got := NewWorkbenchCore(tc.running, tc.version, tc.commit, "0.6.0-beta.14", "abc", 2)
		if got.Outdated != tc.want {
			t.Errorf("%s: outdated = %v, want %v", tc.name, got.Outdated, tc.want)
		}
		if got.DaemonVersion != "0.6.0-beta.14" || got.DaemonCommit != "abc" || got.Sessions != 2 {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

func decodeWorkbenchCore(t *testing.T, rr *httptest.ResponseRecorder) WorkbenchCore {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var info WorkbenchCore
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

// Spec 84 REQ-WBR-002, REQ-WBR-003: the GET reports the core, the POST
// restarts it, answers the new core, and appends an audit line.
func TestWorkbenchCoreGetAndRestart(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := &fakeWorkbenchCore{
		fakeWorkbench: newFakeWorkbench(),
		info:          NewWorkbenchCore(true, "0.6.0-beta.13", "old", "0.6.0-beta.14", "new", 3),
	}
	s.SetWorkbench(fake)

	info := decodeWorkbenchCore(t, workbenchRequest(s, http.MethodGet, "/v1/workbench/core", "", cookie, csrf))
	if !info.Running || !info.Outdated || info.Version != "0.6.0-beta.13" || info.Sessions != 3 {
		t.Fatalf("GET = %+v", info)
	}
	var raw map[string]any
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/core", "", cookie, csrf)
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"running", "version", "commit", "daemon_version", "daemon_commit", "outdated", "sessions"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("GET body lacks %q: %s", key, rr.Body.String())
		}
	}

	// Without the CSRF header the restart is refused and nothing restarts.
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/core/restart", "", cookie, "")
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
	if fake.restarts != 0 {
		t.Fatalf("restarts = %d after a refused request", fake.restarts)
	}

	fake.mu.Lock()
	fake.info = NewWorkbenchCore(true, "0.6.0-beta.14", "new", "0.6.0-beta.14", "new", 3)
	fake.mu.Unlock()
	info = decodeWorkbenchCore(t, workbenchRequest(s, http.MethodPost, "/v1/workbench/core/restart", "", cookie, csrf))
	if info.Outdated || info.Sessions != 3 || fake.restarts != 1 {
		t.Fatalf("restart = %+v, restarts = %d", info, fake.restarts)
	}
	page, err := webfiles.ReadAudit(webfiles.AuditPath(s.auditHome()), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Entries {
		if e.Kind == "workbench.core-restart" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no workbench.core-restart audit line: %+v", page.Entries)
	}
}

// A restart while one runs is 409; a core that does not come back is 503.
func TestWorkbenchCoreRestartErrors(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	fake := &fakeWorkbenchCore{fakeWorkbench: newFakeWorkbench(), restartErr: ErrWorkbenchRestartRunning}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/core/restart", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "restart_running")

	fake.mu.Lock()
	fake.restartErr = errors.New("the new Workbench core did not start: timeout")
	fake.mu.Unlock()
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/core/restart", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")
	if !strings.Contains(rr.Body.String(), "did not start") {
		t.Fatalf("body = %q, want the reason", rr.Body.String())
	}
}

// A backend without core control, or none at all, answers 503.
func TestWorkbenchCoreUnavailable(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/core", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")

	s.SetWorkbench(newFakeWorkbench())
	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/core", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/core/restart", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")
}

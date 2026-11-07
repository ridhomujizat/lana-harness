package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// fakeUpdateStore is an in-memory web.UpdateStore for the handler tests.
type fakeUpdateStore struct {
	status      UpdateStatus
	checkErr    error
	applyErr    error
	cancel      bool
	checks      int
	applies     []string
	forces      []bool
	skips       []string
	channels    []*string
	checkFlags  []*bool
	clears      int
	settingsErr error
}

func (f *fakeUpdateStore) UpdateStatus(context.Context) UpdateStatus { return f.status }

func (f *fakeUpdateStore) CheckUpdate(context.Context) (UpdateStatus, error) {
	f.checks++
	if f.checkErr != nil {
		return UpdateStatus{}, f.checkErr
	}
	return f.status, nil
}

func (f *fakeUpdateStore) ApplyUpdate(_ context.Context, version string, force bool) (UpdateStatus, error) {
	f.applies = append(f.applies, version)
	f.forces = append(f.forces, force)
	if f.applyErr != nil {
		return UpdateStatus{}, f.applyErr
	}
	status := f.status
	status.Job = &UpdateJob{State: UpdateStateDownloading, Version: version, Progress: &UpdateProgress{}}
	return status, nil
}

func (f *fakeUpdateStore) CancelUpdate(context.Context) (bool, error) { return f.cancel, nil }

func (f *fakeUpdateStore) SkipUpdate(_ context.Context, version string) (UpdateStatus, error) {
	f.skips = append(f.skips, version)
	status := f.status
	status.Skipped = version
	return status, nil
}

func (f *fakeUpdateStore) UpdateSettings(_ context.Context, channel *string, check *bool) (UpdateStatus, error) {
	f.channels = append(f.channels, channel)
	f.checkFlags = append(f.checkFlags, check)
	if f.settingsErr != nil {
		return UpdateStatus{}, f.settingsErr
	}
	status := f.status
	if channel != nil {
		status.Channel = *channel
	}
	if check != nil {
		status.Check = *check
	}
	return status, nil
}

func (f *fakeUpdateStore) ClearUpdateResult(context.Context) error {
	f.clears++
	return nil
}

func newUpdateTestServer(t *testing.T, store UpdateStore) (*Server, *http.Cookie, string) {
	t.Helper()
	s, token := newAuthServerWithToken(t)
	s.SetUpdateStore(store)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

func sampleUpdateStatus() UpdateStatus {
	checked := int64(1762600000000)
	return UpdateStatus{
		Current:       "0.1.0-beta.1",
		Channel:       UpdateChannelStable,
		Check:         true,
		Supported:     true,
		CanApply:      true,
		ManualCommand: "curl -fsSL https://raw.githubusercontent.com/ridhomujizat/lana-harness/development/install.sh | bash",
		CheckedAt:     &checked,
		Available: &UpdateRelease{
			Version: "0.1.0", Tag: "v0.1.0", Name: "lana 0.1.0", PublishedAt: 1762590000000,
			Notes: "### Fixed", URL: "https://github.com/ridhomujizat/lana-harness/releases/tag/v0.1.0", Size: 31000000,
		},
		LastResult: &UpdateResult{Outcome: "updated", From: "0.0.9", To: "0.1.0-beta.1", At: 1762600100000},
	}
}

// TestUpdateStatusFields covers API-UPD-002 and API-UPD-003: the JSON field
// names and nulls of GET /v1/update.
func TestUpdateStatusFields(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, _ := newUpdateTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodGet, "/v1/update", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"current", "channel", "check", "supported", "can_apply", "manual_reason", "manual_command",
		"checked_at", "check_error", "available", "skipped", "job", "last_result"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing key %q in %s", key, rr.Body.String())
		}
	}
	if raw["job"] != nil {
		t.Fatalf("job = %v, want null", raw["job"])
	}
	available := raw["available"].(map[string]any)
	for _, key := range []string{"version", "tag", "name", "published_at", "notes", "url", "size", "prerelease", "upgrade_notes"} {
		if _, ok := available[key]; !ok {
			t.Fatalf("missing available.%s", key)
		}
	}
	result := raw["last_result"].(map[string]any)
	for _, key := range []string{"outcome", "from", "to", "reason", "at"} {
		if _, ok := result[key]; !ok {
			t.Fatalf("missing last_result.%s", key)
		}
	}

	// Without a cookie the route is closed like every other /v1 route.
	rr = serveAuth(s, daemonRequest(http.MethodGet, "/v1/update", "", nil, ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", rr.Code)
	}
}

// TestUpdateCheck covers API-UPD-004.
func TestUpdateCheck(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, csrf := newUpdateTestServer(t, store)

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/check", "", cookie, csrf))
	if rr.Code != http.StatusOK || store.checks != 1 {
		t.Fatalf("status = %d, checks = %d, body = %q", rr.Code, store.checks, rr.Body.String())
	}
	store.checkErr = ErrUpdateInvalidState
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/check", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")

	// The CSRF header is required.
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/check", "", cookie, ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no csrf status = %d", rr.Code)
	}
}

// TestUpdateApply covers API-UPD-005.
func TestUpdateApply(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, csrf := newUpdateTestServer(t, store)

	for _, body := range []string{`{}`, `{"version":"0.1.0"}`, `{"version":"0.1.0","confirm":false}`, `{"confirm":true}`, `[]`, `{"version":"0.1.0","confirm":true,"x":1}`} {
		rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/apply", body, cookie, csrf))
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	}
	if len(store.applies) != 0 {
		t.Fatalf("apply called without confirm: %v", store.applies)
	}

	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/apply", `{"version":"0.1.0","confirm":true,"force":true}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got UpdateStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Job == nil || got.Job.State != UpdateStateDownloading || got.Job.Version != "0.1.0" {
		t.Fatalf("job = %+v", got.Job)
	}
	if len(store.applies) != 1 || store.applies[0] != "0.1.0" || !store.forces[0] {
		t.Fatalf("applies = %v forces = %v", store.applies, store.forces)
	}

	store.applyErr = ErrUpdateInvalidState
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/apply", `{"version":"0.1.0","confirm":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "invalid_state")
	store.applyErr = ErrUpdateUnavailable
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/apply", `{"version":"0.1.0","confirm":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "update_unavailable")
	store.applyErr = errors.New("disk full")
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/apply", `{"version":"0.1.0","confirm":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

// TestUpdateCancel covers API-UPD-006.
func TestUpdateCancel(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, csrf := newUpdateTestServer(t, store)
	for _, want := range []bool{false, true} {
		store.cancel = want
		rr := serveAuth(s, daemonRequest(http.MethodDelete, "/v1/update/apply", "", cookie, csrf))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		var body struct {
			Cancelled bool `json:"cancelled"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Cancelled != want {
			t.Fatalf("body = %q, want cancelled=%v", rr.Body.String(), want)
		}
	}
}

// TestUpdateSkip covers API-UPD-007.
func TestUpdateSkip(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, csrf := newUpdateTestServer(t, store)
	rr := serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/skip", `{"version":"0.1.0"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/skip", `{"version":""}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rr.Code)
	}
	if len(store.skips) != 2 || store.skips[0] != "0.1.0" || store.skips[1] != "" {
		t.Fatalf("skips = %v", store.skips)
	}
	rr = serveAuth(s, daemonRequest(http.MethodPost, "/v1/update/skip", `{}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// TestUpdateSettings covers API-UPD-008.
func TestUpdateSettings(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, csrf := newUpdateTestServer(t, store)
	rr := serveAuth(s, daemonRequest(http.MethodPatch, "/v1/update/settings", `{"channel":"beta"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got UpdateStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.Channel != "beta" {
		t.Fatalf("channel = %q, %v", got.Channel, err)
	}
	rr = serveAuth(s, daemonRequest(http.MethodPatch, "/v1/update/settings", `{"check":false}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(store.channels) != 2 || store.channels[1] != nil || store.checkFlags[1] == nil || *store.checkFlags[1] {
		t.Fatalf("settings calls = %v %v", store.channels, store.checkFlags)
	}
	rr = serveAuth(s, daemonRequest(http.MethodPatch, "/v1/update/settings", `{"channel":"nightly"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
	if len(store.channels) != 2 {
		t.Fatal("unknown channel reached the store")
	}
}

// TestUpdateResultClear covers API-UPD-009.
func TestUpdateResultClear(t *testing.T) {
	store := &fakeUpdateStore{status: sampleUpdateStatus()}
	s, cookie, csrf := newUpdateTestServer(t, store)
	rr := serveAuth(s, daemonRequest(http.MethodDelete, "/v1/update/result", "", cookie, csrf))
	if rr.Code != http.StatusNoContent || store.clears != 1 {
		t.Fatalf("status = %d, clears = %d", rr.Code, store.clears)
	}
}

// TestUpdateStoreMissing: without the port the routes fail closed with 500.
func TestUpdateStoreMissing(t *testing.T) {
	s, token := newAuthServerWithToken(t)
	cookie, _ := loginAndSession(t, s, token)
	rr := serveAuth(s, daemonRequest(http.MethodGet, "/v1/update", "", cookie, ""))
	assertAuthError(t, rr, http.StatusInternalServerError, "internal_error")
}

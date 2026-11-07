package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

// *calendar.Service satisfies the CalendarBackend port the web handlers use
// (spec #35).
var _ CalendarBackend = (*calendar.Service)(nil)

// calendarTestNow is the fixed clock of the calendar handler tests.
var calendarTestNow = time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)

// fakeCalendarBackend is the real calendar service over a memory store, with
// error injection and call capture for the methods whose results the store
// cannot produce on its own.
type fakeCalendarBackend struct {
	*calendar.Service

	mu sync.Mutex

	entriesFrom, entriesTo time.Time
	entriesSources         []string

	completeItem  *calendar.Item
	completeHabit *calendar.EntryHabit
	completeErr   error

	override    *calendar.Override
	overrideErr error
	overrideIn  calendar.Override
	overrideBy  string

	cleared []string

	firing     calendar.Firing
	firingErr  error
	firingCall string
}

func newFakeCalendarBackend() *fakeCalendarBackend {
	svc := calendar.NewService(calendar.NewMemStore(),
		calendar.WithClock(func() time.Time { return calendarTestNow }),
		calendar.WithLocation(time.UTC))
	return &fakeCalendarBackend{Service: svc}
}

func (f *fakeCalendarBackend) Entries(ctx context.Context, from, to time.Time, sources []string) (calendar.EntriesResult, error) {
	f.mu.Lock()
	f.entriesFrom, f.entriesTo, f.entriesSources = from, to, append([]string(nil), sources...)
	f.mu.Unlock()
	return f.Service.Entries(ctx, from, to, sources)
}

func (f *fakeCalendarBackend) Complete(ctx context.Context, id string, occurrence int64, done bool, by string) (calendar.Item, *calendar.EntryHabit, error) {
	f.mu.Lock()
	item, habit, err := f.completeItem, f.completeHabit, f.completeErr
	f.mu.Unlock()
	if err != nil {
		return calendar.Item{}, nil, err
	}
	if item != nil {
		return *item, habit, nil
	}
	return f.Service.Complete(ctx, id, occurrence, done, by)
}

func (f *fakeCalendarBackend) SaveOverride(ctx context.Context, o calendar.Override, by string) (calendar.Override, error) {
	f.mu.Lock()
	f.overrideIn, f.overrideBy = o, by
	saved, err := f.override, f.overrideErr
	f.mu.Unlock()
	if err != nil {
		return calendar.Override{}, err
	}
	if saved != nil {
		return *saved, nil
	}
	return f.Service.SaveOverride(ctx, o, by)
}

func (f *fakeCalendarBackend) ClearOverride(_ context.Context, source, ref string, occurrence int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, source, ref, strconv.FormatInt(occurrence, 10))
	return nil
}

func (f *fakeCalendarBackend) RunFiring(_ context.Context, itemID string, occurrence int64, by string) (calendar.Firing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.firingCall = "run " + itemID + " " + strconv.FormatInt(occurrence, 10) + " " + by
	return f.firing, f.firingErr
}

func (f *fakeCalendarBackend) DismissFiring(_ context.Context, itemID string, occurrence int64, by string) (calendar.Firing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.firingCall = "dismiss " + itemID + " " + strconv.FormatInt(occurrence, 10) + " " + by
	return f.firing, f.firingErr
}

// fakeCalendarGoogle is an in-memory CalendarGoogle.
type fakeCalendarGoogle struct {
	mu sync.Mutex

	link    map[string]any
	putErr  error
	authErr error
	exchErr error

	selected      map[string]bool
	target        string
	clientID      string
	clientSecret  string
	authRedirect  string
	authState     string
	exchanges     []string
	syncs         int
	disconnects   int
	exchRedirects []string
}

func newFakeCalendarGoogle() *fakeCalendarGoogle {
	return &fakeCalendarGoogle{link: map[string]any{
		"client_configured": true, "state": "linked", "account": "owner@example.com",
		"calendars": []any{}, "target": "primary", "last_sync": 0, "error": "",
	}}
}

func (g *fakeCalendarGoogle) Link(_ context.Context) (any, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.link, nil
}

func (g *fakeCalendarGoogle) Put(_ context.Context, selected map[string]bool, target string) (any, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.selected, g.target = selected, target
	if g.putErr != nil {
		return nil, g.putErr
	}
	return g.link, nil
}

func (g *fakeCalendarGoogle) SetClient(_ context.Context, clientID, clientSecret string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clientID, g.clientSecret = clientID, clientSecret
	return nil
}

func (g *fakeCalendarGoogle) AuthURL(_ context.Context, redirectURI, state string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.authErr != nil {
		return "", g.authErr
	}
	g.authRedirect, g.authState = redirectURI, state
	return "https://accounts.google.com/o/oauth2/v2/auth?state=" + url.QueryEscape(state), nil
}

func (g *fakeCalendarGoogle) Exchange(_ context.Context, code, redirectURI string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.exchanges = append(g.exchanges, code)
	g.exchRedirects = append(g.exchRedirects, redirectURI)
	return g.exchErr
}

func (g *fakeCalendarGoogle) Sync(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.syncs++
	return nil
}

func (g *fakeCalendarGoogle) Disconnect(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disconnects++
	return nil
}

// codeError is an error with a contract code, as the Google port returns.
type codeError struct{ code string }

func (e codeError) Error() string { return "calendar: " + e.code }
func (e codeError) Code() string  { return e.code }

// newCalendarTestServer logs in an owner and installs the calendar port.
func newCalendarTestServer(t *testing.T, backend CalendarBackend) (*Server, *http.Cookie, string) {
	t.Helper()
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	if backend != nil {
		s.SetCalendar(backend)
	}
	return s, cookie, csrf
}

func decodeCalendarBody(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
}

func expectStatus(t *testing.T, rr *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body = %q", rr.Code, status, rr.Body.String())
	}
}

const calendarEventBody = `{"kind":"event","title":"Dentist","start":1793084400000,"end":1793088000000}`

// createCalendarItem creates an item and returns its id.
func createCalendarItem(t *testing.T, s *Server, cookie *http.Cookie, csrf, body string) string {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items", body, cookie, csrf))
	expectStatus(t, rr, http.StatusCreated)
	var resp struct {
		Item calendar.Item `json:"item"`
	}
	decodeCalendarBody(t, rr, &resp)
	return resp.Item.ID
}

// Every /v1/calendar route answers 404 not_found without a calendar port, so
// the web UI falls back; the ICS feed answers 404 with an empty body.
func TestCalendarRoutesWithoutBackend(t *testing.T) {
	s, cookie, csrf := newCalendarTestServer(t, nil)
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/v1/calendar/entries?from=1&to=2", ""},
		{http.MethodPost, "/v1/calendar/items", calendarEventBody},
		{http.MethodGet, "/v1/calendar/items/ci_x", ""},
		{http.MethodPatch, "/v1/calendar/items/ci_x", `{}`},
		{http.MethodDelete, "/v1/calendar/items/ci_x", ""},
		{http.MethodPost, "/v1/calendar/items/ci_x/complete", `{"occurrence":1}`},
		{http.MethodPost, "/v1/calendar/overrides", `{}`},
		{http.MethodDelete, "/v1/calendar/overrides?source=item&ref=x&occurrence=1", ""},
		{http.MethodPost, "/v1/calendar/firings/run", `{}`},
		{http.MethodPost, "/v1/calendar/firings/dismiss", `{}`},
		{http.MethodGet, "/v1/calendar/settings", ""},
		{http.MethodPut, "/v1/calendar/settings", `{}`},
		{http.MethodPost, "/v1/calendar/ics/rotate", ""},
		{http.MethodGet, "/v1/calendar/stream", ""},
		{http.MethodGet, "/v1/calendar/google", ""},
		{http.MethodPost, "/v1/calendar/google/connect", ""},
	} {
		rr := serveAuth(s, sessionTestRequest(tc.method, tc.target, tc.body, cookie, csrf))
		assertAuthError(t, rr, http.StatusNotFound, "not_found")
	}

	rr := serveAuth(s, httptest.NewRequest(http.MethodGet, "/cal/anything.ics", nil))
	expectStatus(t, rr, http.StatusNotFound)
	if rr.Body.Len() != 0 {
		t.Fatalf("feed body = %q", rr.Body.String())
	}
}

// The /v1/calendar routes need the session cookie and CSRF token.
func TestCalendarRoutesNeedSession(t *testing.T) {
	s, cookie, _ := newCalendarTestServer(t, newFakeCalendarBackend())
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/entries?from=1&to=2", "", nil, ""))
	assertAuthError(t, rr, http.StatusUnauthorized, "unauthenticated")
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items", calendarEventBody, cookie, ""))
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
}

// API-CAL-001, AC-CAL-004: the entries read checks the range and passes the
// sources filter.
func TestCalendarEntries(t *testing.T) {
	backend := newFakeCalendarBackend()
	s, cookie, csrf := newCalendarTestServer(t, backend)
	createCalendarItem(t, s, cookie, csrf, calendarEventBody)

	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	target := "/v1/calendar/entries?from=" + strconv.FormatInt(from, 10) + "&to=" + strconv.FormatInt(to, 10) + "&sources=item,google"
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, target, "", cookie, ""))
	expectStatus(t, rr, http.StatusOK)
	var resp struct {
		Entries   []map[string]any `json:"entries"`
		Timezone  string           `json:"timezone"`
		WeekStart int              `json:"week_start"`
		Now       int64            `json:"now"`
		Google    struct {
			State string `json:"state"`
		} `json:"google"`
		Missed *int `json:"missed"`
	}
	decodeCalendarBody(t, rr, &resp)
	if len(resp.Entries) != 1 || resp.Entries[0]["title"] != "Dentist" || resp.Entries[0]["source"] != "item" {
		t.Fatalf("entries = %+v", resp.Entries)
	}
	if resp.Timezone != "UTC" || resp.WeekStart != 1 || resp.Now != calendarTestNow.UnixMilli() || resp.Missed == nil {
		t.Fatalf("resp = %+v", resp)
	}
	backend.mu.Lock()
	gotFrom, gotTo, sources := backend.entriesFrom, backend.entriesTo, backend.entriesSources
	backend.mu.Unlock()
	if gotFrom.UnixMilli() != from || gotTo.UnixMilli() != to || len(sources) != 1 || sources[0] != "item,google" {
		t.Fatalf("entries call = %v %v %v", gotFrom, gotTo, sources)
	}

	// AC-CAL-004: more than 92 days is 400 range_too_large.
	far := from + (93 * 24 * time.Hour).Milliseconds()
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/entries?from="+strconv.FormatInt(from, 10)+"&to="+strconv.FormatInt(far, 10), "", cookie, ""))
	assertAuthError(t, rr, http.StatusBadRequest, "range_too_large")

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/entries?from="+strconv.FormatInt(to, 10)+"&to="+strconv.FormatInt(from, 10), "", cookie, ""))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_range")

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/entries?from=abc&to=2", "", cookie, ""))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_range")
}

// API-CAL-002 through API-CAL-005: create, read, update and delete an item.
func TestCalendarItemLifecycle(t *testing.T) {
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items", calendarEventBody, cookie, csrf))
	expectStatus(t, rr, http.StatusCreated)
	var created struct {
		Item map[string]any `json:"item"`
	}
	decodeCalendarBody(t, rr, &created)
	id, _ := created.Item["id"].(string)
	if !strings.HasPrefix(id, "ci_") || created.Item["created_by"] != "owner" || created.Item["kind"] != "event" {
		t.Fatalf("item = %+v", created.Item)
	}
	for _, key := range []string{"repeat", "action", "google", "all_day", "updated_at"} {
		if _, ok := created.Item[key]; !ok {
			t.Fatalf("item has no %q: %+v", key, created.Item)
		}
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/items/"+id, "", cookie, ""))
	expectStatus(t, rr, http.StatusOK)
	var detail struct {
		Item    map[string]any   `json:"item"`
		Firings []any            `json:"firings"`
		History []map[string]any `json:"history"`
	}
	decodeCalendarBody(t, rr, &detail)
	if detail.Item["id"] != id || detail.Firings == nil || len(detail.History) != 1 || detail.History[0]["by"] != "owner" {
		t.Fatalf("detail = %+v", detail)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/calendar/items/"+id, `{"title":"Dentist (moved)","kind":"task","unknown":1}`, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	var updated struct {
		Item calendar.Item `json:"item"`
	}
	decodeCalendarBody(t, rr, &updated)
	if updated.Item.Title != "Dentist (moved)" || updated.Item.Kind != calendar.KindEvent || updated.Item.UpdatedBy != "owner" {
		t.Fatalf("updated = %+v", updated.Item)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/calendar/items/"+id, `[1]`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	// An event cannot be completed.
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items/"+id+"/complete", `{"occurrence":1793084400000,"done":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "not_completable")

	// A non-repeating item is moved by PATCH, not by an override.
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/overrides", `{"source":"item","ref":"`+id+`","occurrence":1793084400000,"op":"skip"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "not_repeating")

	rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/calendar/items/"+id, "", cookie, csrf))
	expectStatus(t, rr, http.StatusNoContent)

	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/items/"+id, "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
	rr = serveAuth(s, sessionTestRequest(http.MethodPatch, "/v1/calendar/items/ci_missing", `{"title":"x"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
	rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/calendar/items/ci_missing", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// API-CAL-002, AC-CAL-002: an invalid item is 400 invalid_item with field
// details; a malformed body is 400 invalid_request.
func TestCalendarCreateInvalidItem(t *testing.T) {
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items", `{"kind":"event","title":"  ","start":2000,"end":1000}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_item")
	var env errorEnvelope
	decodeCalendarBody(t, rr, &env)
	if len(env.Error.Details) == 0 {
		t.Fatalf("details = %+v", env.Error.Details)
	}
	fields := map[string]bool{}
	for _, detail := range env.Error.Details {
		field, _ := detail["field"].(string)
		message, _ := detail["message"].(string)
		if field == "" || message == "" {
			t.Fatalf("detail = %+v", detail)
		}
		fields[field] = true
	}
	if !fields["title"] {
		t.Fatalf("no title detail in %+v", env.Error.Details)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items", `{"kind":"meeting","title":"x"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_item")

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items", `{"kind":"event","bogus":1}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

// API-CAL-007: complete answers the item and, for a habit, the habit state;
// done defaults to true.
func TestCalendarComplete(t *testing.T) {
	backend := newFakeCalendarBackend()
	s, cookie, csrf := newCalendarTestServer(t, backend)

	taskID := createCalendarItem(t, s, cookie, csrf, `{"kind":"task","title":"Pay electricity","date":"2026-11-05"}`)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items/"+taskID+"/complete", `{"occurrence":0}`, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	var task map[string]map[string]any
	decodeCalendarBody(t, rr, &task)
	if task["item"]["state"] != "done" {
		t.Fatalf("task = %+v", task)
	}
	if _, ok := task["habit"]; ok {
		t.Fatalf("task response has habit: %s", rr.Body.String())
	}

	backend.mu.Lock()
	backend.completeItem = &calendar.Item{ID: "ci_habit", Kind: calendar.KindHabit, Title: "Read"}
	backend.completeHabit = &calendar.EntryHabit{Done: true, Streak: 5}
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items/ci_habit/complete", `{"occurrence":1793084400000,"done":true}`, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	var habit struct {
		Item  calendar.Item        `json:"item"`
		Habit *calendar.EntryHabit `json:"habit"`
	}
	decodeCalendarBody(t, rr, &habit)
	if habit.Item.ID != "ci_habit" || habit.Habit == nil || !habit.Habit.Done || habit.Habit.Streak != 5 {
		t.Fatalf("habit = %+v", habit)
	}

	backend.mu.Lock()
	backend.completeItem, backend.completeErr = nil, calendar.ErrNoSuchOccurrence
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/items/ci_habit/complete", `{"occurrence":1}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "no_such_occurrence")
}

// API-CAL-006: save answers {"override"} with by "owner"; clear passes the
// query and answers 204; a bad query is 400 invalid_override.
func TestCalendarOverrides(t *testing.T) {
	backend := newFakeCalendarBackend()
	backend.override = &calendar.Override{Source: "workflow", Ref: "wf_x/every-day", Occurrence: 1793055600000, Op: "move", To: 1793064600000, By: "owner", At: 5}
	s, cookie, csrf := newCalendarTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/overrides", `{"source":"workflow","ref":"wf_x/every-day","occurrence":1793055600000,"op":"move","to":1793064600000}`, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	var resp struct {
		Override map[string]any `json:"override"`
	}
	decodeCalendarBody(t, rr, &resp)
	for _, key := range []string{"source", "ref", "occurrence", "op", "to", "by", "at"} {
		if _, ok := resp.Override[key]; !ok {
			t.Fatalf("override has no %q: %+v", key, resp.Override)
		}
	}
	backend.mu.Lock()
	in, by := backend.overrideIn, backend.overrideBy
	backend.mu.Unlock()
	if in.Ref != "wf_x/every-day" || in.Op != "move" || in.To != 1793064600000 || by != "owner" {
		t.Fatalf("override call = %+v %q", in, by)
	}

	backend.mu.Lock()
	backend.override, backend.overrideErr = nil, calendar.ErrInvalidOverride
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/overrides", `{"source":"x","ref":"y","occurrence":1,"op":"skip"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_override")

	rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/calendar/overrides?source=workflow&ref=wf_x%2Fevery-day&occurrence=1793055600000", "", cookie, csrf))
	expectStatus(t, rr, http.StatusNoContent)
	backend.mu.Lock()
	cleared := strings.Join(backend.cleared, " ")
	backend.mu.Unlock()
	if cleared != "workflow wf_x/every-day 1793055600000" {
		t.Fatalf("cleared = %q", cleared)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodDelete, "/v1/calendar/overrides?source=item&ref=x", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_override")
}

// API-CAL-008: run is 202, dismiss is 200, both {"firing"}; running and done
// map to 409; an unknown error is 500.
func TestCalendarFirings(t *testing.T) {
	backend := newFakeCalendarBackend()
	backend.firing = calendar.Firing{ItemID: "ci_x", Occurrence: 1793084400000, Status: calendar.FiringRunning, StartedAt: 7}
	s, cookie, csrf := newCalendarTestServer(t, backend)
	body := `{"item_id":"ci_x","occurrence":1793084400000}`

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/firings/run", body, cookie, csrf))
	expectStatus(t, rr, http.StatusAccepted)
	var resp struct {
		Firing map[string]any `json:"firing"`
	}
	decodeCalendarBody(t, rr, &resp)
	if resp.Firing["status"] != "running" || resp.Firing["at"] != float64(1793084400000) {
		t.Fatalf("firing = %+v", resp.Firing)
	}
	if _, ok := resp.Firing["run"]; !ok {
		t.Fatalf("firing has no run: %+v", resp.Firing)
	}
	backend.mu.Lock()
	call := backend.firingCall
	backend.mu.Unlock()
	if call != "run ci_x 1793084400000 owner" {
		t.Fatalf("call = %q", call)
	}

	backend.mu.Lock()
	backend.firing.Status = calendar.FiringDismissed
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/firings/dismiss", body, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	decodeCalendarBody(t, rr, &resp)
	if resp.Firing["status"] != "dismissed" {
		t.Fatalf("firing = %+v", resp.Firing)
	}

	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{calendar.ErrAlreadyRunning, http.StatusConflict, "already_running"},
		{calendar.ErrAlreadyDone, http.StatusConflict, "already_done"},
		{calendar.ErrNotFound, http.StatusNotFound, "not_found"},
		{errors.New("disk on fire"), http.StatusInternalServerError, "internal_error"},
	} {
		backend.mu.Lock()
		backend.firingErr = tc.err
		backend.mu.Unlock()
		rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/firings/run", body, cookie, csrf))
		assertAuthError(t, rr, tc.status, tc.code)
	}
}

// settingsICSToken reads the feed token out of a settings response URL.
func settingsICSToken(t *testing.T, feedURL string) string {
	t.Helper()
	prefix := "http://" + authTestAddr + "/cal/"
	if !strings.HasPrefix(feedURL, prefix) || !strings.HasSuffix(feedURL, ".ics") {
		t.Fatalf("ics url = %q", feedURL)
	}
	return strings.TrimSuffix(strings.TrimPrefix(feedURL, prefix), ".ics")
}

// API-CAL-009 and API-CAL-010: the settings carry the feed URL; a bad zone is
// 400 invalid_settings; rotate answers a new URL.
func TestCalendarSettings(t *testing.T) {
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/settings", "", cookie, ""))
	expectStatus(t, rr, http.StatusOK)
	var settings struct {
		Timezone  string `json:"timezone"`
		WeekStart int    `json:"week_start"`
		Agenda    struct {
			Enabled bool   `json:"enabled"`
			Time    string `json:"time"`
		} `json:"agenda"`
		ICS struct {
			URL string `json:"url"`
		} `json:"ics"`
	}
	decodeCalendarBody(t, rr, &settings)
	if settings.Timezone != "UTC" || settings.WeekStart != 1 || settings.Agenda.Time != "07:00" {
		t.Fatalf("settings = %+v", settings)
	}
	token := settingsICSToken(t, settings.ICS.URL)

	rr = serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/calendar/settings", `{"timezone":"Mars/Olympus","week_start":1,"agenda":{"enabled":true,"time":"07:00"}}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_settings")

	rr = serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/calendar/settings", `{"timezone":"Asia/Jakarta","week_start":0,"agenda":{"enabled":true,"time":"06:30"},"ics":{"url":"ignored"}}`, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	decodeCalendarBody(t, rr, &settings)
	if settings.Timezone != "Asia/Jakarta" || settings.WeekStart != 0 || !settings.Agenda.Enabled || settings.Agenda.Time != "06:30" {
		t.Fatalf("saved settings = %+v", settings)
	}
	if settingsICSToken(t, settings.ICS.URL) != token {
		t.Fatalf("token changed on save: %q", settings.ICS.URL)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/ics/rotate", "", cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	var rotated struct {
		URL string `json:"url"`
	}
	decodeCalendarBody(t, rr, &rotated)
	if next := settingsICSToken(t, rotated.URL); next == token || next == "" {
		t.Fatalf("rotated url = %q", rotated.URL)
	}
}

// API-CAL-011, AC-CAL-023: the feed needs only the token: no cookie, CSRF or
// matching Host. A wrong token, a missing suffix and a rotated token answer
// 404 with an empty body.
func TestCalendarFeed(t *testing.T) {
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())
	createCalendarItem(t, s, cookie, csrf, calendarEventBody)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/settings", "", cookie, ""))
	expectStatus(t, rr, http.StatusOK)
	var settings struct {
		ICS struct {
			URL string `json:"url"`
		} `json:"ics"`
	}
	decodeCalendarBody(t, rr, &settings)
	token := settingsICSToken(t, settings.ICS.URL)

	feed := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "calendar.example.test"
		return serveAuth(s, req)
	}

	rr = feed("/cal/" + token + ".ics")
	expectStatus(t, rr, http.StatusOK)
	if got := rr.Header().Get("Content-Type"); got != "text/calendar; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "BEGIN:VCALENDAR") || !strings.Contains(body, "Dentist") {
		t.Fatalf("feed = %q", body)
	}

	for _, path := range []string{"/cal/wrong.ics", "/cal/" + token, "/cal/.ics"} {
		rr = feed(path)
		expectStatus(t, rr, http.StatusNotFound)
		if rr.Body.Len() != 0 {
			t.Fatalf("%s body = %q", path, rr.Body.String())
		}
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/ics/rotate", "", cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	rr = feed("/cal/" + token + ".ics")
	expectStatus(t, rr, http.StatusNotFound)
}

// API-CAL-015: the stream emits a changed event per change.
func TestCalendarStreamEmitsChanged(t *testing.T) {
	s, ts, cookie, csrf := newStreamTestServer(t, newFakeWebBackend())
	s.SetCalendar(newFakeCalendarBackend())
	s.SetHeartbeatInterval(20 * time.Millisecond)

	resp := openStream(t, ts, "/v1/calendar/stream", cookie, "")
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	waitSSE(t, reader, "ping")

	id := createCalendarItem(t, s, cookie, csrf, calendarEventBody)

	block := waitSSE(t, reader, "changed")
	var change calendar.Change
	if err := json.Unmarshal([]byte(block.Data), &change); err != nil {
		t.Fatalf("data %q: %v", block.Data, err)
	}
	if change.What != "item" || change.ItemID != id {
		t.Fatalf("change = %+v", change)
	}
}

// API-CAL-012 without a Google port: GET answers the "off" link state and
// the other Google routes answer 404 not_found.
func TestCalendarGoogleWithoutPort(t *testing.T) {
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/google", "", cookie, ""))
	expectStatus(t, rr, http.StatusOK)
	want := `{"client_configured":false,"state":"off","account":"","calendars":[],"target":"","last_sync":0,"error":""}`
	if got := strings.TrimSpace(rr.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}

	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPut, "/v1/calendar/google", `{"calendars":[],"target":""}`},
		{http.MethodPut, "/v1/calendar/google/client", `{"client_id":"a","client_secret":"b"}`},
		{http.MethodPost, "/v1/calendar/google/connect", ""},
		{http.MethodPost, "/v1/calendar/google/sync", ""},
		{http.MethodPost, "/v1/calendar/google/disconnect", ""},
		{http.MethodGet, "/v1/calendar/google/callback?code=c&state=s", ""},
	} {
		rr := serveAuth(s, sessionTestRequest(tc.method, tc.target, tc.body, cookie, csrf))
		assertAuthError(t, rr, http.StatusNotFound, "not_found")
	}
}

// API-CAL-012 through API-CAL-014 with a Google port.
func TestCalendarGoogleRoutes(t *testing.T) {
	google := newFakeCalendarGoogle()
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())
	s.SetCalendarGoogle(google)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/calendar/google", "", cookie, ""))
	expectStatus(t, rr, http.StatusOK)
	var link map[string]any
	decodeCalendarBody(t, rr, &link)
	if link["state"] != "linked" || link["account"] != "owner@example.com" {
		t.Fatalf("link = %+v", link)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/calendar/google", `{"calendars":[{"id":"primary","selected":true},{"id":"work","selected":false}],"target":"primary"}`, cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	google.mu.Lock()
	selected, target := google.selected, google.target
	google.mu.Unlock()
	if len(selected) != 2 || !selected["primary"] || selected["work"] || target != "primary" {
		t.Fatalf("put = %v %q", selected, target)
	}

	google.mu.Lock()
	google.putErr = codeError{"invalid_target"}
	google.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/calendar/google", `{"calendars":[],"target":"work"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_target")

	rr = serveAuth(s, sessionTestRequest(http.MethodPut, "/v1/calendar/google/client", `{"client_id":"id-1","client_secret":"secret-1"}`, cookie, csrf))
	expectStatus(t, rr, http.StatusNoContent)
	google.mu.Lock()
	clientID, clientSecret := google.clientID, google.clientSecret
	google.mu.Unlock()
	if clientID != "id-1" || clientSecret != "secret-1" {
		t.Fatalf("client = %q %q", clientID, clientSecret)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/google/sync", "", cookie, csrf))
	expectStatus(t, rr, http.StatusAccepted)
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/google/disconnect", "", cookie, csrf))
	expectStatus(t, rr, http.StatusNoContent)
	google.mu.Lock()
	syncs, disconnects := google.syncs, google.disconnects
	google.authErr = codeError{"client_missing"}
	google.mu.Unlock()
	if syncs != 1 || disconnects != 1 {
		t.Fatalf("syncs = %d, disconnects = %d", syncs, disconnects)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/google/connect", "", cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "client_missing")
}

// googleConnect starts a connect and returns the issued state.
func googleConnect(t *testing.T, s *Server, google *fakeCalendarGoogle, cookie *http.Cookie, csrf string) string {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/calendar/google/connect", "", cookie, csrf))
	expectStatus(t, rr, http.StatusOK)
	var resp struct {
		AuthURL string `json:"auth_url"`
	}
	decodeCalendarBody(t, rr, &resp)
	google.mu.Lock()
	state, redirect := google.authState, google.authRedirect
	google.mu.Unlock()
	if state == "" || !strings.Contains(resp.AuthURL, url.QueryEscape(state)) {
		t.Fatalf("auth url = %q, state = %q", resp.AuthURL, state)
	}
	if redirect != "http://"+authTestAddr+"/v1/calendar/google/callback" {
		t.Fatalf("redirect = %q", redirect)
	}
	return state
}

// googleCallback follows the cookie-less Google redirect and returns the
// finish URL of the bounce page.
func googleCallback(t *testing.T, s *Server, query string) string {
	t.Helper()
	rr := serveAuth(s, authRequest(http.MethodGet, "/v1/calendar/google/callback?"+query, ""))
	expectStatus(t, rr, http.StatusOK)
	body := rr.Body.String()
	const marker = `url=`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("bounce page = %q", body)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	finish := html.UnescapeString(rest[:end])
	if !strings.HasPrefix(finish, "/v1/calendar/google/callback/finish?") {
		t.Fatalf("finish = %q", finish)
	}
	return finish
}

func expectRedirect(t *testing.T, rr *httptest.ResponseRecorder, location string) {
	t.Helper()
	expectStatus(t, rr, http.StatusFound)
	if got := rr.Header().Get("Location"); got != location {
		t.Fatalf("Location = %q, want %q", got, location)
	}
}

// API-CAL-013: the OAuth state is one-use and bound to the browser session.
// The Google redirect carries no cookie (SameSite=Strict), so the callback
// bounces to the same-site finish route, which checks the session.
func TestCalendarGoogleOAuth(t *testing.T) {
	google := newFakeCalendarGoogle()
	s, cookie, csrf := newCalendarTestServer(t, newFakeCalendarBackend())
	s.SetCalendarGoogle(google)

	state := googleConnect(t, s, google, cookie, csrf)
	finish := googleCallback(t, s, "code=auth-code&state="+url.QueryEscape(state))

	// The finish route without the session cookie fails and burns the state.
	rr := serveAuth(s, authRequest(http.MethodGet, finish, ""))
	expectRedirect(t, rr, "/#/calendar?google=error")
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, finish, "", cookie, ""))
	expectRedirect(t, rr, "/#/calendar?google=error")

	state = googleConnect(t, s, google, cookie, csrf)
	finish = googleCallback(t, s, "code=auth-code&state="+url.QueryEscape(state))
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, finish, "", cookie, ""))
	expectRedirect(t, rr, "/#/calendar?google=linked")
	google.mu.Lock()
	exchanges, redirects := append([]string(nil), google.exchanges...), append([]string(nil), google.exchRedirects...)
	google.mu.Unlock()
	if len(exchanges) != 1 || exchanges[0] != "auth-code" || redirects[0] != "http://"+authTestAddr+"/v1/calendar/google/callback" {
		t.Fatalf("exchanges = %v %v", exchanges, redirects)
	}

	// A replayed state is rejected.
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, finish, "", cookie, ""))
	expectRedirect(t, rr, "/#/calendar?google=error")
	rr = serveAuth(s, authRequest(http.MethodGet, "/v1/calendar/google/callback?code=x&state="+url.QueryEscape(state), ""))
	expectRedirect(t, rr, "/#/calendar?google=error")

	// An unknown state and a Google error are rejected.
	rr = serveAuth(s, authRequest(http.MethodGet, "/v1/calendar/google/callback?code=x&state=forged", ""))
	expectRedirect(t, rr, "/#/calendar?google=error")
	state = googleConnect(t, s, google, cookie, csrf)
	finish = googleCallback(t, s, "error=access_denied&state="+url.QueryEscape(state))
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, finish, "", cookie, ""))
	expectRedirect(t, rr, "/#/calendar?google=error")

	// An expired state is rejected.
	state = googleConnect(t, s, google, cookie, csrf)
	s.calendar.statesMu.Lock()
	pending := s.calendar.states[state]
	pending.expiresAt = s.nowTime().Add(-time.Second)
	s.calendar.states[state] = pending
	s.calendar.statesMu.Unlock()
	rr = serveAuth(s, authRequest(http.MethodGet, "/v1/calendar/google/callback?code=x&state="+url.QueryEscape(state), ""))
	expectRedirect(t, rr, "/#/calendar?google=error")

	// A failed exchange redirects to the error page.
	google.mu.Lock()
	google.exchErr = errors.New("token endpoint down")
	google.mu.Unlock()
	state = googleConnect(t, s, google, cookie, csrf)
	finish = googleCallback(t, s, "code=c2&state="+url.QueryEscape(state))
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, finish, "", cookie, ""))
	expectRedirect(t, rr, "/#/calendar?google=error")
}

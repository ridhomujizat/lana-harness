package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fake Google ---------------------------------------------------------------

const (
	fakePrimary  = "owner@example.com"
	fakeHolidays = "holidays@group.v.calendar.google.com"
	fakeRedirect = "http://127.0.0.1:7777/v1/calendar/google/callback"
)

type fakeEvent struct {
	data map[string]any
	seq  int
}

type fakeRequest struct {
	method, path string
	query        url.Values
	ifMatch      string
	body         map[string]any
}

// fakeGoogle is an httptest Google: token, revoke, calendarList and events.
type fakeGoogle struct {
	mu        sync.Mutex
	clock     *fakeClock
	seq       int
	nextID    int
	events    map[string]map[string]*fakeEvent
	challenge string
	// refreshInvalid makes the refresh grant fail with invalid_grant.
	refreshInvalid bool
	// goneOnce answers the next incremental list with 410.
	goneOnce bool
	// failInsert answers event inserts with 503.
	failInsert bool
	revoked    []string
	requests   []fakeRequest
	tokens     int
}

func newFakeGoogle(clock *fakeClock) *fakeGoogle {
	return &fakeGoogle{clock: clock, events: map[string]map[string]*fakeEvent{fakePrimary: {}, fakeHolidays: {}}}
}

func (f *fakeGoogle) stamp(ev *fakeEvent) {
	f.seq++
	ev.seq = f.seq
	ev.data["etag"] = fmt.Sprintf("\"e%d\"", f.seq)
	ev.data["updated"] = f.clock.Now().UTC().Format(time.RFC3339Nano)
	if _, ok := ev.data["status"]; !ok {
		ev.data["status"] = "confirmed"
	}
}

// put adds or replaces a Google-side event.
func (f *fakeGoogle) put(cal string, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := &fakeEvent{data: data}
	f.stamp(ev)
	f.events[cal][data["id"].(string)] = ev
}

// edit changes a Google-side event.
func (f *fakeGoogle) edit(cal, id string, change func(map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := f.events[cal][id]
	change(ev.data)
	f.stamp(ev)
}

func (f *fakeGoogle) event(cal, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev, ok := f.events[cal][id]
	if !ok {
		return nil
	}
	raw, _ := json.Marshal(ev.data)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (f *fakeGoogle) calls(method, pathPart string) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeRequest
	for _, r := range f.requests {
		if r.method == method && strings.Contains(r.path, pathPart) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeGoogle) set(fn func()) {
	f.mu.Lock()
	fn()
	f.mu.Unlock()
}

func writeJSONResp(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req := fakeRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), ifMatch: r.Header.Get("If-Match")}
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &req.body)
		}
	}
	switch r.URL.Path {
	case "/token":
		_ = r.ParseForm()
		f.requests = append(f.requests, req)
		f.token(w, r.PostForm)
		return
	case "/revoke":
		_ = r.ParseForm()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		w.WriteHeader(http.StatusOK)
		return
	}
	f.requests = append(f.requests, req)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at") {
		writeJSONResp(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api")
	if path == "/users/me/calendarList" {
		writeJSONResp(w, http.StatusOK, map[string]any{"items": []map[string]any{
			{"id": fakePrimary, "summary": fakePrimary, "summaryOverride": "Owner", "backgroundColor": "#4285f4", "accessRole": "owner", "primary": true},
			{"id": fakeHolidays, "summary": "Holidays", "backgroundColor": "#0b8043", "accessRole": "reader"},
		}})
		return
	}
	rest, ok := strings.CutPrefix(path, "/calendars/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	cal, rest, _ := strings.Cut(rest, "/events")
	events := f.events[cal]
	if events == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(rest, "/")
	switch {
	case id == "" && r.Method == http.MethodGet:
		f.list(w, req.query, events)
	case id == "" && r.Method == http.MethodPost:
		if f.failInsert {
			writeJSONResp(w, http.StatusServiceUnavailable, map[string]any{"error": "backend"})
			return
		}
		f.nextID++
		req.body["id"] = fmt.Sprintf("lev%d", f.nextID)
		ev := &fakeEvent{data: req.body}
		f.stamp(ev)
		events[ev.data["id"].(string)] = ev
		writeJSONResp(w, http.StatusOK, ev.data)
	case r.Method == http.MethodGet:
		ev, ok := events[id]
		if !ok {
			writeJSONResp(w, http.StatusNotFound, map[string]any{})
			return
		}
		writeJSONResp(w, http.StatusOK, ev.data)
	case r.Method == http.MethodPatch:
		ev, ok := events[id]
		if !ok {
			writeJSONResp(w, http.StatusNotFound, map[string]any{})
			return
		}
		if req.ifMatch != "" && req.ifMatch != ev.data["etag"] {
			writeJSONResp(w, http.StatusPreconditionFailed, map[string]any{"error": "conditionNotMet"})
			return
		}
		for k, v := range req.body {
			ev.data[k] = v
		}
		f.stamp(ev)
		writeJSONResp(w, http.StatusOK, ev.data)
	case r.Method == http.MethodDelete:
		ev, ok := events[id]
		if !ok || ev.data["status"] == "cancelled" {
			writeJSONResp(w, http.StatusGone, map[string]any{})
			return
		}
		ev.data["status"] = "cancelled"
		f.stamp(ev)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGoogle) token(w http.ResponseWriter, form url.Values) {
	switch form.Get("grant_type") {
	case "authorization_code":
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if form.Get("code") != "code1" || form.Get("redirect_uri") != fakeRedirect ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge || form.Get("client_id") != "cid" {
			writeJSONResp(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		f.tokens++
		writeJSONResp(w, http.StatusOK, map[string]any{"access_token": fmt.Sprintf("at%d", f.tokens), "refresh_token": "rt1", "expires_in": 3600})
	case "refresh_token":
		if f.refreshInvalid || form.Get("refresh_token") != "rt1" {
			writeJSONResp(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
			return
		}
		f.tokens++
		writeJSONResp(w, http.StatusOK, map[string]any{"access_token": fmt.Sprintf("at%d", f.tokens), "expires_in": 3600})
	default:
		writeJSONResp(w, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"})
	}
}

func (f *fakeGoogle) list(w http.ResponseWriter, q url.Values, events map[string]*fakeEvent) {
	since, full := 0, true
	if tok := q.Get("syncToken"); tok != "" {
		full = false
		if f.goneOnce {
			f.goneOnce = false
			writeJSONResp(w, http.StatusGone, map[string]any{"error": "fullSyncRequired"})
			return
		}
		since, _ = strconv.Atoi(strings.TrimPrefix(tok, "s"))
	}
	items := []map[string]any{}
	for _, ev := range events {
		if full && ev.data["status"] == "cancelled" {
			continue
		}
		if ev.seq > since {
			items = append(items, ev.data)
		}
	}
	writeJSONResp(w, http.StatusOK, map[string]any{"items": items, "nextSyncToken": fmt.Sprintf("s%d", f.seq)})
}

// ---- harness -------------------------------------------------------------------

type memSecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSecrets) Get(_ context.Context, name string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	return v, ok, nil
}

func (s *memSecrets) Set(_ context.Context, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = value
	return nil
}

func (s *memSecrets) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, name)
	return nil
}

type googleHarness struct {
	t       *testing.T
	ctx     context.Context
	fake    *fakeGoogle
	g       *Google
	svc     *Service
	st      *MemStore
	clock   *fakeClock
	secrets *memSecrets
}

func newGoogleHarness(t *testing.T, opts ...GoogleOption) *googleHarness {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	fake := newFakeGoogle(clock)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	st := NewMemStore()
	svc := NewService(st, WithClock(clock.Now), WithLocation(time.UTC))
	secrets := &memSecrets{m: map[string]string{}}
	all := append([]GoogleOption{
		WithGoogleHTTPClient(srv.Client()),
		WithGoogleClock(clock.Now),
		WithGoogleEndpoints(GoogleEndpoints{
			Auth:   "https://accounts.example/o/oauth2/v2/auth",
			Token:  srv.URL + "/token",
			Revoke: srv.URL + "/revoke",
			API:    srv.URL + "/api",
		}),
	}, opts...)
	g := NewGoogle(svc, st, secrets, all...)
	return &googleHarness{t: t, ctx: context.Background(), fake: fake, g: g, svc: svc, st: st, clock: clock, secrets: secrets}
}

// link runs the OAuth flow against the fake.
func (h *googleHarness) link() {
	h.t.Helper()
	if err := h.g.SetClient(h.ctx, "cid", "csecret"); err != nil {
		h.t.Fatal(err)
	}
	raw, err := h.g.AuthURL(h.ctx, fakeRedirect, "state1")
	if err != nil {
		h.t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	h.fake.set(func() { h.fake.challenge = u.Query().Get("code_challenge") })
	if err := h.g.Exchange(h.ctx, "code1", fakeRedirect); err != nil {
		h.t.Fatal(err)
	}
}

func (h *googleHarness) advance(d time.Duration) { h.clock.Set(h.clock.Now().Add(d)) }

func (h *googleHarness) sync() {
	h.t.Helper()
	if err := h.g.Sync(h.ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *googleHarness) linkState() GoogleLink {
	h.t.Helper()
	v, err := h.g.Link(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return v.(GoogleLink)
}

func (h *googleHarness) entries() []Entry {
	h.t.Helper()
	now := h.clock.Now()
	res, err := h.svc.Entries(h.ctx, now.Add(-24*time.Hour), now.Add(14*24*time.Hour), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return res.Entries
}

func (h *googleHarness) item(id string) Item {
	h.t.Helper()
	it, err := h.st.GetItem(h.ctx, id)
	if err != nil {
		h.t.Fatalf("get %s: %v", id, err)
	}
	return it
}

// createEvent creates a one-hour event at start through the service.
func (h *googleHarness) createEvent(title string, start time.Time) Item {
	h.t.Helper()
	it, err := h.svc.CreateItem(h.ctx, Item{Kind: KindEvent, Title: title, Start: start.UnixMilli(), End: start.Add(time.Hour).UnixMilli()}, Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	return it
}

func timed(t time.Time) map[string]any {
	return map[string]any{"dateTime": t.UTC().Format(time.RFC3339)}
}

func hasEntry(entries []Entry, source, title string) bool {
	for _, e := range entries {
		if e.Source == source && e.Title == title {
			return true
		}
	}
	return false
}

// ---- tests ---------------------------------------------------------------------

// API-CAL-013: the consent URL carries the scope, offline access and a PKCE
// S256 challenge; the exchange stores the refresh token, the calendars and
// links the account.
func TestGoogleAuthURLAndExchange(t *testing.T) {
	h := newGoogleHarness(t)
	if _, err := h.g.AuthURL(h.ctx, fakeRedirect, "s"); !errors.Is(err, ErrClientMissing) || ErrorCode(err) != "client_missing" {
		t.Fatalf("auth url without client: %v", err)
	}
	if err := h.g.SetClient(h.ctx, " ", "x"); ErrorCode(err) != "invalid_client" {
		t.Fatalf("empty client: %v", err)
	}
	if l := h.linkState(); l.State != GoogleOff || l.ClientConfigured || l.Calendars == nil {
		t.Fatalf("initial link %+v", l)
	}
	if err := h.g.SetClient(h.ctx, "cid", "csecret"); err != nil {
		t.Fatal(err)
	}
	raw, err := h.g.AuthURL(h.ctx, fakeRedirect, "state1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(raw, "https://accounts.example/o/oauth2/v2/auth?") {
		t.Fatalf("auth url %q", raw)
	}
	q := u.Query()
	want := map[string]string{
		"client_id": "cid", "redirect_uri": fakeRedirect, "response_type": "code", "scope": GoogleScope,
		"access_type": "offline", "prompt": "consent", "state": "state1", "code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if len(q.Get("code_challenge")) != 43 {
		t.Fatalf("challenge %q", q.Get("code_challenge"))
	}
	h.fake.set(func() { h.fake.challenge = q.Get("code_challenge") })
	if err := h.g.Exchange(h.ctx, "code1", fakeRedirect); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := h.secrets.Get(h.ctx, SecretGoogleRefreshToken); v != "rt1" {
		t.Fatalf("refresh token %q", v)
	}
	l := h.linkState()
	if l.State != GoogleLinked || l.Account != fakePrimary || l.Target != fakePrimary || !l.ClientConfigured || l.Error != "" {
		t.Fatalf("link %+v", l)
	}
	if len(l.Calendars) != 2 {
		t.Fatalf("calendars %+v", l.Calendars)
	}
	if c := l.Calendars[0]; c.ID != fakePrimary || c.Name != "Owner" || c.Color != "#4285f4" || !c.Selected || !c.Writable {
		t.Fatalf("primary %+v", c)
	}
	if c := l.Calendars[1]; c.Selected || c.Writable {
		t.Fatalf("holidays %+v", c)
	}
	if res, _ := h.svc.Entries(h.ctx, h.clock.Now(), h.clock.Now().Add(time.Hour), nil); res.GoogleState != GoogleLinked {
		t.Fatalf("entries google state %q", res.GoogleState)
	}
	// The verifier is used once.
	if err := h.g.Exchange(h.ctx, "code1", fakeRedirect); err == nil {
		t.Fatal("second exchange without a consent succeeded")
	}
	// The link object is the contract JSON.
	data, _ := json.Marshal(l)
	for _, key := range []string{`"client_configured":true`, `"state":"linked"`, `"last_sync":`, `"writable":true`, `"target":"owner@example.com"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("link json %s lacks %s", data, key)
		}
	}
}

// AC-CAL-020: an event created in Google appears after Sync; the next sync
// uses the sync token; a 410 makes a full list again.
func TestGooglePullNewEvents(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	start := h.clock.Now().Add(26 * time.Hour)
	h.fake.put(fakePrimary, map[string]any{"id": "g1", "summary": "Dentist", "start": timed(start), "end": timed(start.Add(time.Hour))})
	h.sync()
	if !hasEntry(h.entries(), SourceGoogle, "Dentist") {
		t.Fatalf("google event missing: %+v", h.entries())
	}
	lists := h.fake.calls(http.MethodGet, "/events")
	if len(lists) != 1 || lists[0].query.Get("timeMin") == "" || lists[0].query.Get("syncToken") != "" || lists[0].query.Get("singleEvents") != "true" {
		t.Fatalf("first list %+v", lists)
	}
	if l := h.linkState(); l.LastSync != h.clock.Now().UnixMilli() || l.State != GoogleLinked {
		t.Fatalf("link after sync %+v", l)
	}

	h.fake.put(fakePrimary, map[string]any{"id": "g2", "summary": "Lunch", "start": map[string]any{"date": "2026-11-07"}, "end": map[string]any{"date": "2026-11-08"}})
	h.fake.edit(fakePrimary, "g1", func(d map[string]any) { d["status"] = "cancelled" })
	h.sync()
	lists = h.fake.calls(http.MethodGet, "/events")
	if last := lists[len(lists)-1]; last.query.Get("syncToken") == "" || last.query.Get("timeMin") != "" {
		t.Fatalf("incremental list %+v", last.query)
	}
	entries := h.entries()
	if !hasEntry(entries, SourceGoogle, "Lunch") || hasEntry(entries, SourceGoogle, "Dentist") {
		t.Fatalf("entries after incremental %+v", entries)
	}

	h.fake.set(func() { h.fake.goneOnce = true })
	h.fake.put(fakePrimary, map[string]any{"id": "g3", "summary": "Gym", "start": timed(start), "end": timed(start.Add(time.Hour))})
	h.sync()
	lists = h.fake.calls(http.MethodGet, "/events")
	if last := lists[len(lists)-1]; last.query.Get("timeMin") == "" || last.query.Get("syncToken") != "" {
		t.Fatalf("list after 410 %+v", last.query)
	}
	entries = h.entries()
	if !hasEntry(entries, SourceGoogle, "Gym") || !hasEntry(entries, SourceGoogle, "Lunch") || hasEntry(entries, SourceGoogle, "Dentist") {
		t.Fatalf("entries after full resync %+v", entries)
	}
}

// AC-CAL-021: a new lana event is inserted with lana_item; moving it patches
// the Google event with the new time and the stored etag.
func TestGooglePushInsertAndMove(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	it := h.createEvent("Review", start)
	if it.Google == nil || it.Google.Sync != SyncPending || it.Google.CalendarID != fakePrimary {
		t.Fatalf("created ref %+v", it.Google)
	}
	h.sync()
	got := h.item(it.ID)
	if got.Google == nil || got.Google.Sync != SyncSynced || got.Google.EventID == "" || got.Google.ETag == "" {
		t.Fatalf("ref after insert %+v", got.Google)
	}
	ev := h.fake.event(fakePrimary, got.Google.EventID)
	props := ev["extendedProperties"].(map[string]any)["private"].(map[string]any)
	if props[googleLanaProp] != it.ID || ev["summary"] != "Review" {
		t.Fatalf("inserted event %+v", ev)
	}
	if s := ev["start"].(map[string]any); s["dateTime"] != "2026-11-06T10:00:00Z" || s["timeZone"] != "UTC" {
		t.Fatalf("start %+v", s)
	}
	// The pulled copy of lana's own event is not shown twice.
	if hasEntry(h.entries(), SourceGoogle, "Review") {
		t.Fatal("own event cached as a google entry")
	}

	h.advance(time.Minute)
	moved := start.Add(2 * time.Hour)
	patch := fmt.Sprintf(`{"start":%d,"end":%d}`, moved.UnixMilli(), moved.Add(time.Hour).UnixMilli())
	if _, err := h.svc.UpdateItem(h.ctx, it.ID, json.RawMessage(patch), Owner); err != nil {
		t.Fatal(err)
	}
	if h.item(it.ID).Google.Sync != SyncPending {
		t.Fatal("moved item not pending")
	}
	oldETag := got.Google.ETag
	h.sync()
	patches := h.fake.calls(http.MethodPatch, got.Google.EventID)
	if len(patches) != 1 || patches[0].ifMatch != oldETag {
		t.Fatalf("patches %+v (want If-Match %s)", patches, oldETag)
	}
	ev = h.fake.event(fakePrimary, got.Google.EventID)
	if s := ev["start"].(map[string]any); s["dateTime"] != "2026-11-06T12:00:00Z" {
		t.Fatalf("moved start %+v", s)
	}
	after := h.item(it.ID)
	if after.Google.Sync != SyncSynced || after.Google.ETag != ev["etag"] {
		t.Fatalf("ref after patch %+v", after.Google)
	}
	// The next pull sees lana's own write and changes nothing.
	h.sync()
	if again := h.item(it.ID); again.UpdatedAt != after.UpdatedAt || again.UpdatedBy != Owner {
		t.Fatalf("own write echoed: %+v", again)
	}

	// A Google-side move of a synced item updates it, by "google".
	h.advance(time.Minute)
	gStart := start.Add(5 * time.Hour)
	h.fake.edit(fakePrimary, got.Google.EventID, func(d map[string]any) {
		d["start"], d["end"], d["summary"] = timed(gStart), timed(gStart.Add(30*time.Minute)), "Review (moved)"
	})
	h.sync()
	g := h.item(it.ID)
	if g.Start != gStart.UnixMilli() || g.End != gStart.Add(30*time.Minute).UnixMilli() || g.Title != "Review (moved)" || g.UpdatedBy != ByGoogle || g.Google.Sync != SyncSynced {
		t.Fatalf("google change not applied: %+v", g)
	}
}

// The push body maps repeats, all-day spans and reminders.
func TestGoogleEventBody(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, loc)
	weekly := Item{ID: "ci_w", Kind: KindEvent, Title: "Standup", Start: start.UnixMilli(), End: start.Add(15 * time.Minute).UnixMilli(),
		Repeat: Repeat{Freq: FreqWeekly, Days: []string{"wed", "mon"}, Until: "2026-12-31"}}
	body := eventBody(weekly, loc)
	if got := body["recurrence"].([]string); len(got) != 1 || got[0] != "RRULE:FREQ=WEEKLY;BYDAY=MO,WE;UNTIL=20270101T075959Z" {
		t.Fatalf("weekly rrule %v", got)
	}
	if s := body["start"].(googleTime); s.TimeZone != "America/Los_Angeles" || s.DateTime != "2026-11-02T09:00:00-08:00" {
		t.Fatalf("start %+v", s)
	}
	allDay := Item{ID: "ci_a", Kind: KindEvent, Title: "Trip", AllDay: true, Date: "2026-11-10", EndDate: "2026-11-12",
		Repeat: Repeat{Freq: FreqWeekdays, Until: "2026-11-30"}}
	body = eventBody(allDay, loc)
	if body["start"].(googleTime).Date != "2026-11-10" || body["end"].(googleTime).Date != "2026-11-13" {
		t.Fatalf("all-day span %+v %+v", body["start"], body["end"])
	}
	if got := body["recurrence"].([]string)[0]; got != "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;UNTIL=20261130" {
		t.Fatalf("weekdays rrule %s", got)
	}
	rem := Item{ID: "ci_r", Kind: KindReminder, Title: "Call", Start: start.UnixMilli()}
	body = eventBody(rem, loc)
	if body["end"].(googleTime).DateTime != "2026-11-02T09:30:00-08:00" || len(body["recurrence"].([]string)) != 0 {
		t.Fatalf("reminder body %+v", body)
	}
	for _, it := range []Item{weekly, allDay, {Kind: KindEvent, Repeat: Repeat{Freq: FreqDaily}}, {Kind: KindEvent, Repeat: Repeat{Freq: FreqMonthly}}, {Kind: KindEvent, Repeat: Repeat{Freq: FreqYearly, Until: "2030-01-01"}}} {
		rule := toRRULE(it, loc)
		r, ok := parseRRULE([]string{rule}, start, loc)
		if !ok || r.Freq != it.Repeat.Freq || r.Until != it.Repeat.Until {
			t.Fatalf("round trip %s: %+v %v", rule, r, ok)
		}
	}
	if _, ok := parseRRULE([]string{"RRULE:FREQ=WEEKLY;INTERVAL=2"}, start, loc); ok {
		t.Fatal("interval 2 accepted")
	}
}

// REQ-CAL-012: a 412 on patch reads the event again and the later update
// wins; the loser is kept in the item history.
func TestGoogleConflictOn412(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

	// lana changed later: lana wins and overwrites Google.
	a := h.createEvent("Plan", start)
	h.sync()
	evA := h.item(a.ID).Google.EventID
	h.advance(time.Minute)
	h.fake.edit(fakePrimary, evA, func(d map[string]any) { d["summary"] = "Plan (google)" })
	h.advance(time.Minute)
	if _, err := h.svc.UpdateItem(h.ctx, a.ID, json.RawMessage(`{"title":"Plan (lana)"}`), Owner); err != nil {
		t.Fatal(err)
	}
	if err := h.g.push(h.ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.event(fakePrimary, evA)["summary"]; got != "Plan (lana)" {
		t.Fatalf("google summary %v", got)
	}
	if it := h.item(a.ID); it.Title != "Plan (lana)" || it.Google.Sync != SyncSynced {
		t.Fatalf("item %+v", it)
	}
	if !historyHas(t, h, a.ID, "kept the lana version", "Plan (google)") {
		t.Fatal("google loser not in history")
	}

	// Google changed later: Google wins and the lana side goes to history.
	b := h.createEvent("Gym", start)
	h.sync()
	evB := h.item(b.ID).Google.EventID
	h.advance(time.Minute)
	if _, err := h.svc.UpdateItem(h.ctx, b.ID, json.RawMessage(`{"title":"Gym (lana)"}`), Owner); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	h.fake.edit(fakePrimary, evB, func(d map[string]any) { d["summary"] = "Gym (google)" })
	if err := h.g.push(h.ctx); err != nil {
		t.Fatal(err)
	}
	it := h.item(b.ID)
	if it.Title != "Gym (google)" || it.UpdatedBy != ByGoogle || it.Google.Sync != SyncSynced {
		t.Fatalf("item after google win %+v", it)
	}
	if got := h.fake.event(fakePrimary, evB)["summary"]; got != "Gym (google)" {
		t.Fatalf("google overwritten: %v", got)
	}
	if !historyHas(t, h, b.ID, "kept the Google version", "Gym (lana)") {
		t.Fatal("lana loser not in history")
	}
}

func historyHas(t *testing.T, h *googleHarness, itemID, summary, snapshot string) bool {
	t.Helper()
	lines, err := h.st.ListHistory(h.ctx, itemID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if strings.Contains(l.Summary, summary) && strings.Contains(l.Snapshot, snapshot) && l.By == ByGoogle {
			return true
		}
	}
	return false
}

// A cancelled Google event deletes its lana item; a cancelled instance of a
// lana repeating event skips that occurrence.
func TestGoogleCancelledEvents(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	it := h.createEvent("Coffee", start)
	h.sync()
	evID := h.item(it.ID).Google.EventID
	h.advance(time.Minute)
	h.fake.edit(fakePrimary, evID, func(d map[string]any) { d["status"] = "cancelled" })
	h.sync()
	if _, err := h.st.GetItem(h.ctx, it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("item not deleted: %v", err)
	}
	if del, _ := h.st.ListDeletedItems(h.ctx); len(del) != 0 {
		t.Fatalf("deleted item still linked %+v", del)
	}
	if !historyHas(t, h, it.ID, "Deleted in Google", "") {
		t.Fatal("no delete history")
	}

	daily, err := h.svc.CreateItem(h.ctx, Item{Kind: KindEvent, Title: "Walk", Start: start.UnixMilli(), End: start.Add(time.Hour).UnixMilli(),
		Repeat: Repeat{Freq: FreqDaily}}, Owner)
	if err != nil {
		t.Fatal(err)
	}
	h.sync()
	master := h.item(daily.ID).Google.EventID
	if rec := h.fake.event(fakePrimary, master)["recurrence"].([]any); len(rec) != 1 || rec[0] != "RRULE:FREQ=DAILY" {
		t.Fatalf("recurrence %v", rec)
	}
	occ := start.Add(24 * time.Hour)
	h.fake.put(fakePrimary, map[string]any{
		"id": master + "_20261107T100000Z", "status": "cancelled", "recurringEventId": master,
		"originalStartTime": timed(occ),
	})
	// A changed instance of a lana event is not shown as a Google entry.
	h.fake.put(fakePrimary, map[string]any{
		"id": master + "_20261108T100000Z", "recurringEventId": master, "summary": "Walk",
		"start": timed(occ.Add(25 * time.Hour)), "end": timed(occ.Add(26 * time.Hour)),
		"extendedProperties": map[string]any{"private": map[string]any{googleLanaProp: daily.ID}},
	})
	h.sync()
	overrides, _ := h.st.ListOverrides(h.ctx)
	if len(overrides) != 1 || overrides[0].Ref != daily.ID || overrides[0].Occurrence != occ.UnixMilli() || overrides[0].Op != OpSkip || overrides[0].By != ByGoogle {
		t.Fatalf("overrides %+v", overrides)
	}
	if hasEntry(h.entries(), SourceGoogle, "Walk") {
		t.Fatal("lana instance cached as a google entry")
	}
}

// API-CAL-005: deleting a synced lana item deletes the Google event, then
// the item is purged.
func TestGoogleDeleteItemDeletesEvent(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	it := h.createEvent("Call", time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	h.sync()
	evID := h.item(it.ID).Google.EventID
	if err := h.svc.DeleteItem(h.ctx, it.ID, Owner); err != nil {
		t.Fatal(err)
	}
	if del, _ := h.st.ListDeletedItems(h.ctx); len(del) != 1 {
		t.Fatalf("deleted items %+v", del)
	}
	h.sync()
	if len(h.fake.calls(http.MethodDelete, evID)) != 1 || h.fake.event(fakePrimary, evID)["status"] != "cancelled" {
		t.Fatal("google event not deleted")
	}
	if del, _ := h.st.ListDeletedItems(h.ctx); len(del) != 0 {
		t.Fatalf("deleted item not purged %+v", del)
	}
	// An unsynced deleted item is purged without a Google call.
	pending := h.createEvent("Never pushed", time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC))
	if err := h.svc.DeleteItem(h.ctx, pending.ID, Owner); err != nil {
		t.Fatal(err)
	}
	deletes := len(h.fake.calls(http.MethodDelete, ""))
	h.sync()
	if len(h.fake.calls(http.MethodDelete, "")) != deletes || len(h.fake.calls(http.MethodPost, "/events")) != 1 {
		t.Fatal("unsynced deleted item reached Google")
	}
}

// AC-CAL-022: an auth error makes the link needs_reauth and keeps every lana
// item; the loop stays idle until the owner connects again.
func TestGoogleInvalidGrantNeedsReauth(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	it := h.createEvent("Keep me", time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	h.sync()
	h.fake.set(func() { h.fake.refreshInvalid = true })
	h.advance(2 * time.Hour) // the access token expired
	if _, err := h.svc.UpdateItem(h.ctx, it.ID, json.RawMessage(`{"title":"Keep me too"}`), Owner); err != nil {
		t.Fatal(err)
	}
	h.sync()
	l := h.linkState()
	if l.State != GoogleNeedsReauth || l.Error == "" {
		t.Fatalf("link %+v", l)
	}
	if res, _ := h.svc.Entries(h.ctx, h.clock.Now(), h.clock.Now().Add(time.Hour), nil); res.GoogleState != GoogleNeedsReauth {
		t.Fatalf("entries state %q", res.GoogleState)
	}
	got := h.item(it.ID)
	if got.Title != "Keep me too" || got.Google == nil || got.Google.Sync != SyncPending {
		t.Fatalf("item changed: %+v", got)
	}
	// New items still collect a pending link for after the new consent.
	later := h.createEvent("Later", time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC))
	if later.Google == nil || later.Google.Sync != SyncPending {
		t.Fatalf("new item ref %+v", later.Google)
	}
	requests := len(h.fake.calls(http.MethodGet, "")) + len(h.fake.calls(http.MethodPost, ""))
	h.g.loopPass(h.ctx, true)
	if n := len(h.fake.calls(http.MethodGet, "")) + len(h.fake.calls(http.MethodPost, "")); n != requests {
		t.Fatal("loop ran while needs_reauth")
	}
	// Connecting again pushes the collected changes.
	h.fake.set(func() { h.fake.refreshInvalid = false })
	h.link()
	h.sync()
	if l := h.linkState(); l.State != GoogleLinked {
		t.Fatalf("relinked %+v", l)
	}
	if h.item(later.ID).Google.Sync != SyncSynced || h.item(it.ID).Google.Sync != SyncSynced {
		t.Fatal("pending items not pushed after relink")
	}
}

// API-CAL-014: disconnect revokes the token and drops the Google data;
// items stay without a Google link.
func TestGoogleDisconnect(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	start := h.clock.Now().Add(26 * time.Hour)
	h.fake.put(fakePrimary, map[string]any{"id": "g1", "summary": "Dentist", "start": timed(start), "end": timed(start.Add(time.Hour))})
	it := h.createEvent("Mine", start)
	h.sync()
	if evs, _ := h.st.ListGoogleEvents(h.ctx); len(evs) != 1 {
		t.Fatalf("cache %+v", evs)
	}
	if err := h.g.Disconnect(h.ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.fake.revoked) != 1 || h.fake.revoked[0] != "rt1" {
		t.Fatalf("revoked %v", h.fake.revoked)
	}
	if _, ok, _ := h.secrets.Get(h.ctx, SecretGoogleRefreshToken); ok {
		t.Fatal("refresh token kept")
	}
	if l := h.linkState(); l.State != GoogleOff || len(l.Calendars) != 0 || l.Account != "" || !l.ClientConfigured {
		t.Fatalf("link %+v", l)
	}
	if evs, _ := h.st.ListGoogleEvents(h.ctx); len(evs) != 0 {
		t.Fatalf("cache kept %+v", evs)
	}
	if got := h.item(it.ID); got.Google != nil {
		t.Fatalf("item kept its link %+v", got.Google)
	}
	if err := h.g.Sync(h.ctx); !errors.Is(err, ErrNotLinked) || ErrorCode(err) != "not_linked" {
		t.Fatalf("sync after disconnect: %v", err)
	}
	if _, err := h.svc.CreateItem(h.ctx, Item{Kind: KindEvent, Title: "x", Start: start.UnixMilli(), End: start.Add(time.Hour).UnixMilli()}, Owner); err != nil {
		t.Fatal(err)
	}
}

// API-CAL-012: the target must be a selected, writable calendar.
func TestGooglePutValidatesTarget(t *testing.T) {
	h := newGoogleHarness(t)
	if _, err := h.g.Put(h.ctx, map[string]bool{}, fakePrimary); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("put unlinked: %v", err)
	}
	h.link()
	if _, err := h.g.Put(h.ctx, map[string]bool{fakeHolidays: true}, fakeHolidays); ErrorCode(err) != "invalid_target" {
		t.Fatalf("read-only target: %v", err)
	}
	if _, err := h.g.Put(h.ctx, map[string]bool{fakePrimary: false}, fakePrimary); ErrorCode(err) != "invalid_target" {
		t.Fatalf("unselected target: %v", err)
	}
	if _, err := h.g.Put(h.ctx, nil, "nope"); ErrorCode(err) != "invalid_target" {
		t.Fatalf("unknown target: %v", err)
	}
	v, err := h.g.Put(h.ctx, map[string]bool{fakeHolidays: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	l := v.(GoogleLink)
	if l.Target != fakePrimary || !l.Calendars[1].Selected || !l.Calendars[0].Selected {
		t.Fatalf("link %+v", l)
	}
	// A deselected calendar loses its cached events.
	start := h.clock.Now().Add(26 * time.Hour)
	h.fake.put(fakeHolidays, map[string]any{"id": "h1", "summary": "Holiday", "start": timed(start), "end": timed(start.Add(time.Hour))})
	h.sync()
	entries := h.entries()
	if !hasEntry(entries, SourceGoogle, "Holiday") {
		t.Fatal("holiday not pulled")
	}
	for _, e := range entries {
		if e.Title == "Holiday" && (e.Google.CalendarName != "Holidays" || e.Google.Color != "#0b8043") {
			t.Fatalf("holiday entry %+v", e.Google)
		}
	}
	if _, err := h.g.Put(h.ctx, map[string]bool{fakeHolidays: false}, fakePrimary); err != nil {
		t.Fatal(err)
	}
	if hasEntry(h.entries(), SourceGoogle, "Holiday") {
		t.Fatal("deselected calendar still shown")
	}
}

// Network and 5xx errors mark the item "error" and retry with backoff.
func TestGooglePushBackoff(t *testing.T) {
	h := newGoogleHarness(t)
	h.link()
	h.fake.set(func() { h.fake.failInsert = true })
	it := h.createEvent("Flaky", time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	if err := h.g.push(h.ctx); err == nil {
		t.Fatal("push succeeded")
	}
	if got := h.item(it.ID); got.Google.Sync != SyncError {
		t.Fatalf("sync %q", got.Google.Sync)
	}
	posts := func() int { return len(h.fake.calls(http.MethodPost, "/events")) }
	if posts() != 1 {
		t.Fatalf("posts %d", posts())
	}
	_ = h.g.push(h.ctx)
	if posts() != 1 {
		t.Fatal("retried before the backoff")
	}
	h.advance(61 * time.Second)
	_ = h.g.push(h.ctx)
	if posts() != 2 {
		t.Fatalf("posts after 1 min %d", posts())
	}
	h.advance(61 * time.Second) // second wait is 2 min
	_ = h.g.push(h.ctx)
	if posts() != 2 {
		t.Fatal("retried before the 2 min backoff")
	}
	h.advance(time.Minute)
	h.sync() // Sync records the failure in the link state
	if posts() != 3 {
		t.Fatalf("posts after 2 min %d", posts())
	}
	if l := h.linkState(); l.State != GoogleError || !strings.Contains(l.Error, "not synced") {
		t.Fatalf("link %+v", l)
	}
	h.fake.set(func() { h.fake.failInsert = false })
	h.advance(5 * time.Minute)
	h.sync()
	if got := h.item(it.ID); got.Google.Sync != SyncSynced {
		t.Fatalf("sync after recovery %q", got.Google.Sync)
	}
	if l := h.linkState(); l.State != GoogleLinked || l.Error != "" {
		t.Fatalf("link after recovery %+v", l)
	}
}

// The loop pulls right after Start and stops with Close.
func TestGoogleLoop(t *testing.T) {
	h := newGoogleHarness(t, WithGoogleIntervals(time.Hour, time.Hour))
	h.link()
	start := h.clock.Now().Add(26 * time.Hour)
	h.fake.put(fakePrimary, map[string]any{"id": "g1", "summary": "Loop", "start": timed(start), "end": timed(start.Add(time.Hour))})
	ch, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()
	h.g.Start(h.ctx)
	defer h.g.Close()
	deadline := time.After(5 * time.Second)
	for !hasEntry(h.entries(), SourceGoogle, "Loop") {
		select {
		case <-ch:
		case <-deadline:
			t.Fatal("loop did not pull")
		}
	}
	h.g.Close()
	h.g.Start(h.ctx) // no-op after Close
}

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

const (
	// calendarBodyLimit is the largest accepted /v1/calendar body.
	calendarBodyLimit = 1 << 20
	// calendarOAuthTTL is how long a Google OAuth state stays valid
	// (API-CAL-013).
	calendarOAuthTTL = 10 * time.Minute
	// calendarOAuthMaxStates bounds the pending OAuth states kept in memory.
	calendarOAuthMaxStates = 64
	// calendarActor is the "by" value of an owner request.
	calendarActor = calendar.Owner

	calendarGoogleLinked = "/#/calendar?google=linked"
	calendarGoogleError  = "/#/calendar?google=error"
)

// CalendarBackend is the subset of *calendar.Service the calendar handlers use
// (spec #35, API-CAL-001 through API-CAL-011 and API-CAL-015). It is defined in
// web so the package does not depend on the service type; the service
// satisfies it. The daemon installs it with SetCalendar.
type CalendarBackend interface {
	Entries(ctx context.Context, from, to time.Time, sources []string) (calendar.EntriesResult, error)
	CreateItem(ctx context.Context, it calendar.Item, by string) (calendar.Item, error)
	GetItem(ctx context.Context, id string) (calendar.ItemDetail, error)
	UpdateItem(ctx context.Context, id string, patch json.RawMessage, by string) (calendar.Item, error)
	DeleteItem(ctx context.Context, id, by string) error
	SaveOverride(ctx context.Context, o calendar.Override, by string) (calendar.Override, error)
	ClearOverride(ctx context.Context, source, ref string, occurrence int64) error
	Complete(ctx context.Context, id string, occurrence int64, done bool, by string) (calendar.Item, *calendar.EntryHabit, error)
	RunFiring(ctx context.Context, itemID string, occurrence int64, by string) (calendar.Firing, error)
	DismissFiring(ctx context.Context, itemID string, occurrence int64, by string) (calendar.Firing, error)
	Settings(ctx context.Context) (calendar.Settings, error)
	PutSettings(ctx context.Context, st calendar.Settings) (calendar.Settings, error)
	// ICSToken returns the feed token, creating one on first use.
	ICSToken(ctx context.Context) (string, error)
	RotateICS(ctx context.Context) (string, error)
	// CheckICSToken reports, in constant time, whether token is the feed token.
	CheckICSToken(ctx context.Context, token string) bool
	WriteFeed(ctx context.Context, w io.Writer) error
	// Subscribe returns the change stream and its unsubscribe function
	// (API-CAL-015).
	Subscribe() (<-chan calendar.Change, func())
}

// CalendarGoogle is the Google Calendar link of API-CAL-012 through
// API-CAL-014. Link and Put return the link state object of API-CAL-012; it is
// written as JSON as is. Errors with a contract code (calendar.ErrorCode) map
// like the other calendar errors: invalid_target, client_missing and
// invalid_client are 400, not_linked is 409.
type CalendarGoogle interface {
	// Link returns the link state (API-CAL-012).
	Link(ctx context.Context) (any, error)
	// Put saves the calendar selection, keyed by calendar id, and the target
	// calendar, and returns the new link state (API-CAL-012).
	Put(ctx context.Context, selected map[string]bool, target string) (any, error)
	// SetClient stores the OAuth client in the secret store (API-CAL-013).
	SetClient(ctx context.Context, clientID, clientSecret string) error
	// AuthURL returns the Google consent URL for redirectURI and state
	// (API-CAL-013).
	AuthURL(ctx context.Context, redirectURI, state string) (string, error)
	// Exchange trades the authorization code for a token and links the
	// account (API-CAL-013).
	Exchange(ctx context.Context, code, redirectURI string) error
	// Sync pulls then pushes now (API-CAL-014).
	Sync(ctx context.Context) error
	// Disconnect revokes the token and unlinks the account (API-CAL-014).
	Disconnect(ctx context.Context) error
}

// calendarPort holds the calendar ports and the pending Google OAuth states.
// The zero value has no ports.
type calendarPort struct {
	mu      sync.RWMutex
	backend CalendarBackend
	google  CalendarGoogle

	statesMu sync.Mutex
	states   map[string]calendarOAuthState
}

// calendarOAuthState is one pending Google OAuth state, bound to the browser
// session that started the connect.
type calendarOAuthState struct {
	session     [32]byte
	redirectURI string
	expiresAt   time.Time
}

// SetCalendar installs the calendar port. Call it before Start; a nil port
// makes every /v1/calendar route answer 404 not_found, so the web UI falls
// back to the workflow data.
func (s *Server) SetCalendar(b CalendarBackend) {
	s.calendar.mu.Lock()
	defer s.calendar.mu.Unlock()
	s.calendar.backend = b
}

// SetCalendarGoogle installs the Google Calendar port. Without it the Google
// routes answer as "off" (API-CAL-012) or 404 not_found.
func (s *Server) SetCalendarGoogle(g CalendarGoogle) {
	s.calendar.mu.Lock()
	defer s.calendar.mu.Unlock()
	s.calendar.google = g
}

func (s *Server) calendarBackend() CalendarBackend {
	s.calendar.mu.RLock()
	defer s.calendar.mu.RUnlock()
	return s.calendar.backend
}

func (s *Server) calendarGoogle() CalendarGoogle {
	s.calendar.mu.RLock()
	defer s.calendar.mu.RUnlock()
	return s.calendar.google
}

// calendarOrError returns the calendar port, or writes 404 not_found.
func (s *Server) calendarOrError(w http.ResponseWriter) CalendarBackend {
	backend := s.calendarBackend()
	if backend == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "the calendar is not available")
		return nil
	}
	return backend
}

// googleOrError returns the Google port, or writes 404 not_found. The
// calendar port must be installed too.
func (s *Server) googleOrError(w http.ResponseWriter) CalendarGoogle {
	if s.calendarOrError(w) == nil {
		return nil
	}
	google := s.calendarGoogle()
	if google == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Google Calendar is not available")
		return nil
	}
	return google
}

// registerCalendarRoutes registers API-CAL-001 through API-CAL-015. The /v1
// routes go through Handle so the Host, Origin, cookie, and CSRF checks apply.
// The ICS feed (API-CAL-011) is on the raw mux: a calendar app carries only the
// token in the path. The OAuth callback (API-CAL-013) keeps only the Host
// check: the session cookie is SameSite=Strict, so it is not sent on the
// redirect from Google. The callback bounces to a same-site finish route that
// checks the session.
func (s *Server) registerCalendarRoutes() {
	s.mux.Handle("GET /cal/{file}", http.HandlerFunc(s.handleCalendarFeed))
	s.mux.Handle("GET /v1/calendar/google/callback", s.checkHost(http.HandlerFunc(s.handleCalendarGoogleCallback)))
	s.mux.Handle("GET /v1/calendar/google/callback/finish", s.checkHost(http.HandlerFunc(s.handleCalendarGoogleFinish)))

	s.Handle("GET /v1/calendar/entries", http.HandlerFunc(s.handleCalendarEntries))
	s.Handle("GET /v1/calendar/stream", http.HandlerFunc(s.handleCalendarStream))
	s.Handle("POST /v1/calendar/items", http.HandlerFunc(s.handleCalendarCreateItem))
	s.Handle("GET /v1/calendar/items/{id}", http.HandlerFunc(s.handleCalendarGetItem))
	s.Handle("PATCH /v1/calendar/items/{id}", http.HandlerFunc(s.handleCalendarUpdateItem))
	s.Handle("DELETE /v1/calendar/items/{id}", http.HandlerFunc(s.handleCalendarDeleteItem))
	s.Handle("POST /v1/calendar/items/{id}/complete", http.HandlerFunc(s.handleCalendarComplete))
	s.Handle("POST /v1/calendar/overrides", http.HandlerFunc(s.handleCalendarSaveOverride))
	s.Handle("DELETE /v1/calendar/overrides", http.HandlerFunc(s.handleCalendarClearOverride))
	s.Handle("POST /v1/calendar/firings/run", http.HandlerFunc(s.handleCalendarRunFiring))
	s.Handle("POST /v1/calendar/firings/dismiss", http.HandlerFunc(s.handleCalendarDismissFiring))
	s.Handle("GET /v1/calendar/settings", http.HandlerFunc(s.handleCalendarSettings))
	s.Handle("PUT /v1/calendar/settings", http.HandlerFunc(s.handleCalendarPutSettings))
	s.Handle("POST /v1/calendar/ics/rotate", http.HandlerFunc(s.handleCalendarRotateICS))
	s.Handle("GET /v1/calendar/google", http.HandlerFunc(s.handleCalendarGoogle))
	s.Handle("PUT /v1/calendar/google", http.HandlerFunc(s.handleCalendarPutGoogle))
	s.Handle("PUT /v1/calendar/google/client", http.HandlerFunc(s.handleCalendarGoogleClient))
	s.Handle("POST /v1/calendar/google/connect", http.HandlerFunc(s.handleCalendarGoogleConnect))
	s.Handle("POST /v1/calendar/google/sync", http.HandlerFunc(s.handleCalendarGoogleSync))
	s.Handle("POST /v1/calendar/google/disconnect", http.HandlerFunc(s.handleCalendarGoogleDisconnect))
}

// handleCalendarEntries serves API-CAL-001 (REQ-CAL-002, REQ-CAL-005,
// REQ-CAL-008).
func (s *Server) handleCalendarEntries(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	query := r.URL.Query()
	from, errFrom := strconv.ParseInt(query.Get("from"), 10, 64)
	to, errTo := strconv.ParseInt(query.Get("to"), 10, 64)
	if errFrom != nil || errTo != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_range", "from and to must be Unix milliseconds")
		return
	}
	result, err := backend.Entries(r.Context(), time.UnixMilli(from), time.UnixMilli(to), query["sources"])
	if err != nil {
		s.writeCalendarError(w, "calendar entries", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// calendarItemResponse is the {"item": item} response of API-CAL-002 and
// API-CAL-004, with the habit state of API-CAL-007.
type calendarItemResponse struct {
	Item  calendar.Item        `json:"item"`
	Habit *calendar.EntryHabit `json:"habit,omitempty"`
}

// handleCalendarCreateItem serves API-CAL-002 (REQ-CAL-001, REQ-CAL-003).
func (s *Server) handleCalendarCreateItem(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var item calendar.Item
	if !decodeJSONObject(w, r, calendarBodyLimit, &item) {
		return
	}
	created, err := backend.CreateItem(r.Context(), item, calendarActor)
	if err != nil {
		s.writeCalendarError(w, "create calendar item", err)
		return
	}
	writeJSON(w, http.StatusCreated, calendarItemResponse{Item: created})
}

// handleCalendarGetItem serves API-CAL-003 (REQ-CAL-001, REQ-CAL-006).
func (s *Server) handleCalendarGetItem(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	detail, err := backend.GetItem(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeCalendarError(w, "get calendar item", err)
		return
	}
	if detail.Firings == nil {
		detail.Firings = []calendar.Firing{}
	}
	if detail.History == nil {
		detail.History = []calendar.HistoryLine{}
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleCalendarUpdateItem serves API-CAL-004 (REQ-CAL-001, REQ-CAL-003). The
// body is passed to the service as is; it ignores kind and server fields.
func (s *Server) handleCalendarUpdateItem(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var patch map[string]json.RawMessage
	raw, ok := readJSONObject(w, r, calendarBodyLimit, &patch)
	if !ok {
		return
	}
	item, err := backend.UpdateItem(r.Context(), r.PathValue("id"), raw, calendarActor)
	if err != nil {
		s.writeCalendarError(w, "update calendar item", err)
		return
	}
	writeJSON(w, http.StatusOK, calendarItemResponse{Item: item})
}

// handleCalendarDeleteItem serves API-CAL-005 (REQ-CAL-001).
func (s *Server) handleCalendarDeleteItem(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	if err := backend.DeleteItem(r.Context(), r.PathValue("id"), calendarActor); err != nil {
		s.writeCalendarError(w, "delete calendar item", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCalendarComplete serves API-CAL-007 (REQ-CAL-005). done defaults to
// true when the body leaves it out.
func (s *Server) handleCalendarComplete(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Occurrence int64 `json:"occurrence"`
		Done       *bool `json:"done"`
	}
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	done := body.Done == nil || *body.Done
	item, habit, err := backend.Complete(r.Context(), r.PathValue("id"), body.Occurrence, done, calendarActor)
	if err != nil {
		s.writeCalendarError(w, "complete calendar item", err)
		return
	}
	writeJSON(w, http.StatusOK, calendarItemResponse{Item: item, Habit: habit})
}

// handleCalendarSaveOverride serves API-CAL-006 (REQ-CAL-004).
func (s *Server) handleCalendarSaveOverride(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Source     string `json:"source"`
		Ref        string `json:"ref"`
		Occurrence int64  `json:"occurrence"`
		Op         string `json:"op"`
		To         int64  `json:"to"`
	}
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	override, err := backend.SaveOverride(r.Context(), calendar.Override{
		Source:     body.Source,
		Ref:        body.Ref,
		Occurrence: body.Occurrence,
		Op:         body.Op,
		To:         body.To,
	}, calendarActor)
	if err != nil {
		s.writeCalendarError(w, "save calendar override", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Override calendar.Override `json:"override"`
	}{Override: override})
}

// handleCalendarClearOverride serves the DELETE of API-CAL-006 (REQ-CAL-004).
func (s *Server) handleCalendarClearOverride(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	query := r.URL.Query()
	source, ref := query.Get("source"), query.Get("ref")
	occurrence, err := strconv.ParseInt(query.Get("occurrence"), 10, 64)
	if err != nil || source == "" || ref == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_override", "source, ref and occurrence (Unix milliseconds) are required")
		return
	}
	if err := backend.ClearOverride(r.Context(), source, ref, occurrence); err != nil {
		s.writeCalendarError(w, "clear calendar override", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// calendarFiringBody is the body of API-CAL-008.
type calendarFiringBody struct {
	ItemID     string `json:"item_id"`
	Occurrence int64  `json:"occurrence"`
}

// calendarFiringResponse is the {"firing": firing} response of API-CAL-008.
type calendarFiringResponse struct {
	Firing calendar.Firing `json:"firing"`
}

// handleCalendarRunFiring serves the run of API-CAL-008 (REQ-CAL-006,
// REQ-CAL-007).
func (s *Server) handleCalendarRunFiring(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var body calendarFiringBody
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	firing, err := backend.RunFiring(r.Context(), body.ItemID, body.Occurrence, calendarActor)
	if err != nil {
		s.writeCalendarError(w, "run calendar firing", err)
		return
	}
	writeJSON(w, http.StatusAccepted, calendarFiringResponse{Firing: firing})
}

// handleCalendarDismissFiring serves the dismiss of API-CAL-008 (REQ-CAL-007).
func (s *Server) handleCalendarDismissFiring(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var body calendarFiringBody
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	firing, err := backend.DismissFiring(r.Context(), body.ItemID, body.Occurrence, calendarActor)
	if err != nil {
		s.writeCalendarError(w, "dismiss calendar firing", err)
		return
	}
	writeJSON(w, http.StatusOK, calendarFiringResponse{Firing: firing})
}

// calendarICS is the ics object of API-CAL-009.
type calendarICS struct {
	URL string `json:"url"`
}

// calendarSettingsResponse is the response of API-CAL-009.
type calendarSettingsResponse struct {
	calendar.Settings
	ICS calendarICS `json:"ics"`
}

// calendarFeedURL builds the ICS feed URL from the request host. The server
// listens on loopback over plain HTTP only.
func calendarFeedURL(r *http.Request, token string) string {
	return "http://" + r.Host + "/cal/" + token + ".ics"
}

// writeCalendarSettings writes settings with the feed URL (API-CAL-009).
func (s *Server) writeCalendarSettings(w http.ResponseWriter, r *http.Request, backend CalendarBackend, settings calendar.Settings) {
	token, err := backend.ICSToken(r.Context())
	if err != nil {
		s.writeCalendarError(w, "calendar ICS token", err)
		return
	}
	writeJSON(w, http.StatusOK, calendarSettingsResponse{
		Settings: settings,
		ICS:      calendarICS{URL: calendarFeedURL(r, token)},
	})
}

// handleCalendarSettings serves the GET of API-CAL-009 (REQ-CAL-011,
// REQ-CAL-013).
func (s *Server) handleCalendarSettings(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	settings, err := backend.Settings(r.Context())
	if err != nil {
		s.writeCalendarError(w, "calendar settings", err)
		return
	}
	s.writeCalendarSettings(w, r, backend, settings)
}

// handleCalendarPutSettings serves the PUT of API-CAL-009 (REQ-CAL-011). An
// ics object in the body is read-only and ignored.
func (s *Server) handleCalendarPutSettings(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	var body struct {
		Timezone  string                  `json:"timezone"`
		WeekStart int                     `json:"week_start"`
		Agenda    calendar.AgendaSettings `json:"agenda"`
		ICS       json.RawMessage         `json:"ics"`
	}
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	settings, err := backend.PutSettings(r.Context(), calendar.Settings{
		Timezone:  body.Timezone,
		WeekStart: body.WeekStart,
		Agenda:    body.Agenda,
	})
	if err != nil {
		s.writeCalendarError(w, "save calendar settings", err)
		return
	}
	s.writeCalendarSettings(w, r, backend, settings)
}

// handleCalendarRotateICS serves API-CAL-010 (REQ-CAL-013).
func (s *Server) handleCalendarRotateICS(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	token, err := backend.RotateICS(r.Context())
	if err != nil {
		s.writeCalendarError(w, "rotate calendar ICS token", err)
		return
	}
	writeJSON(w, http.StatusOK, calendarICS{URL: calendarFeedURL(r, token)})
}

// handleCalendarFeed serves API-CAL-011 (REQ-CAL-013, AC-CAL-023). It is
// registered without the auth wrapper: the token in the path is the only
// credential. A missing calendar port, a path without ".ics", and a wrong
// token all answer 404 with an empty body.
func (s *Server) handleCalendarFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	backend := s.calendarBackend()
	token, ok := strings.CutSuffix(r.PathValue("file"), ".ics")
	if backend == nil || !ok || !backend.CheckICSToken(r.Context(), token) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var feed bytes.Buffer
	if err := backend.WriteFeed(r.Context(), &feed); err != nil {
		s.logger.Error("web: calendar feed", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(feed.Bytes())
}

// handleCalendarStream serves API-CAL-015 (REQ-CAL-014). It follows the
// workflow stream loop: text/event-stream, a heartbeat comment, a shutdown
// event, and a return on client disconnect.
func (s *Server) handleCalendarStream(w http.ResponseWriter, r *http.Request) {
	backend := s.calendarOrError(w)
	if backend == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: calendar stream: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	changes, unsubscribe := backend.Subscribe()
	defer unsubscribe()

	heartbeat := time.NewTicker(s.heartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping():
			_ = writeSSE(w, flusher, "shutdown", "", struct{}{})
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case change, ok := <-changes:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, "changed", "", change); err != nil {
				return
			}
		}
	}
}

// calendarGoogleOff is the link state of API-CAL-012 when no Google port is
// installed.
type calendarGoogleOff struct {
	ClientConfigured bool   `json:"client_configured"`
	State            string `json:"state"`
	Account          string `json:"account"`
	Calendars        []any  `json:"calendars"`
	Target           string `json:"target"`
	LastSync         int64  `json:"last_sync"`
	Error            string `json:"error"`
}

// handleCalendarGoogle serves the GET of API-CAL-012 (REQ-CAL-012).
func (s *Server) handleCalendarGoogle(w http.ResponseWriter, r *http.Request) {
	if s.calendarOrError(w) == nil {
		return
	}
	google := s.calendarGoogle()
	if google == nil {
		writeJSON(w, http.StatusOK, calendarGoogleOff{State: "off", Calendars: []any{}})
		return
	}
	link, err := google.Link(r.Context())
	if err != nil {
		s.writeCalendarError(w, "google calendar link", err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleCalendarPutGoogle serves the PUT of API-CAL-012 (REQ-CAL-012).
func (s *Server) handleCalendarPutGoogle(w http.ResponseWriter, r *http.Request) {
	google := s.googleOrError(w)
	if google == nil {
		return
	}
	var body struct {
		Calendars []struct {
			ID       string `json:"id"`
			Selected bool   `json:"selected"`
		} `json:"calendars"`
		Target string `json:"target"`
	}
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	selected := make(map[string]bool, len(body.Calendars))
	for _, cal := range body.Calendars {
		selected[cal.ID] = cal.Selected
	}
	link, err := google.Put(r.Context(), selected, body.Target)
	if err != nil {
		s.writeCalendarError(w, "save google calendars", err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleCalendarGoogleClient serves PUT /v1/calendar/google/client of
// API-CAL-013 (REQ-CAL-012).
func (s *Server) handleCalendarGoogleClient(w http.ResponseWriter, r *http.Request) {
	google := s.googleOrError(w)
	if google == nil {
		return
	}
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if !decodeJSONObject(w, r, calendarBodyLimit, &body) {
		return
	}
	if err := google.SetClient(r.Context(), body.ClientID, body.ClientSecret); err != nil {
		s.writeCalendarError(w, "save google client", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// calendarCallbackURI is the OAuth redirect URI of API-CAL-013.
func calendarCallbackURI(r *http.Request) string {
	return "http://" + r.Host + "/v1/calendar/google/callback"
}

// handleCalendarGoogleConnect serves POST /v1/calendar/google/connect of
// API-CAL-013 (REQ-CAL-012). It issues a one-use state bound to the browser
// session.
func (s *Server) handleCalendarGoogleConnect(w http.ResponseWriter, r *http.Request) {
	google := s.googleOrError(w)
	if google == nil {
		return
	}
	sess, ok := sessionFromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	state, err := randomToken()
	if err != nil {
		s.writeCalendarError(w, "google oauth state", err)
		return
	}
	redirectURI := calendarCallbackURI(r)
	s.putCalendarOAuthState(state, calendarOAuthState{
		session:     sess.key,
		redirectURI: redirectURI,
		expiresAt:   s.nowTime().Add(calendarOAuthTTL),
	})
	authURL, err := google.AuthURL(r.Context(), redirectURI, state)
	if err != nil {
		s.takeCalendarOAuthState(state)
		s.writeCalendarError(w, "google auth url", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		AuthURL string `json:"auth_url"`
	}{AuthURL: authURL})
}

// handleCalendarGoogleCallback serves GET /v1/calendar/google/callback of
// API-CAL-013. Google redirects here cross-site, so the SameSite=Strict
// session cookie is absent. A known state gets a small page that refreshes to
// the same-site finish route, where the cookie is sent and checked.
func (s *Server) handleCalendarGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if s.calendarOrError(w) == nil {
		return
	}
	if s.calendarGoogle() == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Google Calendar is not available")
		return
	}
	query := r.URL.Query()
	if !s.peekCalendarOAuthState(query.Get("state")) {
		calendarRedirect(w, calendarGoogleError)
		return
	}
	next := url.Values{}
	for _, key := range []string{"code", "state", "error"} {
		if value := query.Get(key); value != "" {
			next.Set(key, value)
		}
	}
	target := html.EscapeString("/v1/calendar/google/callback/finish?" + next.Encode())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><meta http-equiv=\"refresh\" content=\"0;url=%s\"><title>lana</title><a href=\"%s\">Continue</a>\n", target, target)
}

// handleCalendarGoogleFinish completes API-CAL-013. It consumes the state,
// checks that the browser session that started the connect sent this request,
// exchanges the code, and redirects to the calendar page.
func (s *Server) handleCalendarGoogleFinish(w http.ResponseWriter, r *http.Request) {
	if s.calendarOrError(w) == nil {
		return
	}
	google := s.calendarGoogle()
	if google == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "Google Calendar is not available")
		return
	}
	query := r.URL.Query()
	state, ok := s.takeCalendarOAuthState(query.Get("state"))
	if !ok {
		calendarRedirect(w, calendarGoogleError)
		return
	}
	sess, ok := s.calendarRequestSession(r)
	if !ok || sess.key != state.session {
		calendarRedirect(w, calendarGoogleError)
		return
	}
	code := query.Get("code")
	if query.Get("error") != "" || code == "" {
		calendarRedirect(w, calendarGoogleError)
		return
	}
	if err := google.Exchange(r.Context(), code, state.redirectURI); err != nil {
		s.logger.Error("web: google calendar exchange", "error", err)
		calendarRedirect(w, calendarGoogleError)
		return
	}
	calendarRedirect(w, calendarGoogleLinked)
}

// calendarRequestSession reads the browser session of a request that is not
// behind requireSession.
func (s *Server) calendarRequestSession(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return session{}, false
	}
	sess, ok, err := s.sessions.touch(cookie.Value, s.nowTime())
	if err != nil {
		s.logger.Warn("web: save browser session", "error", err)
	}
	return sess, ok
}

// calendarRedirect answers 302 to a page of the web UI. The Location is set
// directly so the fragment is kept as is.
func calendarRedirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

// handleCalendarGoogleSync serves POST /v1/calendar/google/sync of
// API-CAL-014 (REQ-CAL-012).
func (s *Server) handleCalendarGoogleSync(w http.ResponseWriter, r *http.Request) {
	google := s.googleOrError(w)
	if google == nil {
		return
	}
	if err := google.Sync(r.Context()); err != nil {
		s.writeCalendarError(w, "google calendar sync", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleCalendarGoogleDisconnect serves POST /v1/calendar/google/disconnect
// of API-CAL-014 (REQ-CAL-012).
func (s *Server) handleCalendarGoogleDisconnect(w http.ResponseWriter, r *http.Request) {
	google := s.googleOrError(w)
	if google == nil {
		return
	}
	if err := google.Disconnect(r.Context()); err != nil {
		s.writeCalendarError(w, "google calendar disconnect", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putCalendarOAuthState stores a pending OAuth state. Expired states are
// dropped first, and the oldest one goes when the store is full.
func (s *Server) putCalendarOAuthState(key string, state calendarOAuthState) {
	now := s.nowTime()
	s.calendar.statesMu.Lock()
	defer s.calendar.statesMu.Unlock()
	if s.calendar.states == nil {
		s.calendar.states = map[string]calendarOAuthState{}
	}
	for k, v := range s.calendar.states {
		if !now.Before(v.expiresAt) {
			delete(s.calendar.states, k)
		}
	}
	if len(s.calendar.states) >= calendarOAuthMaxStates {
		oldest := ""
		for k, v := range s.calendar.states {
			if oldest == "" || v.expiresAt.Before(s.calendar.states[oldest].expiresAt) {
				oldest = k
			}
		}
		delete(s.calendar.states, oldest)
	}
	s.calendar.states[key] = state
}

// peekCalendarOAuthState reports whether key is a pending, unexpired state.
func (s *Server) peekCalendarOAuthState(key string) bool {
	if key == "" {
		return false
	}
	s.calendar.statesMu.Lock()
	defer s.calendar.statesMu.Unlock()
	state, ok := s.calendar.states[key]
	return ok && s.nowTime().Before(state.expiresAt)
}

// takeCalendarOAuthState removes key and returns it when it was unexpired.
func (s *Server) takeCalendarOAuthState(key string) (calendarOAuthState, bool) {
	if key == "" {
		return calendarOAuthState{}, false
	}
	s.calendar.statesMu.Lock()
	defer s.calendar.statesMu.Unlock()
	state, ok := s.calendar.states[key]
	if !ok {
		return calendarOAuthState{}, false
	}
	delete(s.calendar.states, key)
	if !s.nowTime().Before(state.expiresAt) {
		return calendarOAuthState{}, false
	}
	return state, true
}

// readJSONObject reads a body of at most maxBytes, checks it is one JSON
// object by decoding it into dst, and returns the raw bytes. It writes the
// error response and returns false on failure.
func readJSONObject(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) (json.RawMessage, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be at most 1 MiB")
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return nil, false
	}
	return raw, true
}

// writeCalendarError maps a calendar error to the contract error
// (api-contract.md, "Errors"). An error without a known code is a 500.
func (s *Server) writeCalendarError(w http.ResponseWriter, operation string, err error) {
	code := calendar.ErrorCode(err)
	message := strings.TrimPrefix(err.Error(), "calendar: ")
	switch code {
	case "not_found":
		writeAPIError(w, http.StatusNotFound, code, message)
	case "invalid_item":
		var invalid *calendar.InvalidItemError
		details := []map[string]any{}
		if errors.As(err, &invalid) {
			for _, field := range invalid.Fields {
				details = append(details, map[string]any{"field": field.Field, "message": field.Message})
			}
		}
		writeAPIErrorDetails(w, http.StatusBadRequest, code, "the item is not valid", details)
	case "invalid_range", "range_too_large", "not_repeating", "no_such_occurrence",
		"not_completable", "invalid_settings", "invalid_override",
		"invalid_target", "client_missing", "invalid_client":
		writeAPIError(w, http.StatusBadRequest, code, message)
	case "already_running", "already_done", "not_linked":
		writeAPIError(w, http.StatusConflict, code, message)
	default:
		s.logger.Error("web: "+operation, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

package calendar

// This file holds the Google Calendar link (spec #35, REQ-CAL-012,
// API-CAL-012 … API-CAL-014, design "Google sync"): OAuth with PKCE, access
// token refresh, the pull of events with sync tokens, the push of pending
// items with etags, last-writer-wins conflicts, and the sync loop.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Google endpoints (overridable with WithGoogleEndpoints).
const (
	DefaultGoogleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultGoogleTokenURL  = "https://oauth2.googleapis.com/token"
	DefaultGoogleRevokeURL = "https://oauth2.googleapis.com/revoke"
	DefaultGoogleAPIURL    = "https://www.googleapis.com/calendar/v3"
	// GoogleScope is the OAuth scope the link asks for (API-CAL-013).
	GoogleScope = "https://www.googleapis.com/auth/calendar"
)

// Secret names of the Google link (design "Data"). They live in the secret
// store, never in calendar_kv.
const (
	SecretGoogleClientID     = "calendar.google.client_id"
	SecretGoogleClientSecret = "calendar.google.client_secret"
	SecretGoogleRefreshToken = "calendar.google.refresh_token"
)

// KV keys written by the Google link. "google.state", "google.target" and
// "google.calendars" are declared in service.go.
const (
	kvGoogleAccount  = "google.account"
	kvGoogleLastSync = "google.last_sync"
	kvGoogleError    = "google.error"
	kvGoogleSyncPfx  = "google.sync."
)

// Google link states reported by Link that are not stored in KV.
const (
	GoogleSyncing = "syncing"
	GoogleError   = "error"
)

// ByGoogle is the author of changes made by the Google sync.
const ByGoogle = "google"

// Google sync tuning.
const (
	defaultGooglePull  = 5 * time.Minute
	defaultGooglePush  = time.Minute
	googleBack         = 30 * 24 * time.Hour
	googleAhead        = 365 * 24 * time.Hour
	googlePageSize     = 250
	googleReminderSpan = 30 * time.Minute
	googlePKCETTL      = 10 * time.Minute
	googlePKCEMax      = 64
	googleTokenSlack   = time.Minute
	googleBodyLimit    = 8 << 20
	googleLanaProp     = "lana_item"
	googleReauthText   = "Google access was revoked or expired. Connect again."
)

// googleBackoff is the wait before the next push attempt of an item after
// its n-th failed attempt (design: 1, 2, 5, 10 min; then 10 min).
var googleBackoff = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

// Secrets is the secret store the Google link keeps its OAuth client and
// refresh token in. The runtime adapts its secret store to it.
type Secrets interface {
	// Get returns one secret and whether it is set.
	Get(ctx context.Context, name string) (string, bool, error)
	// Set writes one secret.
	Set(ctx context.Context, name, value string) error
	// Delete removes one secret; a missing one is not an error.
	Delete(ctx context.Context, name string) error
}

// GoogleEndpoints are the OAuth and API base URLs. Empty fields keep the
// defaults.
type GoogleEndpoints struct {
	Auth, Token, Revoke, API string
}

// GoogleOption configures a Google link.
type GoogleOption func(*Google)

// WithGoogleHTTPClient sets the HTTP client (default: 30 s timeout).
func WithGoogleHTTPClient(c *http.Client) GoogleOption {
	return func(g *Google) {
		if c != nil {
			g.client = c
		}
	}
}

// WithGoogleClock replaces the clock used for tokens, sync windows and
// retries.
func WithGoogleClock(now func() time.Time) GoogleOption {
	return func(g *Google) {
		if now != nil {
			g.now = now
		}
	}
}

// WithGoogleEndpoints replaces the Google base URLs; tests point them at an
// httptest server.
func WithGoogleEndpoints(e GoogleEndpoints) GoogleOption {
	return func(g *Google) {
		if e.Auth != "" {
			g.ep.Auth = e.Auth
		}
		if e.Token != "" {
			g.ep.Token = e.Token
		}
		if e.Revoke != "" {
			g.ep.Revoke = e.Revoke
		}
		if e.API != "" {
			g.ep.API = strings.TrimRight(e.API, "/")
		}
	}
}

// WithGoogleIntervals sets the pull and push periods of the loop (default
// 5 min and 1 min).
func WithGoogleIntervals(pull, push time.Duration) GoogleOption {
	return func(g *Google) {
		if pull > 0 {
			g.pullEvery = pull
		}
		if push > 0 {
			g.pushEvery = push
		}
	}
}

// WithGoogleLogger sets the logger (default slog.Default).
func WithGoogleLogger(l *slog.Logger) GoogleOption {
	return func(g *Google) {
		if l != nil {
			g.logger = l
		}
	}
}

// GoogleLinkCalendar is one calendar of the link state (API-CAL-012).
type GoogleLinkCalendar struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Selected bool   `json:"selected"`
	Writable bool   `json:"writable"`
}

// GoogleLink is the link state object of API-CAL-012.
type GoogleLink struct {
	ClientConfigured bool                 `json:"client_configured"`
	State            string               `json:"state"`
	Account          string               `json:"account"`
	Calendars        []GoogleLinkCalendar `json:"calendars"`
	Target           string               `json:"target"`
	LastSync         int64                `json:"last_sync"`
	Error            string               `json:"error"`
}

// Google is the two-way Google Calendar link of one account (REQ-CAL-012).
// Its methods match web.CalendarGoogle. It is safe for concurrent use.
type Google struct {
	svc     *Service
	store   Store
	secrets Secrets
	client  *http.Client
	now     func() time.Time
	logger  *slog.Logger
	ep      GoogleEndpoints

	pullEvery, pushEvery time.Duration

	// syncMu serializes sync passes and every change of the link.
	syncMu  sync.Mutex
	syncing atomic.Bool

	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time

	pkceMu sync.Mutex
	pkce   map[string]googlePKCE

	retryMu sync.Mutex
	retry   map[string]googleRetry

	loopMu  sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	kickCh  chan struct{}
}

// googlePKCE is the PKCE verifier of one pending consent.
type googlePKCE struct {
	verifier    string
	redirectURI string
	created     time.Time
}

// googleRetry is the push backoff of one item, kept in memory only.
type googleRetry struct {
	failures  int
	next      time.Time
	updatedAt int64
}

// NewGoogle builds the Google link over svc and its store st.
func NewGoogle(svc *Service, st Store, secrets Secrets, opts ...GoogleOption) *Google {
	g := &Google{
		svc:       svc,
		store:     st,
		secrets:   secrets,
		client:    &http.Client{Timeout: 30 * time.Second},
		now:       time.Now,
		logger:    slog.Default(),
		ep:        GoogleEndpoints{Auth: DefaultGoogleAuthURL, Token: DefaultGoogleTokenURL, Revoke: DefaultGoogleRevokeURL, API: DefaultGoogleAPIURL},
		pullEvery: defaultGooglePull,
		pushEvery: defaultGooglePush,
		pkce:      map[string]googlePKCE{},
		retry:     map[string]googleRetry{},
		kickCh:    make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// ---- link state (API-CAL-012) --------------------------------------------------

// googleCalendarKV is one entry of KV "google.calendars". The service reads
// id, name and color from it.
type googleCalendarKV struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Selected bool   `json:"selected"`
	Writable bool   `json:"writable"`
}

func (g *Google) kv(ctx context.Context, key string) string {
	v, ok, err := g.store.GetKV(ctx, key)
	if err != nil {
		g.logger.Error("calendar: google kv", "key", key, "error", err)
		return ""
	}
	if !ok {
		return ""
	}
	return v
}

func (g *Google) setKV(ctx context.Context, key, value string) error {
	return g.store.SetKV(ctx, key, value)
}

func (g *Google) calendars(ctx context.Context) []googleCalendarKV {
	raw := g.kv(ctx, kvGoogleCalendars)
	if raw == "" {
		return nil
	}
	var cals []googleCalendarKV
	if err := json.Unmarshal([]byte(raw), &cals); err != nil {
		g.logger.Error("calendar: bad google calendars", "error", err)
		return nil
	}
	return cals
}

func (g *Google) saveCalendars(ctx context.Context, cals []googleCalendarKV) error {
	if cals == nil {
		cals = []googleCalendarKV{}
	}
	raw, err := json.Marshal(cals)
	if err != nil {
		return err
	}
	return g.setKV(ctx, kvGoogleCalendars, string(raw))
}

func (g *Google) secret(ctx context.Context, name string) string {
	if g.secrets == nil {
		return ""
	}
	v, ok, err := g.secrets.Get(ctx, name)
	if err != nil {
		g.logger.Error("calendar: google secret", "name", name, "error", err)
		return ""
	}
	if !ok {
		return ""
	}
	return v
}

func (g *Google) clientConfigured(ctx context.Context) bool {
	return g.secret(ctx, SecretGoogleClientID) != "" && g.secret(ctx, SecretGoogleClientSecret) != ""
}

// Link returns the link state (API-CAL-012). The stored state is off,
// linked or needs_reauth; a linked account reports syncing during a pass
// and error when the last pass failed.
func (g *Google) Link(ctx context.Context) (any, error) {
	return g.link(ctx), nil
}

func (g *Google) link(ctx context.Context) GoogleLink {
	state := g.svc.googleState(ctx)
	errText := g.kv(ctx, kvGoogleError)
	if state == GoogleLinked {
		switch {
		case g.syncing.Load():
			state = GoogleSyncing
		case errText != "":
			state = GoogleError
		}
	}
	out := GoogleLink{
		ClientConfigured: g.clientConfigured(ctx),
		State:            state,
		Calendars:        []GoogleLinkCalendar{},
		Error:            errText,
	}
	if state != GoogleOff {
		out.Account = g.kv(ctx, kvGoogleAccount)
		out.Target = g.kv(ctx, kvGoogleTarget)
		for _, c := range g.calendars(ctx) {
			out.Calendars = append(out.Calendars, GoogleLinkCalendar(c))
		}
	}
	if ms, err := strconv.ParseInt(g.kv(ctx, kvGoogleLastSync), 10, 64); err == nil {
		out.LastSync = ms
	}
	return out
}

// Put saves the calendar selection and the target calendar (API-CAL-012).
// Ids missing from selected keep their selection; an empty target keeps the
// current one. The target must be selected and writable (invalid_target).
// Cached events and the sync token of a calendar that is no longer selected
// are dropped.
func (g *Google) Put(ctx context.Context, selected map[string]bool, target string) (any, error) {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	if g.svc.googleState(ctx) == GoogleOff {
		return nil, ErrNotLinked
	}
	cals := g.calendars(ctx)
	target = strings.TrimSpace(target)
	if target == "" {
		target = g.kv(ctx, kvGoogleTarget)
	}
	var dropped []string
	targetOK := false
	for i := range cals {
		if sel, ok := selected[cals[i].ID]; ok {
			if cals[i].Selected && !sel {
				dropped = append(dropped, cals[i].ID)
			}
			cals[i].Selected = sel
		}
		if cals[i].ID == target && cals[i].Selected && cals[i].Writable {
			targetOK = true
		}
	}
	if !targetOK {
		return nil, ErrInvalidTarget
	}
	if err := g.saveCalendars(ctx, cals); err != nil {
		return nil, err
	}
	if err := g.setKV(ctx, kvGoogleTarget, target); err != nil {
		return nil, err
	}
	for _, id := range dropped {
		if err := g.dropCalendarCache(ctx, id); err != nil {
			return nil, err
		}
	}
	g.svc.broadcast(ChangeGoogle, "")
	g.kick()
	return g.link(ctx), nil
}

// dropCalendarCache removes the cached events and the sync token of one
// calendar.
func (g *Google) dropCalendarCache(ctx context.Context, calendarID string) error {
	evs, err := g.store.ListGoogleEvents(ctx)
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if calendarID != "" && ev.CalendarID != calendarID {
			continue
		}
		if err := g.store.DeleteGoogleEvent(ctx, ev.CalendarID, ev.EventID); err != nil {
			return err
		}
	}
	return g.setKV(ctx, kvGoogleSyncPfx+calendarID, "")
}

// SetClient stores the OAuth client in the secret store (API-CAL-013). A
// different client id on a linked account needs a new consent.
func (g *Google) SetClient(ctx context.Context, clientID, clientSecret string) error {
	clientID, clientSecret = strings.TrimSpace(clientID), strings.TrimSpace(clientSecret)
	if clientID == "" || clientSecret == "" {
		return ErrInvalidClient
	}
	if g.secrets == nil {
		return errors.New("calendar: no secret store")
	}
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	prev := g.secret(ctx, SecretGoogleClientID)
	if err := g.secrets.Set(ctx, SecretGoogleClientID, clientID); err != nil {
		return err
	}
	if err := g.secrets.Set(ctx, SecretGoogleClientSecret, clientSecret); err != nil {
		return err
	}
	g.dropAccessToken()
	if prev != "" && prev != clientID && g.svc.googleState(ctx) == GoogleLinked {
		if err := g.markReauth(ctx); err != nil {
			return err
		}
	}
	g.svc.broadcast(ChangeGoogle, "")
	return nil
}

// ---- OAuth (API-CAL-013) ----------------------------------------------------------

// AuthURL returns the Google consent URL for redirectURI and state, with
// offline access and a PKCE S256 challenge. The verifier is kept in memory
// for Exchange.
func (g *Google) AuthURL(ctx context.Context, redirectURI, state string) (string, error) {
	clientID := g.secret(ctx, SecretGoogleClientID)
	if clientID == "" || g.secret(ctx, SecretGoogleClientSecret) == "" {
		return "", ErrClientMissing
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	g.putPKCE(state, googlePKCE{verifier: verifier, redirectURI: redirectURI, created: g.now()})
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", GoogleScope)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(g.ep.Auth, "?") {
		sep = "&"
	}
	return g.ep.Auth + sep + q.Encode(), nil
}

func (g *Google) putPKCE(state string, p googlePKCE) {
	g.pkceMu.Lock()
	defer g.pkceMu.Unlock()
	now := g.now()
	for k, v := range g.pkce {
		if now.Sub(v.created) > googlePKCETTL {
			delete(g.pkce, k)
		}
	}
	for len(g.pkce) >= googlePKCEMax {
		oldest := ""
		for k, v := range g.pkce {
			if oldest == "" || v.created.Before(g.pkce[oldest].created) {
				oldest = k
			}
		}
		delete(g.pkce, oldest)
	}
	g.pkce[state] = p
}

// takePKCE returns and drops the newest live verifier issued for
// redirectURI. Exchange does not receive the OAuth state, so the newest
// consent for the redirect URI wins.
func (g *Google) takePKCE(redirectURI string) (string, bool) {
	g.pkceMu.Lock()
	defer g.pkceMu.Unlock()
	now := g.now()
	best := ""
	for k, v := range g.pkce {
		if now.Sub(v.created) > googlePKCETTL {
			delete(g.pkce, k)
			continue
		}
		if v.redirectURI != redirectURI {
			continue
		}
		if best == "" || v.created.After(g.pkce[best].created) {
			best = k
		}
	}
	if best == "" {
		return "", false
	}
	v := g.pkce[best].verifier
	delete(g.pkce, best)
	return v, true
}

// googleToken is a token endpoint response.
type googleToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

// Exchange trades the authorization code for tokens, stores the refresh
// token, reads the calendar list and links the account (API-CAL-013). The
// primary calendar is selected and becomes the target on a first link; a
// new consent for the same account keeps the selection. Another account
// replaces the old link's local Google data.
func (g *Google) Exchange(ctx context.Context, code, redirectURI string) error {
	clientID := g.secret(ctx, SecretGoogleClientID)
	clientSecret := g.secret(ctx, SecretGoogleClientSecret)
	if clientID == "" || clientSecret == "" {
		return ErrClientMissing
	}
	verifier, ok := g.takePKCE(redirectURI)
	if !ok {
		return errors.New("calendar: no pending Google consent for this redirect URI")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code_verifier", verifier)
	tok, err := g.tokenRequest(ctx, form)
	if err != nil {
		return err
	}
	if tok.RefreshToken == "" {
		return errors.New("calendar: Google returned no refresh token")
	}

	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	if err := g.secrets.Set(ctx, SecretGoogleRefreshToken, tok.RefreshToken); err != nil {
		return err
	}
	g.setAccessToken(tok)

	list, err := g.calendarList(ctx)
	if err != nil {
		return err
	}
	account := ""
	for _, c := range list {
		if c.Primary {
			account = c.ID
		}
	}
	prevAccount := g.kv(ctx, kvGoogleAccount)
	prev := map[string]googleCalendarKV{}
	if prevAccount != "" && prevAccount == account {
		for _, c := range g.calendars(ctx) {
			prev[c.ID] = c
		}
	} else if prevAccount != "" || g.svc.googleState(ctx) != GoogleOff {
		if err := g.clearLocal(ctx); err != nil {
			return err
		}
	}
	cals := make([]googleCalendarKV, 0, len(list))
	target := ""
	for _, c := range list {
		name := c.SummaryOverride
		if name == "" {
			name = c.Summary
		}
		kc := googleCalendarKV{
			ID:       c.ID,
			Name:     name,
			Color:    c.BackgroundColor,
			Writable: c.AccessRole == "owner" || c.AccessRole == "writer",
			Selected: c.Primary,
		}
		if p, ok := prev[c.ID]; ok {
			kc.Selected = p.Selected
		}
		if c.Primary {
			target = c.ID
		}
		cals = append(cals, kc)
	}
	if old := g.kv(ctx, kvGoogleTarget); len(prev) > 0 && old != "" {
		for _, c := range cals {
			if c.ID == old && c.Selected && c.Writable {
				target = old
			}
		}
	}
	if err := g.saveCalendars(ctx, cals); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{kvGoogleAccount, account},
		{kvGoogleTarget, target},
		{kvGoogleError, ""},
		{kvGoogleState, GoogleLinked},
	} {
		if err := g.setKV(ctx, kv[0], kv[1]); err != nil {
			return err
		}
	}
	g.svc.broadcast(ChangeGoogle, "")
	g.kick()
	return nil
}

// googleListCalendar is one calendarList entry.
type googleListCalendar struct {
	ID              string `json:"id"`
	Summary         string `json:"summary"`
	SummaryOverride string `json:"summaryOverride"`
	BackgroundColor string `json:"backgroundColor"`
	AccessRole      string `json:"accessRole"`
	Primary         bool   `json:"primary"`
}

func (g *Google) calendarList(ctx context.Context) ([]googleListCalendar, error) {
	var out []googleListCalendar
	page := ""
	for {
		q := url.Values{}
		q.Set("maxResults", "250")
		if page != "" {
			q.Set("pageToken", page)
		}
		var resp struct {
			Items         []googleListCalendar `json:"items"`
			NextPageToken string               `json:"nextPageToken"`
		}
		if err := g.apiJSON(ctx, http.MethodGet, "/users/me/calendarList?"+q.Encode(), nil, "", &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if resp.NextPageToken == "" {
			return out, nil
		}
		page = resp.NextPageToken
	}
}

// Disconnect revokes the token, deletes it, and unlinks the account
// (API-CAL-014): cached Google events and sync tokens are dropped and items
// lose their Google link but stay. A failing revoke is logged only.
func (g *Google) Disconnect(ctx context.Context) error {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	if tok := g.secret(ctx, SecretGoogleRefreshToken); tok != "" {
		form := url.Values{}
		form.Set("token", tok)
		if err := g.postForm(ctx, g.ep.Revoke, form, nil); err != nil {
			g.logger.Warn("calendar: google revoke", "error", err)
		}
	}
	if g.secrets != nil {
		if err := g.secrets.Delete(ctx, SecretGoogleRefreshToken); err != nil {
			return err
		}
	}
	g.dropAccessToken()
	if err := g.clearLocal(ctx); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{kvGoogleState, GoogleOff},
		{kvGoogleAccount, ""},
		{kvGoogleTarget, ""},
		{kvGoogleCalendars, "[]"},
		{kvGoogleError, ""},
		{kvGoogleLastSync, ""},
	} {
		if err := g.setKV(ctx, kv[0], kv[1]); err != nil {
			return err
		}
	}
	g.svc.broadcast(ChangeGoogle, "")
	return nil
}

// clearLocal drops the local Google data of a link: cached events, sync
// tokens, the Google refs of live items and the deleted items still waiting
// for a Google delete. Items themselves stay.
func (g *Google) clearLocal(ctx context.Context) error {
	for _, c := range g.calendars(ctx) {
		if err := g.setKV(ctx, kvGoogleSyncPfx+c.ID, ""); err != nil {
			return err
		}
	}
	if err := g.dropCalendarCache(ctx, ""); err != nil {
		return err
	}
	items, err := g.store.ListItems(ctx)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Google == nil {
			continue
		}
		it.Google = nil
		if err := g.store.UpdateItem(ctx, it); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		g.svc.broadcast(ChangeGoogle, it.ID)
	}
	deleted, err := g.store.ListDeletedItems(ctx)
	if err != nil {
		return err
	}
	for _, it := range deleted {
		if err := g.store.PurgeItem(ctx, it.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	g.retryMu.Lock()
	g.retry = map[string]googleRetry{}
	g.retryMu.Unlock()
	return nil
}

// ---- tokens --------------------------------------------------------------------------

// googleHTTPError is a non-2xx Google response.
type googleHTTPError struct {
	Status int
	Body   string
}

func (e *googleHTTPError) Error() string {
	body := e.Body
	if len(body) > 300 {
		body = body[:300]
	}
	return fmt.Sprintf("google: HTTP %d: %s", e.Status, strings.TrimSpace(body))
}

func httpStatus(err error) int {
	var he *googleHTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func (g *Google) dropAccessToken() {
	g.tokenMu.Lock()
	g.accessToken, g.tokenExpiry = "", time.Time{}
	g.tokenMu.Unlock()
}

func (g *Google) setAccessToken(tok googleToken) {
	g.tokenMu.Lock()
	defer g.tokenMu.Unlock()
	g.accessToken = tok.AccessToken
	exp := tok.ExpiresIn
	if exp <= 0 {
		exp = 3600
	}
	g.tokenExpiry = g.now().Add(time.Duration(exp) * time.Second)
}

// token returns a cached access token, refreshing it with the refresh token
// when it expires within a minute. A refused refresh returns ErrGoogleReauth.
func (g *Google) token(ctx context.Context) (string, error) {
	g.tokenMu.Lock()
	if g.accessToken != "" && g.now().Add(googleTokenSlack).Before(g.tokenExpiry) {
		tok := g.accessToken
		g.tokenMu.Unlock()
		return tok, nil
	}
	g.tokenMu.Unlock()
	refresh := g.secret(ctx, SecretGoogleRefreshToken)
	clientID := g.secret(ctx, SecretGoogleClientID)
	clientSecret := g.secret(ctx, SecretGoogleClientSecret)
	if refresh == "" || clientID == "" || clientSecret == "" {
		return "", ErrGoogleReauth
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	tok, err := g.tokenRequest(ctx, form)
	if err != nil {
		return "", err
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if err := g.secrets.Set(ctx, SecretGoogleRefreshToken, tok.RefreshToken); err != nil {
			g.logger.Warn("calendar: save rotated google refresh token", "error", err)
		}
	}
	g.setAccessToken(tok)
	return tok.AccessToken, nil
}

// tokenRequest posts to the token endpoint. invalid_grant, invalid_client,
// unauthorized_client and HTTP 401 return ErrGoogleReauth.
func (g *Google) tokenRequest(ctx context.Context, form url.Values) (googleToken, error) {
	var tok googleToken
	err := g.postForm(ctx, g.ep.Token, form, &tok)
	if err != nil {
		var he *googleHTTPError
		if errors.As(err, &he) {
			var body googleToken
			_ = json.Unmarshal([]byte(he.Body), &body)
			switch {
			case body.Error == "invalid_grant", body.Error == "invalid_client", body.Error == "unauthorized_client", he.Status == http.StatusUnauthorized:
				return googleToken{}, fmt.Errorf("%w (%s)", ErrGoogleReauth, he.Error())
			}
		}
		return googleToken{}, err
	}
	if tok.AccessToken == "" {
		return googleToken{}, errors.New("calendar: Google token response has no access_token")
	}
	return tok, nil
}

func (g *Google) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, googleBodyLimit))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &googleHTTPError{Status: resp.StatusCode, Body: string(body)}
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// api sends one Calendar API request with a bearer token and returns the
// body of a 2xx response. A 401 refreshes the token once; a second 401
// returns ErrGoogleReauth. Other non-2xx responses return *googleHTTPError.
func (g *Google) api(ctx context.Context, method, path string, body any, ifMatch string) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		tok, err := g.token(ctx)
		if err != nil {
			return nil, err
		}
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, g.ep.API+path, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		resp, err := g.client.Do(req)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, googleBodyLimit))
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			g.dropAccessToken()
			if attempt == 0 {
				continue
			}
			return nil, fmt.Errorf("%w (%s)", ErrGoogleReauth, (&googleHTTPError{Status: resp.StatusCode, Body: string(data)}).Error())
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, &googleHTTPError{Status: resp.StatusCode, Body: string(data)}
		}
		return data, nil
	}
}

func (g *Google) apiJSON(ctx context.Context, method, path string, body any, ifMatch string, out any) error {
	data, err := g.api(ctx, method, path, body, ifMatch)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func eventsPath(calendarID string) string {
	return "/calendars/" + url.PathEscape(calendarID) + "/events"
}

func eventPath(calendarID, eventID string) string {
	return eventsPath(calendarID) + "/" + url.PathEscape(eventID)
}

// ---- loop -------------------------------------------------------------------------------

// Start starts the sync loop: a pull every 5 minutes, a push every minute,
// both right away and after a kick. Passes are no-ops while no account is
// linked. A second Start is a no-op.
func (g *Google) Start(ctx context.Context) {
	g.loopMu.Lock()
	defer g.loopMu.Unlock()
	if g.started || g.closed {
		return
	}
	g.started = true
	ctx, g.cancel = context.WithCancel(ctx)
	g.wg.Add(1)
	go g.loop(ctx)
}

// Close stops the loop and waits for a running pass.
func (g *Google) Close() {
	g.loopMu.Lock()
	g.closed = true
	cancel := g.cancel
	g.loopMu.Unlock()
	if cancel != nil {
		cancel()
	}
	g.wg.Wait()
}

// kick asks the loop for a pull and push soon.
func (g *Google) kick() {
	select {
	case g.kickCh <- struct{}{}:
	default:
	}
}

func (g *Google) loop(ctx context.Context) {
	defer g.wg.Done()
	pull := time.NewTicker(g.pullEvery)
	defer pull.Stop()
	push := time.NewTicker(g.pushEvery)
	defer push.Stop()
	g.loopPass(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-pull.C:
			g.loopPass(ctx, true)
		case <-push.C:
			g.loopPass(ctx, false)
		case <-g.kickCh:
			g.loopPass(ctx, true)
		}
	}
}

func (g *Google) loopPass(ctx context.Context, pull bool) {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	if g.svc.googleState(ctx) != GoogleLinked {
		return
	}
	g.pass(ctx, pull)
}

// Sync pulls then pushes now (API-CAL-014). It also retries a link that
// needs a new consent. Failures are recorded in the link state, not
// returned; an unlinked account returns ErrNotLinked.
func (g *Google) Sync(ctx context.Context) error {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	if g.svc.googleState(ctx) == GoogleOff || g.secret(ctx, SecretGoogleRefreshToken) == "" {
		return ErrNotLinked
	}
	g.pass(ctx, true)
	return nil
}

// pass runs one pull (when pull is set) and one push and records the
// outcome. Callers hold syncMu.
func (g *Google) pass(ctx context.Context, pull bool) {
	g.syncing.Store(true)
	defer g.syncing.Store(false)
	var errs []error
	if pull {
		if err := g.pull(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if !g.reauthIn(errs) {
		if err := g.push(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if ctx.Err() != nil {
		return
	}
	if g.reauthIn(errs) {
		if err := g.markReauth(ctx); err != nil {
			g.logger.Error("calendar: google needs_reauth", "error", err)
		}
		return
	}
	prevErr := g.kv(ctx, kvGoogleError)
	prevState := g.svc.googleState(ctx)
	errText := ""
	if err := errors.Join(errs...); err != nil {
		errText = err.Error()
		g.logger.Warn("calendar: google sync", "error", err)
	}
	if pull && len(errs) == 0 {
		_ = g.setKV(ctx, kvGoogleLastSync, fmt.Sprint(g.now().UnixMilli()))
	}
	_ = g.setKV(ctx, kvGoogleError, errText)
	if prevState != GoogleLinked {
		_ = g.setKV(ctx, kvGoogleState, GoogleLinked)
	}
	if prevErr != errText || prevState != GoogleLinked {
		g.svc.broadcast(ChangeGoogle, "")
	}
}

func (g *Google) reauthIn(errs []error) bool {
	for _, err := range errs {
		if errors.Is(err, ErrGoogleReauth) {
			return true
		}
	}
	return false
}

// markReauth sets the link to needs_reauth (AC-CAL-022). Local data stays.
func (g *Google) markReauth(ctx context.Context) error {
	g.dropAccessToken()
	if err := g.setKV(ctx, kvGoogleState, GoogleNeedsReauth); err != nil {
		return err
	}
	if err := g.setKV(ctx, kvGoogleError, googleReauthText); err != nil {
		return err
	}
	g.svc.broadcast(ChangeGoogle, "")
	return nil
}

// ---- Google event resource ------------------------------------------------------------

type googleTime struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

// googleEvent is the part of a Google event resource the sync reads.
type googleEvent struct {
	ID                 string      `json:"id"`
	Status             string      `json:"status"`
	ETag               string      `json:"etag"`
	Updated            string      `json:"updated"`
	Summary            string      `json:"summary"`
	Description        string      `json:"description"`
	Start              googleTime  `json:"start"`
	End                googleTime  `json:"end"`
	RecurringEventID   string      `json:"recurringEventId"`
	OriginalStartTime  *googleTime `json:"originalStartTime"`
	Recurrence         []string    `json:"recurrence"`
	ExtendedProperties *struct {
		Private map[string]string `json:"private"`
	} `json:"extendedProperties"`
}

func (e googleEvent) lanaItem() string {
	if e.ExtendedProperties == nil {
		return ""
	}
	return e.ExtendedProperties.Private[googleLanaProp]
}

func (e googleEvent) updatedMs() int64 {
	t, err := time.Parse(time.RFC3339Nano, e.Updated)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func (e googleEvent) cancelled() bool { return e.Status == "cancelled" }

// ---- pull --------------------------------------------------------------------------------

// pullIndex maps Google event ids and item ids to the live items.
type pullIndex struct {
	byID    map[string]Item
	byEvent map[string]Item
	// changed is set when the cache of plain Google events changed.
	changed bool
}

func (g *Google) index(ctx context.Context) (pullIndex, error) {
	items, err := g.store.ListItems(ctx)
	if err != nil {
		return pullIndex{}, err
	}
	idx := pullIndex{byID: map[string]Item{}, byEvent: map[string]Item{}}
	for _, it := range items {
		idx.byID[it.ID] = it
		if it.Google != nil && it.Google.EventID != "" {
			idx.byEvent[it.Google.EventID] = it
		}
	}
	return idx, nil
}

// pull reads the changes of every selected calendar (AC-CAL-020).
func (g *Google) pull(ctx context.Context) error {
	var errs []error
	for _, c := range g.calendars(ctx) {
		if !c.Selected {
			continue
		}
		if err := g.pullCalendar(ctx, c.ID); err != nil {
			if errors.Is(err, ErrGoogleReauth) {
				return err
			}
			errs = append(errs, fmt.Errorf("pull %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

// errSyncTokenGone reports a 410 on an incremental list.
var errSyncTokenGone = errors.New("calendar: google sync token expired")

// pullCalendar lists one calendar's events: with the stored sync token, or
// the full window (-30 d … +365 d) on the first run and after a 410. A full
// list replaces the calendar's cache.
func (g *Google) pullCalendar(ctx context.Context, calendarID string) error {
	token := g.kv(ctx, kvGoogleSyncPfx+calendarID)
	err := g.listCalendar(ctx, calendarID, token)
	if errors.Is(err, errSyncTokenGone) {
		if err := g.setKV(ctx, kvGoogleSyncPfx+calendarID, ""); err != nil {
			return err
		}
		err = g.listCalendar(ctx, calendarID, "")
	}
	return err
}

// listCalendar runs one events.list over every page and stores the next
// sync token. syncToken "" lists the full window.
func (g *Google) listCalendar(ctx context.Context, calendarID, syncToken string) error {
	loc := g.svc.location(ctx)
	full := syncToken == ""
	idx, err := g.index(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	page, next := "", ""
	for {
		q := url.Values{}
		q.Set("singleEvents", "true")
		q.Set("maxResults", strconv.Itoa(googlePageSize))
		if full {
			now := g.now()
			q.Set("timeMin", now.Add(-googleBack).UTC().Format(time.RFC3339))
			q.Set("timeMax", now.Add(googleAhead).UTC().Format(time.RFC3339))
		} else {
			q.Set("syncToken", syncToken)
		}
		if page != "" {
			q.Set("pageToken", page)
		}
		var resp struct {
			Items         []json.RawMessage `json:"items"`
			NextPageToken string            `json:"nextPageToken"`
			NextSyncToken string            `json:"nextSyncToken"`
		}
		err := g.apiJSON(ctx, http.MethodGet, eventsPath(calendarID)+"?"+q.Encode(), nil, "", &resp)
		if !full && httpStatus(err) == http.StatusGone {
			return errSyncTokenGone
		}
		if err != nil {
			return err
		}
		for _, raw := range resp.Items {
			var ev googleEvent
			if err := json.Unmarshal(raw, &ev); err != nil || ev.ID == "" {
				continue
			}
			seen[ev.ID] = true
			if err := g.applyPulled(ctx, calendarID, ev, string(raw), &idx, loc); err != nil {
				return err
			}
		}
		if resp.NextPageToken == "" {
			next = resp.NextSyncToken
			break
		}
		page = resp.NextPageToken
	}
	if full {
		cached, err := g.store.ListGoogleEvents(ctx)
		if err != nil {
			return err
		}
		for _, ev := range cached {
			if ev.CalendarID == calendarID && !seen[ev.EventID] {
				if err := g.store.DeleteGoogleEvent(ctx, ev.CalendarID, ev.EventID); err != nil {
					return err
				}
				idx.changed = true
			}
		}
	}
	if idx.changed {
		g.svc.broadcast(ChangeGoogle, "")
	}
	return g.setKV(ctx, kvGoogleSyncPfx+calendarID, next)
}

// applyPulled applies one listed event. Events of lana items (by the
// lana_item property or the linked event id) update or delete the item;
// instances of a lana repeating event are ignored except a cancelled one,
// which skips that occurrence. Other events are cached.
func (g *Google) applyPulled(ctx context.Context, calendarID string, ev googleEvent, raw string, idx *pullIndex, loc *time.Location) error {
	lanaID := ev.lanaItem()
	it, found := idx.byID[lanaID]
	if !found && lanaID == "" {
		it, found = idx.byEvent[ev.ID]
	}
	if ev.RecurringEventID != "" {
		master, ok := it, found
		if !ok {
			master, ok = idx.byEvent[ev.RecurringEventID]
		}
		if ok || lanaID != "" {
			if err := g.store.DeleteGoogleEvent(ctx, calendarID, ev.ID); err != nil {
				return err
			}
			if ok && ev.cancelled() {
				return g.skipInstance(ctx, master, ev, loc)
			}
			return nil
		}
	}
	if lanaID != "" && !found {
		// An event of a lana item this calendar no longer has.
		return g.store.DeleteGoogleEvent(ctx, calendarID, ev.ID)
	}
	if found {
		if err := g.store.DeleteGoogleEvent(ctx, calendarID, ev.ID); err != nil {
			return err
		}
		if it.Google != nil && it.Google.EventID != "" && it.Google.EventID != ev.ID {
			return nil // a stray copy; the item has its own event
		}
		if ev.cancelled() {
			return g.googleDeleted(ctx, it, calendarID, ev)
		}
		return g.googleChanged(ctx, it, calendarID, ev, raw, loc)
	}
	if ev.cancelled() {
		if err := g.store.DeleteGoogleEvent(ctx, calendarID, ev.ID); err != nil {
			return err
		}
	} else {
		err := g.store.PutGoogleEvent(ctx, GoogleEvent{
			CalendarID: calendarID,
			EventID:    ev.ID,
			ETag:       ev.ETag,
			JSON:       raw,
			UpdatedMs:  ev.updatedMs(),
		})
		if err != nil {
			return err
		}
	}
	idx.changed = true
	return nil
}

// skipInstance saves a skip override for a cancelled instance of a lana
// repeating item.
func (g *Google) skipInstance(ctx context.Context, it Item, ev googleEvent, loc *time.Location) error {
	if ev.OriginalStartTime == nil {
		return nil
	}
	occ, ok := googleInstant(*ev.OriginalStartTime, loc)
	if !ok || !it.Repeat.Repeats() || !IsOccurrence(it, loc, occ) {
		return nil
	}
	overrides, err := g.store.ListOverrides(ctx)
	if err != nil {
		return err
	}
	for _, o := range overrides {
		if o.Source == OverrideItem && o.Ref == it.ID && o.Occurrence == occ.UnixMilli() && o.Op == OpSkip {
			return nil
		}
	}
	if err := g.store.PutOverride(ctx, Override{
		Source: OverrideItem, Ref: it.ID, Occurrence: occ.UnixMilli(), Op: OpSkip,
		By: ByGoogle, At: g.svc.now().UnixMilli(),
	}); err != nil {
		return err
	}
	g.svc.history(ctx, it.ID, ByGoogle, "Skipped the "+fmtWhen(occ, loc)+" occurrence in Google")
	g.svc.broadcast(ChangeGoogle, it.ID)
	return nil
}

// googleInstant is the start instant of a Google time: the dateTime, or
// local midnight of the date.
func googleInstant(t googleTime, loc *time.Location) (time.Time, bool) {
	if t.DateTime != "" {
		v, err := time.Parse(time.RFC3339, t.DateTime)
		return v, err == nil
	}
	d, ok := parseDay(t.Date)
	if !ok {
		return time.Time{}, false
	}
	return d.midnight(loc), true
}

// lanaChanged reports whether the item has a lana change not yet in Google.
func lanaChanged(it Item) bool {
	return it.Google != nil && (it.Google.Sync == SyncPending || it.Google.Sync == SyncError)
}

// googleChanged applies a changed Google event to its item (REQ-CAL-012).
// An event with the item's etag is the item's own last write. When the item
// also changed in lana, the later update wins and the loser is kept in the
// item history; otherwise the Google side is applied.
func (g *Google) googleChanged(ctx context.Context, it Item, calendarID string, ev googleEvent, raw string, loc *time.Location) error {
	if it.Google != nil && ev.ETag != "" && ev.ETag == it.Google.ETag {
		return nil
	}
	if lanaChanged(it) && ev.updatedMs() <= it.UpdatedAt {
		// lana wins: keep the Google side in the history and push over it
		// with the new etag.
		if err := g.snapshot(ctx, it.ID, "Google conflict: kept the lana version", raw); err != nil {
			return err
		}
		_, err := g.writeRef(ctx, it, func(cur *Item) {
			cur.Google.CalendarID = calendarID
			cur.Google.EventID = ev.ID
			cur.Google.ETag = ev.ETag
		})
		return err
	}
	return g.applyGoogle(ctx, it, calendarID, ev, lanaChanged(it), loc)
}

// applyGoogle writes the Google side of ev over the item, with a snapshot of
// the replaced item in the history. It does nothing when the item changed
// since it was read.
func (g *Google) applyGoogle(ctx context.Context, base Item, calendarID string, ev googleEvent, conflict bool, loc *time.Location) error {
	cur, err := g.store.GetItem(ctx, base.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.UpdatedAt != base.UpdatedAt {
		return nil
	}
	ref := &GoogleRef{CalendarID: calendarID, EventID: ev.ID, ETag: ev.ETag, Sync: SyncSynced}
	next, ok := itemFromEvent(cur, ev, loc)
	if !ok {
		g.logger.Warn("calendar: google event does not map to the item", "item", cur.ID, "event", ev.ID)
		cur.Google = ref
		return g.store.UpdateItem(ctx, cur)
	}
	changed := changedFields(Normalize(cur), next)
	next.Google = ref
	if len(changed) == 0 {
		if err := g.store.UpdateItem(ctx, next); err != nil {
			return err
		}
		g.svc.broadcast(ChangeGoogle, cur.ID)
		return nil
	}
	next.UpdatedBy = ByGoogle
	next.UpdatedAt = g.svc.now().UnixMilli()
	if err := g.store.UpdateItem(ctx, next); err != nil {
		return err
	}
	for _, f := range changed {
		if scheduleFields[f] {
			g.svc.dropStaleOverrides(ctx, next)
			break
		}
	}
	summary := "Updated in Google: " + strings.Join(changed, ", ")
	if conflict {
		summary = "Google conflict: kept the Google version (" + strings.Join(changed, ", ") + ")"
	}
	old, _ := json.Marshal(cur)
	if err := g.snapshot(ctx, cur.ID, summary, string(old)); err != nil {
		return err
	}
	g.svc.broadcast(ChangeGoogle, cur.ID)
	return nil
}

func (g *Google) snapshot(ctx context.Context, itemID, summary, snapshot string) error {
	return g.store.AppendHistory(ctx, HistoryLine{
		ItemID: itemID, At: g.svc.now().UnixMilli(), By: ByGoogle, Summary: summary, Snapshot: snapshot,
	})
}

// googleDeleted handles a cancelled event of an item: the item is deleted,
// unless it changed in lana after the Google delete, then it is written to
// Google again as a new event.
func (g *Google) googleDeleted(ctx context.Context, it Item, calendarID string, ev googleEvent) error {
	if it.Google == nil {
		return nil // not linked: a Google delete never removes a local item
	}
	if lanaChanged(it) && it.UpdatedAt > ev.updatedMs() {
		ok, err := g.writeRef(ctx, it, func(cur *Item) {
			cur.Google.CalendarID = calendarID
			cur.Google.EventID = ""
			cur.Google.ETag = ""
		})
		if err != nil || !ok {
			return err
		}
		g.svc.history(ctx, it.ID, ByGoogle, "Deleted in Google; kept the lana version")
		return nil
	}
	cur, err := g.store.GetItem(ctx, it.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.UpdatedAt != it.UpdatedAt {
		return nil
	}
	cur.Google = nil
	cur.UpdatedBy = ByGoogle
	now := g.svc.now().UnixMilli()
	cur.UpdatedAt = now
	if err := g.store.UpdateItem(ctx, cur); err != nil {
		return err
	}
	if err := g.store.DeleteItem(ctx, cur.ID, now); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	g.svc.history(ctx, cur.ID, ByGoogle, "Deleted in Google")
	g.svc.broadcast(ChangeGoogle, cur.ID)
	return nil
}

// writeRef changes the Google ref of an item that did not change since base
// was read; it reports whether it wrote.
func (g *Google) writeRef(ctx context.Context, base Item, mutate func(*Item)) (bool, error) {
	cur, err := g.store.GetItem(ctx, base.ID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if cur.UpdatedAt != base.UpdatedAt || cur.Google == nil {
		return false, nil
	}
	mutate(&cur)
	if err := g.store.UpdateItem(ctx, cur); err != nil {
		return false, err
	}
	g.svc.broadcast(ChangeGoogle, cur.ID)
	return true, nil
}

// itemFromEvent maps the Google side of an event onto a copy of it: title,
// notes, time and, when the event carries one, a supported repeat rule.
// It reports false when the result is not a valid item.
func itemFromEvent(it Item, ev googleEvent, loc *time.Location) (Item, bool) {
	next := cloneItem(it)
	next.Title = strings.TrimSpace(ev.Summary)
	if next.Title == "" {
		next.Title = "(no title)"
	}
	next.Notes = ev.Description
	start, ok := googleInstant(ev.Start, loc)
	if !ok {
		return Item{}, false
	}
	switch {
	case it.Kind == KindReminder:
		next.Start = start.UnixMilli()
	case ev.Start.Date != "":
		first, _ := parseDay(ev.Start.Date)
		last := first
		if end, ok := parseDay(ev.End.Date); ok && first.before(end) {
			last = end.add(-1)
		}
		next.AllDay = true
		next.Date, next.EndDate = first.String(), last.String()
	default:
		end, ok := googleInstant(ev.End, loc)
		if !ok || !end.After(start) {
			end = start.Add(googleReminderSpan)
		}
		next.AllDay = false
		next.Start, next.End = start.UnixMilli(), end.UnixMilli()
	}
	if len(ev.Recurrence) == 0 {
		next.Repeat = Repeat{Freq: FreqNone}
	} else if r, ok := parseRRULE(ev.Recurrence, start, loc); ok {
		next.Repeat = r
	}
	next = Normalize(next)
	if errs := Validate(next, nil, nil); len(errs) > 0 {
		return Item{}, false
	}
	return next, true
}

// ---- push --------------------------------------------------------------------------------

// push writes the pending items to Google and deletes the events of deleted
// items (AC-CAL-021). A failing item is marked error and retried with
// backoff.
func (g *Google) push(ctx context.Context) error {
	loc := g.svc.location(ctx)
	target := g.kv(ctx, kvGoogleTarget)
	items, err := g.store.ListItems(ctx)
	if err != nil {
		return err
	}
	var errs []error
	failed := 0
	for _, it := range items {
		if it.Google == nil || (it.Kind != KindEvent && it.Kind != KindReminder) || !lanaChanged(it) {
			continue
		}
		if !g.due(it) {
			continue
		}
		if err := g.pushItem(ctx, it, target, loc); err != nil {
			if errors.Is(err, ErrGoogleReauth) || ctx.Err() != nil {
				return err
			}
			failed++
			errs = append(errs, err)
			g.fail(ctx, it, err)
			continue
		}
		g.clearRetry(it.ID)
	}
	deleted, err := g.store.ListDeletedItems(ctx)
	if err != nil {
		return err
	}
	for _, it := range deleted {
		if err := g.pushDelete(ctx, it); err != nil {
			if errors.Is(err, ErrGoogleReauth) || ctx.Err() != nil {
				return err
			}
			failed++
			errs = append(errs, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d item(s) not synced: %w", failed, errors.Join(errs...))
	}
	return nil
}

// due reports whether an item may be pushed now: always, unless it failed
// before and neither its backoff passed nor the item changed since.
func (g *Google) due(it Item) bool {
	g.retryMu.Lock()
	defer g.retryMu.Unlock()
	r, ok := g.retry[it.ID]
	if !ok || r.updatedAt != it.UpdatedAt {
		return true
	}
	return !g.now().Before(r.next)
}

func (g *Google) clearRetry(id string) {
	g.retryMu.Lock()
	delete(g.retry, id)
	g.retryMu.Unlock()
}

// fail records a failed push: the next attempt waits 1, 2, 5, then 10
// minutes, and the item shows sync "error".
func (g *Google) fail(ctx context.Context, it Item, err error) {
	g.logger.Warn("calendar: google push", "item", it.ID, "error", err)
	g.retryMu.Lock()
	r := g.retry[it.ID]
	if r.updatedAt != it.UpdatedAt {
		r = googleRetry{updatedAt: it.UpdatedAt}
	}
	wait := googleBackoff[len(googleBackoff)-1]
	if r.failures < len(googleBackoff) {
		wait = googleBackoff[r.failures]
	}
	r.failures++
	r.next = g.now().Add(wait)
	g.retry[it.ID] = r
	g.retryMu.Unlock()
	if it.Google.Sync == SyncError {
		return
	}
	if _, werr := g.writeRef(ctx, it, func(cur *Item) { cur.Google.Sync = SyncError }); werr != nil {
		g.logger.Error("calendar: mark google error", "item", it.ID, "error", werr)
	}
}

// pushItem inserts or patches the event of one item.
func (g *Google) pushItem(ctx context.Context, it Item, target string, loc *time.Location) error {
	calendarID := it.Google.CalendarID
	if calendarID == "" {
		calendarID = target
	}
	if calendarID == "" {
		return nil // no target calendar yet; stays pending
	}
	body := eventBody(it, loc)
	if it.Google.EventID == "" {
		return g.insert(ctx, it, calendarID, body)
	}
	var ev googleEvent
	data, err := g.api(ctx, http.MethodPatch, eventPath(calendarID, it.Google.EventID), body, it.Google.ETag)
	switch httpStatus(err) {
	case 0:
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		return g.recordPush(ctx, it, calendarID, ev, false)
	case http.StatusPreconditionFailed:
		return g.conflict(ctx, it, calendarID, body, loc)
	case http.StatusNotFound, http.StatusGone:
		return g.insert(ctx, it, calendarID, body)
	}
	return err
}

func (g *Google) insert(ctx context.Context, it Item, calendarID string, body map[string]any) error {
	var ev googleEvent
	if err := g.apiJSON(ctx, http.MethodPost, eventsPath(calendarID), body, "", &ev); err != nil {
		return err
	}
	return g.recordPush(ctx, it, calendarID, ev, true)
}

// conflict resolves a 412 on patch: the event is read again and the later
// update wins (REQ-CAL-012); the loser goes to the item history.
func (g *Google) conflict(ctx context.Context, it Item, calendarID string, body map[string]any, loc *time.Location) error {
	data, err := g.api(ctx, http.MethodGet, eventPath(calendarID, it.Google.EventID), nil, "")
	if s := httpStatus(err); s == http.StatusNotFound || s == http.StatusGone {
		return g.insert(ctx, it, calendarID, body)
	}
	if err != nil {
		return err
	}
	var ev googleEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}
	if ev.cancelled() {
		return g.googleDeleted(ctx, it, calendarID, ev)
	}
	if ev.updatedMs() > it.UpdatedAt {
		return g.applyGoogle(ctx, it, calendarID, ev, true, loc)
	}
	if err := g.snapshot(ctx, it.ID, "Google conflict: kept the lana version", string(data)); err != nil {
		return err
	}
	var out googleEvent
	if err := g.apiJSON(ctx, http.MethodPatch, eventPath(calendarID, ev.ID), body, ev.ETag, &out); err != nil {
		return err
	}
	return g.recordPush(ctx, it, calendarID, out, false)
}

// recordPush stores the event id and etag of a written event. The item is
// synced unless it changed during the write; then it stays pending. When the
// item was deleted during an insert, the new event is deleted again.
func (g *Google) recordPush(ctx context.Context, it Item, calendarID string, ev googleEvent, inserted bool) error {
	cur, err := g.store.GetItem(ctx, it.ID)
	if errors.Is(err, ErrNotFound) {
		if inserted && ev.ID != "" {
			_, derr := g.api(ctx, http.MethodDelete, eventPath(calendarID, ev.ID), nil, "")
			if s := httpStatus(derr); derr != nil && s != http.StatusNotFound && s != http.StatusGone {
				return derr
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	state := SyncSynced
	if cur.UpdatedAt != it.UpdatedAt && lanaChanged(cur) {
		state = SyncPending
	}
	cur.Google = &GoogleRef{CalendarID: calendarID, EventID: ev.ID, ETag: ev.ETag, Sync: state}
	if err := g.store.UpdateItem(ctx, cur); err != nil {
		return err
	}
	g.svc.broadcast(ChangeGoogle, cur.ID)
	return nil
}

// pushDelete deletes the event of a deleted item, then purges the item. An
// event already gone (404, 410) counts as deleted.
func (g *Google) pushDelete(ctx context.Context, it Item) error {
	if it.Google.EventID != "" {
		calendarID := it.Google.CalendarID
		if calendarID == "" {
			calendarID = g.kv(ctx, kvGoogleTarget)
		}
		_, err := g.api(ctx, http.MethodDelete, eventPath(calendarID, it.Google.EventID), nil, "")
		if s := httpStatus(err); err != nil && s != http.StatusNotFound && s != http.StatusGone {
			return fmt.Errorf("delete %s: %w", it.ID, err)
		}
	}
	if err := g.store.PurgeItem(ctx, it.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// googleZone is the IANA name Google gets for loc; "UTC" when it has none.
func googleZone(loc *time.Location) string {
	name := ZoneName(loc)
	if name == "" || name == "Local" {
		return "UTC"
	}
	return name
}

// eventBody is the Google event resource of an item: summary, description,
// start and end (an all-day date span, a timed span, or a 30-minute span for
// a reminder), the repeat rule and the lana_item property.
func eventBody(it Item, loc *time.Location) map[string]any {
	zone := googleZone(loc)
	if zone == "UTC" {
		loc = time.UTC
	}
	body := map[string]any{
		"summary":     it.Title,
		"description": it.Notes,
		"extendedProperties": map[string]any{
			"private": map[string]string{googleLanaProp: it.ID},
		},
	}
	if it.Kind == KindEvent && it.AllDay {
		first, _ := parseDay(it.Date)
		last, ok := parseDay(it.EndDate)
		if !ok || last.before(first) {
			last = first
		}
		body["start"] = googleTime{Date: first.String()}
		body["end"] = googleTime{Date: last.add(1).String()}
	} else {
		start := time.UnixMilli(it.Start).In(loc)
		end := time.UnixMilli(it.End).In(loc)
		if it.Kind == KindReminder || !end.After(start) {
			end = start.Add(googleReminderSpan)
		}
		body["start"] = googleTime{DateTime: start.Format(time.RFC3339), TimeZone: zone}
		body["end"] = googleTime{DateTime: end.Format(time.RFC3339), TimeZone: zone}
	}
	if it.Kind == KindReminder {
		body["reminders"] = map[string]any{
			"useDefault": false,
			"overrides":  []map[string]any{{"method": "popup", "minutes": 0}},
		}
	}
	if rule := toRRULE(it, loc); rule != "" {
		body["recurrence"] = []string{rule}
	} else {
		body["recurrence"] = []string{}
	}
	return body
}

// ---- repeat rules --------------------------------------------------------------------

var rruleDays = []struct{ name, code string }{
	{"mon", "MO"}, {"tue", "TU"}, {"wed", "WE"}, {"thu", "TH"}, {"fri", "FR"}, {"sat", "SA"}, {"sun", "SU"},
}

// toRRULE maps a repeat to an RRULE line ("" when it does not repeat). The
// inclusive Until becomes the end of that local day, as UTC for timed items
// and as a date for all-day events.
func toRRULE(it Item, loc *time.Location) string {
	r := it.Repeat
	var parts []string
	switch r.Freq {
	case FreqDaily:
		parts = append(parts, "FREQ=DAILY")
	case FreqWeekdays:
		parts = append(parts, "FREQ=WEEKLY", "BYDAY=MO,TU,WE,TH,FR")
	case FreqWeekly:
		var codes []string
		for _, d := range rruleDays {
			for _, have := range r.Days {
				if have == d.name {
					codes = append(codes, d.code)
					break
				}
			}
		}
		parts = append(parts, "FREQ=WEEKLY")
		if len(codes) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(codes, ","))
		}
	case FreqMonthly:
		parts = append(parts, "FREQ=MONTHLY")
	case FreqYearly:
		parts = append(parts, "FREQ=YEARLY")
	default:
		return ""
	}
	if until, ok := parseDay(r.Until); ok {
		if it.Kind == KindEvent && it.AllDay {
			parts = append(parts, "UNTIL="+strings.ReplaceAll(until.String(), "-", ""))
		} else {
			end := until.at(loc, 23, 59, 59).UTC()
			parts = append(parts, "UNTIL="+end.Format("20060102T150405Z"))
		}
	}
	return "RRULE:" + strings.Join(parts, ";")
}

// parseRRULE reads the rules toRRULE writes, plus a weekly rule without
// BYDAY (the start's weekday). Other rules (INTERVAL, COUNT, BYSETPOS, …)
// report false.
func parseRRULE(lines []string, start time.Time, loc *time.Location) (Repeat, bool) {
	rule := ""
	for _, l := range lines {
		if strings.HasPrefix(strings.ToUpper(l), "RRULE:") {
			if rule != "" {
				return Repeat{}, false
			}
			rule = l[len("RRULE:"):]
		} else {
			return Repeat{}, false // EXDATE, RDATE: not representable
		}
	}
	if rule == "" {
		return Repeat{}, false
	}
	kv := map[string]string{}
	for _, p := range strings.Split(rule, ";") {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return Repeat{}, false
		}
		kv[strings.ToUpper(k)] = strings.ToUpper(v)
	}
	r := Repeat{Days: []string{}}
	for k, v := range kv {
		switch k {
		case "FREQ", "BYDAY", "UNTIL", "WKST":
		case "INTERVAL":
			if v != "1" {
				return Repeat{}, false
			}
		default:
			return Repeat{}, false
		}
	}
	byDay := kv["BYDAY"]
	switch kv["FREQ"] {
	case "DAILY":
		r.Freq = FreqDaily
	case "WEEKLY":
		if byDay == "MO,TU,WE,TH,FR" {
			r.Freq = FreqWeekdays
			break
		}
		r.Freq = FreqWeekly
		if byDay == "" {
			r.Days = []string{strings.ToLower(start.In(loc).Weekday().String()[:3])}
			break
		}
		for _, code := range strings.Split(byDay, ",") {
			found := false
			for _, d := range rruleDays {
				if d.code == code {
					r.Days = append(r.Days, d.name)
					found = true
				}
			}
			if !found {
				return Repeat{}, false
			}
		}
	case "MONTHLY":
		r.Freq = FreqMonthly
	case "YEARLY":
		r.Freq = FreqYearly
	default:
		return Repeat{}, false
	}
	if byDay != "" && r.Freq != FreqWeekly && r.Freq != FreqWeekdays {
		return Repeat{}, false
	}
	if u := kv["UNTIL"]; u != "" {
		var t time.Time
		var err error
		if len(u) == 8 {
			t, err = time.ParseInLocation("20060102", u, loc)
		} else {
			t, err = time.Parse("20060102T150405Z", u)
		}
		if err != nil {
			return Repeat{}, false
		}
		r.Until = dayOf(t, loc).String()
	}
	return r, true
}

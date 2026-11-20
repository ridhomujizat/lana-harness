// Package lana is the daemon API client of the native Workbench (spec 58).
//
// The app is a client of the daemon's HTTP API, the same API the web UI uses
// (REQ-DW-010). The local connection signs in the way Lana.app does
// (REQ-DW-011): it reads $LANA_HOME/daemon.secret, asks for a one-time app
// link with the local secret, loads the link so the cookie jar holds the
// session cookie, and reads the CSRF value. Every request carries
// Origin: http://<addr>; unsafe requests also carry X-Lana-CSRF. A call that
// answers 401 signs in again once and retries once (REQ-DW-013).
package lana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
)

// Header and path constants of the daemon API (REQ-DW-011, REQ-DW-014).
const (
	// headerLocalSecret carries the daemon local secret (API-WLG-008).
	headerLocalSecret = "X-Lana-Local-Secret"
	// headerCSRF carries the session CSRF value on unsafe requests.
	headerCSRF = "X-Lana-CSRF"
	// headerOrigin is sent on every request, as a browser on the dashboard
	// would (REQ-DW-011).
	headerOrigin = "Origin"

	// appLinkPath answers POST with a one-time app link (API-DSK-001).
	appLinkPath = "/v1/local/app-link"
	// authSessionPath answers the CSRF value of the cookie's session.
	authSessionPath = "/v1/auth/session"

	// errorBodyLimit bounds how much of an error answer is read.
	errorBodyLimit = 64 << 10
)

// Errors a view can tell apart (REQ-DW-013).
var (
	// ErrUnreachable reports that the daemon did not answer at all: nothing is
	// listening on its address.
	ErrUnreachable = errors.New("lana: the daemon is not running")
	// ErrUnavailable reports a 503 workbench_unavailable answer: the daemon is
	// up but its workbench core is not. The error text is the core's reason.
	ErrUnavailable = errors.New("lana: the workbench is unavailable")
	// ErrSessionNotFound reports an attach whose session is gone: the daemon
	// closes the WebSocket with code 4404.
	ErrSessionNotFound = errors.New("lana: the workbench session is gone")
)

// codeWorkbenchUnavailable is the error code of a 503 answer (REQ-WB-042).
const codeWorkbenchUnavailable = "workbench_unavailable"

// APIError is a non-2xx answer, parsed from the daemon's error envelope
// {"error": {"code", "message"}}.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("%s (HTTP %d %s)", message, e.Status, e.Code)
	}
	return fmt.Sprintf("%s (HTTP %d)", message, e.Status)
}

// Is makes a 503 workbench_unavailable answer match ErrUnavailable, so a caller
// can tell the core's reason from other failures with errors.Is.
func (e *APIError) Is(target error) bool {
	return target == ErrUnavailable &&
		e.Status == http.StatusServiceUnavailable &&
		e.Code == codeWorkbenchUnavailable
}

// Conn is the daemon connection the views use. Local is the only
// implementation now; a remote implementation (a URL and a sign-in) can be
// added later without changing the views (REQ-DW-010).
type Conn interface {
	// BaseURL is the daemon address the connection talks to.
	BaseURL() string
	// Do performs an authenticated JSON request. A non-nil body is marshalled
	// to JSON; a 2xx answer is decoded into out, or drained when out is nil.
	Do(ctx context.Context, method, path string, body, out any) error

	ListSessions(ctx context.Context) ([]Session, error)
	CreateSession(ctx context.Context, opts CreateOptions) (Session, error)
	KillSession(ctx context.Context, id string) error
	RestartSession(ctx context.Context, id string) (Session, error)
	Resize(ctx context.Context, id string, cols, rows int) error
	GetLayout(ctx context.Context) (json.RawMessage, error)
	PutLayout(ctx context.Context, layout json.RawMessage) error
	// Subscribe reads the event stream, calling onEvent for each event until
	// ctx ends or the stream fails.
	Subscribe(ctx context.Context, onEvent func(Event)) error
	// Attach opens the control-mode WebSocket of one session.
	Attach(ctx context.Context, id string) (*Attachment, error)
}

// Local is a connection to the daemon on this machine (REQ-DW-011). Make one
// with NewLocal and call Connect before any other method.
type Local struct {
	home string

	base        string
	localSecret string
	client      *http.Client
	jar         http.CookieJar

	mu   sync.Mutex
	csrf string
}

var _ Conn = (*Local)(nil)

// NewLocal makes a connection for a Lana home. Connect reads the secret and
// signs in.
func NewLocal(home string) *Local { return &Local{home: home} }

// Connect reads <home>/daemon.secret and signs in (REQ-DW-011). The sign-in
// leaves the session cookie in the jar and the CSRF value on the connection,
// so later calls are authenticated without a password.
func (c *Local) Connect(ctx context.Context) error {
	secret, err := ReadSecret(c.home)
	if err != nil {
		return err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	c.base = "http://" + secret.Addr
	c.localSecret = secret.Secret
	c.jar = jar
	c.client = &http.Client{
		Jar: jar,
		// Sign-in must not follow the redirect the app link answers with; the
		// Set-Cookie on that answer is what matters.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return c.signIn(ctx)
}

// BaseURL is the daemon address the connection talks to.
func (c *Local) BaseURL() string { return c.base }

// Do performs an authenticated JSON request (see Conn).
func (c *Local) Do(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.send(ctx, method, path, body)
	if err != nil {
		return err
	}
	return c.decode(resp, out)
}

// signIn runs the local sign-in: an app link with the local secret, the link
// itself for the cookie, then the session for the CSRF value.
func (c *Local) signIn(ctx context.Context) error {
	link, err := c.appLink(ctx)
	if err != nil {
		return err
	}
	if err := c.loadLink(ctx, link); err != nil {
		return err
	}
	return c.readSession(ctx)
}

// appLink asks the daemon for a one-time app link (API-DSK-001).
func (c *Local) appLink(ctx context.Context) (string, error) {
	req, err := c.newRequest(ctx, http.MethodPost, appLinkPath, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(headerLocalSecret, c.localSecret)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", c.transportError(err)
	}
	var answer struct {
		Link string `json:"link"`
	}
	if err := c.decode(resp, &answer); err != nil {
		return "", err
	}
	if answer.Link == "" {
		return "", errors.New("lana: the daemon answered an empty app link")
	}
	return answer.Link, nil
}

// loadLink loads the app link so the jar keeps the session cookie. The daemon
// answers it with a redirect, which the client does not follow.
func (c *Local) loadLink(ctx context.Context, link string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	req.Header.Set(headerOrigin, c.base)
	resp, err := c.client.Do(req)
	if err != nil {
		return c.transportError(err)
	}
	defer resp.Body.Close()
	// The client's jar keeps the Set-Cookie before the redirect is stopped;
	// setting it here as well makes the sign-in independent of that detail.
	if cookies := resp.Cookies(); len(cookies) > 0 && resp.Request != nil {
		c.jar.SetCookies(resp.Request.URL, cookies)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil
	}
	return c.checkResponse(resp)
}

// readSession reads the CSRF value of the session the jar now holds.
func (c *Local) readSession(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, authSessionPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return c.transportError(err)
	}
	var answer struct {
		CSRF      string `json:"csrf"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := c.decode(resp, &answer); err != nil {
		return err
	}
	if answer.CSRF == "" {
		return errors.New("lana: the daemon answered no CSRF value")
	}
	c.setCSRF(answer.CSRF)
	return nil
}

// send performs one authenticated request, and on a 401 signs in again and
// retries the request once (REQ-DW-013).
func (c *Local) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	data, err := encodeBody(body)
	if err != nil {
		return nil, err
	}
	resp, err := c.roundTrip(ctx, method, path, data)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// The session expired or was revoked: sign in again, then try once more.
	_ = resp.Body.Close()
	if err := c.signIn(ctx); err != nil {
		return nil, err
	}
	return c.roundTrip(ctx, method, path, data)
}

// roundTrip sends one request and returns its answer, open for the caller.
func (c *Local) roundTrip(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, c.transportError(err)
	}
	return resp, nil
}

// newRequest builds a request with the Origin header and, for an unsafe method,
// the CSRF header when one is known.
func (c *Local) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(headerOrigin, c.base)
	if isUnsafe(method) {
		if csrf := c.csrfValue(); csrf != "" {
			req.Header.Set(headerCSRF, csrf)
		}
	}
	return req, nil
}

// encodeBody marshals a request body. A []byte or json.RawMessage is sent as
// is, so the layout document reaches the daemon unchanged.
func encodeBody(body any) ([]byte, error) {
	switch v := body.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return v, nil
	case []byte:
		return v, nil
	default:
		return json.Marshal(body)
	}
}

// decode closes the response body after checking the status. A 2xx answer is
// decoded into out, or drained when out is nil.
func (c *Local) decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if err := c.checkResponse(resp); err != nil {
		return err
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// checkResponse turns a non-2xx answer into an *APIError.
func (c *Local) checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	return newAPIError(resp.StatusCode, body)
}

// newAPIError parses the daemon's error answer, falling back to the raw body
// and then to the status text. The API uses the {"error": {"code", "message"}}
// envelope; the older flat {"error": "..."} shape is still read as a fallback
// (REQ-WP-001). The message comes from error.message for the envelope and from
// the error string for the flat shape.
func newAPIError(status int, body []byte) *APIError {
	apiErr := &APIError{Status: status}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Error) > 0 {
		var obj struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(envelope.Error, &obj); err == nil {
			apiErr.Code, apiErr.Message = obj.Code, obj.Message
		} else {
			var message string
			if err := json.Unmarshal(envelope.Error, &message); err == nil {
				apiErr.Message = message
			}
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	return apiErr
}

// transportError marks an error that means the daemon did not answer. A
// cancelled context is returned as is, so a caller can tell its own cancel
// from a daemon that is down.
func (c *Local) transportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}

// csrfValue returns the current CSRF value, empty before the first sign-in.
func (c *Local) csrfValue() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.csrf
}

func (c *Local) setCSRF(value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.csrf = value
}

// isUnsafe reports a method whose request needs the CSRF header.
func isUnsafe(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

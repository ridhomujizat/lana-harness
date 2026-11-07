package openaiproxy

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/router"
)

// DefaultMaxBodyBytes is the largest request body accepted when
// WithMaxBodyBytes is not set. Images arrive inline as data URLs, so the
// limit is generous.
const DefaultMaxBodyBytes int64 = 32 << 20

// HeaderServedBy names the provider and model that served a call.
const HeaderServedBy = "X-Lana-Served-By"

// Option configures NewHandler.
type Option func(*config)

type config struct {
	prefix    string
	logger    *slog.Logger
	maxBody   int64
	onRequest func(keyID string)
	now       func() time.Time
}

// WithPrefix sets the path prefix the handler is mounted at when it is not
// wrapped in http.StripPrefix, for example "/router/v1".
func WithPrefix(prefix string) Option {
	return func(c *config) { c.prefix = strings.TrimRight(prefix, "/") }
}

// WithLogger sets the logger. The default is slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithMaxBodyBytes sets the largest accepted request body. Zero or negative
// keeps DefaultMaxBodyBytes.
func WithMaxBodyBytes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxBody = n
		}
	}
}

// WithOnRequest sets a hook called with the key ID of every authenticated
// request, before the request is served (for last-used tracking). It must
// not block.
func WithOnRequest(f func(keyID string)) Option {
	return func(c *config) { c.onRequest = f }
}

// withClock sets the clock of the "created" field (tests).
func withClock(now func() time.Time) Option {
	return func(c *config) { c.now = now }
}

type handler struct {
	r    *router.Router
	auth func(bearer string) (string, bool)
	cfg  config
}

// NewHandler returns the OpenAI-compatible proxy over r. auth checks the
// bearer key and returns the key ID; a nil auth rejects every request. See
// the package documentation for mounting.
func NewHandler(r *router.Router, auth func(bearer string) (keyID string, ok bool), opts ...Option) http.Handler {
	h := &handler{r: r, auth: auth, cfg: config{maxBody: DefaultMaxBodyBytes, now: time.Now}}
	for _, opt := range opts {
		opt(&h.cfg)
	}
	if h.cfg.logger == nil {
		h.cfg.logger = slog.Default()
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path, ok := h.route(req.URL.Path)
	keyID, authed := h.authenticate(req)
	if !authed {
		writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key",
			"Invalid or missing API key. Send it as 'Authorization: Bearer <key>'.")
		return
	}
	if h.cfg.onRequest != nil {
		h.cfg.onRequest(keyID)
	}
	switch {
	case ok && path == "/chat/completions":
		if req.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Use POST for /chat/completions.")
			return
		}
		h.chatCompletions(w, req, keyID)
	case ok && path == "/models":
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Use GET for /models.")
			return
		}
		h.models(w, req)
	default:
		writeError(w, http.StatusNotFound, "invalid_request_error", "unknown_url", "Unknown request URL: "+req.Method+" "+req.URL.Path)
	}
}

// route returns the path below the prefix, always with a leading "/" and no
// trailing "/". http.StripPrefix("/router/v1/", …) leaves "chat/completions",
// so a missing leading slash is accepted too.
func (h *handler) route(path string) (string, bool) {
	if h.cfg.prefix != "" {
		rest, ok := strings.CutPrefix(path, h.cfg.prefix)
		if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
			return "", false
		}
		path = rest
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path, true
}

// authenticate reads the key from "Authorization: Bearer <key>" or
// "x-api-key: <key>" and checks it.
func (h *handler) authenticate(req *http.Request) (string, bool) {
	if h.auth == nil {
		return "", false
	}
	key := ""
	if value := strings.TrimSpace(req.Header.Get("Authorization")); value != "" {
		scheme, rest, found := strings.Cut(value, " ")
		if found && strings.EqualFold(scheme, "Bearer") {
			key = strings.TrimSpace(rest)
		}
	}
	if key == "" {
		key = strings.TrimSpace(req.Header.Get("x-api-key"))
	}
	if key == "" {
		return "", false
	}
	return h.auth(key)
}

// apiError is the OpenAI error body.
type apiError struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

func writeError(w http.ResponseWriter, status int, typ, code, message string) {
	writeJSON(w, status, apiError{Error: errorDetail{Message: message, Type: typ, Code: code}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// requestError is a 4xx caused by the client request.
type requestError struct {
	status  int
	code    string
	message string
}

func (e *requestError) Error() string { return e.message }

func badRequest(code, message string) *requestError {
	return &requestError{status: http.StatusBadRequest, code: code, message: message}
}

func writeRequestError(w http.ResponseWriter, err error) {
	var re *requestError
	if errors.As(err, &re) {
		writeError(w, re.status, "invalid_request_error", re.code, re.message)
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error())
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// newCompletionID returns "chatcmpl-" and 24 random base62 characters.
func newCompletionID() string {
	return "chatcmpl-" + randomString(24)
}

package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// contextLengthPhrases are the API messages that mean the request did not
// fit the model context window.
var contextLengthPhrases = []string{
	"prompt is too long",
	"context window",
	"context length",
	"too many tokens",
	"exceeds the maximum",
}

// apiError is the error envelope of the Messages API, used both for HTTP
// error bodies and for the SSE error event.
type apiError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// classifyHTTP maps an HTTP error status and body to a provider error class:
// 401/402/403 auth, 404 not_found, 413 and prompt-too-long 400 context_length,
// other 4xx client, 408 network, 429 rate_limit, 5xx and 529 server. The API
// key is redacted from the message.
func classifyHTTP(status int, body []byte, key string) *core.ProviderError {
	var payload apiError
	parsed := json.Unmarshal(body, &payload) == nil && payload.Error.Type != ""
	class := "client"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusPaymentRequired:
		class = "auth"
	case status == http.StatusNotFound:
		class = core.ErrorClassNotFound
	case status == http.StatusRequestEntityTooLarge:
		class = "context_length"
	case status == http.StatusRequestTimeout:
		class = "network"
	case status == http.StatusTooManyRequests:
		class = "rate_limit"
	case status >= 500:
		class = "server"
	case status == http.StatusBadRequest && parsed && isContextLengthMessage(payload.Error.Message):
		class = "context_length"
	}
	msg := fmt.Sprintf("HTTP %d", status)
	switch {
	case parsed:
		msg += ": " + payload.Error.Type + ": " + payload.Error.Message
		if payload.RequestID != "" {
			msg += " (request " + payload.RequestID + ")"
		}
	case len(body) > 0:
		msg += ": " + string(body)
	}
	return &core.ProviderError{Class: class, Status: status, Message: clip(router.Redact(msg, key))}
}

// classifyStreamError maps the error event of an SSE stream. The HTTP status
// was 200, so the class comes from the error type.
func classifyStreamError(errType, message, key string) *core.ProviderError {
	class := "server"
	status := 0
	switch errType {
	case "overloaded_error":
		status = 529
	case "api_error":
		status = http.StatusInternalServerError
	case "rate_limit_error":
		class, status = "rate_limit", http.StatusTooManyRequests
	case "authentication_error":
		class, status = "auth", http.StatusUnauthorized
	case "permission_error":
		class, status = "auth", http.StatusForbidden
	case "billing_error":
		class, status = "auth", http.StatusPaymentRequired
	case "not_found_error":
		class, status = core.ErrorClassNotFound, http.StatusNotFound
	case "request_too_large":
		class, status = "context_length", http.StatusRequestEntityTooLarge
	case "invalid_request_error":
		class, status = "client", http.StatusBadRequest
		if isContextLengthMessage(message) {
			class = "context_length"
		}
	}
	msg := "stream error"
	if errType != "" {
		msg += ": " + errType
	}
	if message != "" {
		msg += ": " + message
	}
	return &core.ProviderError{Class: class, Status: status, Message: clip(router.Redact(msg, key))}
}

func clip(msg string) string {
	const limit = 600
	if len(msg) > limit {
		return msg[:limit]
	}
	return msg
}

// isContextLengthMessage reports whether an error message means the request
// overflowed the context window.
func isContextLengthMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, phrase := range contextLengthPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// parseRetryAfter reads a Retry-After header value: either delta-seconds or
// an HTTP-date. It returns zero when the header is absent or already past.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func classifyNetwork(err error, key string) *core.ProviderError {
	return &core.ProviderError{Class: "network", Message: router.Redact(err.Error(), key)}
}

// parseRateLimits reads the anthropic-ratelimit-{requests,tokens,
// input-tokens,output-tokens}-{limit,remaining,reset} headers. reset is an
// RFC 3339 time. It returns nil when no header is present (REQ-RTR-012).
func parseRateLimits(h http.Header) *router.RateLimits {
	var rate router.RateLimits
	found := false
	for _, item := range []struct {
		name string
		dst  **router.RateLimit
	}{
		{"requests", &rate.Requests},
		{"tokens", &rate.Tokens},
		{"input-tokens", &rate.InputTokens},
		{"output-tokens", &rate.OutputTokens},
	} {
		if limit := parseRateLimit(h, "anthropic-ratelimit-"+item.name); limit != nil {
			*item.dst = limit
			found = true
		}
	}
	if !found {
		return nil
	}
	return &rate
}

func parseRateLimit(h http.Header, prefix string) *router.RateLimit {
	limitValue := strings.TrimSpace(h.Get(prefix + "-limit"))
	remainingValue := strings.TrimSpace(h.Get(prefix + "-remaining"))
	if limitValue == "" && remainingValue == "" {
		return nil
	}
	var out router.RateLimit
	if n, err := strconv.ParseInt(limitValue, 10, 64); err == nil {
		out.Limit = n
	}
	if n, err := strconv.ParseInt(remainingValue, 10, 64); err == nil {
		out.Remaining = n
	}
	if reset := strings.TrimSpace(h.Get(prefix + "-reset")); reset != "" {
		if when, err := time.Parse(time.RFC3339, reset); err == nil {
			when = when.UTC()
			out.ResetAt = &when
		}
	}
	return &out
}

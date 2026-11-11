package commandcode

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// contextLengthPhrases are the provider messages that mean the request did
// not fit the model context window.
var contextLengthPhrases = []string{
	"maximum context length",
	"context length",
	"context window",
	"too many tokens",
	"prompt is too long",
}

// classOf maps an HTTP status and message to a router error class.
func classOf(status int, message string) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth"
	case status == http.StatusTooManyRequests:
		return "rate_limit"
	case status == http.StatusNotFound:
		return core.ErrorClassNotFound
	case status >= 500:
		return "server"
	default:
		return classOfMessage("client", message)
	}
}

// classOfMessage returns context_length when the message says the request
// overflowed the context window, else fallback.
func classOfMessage(fallback, message string) string {
	lower := strings.ToLower(message)
	for _, phrase := range contextLengthPhrases {
		if strings.Contains(lower, phrase) {
			return "context_length"
		}
	}
	return fallback
}

// responseError reads a non-2xx response into a provider error. The key is
// redacted from the message.
func responseError(resp *http.Response, key string) *core.ProviderError {
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	snippet := string(data)
	if len(snippet) > 500 {
		snippet = snippet[:500]
	}
	snippet = redact(snippet, key)
	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if snippet != "" {
		msg += ": " + snippet
	}
	return &core.ProviderError{
		Class:      classOf(resp.StatusCode, snippet),
		Status:     resp.StatusCode,
		Message:    msg,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}

// classifyNetwork wraps a transport error.
func classifyNetwork(err error, key string) *core.ProviderError {
	return &core.ProviderError{Class: "network", Message: redact(err.Error(), key)}
}

func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

// parseRetryAfter reads a Retry-After value: delta-seconds or an HTTP-date.
// It returns zero when the header is absent or already past.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

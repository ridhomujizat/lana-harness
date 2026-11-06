package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

func classifyHTTP(status int, body []byte) *core.ProviderError {
	class := "client"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		class = "auth"
	case status == http.StatusTooManyRequests:
		class = "rate_limit"
	case status >= 500:
		class = "server"
	case status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge:
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			if payload.Error.Code == "context_length_exceeded" || isContextLengthMessage(payload.Error.Message) {
				class = "context_length"
			}
		}
	}
	msg := fmt.Sprintf("HTTP %d", status)
	if len(body) > 0 {
		snippet := string(body)
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		msg += ": " + snippet
	}
	return &core.ProviderError{Class: class, Status: status, Message: msg}
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

func classifyNetwork(err error) *core.ProviderError {
	if err == nil {
		return nil
	}
	class := "network"
	var netErr net.Error
	if !errors.As(err, &netErr) && !errors.Is(err, context.Canceled) {
		class = "network"
	}
	_ = netErr
	return &core.ProviderError{Class: class, Message: err.Error()}
}

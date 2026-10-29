package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/ridhomujizat/lana-harness/core"
)

func classifyHTTP(status int, body []byte) *core.ProviderError {
	class := "client"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		class = "auth"
	case status == http.StatusTooManyRequests:
		class = "rate_limit"
	case status >= 500:
		class = "server"
	case status == http.StatusBadRequest:
		class = "client"
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Code == "context_length_exceeded" {
			class = "context_length"
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

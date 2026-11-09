package openaiproxy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// Response shapes of the Chat Completions API.

type completion struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   *usage             `json:"usage,omitempty"`
}

type completionChoice struct {
	Index        int               `json:"index"`
	Message      completionMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type completionMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
}

type chunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *usage        `json:"usage,omitempty"`
}

type chunkChoice struct {
	Index        int     `json:"index"`
	Delta        delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type delta struct {
	Role             string     `json:"role,omitempty"`
	Content          *string    `json:"content,omitempty"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
}

type usage struct {
	PromptTokens        int64         `json:"prompt_tokens"`
	CompletionTokens    int64         `json:"completion_tokens"`
	TotalTokens         int64         `json:"total_tokens"`
	PromptTokensDetails promptDetails `json:"prompt_tokens_details"`
}

type promptDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

func toUsage(u core.Usage) *usage {
	return &usage{
		PromptTokens:        u.InputTokens,
		CompletionTokens:    u.OutputTokens,
		TotalTokens:         u.InputTokens + u.OutputTokens,
		PromptTokensDetails: promptDetails{CachedTokens: u.CachedInputTokens},
	}
}

func (h *handler) chatCompletions(w http.ResponseWriter, req *http.Request, keyID string) {
	body := http.MaxBytesReader(w, req.Body, h.cfg.maxBody)
	var in chatRequest
	if err := json.NewDecoder(body).Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large",
				fmt.Sprintf("The request body is larger than %d bytes.", h.cfg.maxBody))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "The request body is not valid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_value", "'model' is required.")
		return
	}
	creq, err := toChatRequest(&in)
	if err != nil {
		writeRequestError(w, err)
		return
	}
	chain, err := h.r.Resolve(in.Model)
	if err != nil {
		if errors.Is(err, router.ErrUnknownAddress) {
			writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_found",
				fmt.Sprintf("The model %q does not exist. Use a combo name or a provider/model address; GET /models lists them.", in.Model))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_value", err.Error())
		return
	}

	id := newCompletionID()
	meta := router.CallMeta{Source: "proxy", Agent: "proxy:" + keyID, TurnID: id, Requested: in.Model}
	ctx := req.Context()
	onAttempt := func(a router.Attempt) {
		h.cfg.logger.Warn("router proxy attempt failed",
			"id", id, "target", router.Address(a.Target), "attempt", a.N, "class", a.Err.Class, "error", a.Err.Message)
	}
	stream, target, err := h.r.StreamChain(ctx, chain, creq, meta, onAttempt)
	if err != nil {
		if ctx.Err() != nil {
			// The client went away; nobody reads a response.
			return
		}
		h.cfg.logger.Warn("router proxy call failed", "id", id, "model", in.Model, "error", err)
		writeUpstreamError(w, err)
		return
	}
	w.Header().Set(HeaderServedBy, router.Address(target))
	created := h.cfg.now().Unix()
	if in.Stream {
		includeUsage := in.StreamOptions != nil && in.StreamOptions.IncludeUsage
		h.streamResponse(ctx, w, stream, id, created, in.Model, includeUsage)
		return
	}
	h.completeResponse(ctx, w, stream, id, created, in.Model)
}

// accumulator collects a stream into a reply and turns each event into the
// delta to stream for it.
type accumulator struct {
	text, reasoning      strings.Builder
	sawText, sawThinking bool
	toolCalls            []toolCall
	usage                core.Usage
	hasUsage             bool
	stop                 string
}

// add records event and returns the delta to send for it, or nil.
func (a *accumulator) add(event core.ChatEvent) *delta {
	switch event.Kind {
	case core.ChatTextDelta:
		return a.addText(event.Text)
	case core.ChatThinkingDelta:
		return a.addReasoning(event.Text)
	case core.ChatBlock:
		block := event.Block
		switch block.Kind {
		case core.BlockToolUse:
			args := strings.TrimSpace(string(block.Input))
			if args == "" {
				args = "{}"
			}
			callID := block.ID
			if callID == "" {
				callID = "call_" + randomString(24)
			}
			index := len(a.toolCalls)
			call := toolCall{Index: &index, ID: callID, Type: "function", Function: functionCall{Name: block.Name, Arguments: args}}
			a.toolCalls = append(a.toolCalls, call)
			return &delta{ToolCalls: []toolCall{call}}
		case core.BlockText:
			// Adapters stream text as deltas; a whole text block is used only
			// when no delta came before it.
			if !a.sawText {
				return a.addText(block.Text)
			}
		case core.BlockThinking:
			// Adapters also emit the streamed reasoning as one block at the end.
			if !a.sawThinking {
				return a.addReasoning(block.Text)
			}
		}
	case core.ChatUsage:
		a.hasUsage = true
		if event.Usage.InputTokens > 0 {
			a.usage.InputTokens = event.Usage.InputTokens
		}
		if event.Usage.OutputTokens > 0 {
			a.usage.OutputTokens = event.Usage.OutputTokens
		}
		if event.Usage.CachedInputTokens > 0 {
			a.usage.CachedInputTokens = event.Usage.CachedInputTokens
		}
	case core.ChatStop:
		a.stop = event.StopReason
	}
	return nil
}

func (a *accumulator) addText(text string) *delta {
	if text == "" {
		return nil
	}
	a.sawText = true
	a.text.WriteString(text)
	return &delta{Content: &text}
}

func (a *accumulator) addReasoning(text string) *delta {
	if text == "" {
		return nil
	}
	a.sawThinking = true
	a.reasoning.WriteString(text)
	return &delta{ReasoningContent: &text}
}

// finishReason maps the core stop reason to an OpenAI finish_reason.
func (a *accumulator) finishReason() string {
	switch a.stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	}
	if len(a.toolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func (h *handler) streamResponse(ctx context.Context, w http.ResponseWriter, stream core.ChatStream, id string, created int64, model string, includeUsage bool) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sse := &sseWriter{w: w, rc: http.NewResponseController(w)}
	newChunk := func(d delta, finish *string) chunk {
		return chunk{ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chunkChoice{{Index: 0, Delta: d, FinishReason: finish}}}
	}
	empty := ""
	if !sse.data(newChunk(delta{Role: "assistant", Content: &empty}, nil)) {
		return
	}
	var acc accumulator
	for event, err := range stream {
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.cfg.logger.Warn("router proxy stream failed", "id", id, "model", model, "error", err)
			_, typ, code, message := upstreamError(err)
			sse.data(apiError{Error: errorDetail{Message: message, Type: typ, Code: code}})
			return
		}
		if d := acc.add(event); d != nil {
			if !sse.data(newChunk(*d, nil)) {
				return
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	finish := acc.finishReason()
	if !sse.data(newChunk(delta{}, &finish)) {
		return
	}
	if includeUsage {
		final := chunk{ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chunkChoice{}, Usage: toUsage(acc.usage)}
		if !sse.data(final) {
			return
		}
	}
	sse.raw("[DONE]")
}

func (h *handler) completeResponse(ctx context.Context, w http.ResponseWriter, stream core.ChatStream, id string, created int64, model string) {
	var acc accumulator
	for event, err := range stream {
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.cfg.logger.Warn("router proxy stream failed", "id", id, "model", model, "error", err)
			w.Header().Del(HeaderServedBy)
			writeUpstreamError(w, err)
			return
		}
		acc.add(event)
	}
	msg := completionMessage{Role: "assistant", ReasoningContent: acc.reasoning.String()}
	for _, call := range acc.toolCalls {
		call.Index = nil // index is a streaming field
		msg.ToolCalls = append(msg.ToolCalls, call)
	}
	if text := acc.text.String(); text != "" || len(acc.toolCalls) == 0 {
		msg.Content = &text
	}
	writeJSON(w, http.StatusOK, completion{
		ID: id, Object: "chat.completion", Created: created, Model: model,
		Choices: []completionChoice{{Index: 0, Message: msg, FinishReason: acc.finishReason()}},
		Usage:   toUsage(acc.usage),
	})
}

// sseWriter writes server-sent events and flushes after each one. After a
// failed write it writes nothing more.
type sseWriter struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	failed bool
}

func (s *sseWriter) data(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		s.failed = true
		return false
	}
	return s.raw(string(b))
}

func (s *sseWriter) raw(payload string) bool {
	if s.failed {
		return false
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", payload); err != nil {
		s.failed = true
		return false
	}
	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.failed = true
		return false
	}
	return true
}

func writeUpstreamError(w http.ResponseWriter, err error) {
	status, typ, code, message := upstreamError(err)
	var pe *core.ProviderError
	if status == http.StatusTooManyRequests && errors.As(err, &pe) && pe.RetryAfter > 0 {
		seconds := int64((pe.RetryAfter + 999_999_999) / 1_000_000_000)
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	writeError(w, status, typ, code, message)
}

// upstreamError maps an error of the router to an HTTP status and an OpenAI
// error type, code, and message. The router redacts API keys from chain
// errors before they get here.
func upstreamError(err error) (status int, typ, code, message string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "api_error", "timeout", "The upstream provider timed out."
	}
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe == nil {
		return http.StatusBadGateway, "api_error", "upstream_error", err.Error()
	}
	message = pe.Error()
	class := pe.Class
	if class == "client" && pe.Status == http.StatusNotFound {
		class = core.ErrorClassNotFound
	}
	switch class {
	case "rate_limit":
		return http.StatusTooManyRequests, "rate_limit_error", "rate_limit", message
	case "auth":
		return http.StatusBadGateway, "api_error", "upstream_auth", message
	case "server":
		return http.StatusBadGateway, "api_error", "server", message
	case "network":
		if isTimeout(pe.Message) {
			return http.StatusGatewayTimeout, "api_error", "timeout", message
		}
		return http.StatusBadGateway, "api_error", "network", message
	case "client":
		return http.StatusBadRequest, "invalid_request_error", "client", message
	case "context_length":
		return http.StatusBadRequest, "invalid_request_error", "context_length_exceeded", message
	case core.ErrorClassNotFound:
		return http.StatusNotFound, "invalid_request_error", core.ErrorClassNotFound, message
	default:
		if class == "" {
			class = "upstream_error"
		}
		return http.StatusBadGateway, "api_error", class, message
	}
}

func isTimeout(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded")
}

// randomString returns n random base62 characters.
func randomString(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	for i, b := range raw {
		raw[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(raw)
}

package commandcode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// event is one NDJSON stream event. The provider streams AI SDK events:
// text-delta, reasoning-delta, tool-input-start, tool-input-delta,
// tool-call, finish-step, finish, and error.
type event struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Delta        json.RawMessage `json:"delta"`
	ID           string          `json:"id"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	Input        json.RawMessage `json:"input"`
	FinishReason string          `json:"finishReason"`
	Usage        *wireUsage      `json:"usage"`
	TotalUsage   *wireUsage      `json:"totalUsage"`
	Error        json.RawMessage `json:"error"`
	Message      string          `json:"message"`
	StatusCode   int             `json:"statusCode"`
}

// wireUsage reads AI SDK v5 names and the older prompt/completion names.
type wireUsage struct {
	InputTokens       *int64 `json:"inputTokens"`
	OutputTokens      *int64 `json:"outputTokens"`
	PromptTokens      *int64 `json:"promptTokens"`
	CompletionTokens  *int64 `json:"completionTokens"`
	CachedInputTokens int64  `json:"cachedInputTokens"`
}

func (u *wireUsage) core() core.Usage {
	out := core.Usage{CachedInputTokens: u.CachedInputTokens}
	if u.InputTokens != nil {
		out.InputTokens = *u.InputTokens
	} else if u.PromptTokens != nil {
		out.InputTokens = *u.PromptTokens
	}
	if u.OutputTokens != nil {
		out.OutputTokens = *u.OutputTokens
	} else if u.CompletionTokens != nil {
		out.OutputTokens = *u.CompletionTokens
	}
	return out
}

// deltaText returns the delta field when it is a string.
func (e event) deltaText() string {
	var s string
	if len(e.Delta) > 0 && json.Unmarshal(e.Delta, &s) == nil {
		return s
	}
	return ""
}

// callID returns the tool call id of a tool event.
func (e event) callID() string {
	if e.ID != "" {
		return e.ID
	}
	return e.ToolCallID
}

type toolCall struct {
	id    string
	name  string
	args  strings.Builder
	final json.RawMessage
}

// readStream turns the NDJSON body into chat events. Text and reasoning
// stream as deltas; the reasoning, the tool calls, the usage, and the stop
// reason follow at the end, as the other adapters emit them.
func (a *Adapter) readStream(ctx context.Context, body io.ReadCloser) core.ChatStream {
	return func(yield func(core.ChatEvent, error) bool) {
		defer func() { _ = body.Close() }()
		scanner := bufio.NewScanner(newIdleReader(body, a.streamIdle))
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		var reasoning strings.Builder
		var calls []*toolCall
		byID := map[string]*toolCall{}
		var usage *wireUsage
		finish := ""
		finished := false
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				yield(core.ChatEvent{}, err)
				return
			}
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "data:") {
				line = strings.TrimSpace(line[len("data:"):])
			}
			if line == "" || line == "[DONE]" {
				continue
			}
			var ev event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			switch ev.Type {
			case "text-delta":
				text := ev.Text
				if text == "" {
					text = ev.deltaText()
				}
				if text != "" && !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil) {
					return
				}
			case "reasoning-delta":
				text := ev.Text
				if text == "" {
					text = ev.deltaText()
				}
				if text != "" {
					reasoning.WriteString(text)
					if !yield(core.ChatEvent{Kind: core.ChatThinkingDelta, Text: text}, nil) {
						return
					}
				}
			case "tool-input-start":
				callID := ev.callID()
				if callID == "" {
					callID = id.New()
				}
				if _, ok := byID[callID]; !ok {
					call := &toolCall{id: callID, name: ev.ToolName}
					byID[callID] = call
					calls = append(calls, call)
				}
			case "tool-input-delta":
				if call, ok := byID[ev.callID()]; ok {
					call.args.WriteString(ev.deltaText())
				}
			case "tool-call":
				callID := ev.callID()
				if callID == "" {
					callID = id.New()
				}
				call, ok := byID[callID]
				if !ok {
					call = &toolCall{id: callID}
					byID[callID] = call
					calls = append(calls, call)
				}
				if ev.ToolName != "" {
					call.name = ev.ToolName
				}
				call.final = callInput(ev.Input)
			case "finish-step":
				if ev.FinishReason != "" {
					finish = ev.FinishReason
				}
				if ev.Usage != nil {
					usage = ev.Usage
				}
			case "finish":
				if finish == "" {
					finish = ev.FinishReason
				}
				if ev.TotalUsage != nil {
					usage = ev.TotalUsage
				}
				finished = true
			case "error":
				yield(core.ChatEvent{}, streamError(ev))
				return
			}
		}
		if err := scanner.Err(); err != nil {
			if ctx.Err() != nil {
				yield(core.ChatEvent{}, ctx.Err())
				return
			}
			yield(core.ChatEvent{}, &core.ProviderError{Class: "network", Message: err.Error()})
			return
		}
		if !finished && finish == "" {
			yield(core.ChatEvent{}, &core.ProviderError{Class: "network", Message: "stream disconnected before finish"})
			return
		}
		if reasoning.Len() > 0 {
			block := core.ContentBlock{Kind: core.BlockThinking, Text: reasoning.String()}
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: block}, nil) {
				return
			}
		}
		for _, call := range calls {
			input := call.final
			if len(input) == 0 {
				input = callInput(json.RawMessage(call.args.String()))
			}
			block := core.ContentBlock{Kind: core.BlockToolUse, ID: call.id, Name: call.name, Input: input}
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: block}, nil) {
				return
			}
		}
		if usage != nil {
			if !yield(core.ChatEvent{Kind: core.ChatUsage, Usage: usage.core()}, nil) {
				return
			}
		}
		stop := mapStop(finish)
		if len(calls) > 0 && stop == "end_turn" {
			stop = "tool_use"
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: stop}, nil)
	}
}

// callInput normalises a tool call input: an object stays, a JSON string
// holding an object is unwrapped, anything else becomes {}.
func callInput(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(strings.TrimSpace(s))
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return json.RawMessage("{}")
	}
	return raw
}

// mapStop converts AI SDK finish reasons to lana stop reasons.
func mapStop(reason string) string {
	switch reason {
	case "", "stop", "error":
		return "end_turn"
	case "tool-calls", "tool_calls", "tool_use":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return reason
	}
}

// streamError converts an in-stream {"type":"error"} event.
func streamError(ev event) *core.ProviderError {
	message := ev.Message
	status := ev.StatusCode
	if len(ev.Error) > 0 {
		var s string
		var obj struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
			Status     int    `json:"status"`
		}
		if json.Unmarshal(ev.Error, &s) == nil && s != "" {
			message = s
		} else if json.Unmarshal(ev.Error, &obj) == nil {
			if obj.Message != "" {
				message = obj.Message
			}
			if status == 0 {
				status = obj.StatusCode
			}
			if status == 0 {
				status = obj.Status
			}
		}
	}
	if message == "" {
		message = "Command Code upstream error"
	}
	if status < 400 || status > 599 {
		status = 0
	}
	lower := strings.ToLower(message)
	if status == 0 && (strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests")) {
		status = http.StatusTooManyRequests
	}
	if status == 0 {
		return &core.ProviderError{Class: classOfMessage("server", message), Message: message}
	}
	return &core.ProviderError{Class: classOf(status, message), Status: status, Message: message}
}

// idleReader fails a read that stays idle longer than the timeout instead
// of blocking forever. Each Read arms a fresh timer.
type idleReader struct {
	body    io.ReadCloser
	timeout time.Duration
	buf     []byte
	pending []byte
	err     error
}

func newIdleReader(body io.ReadCloser, timeout time.Duration) *idleReader {
	return &idleReader{body: body, timeout: timeout, buf: make([]byte, 32*1024)}
}

func (r *idleReader) Read(p []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	type readResult struct {
		n   int
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		n, err := r.body.Read(r.buf)
		done <- readResult{n: n, err: err}
	}()
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case res := <-done:
		if res.n > 0 {
			r.pending = r.buf[:res.n]
		}
		if res.err != nil {
			r.err = res.err
		}
		if len(r.pending) == 0 {
			return 0, r.err
		}
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	case <-timer.C:
		_ = r.body.Close()
		r.err = fmt.Errorf("stream idle for %s", r.timeout)
		return 0, r.err
	}
}

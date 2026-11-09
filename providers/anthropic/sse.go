package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// sseUsage is the usage object of message_start and message_delta.
type sseUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

// sseEvent is the union of the Messages stream events this adapter reads.
type sseEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage sseUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *sseUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// openBlock is a content block between content_block_start and stop.
type openBlock struct {
	kind      string
	id        string
	name      string
	text      strings.Builder
	signature string
	data      string
	input     strings.Builder
}

// streamUsage accumulates the token counts of one response.
type streamUsage struct {
	input, cacheRead, cacheCreation, output int64
	seen                                    bool
}

func (u *streamUsage) merge(in sseUsage) {
	if in.InputTokens != nil {
		u.input, u.seen = *in.InputTokens, true
	}
	if in.CacheReadInputTokens != nil {
		u.cacheRead, u.seen = *in.CacheReadInputTokens, true
	}
	if in.CacheCreationInputTokens != nil {
		u.cacheCreation, u.seen = *in.CacheCreationInputTokens, true
	}
	if in.OutputTokens != nil {
		u.output, u.seen = *in.OutputTokens, true
	}
}

// core returns the usage in lana's terms: input counts every prompt token
// (uncached, cache reads, and cache writes); cached is the cache reads.
func (u *streamUsage) core() core.Usage {
	return core.Usage{
		InputTokens:       u.input + u.cacheRead + u.cacheCreation,
		OutputTokens:      u.output,
		CachedInputTokens: u.cacheRead,
	}
}

// readSSE turns a Messages event stream into chat events. Text and thinking
// stream as deltas; a thinking block (with its signature) and a tool_use
// block (with its accumulated input) are emitted as blocks when they close,
// so blocks arrive in response order. Usage and the stop reason follow
// message_stop.
func (a *Adapter) readSSE(ctx context.Context, body io.ReadCloser, key string) core.ChatStream {
	return func(yield func(core.ChatEvent, error) bool) {
		defer func() { _ = body.Close() }()
		scanner := bufio.NewScanner(newIdleReader(body, a.streamIdle))
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		blocks := map[int]*openBlock{}
		var usage streamUsage
		stop := ""
		done := false
		var data strings.Builder

		// emit yields an event and records whether the consumer stopped.
		aborted := false
		emit := func(ev core.ChatEvent) bool {
			if !yield(ev, nil) {
				aborted = true
				return false
			}
			return true
		}
		fail := func(err error) {
			aborted = true
			yield(core.ChatEvent{}, err)
		}

		// handle processes one event; it returns false when the stream ends:
		// message_stop (done), an error, or a consumer that stopped (aborted).
		handle := func(payload string) bool {
			var ev sseEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				fail(&core.ProviderError{Class: "server", Message: "invalid stream event"})
				return false
			}
			switch ev.Type {
			case "message_start":
				usage.merge(ev.Message.Usage)
			case "content_block_start":
				open := &openBlock{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name, signature: ev.ContentBlock.Signature, data: ev.ContentBlock.Data}
				blocks[ev.Index] = open
				switch open.kind {
				case "text":
					if ev.ContentBlock.Text != "" {
						return emit(core.ChatEvent{Kind: core.ChatTextDelta, Text: ev.ContentBlock.Text})
					}
				case "thinking":
					if ev.ContentBlock.Thinking != "" {
						open.text.WriteString(ev.ContentBlock.Thinking)
						return emit(core.ChatEvent{Kind: core.ChatThinkingDelta, Text: ev.ContentBlock.Thinking})
					}
				}
			case "content_block_delta":
				open := blocks[ev.Index]
				switch ev.Delta.Type {
				case "text_delta":
					if ev.Delta.Text != "" {
						return emit(core.ChatEvent{Kind: core.ChatTextDelta, Text: ev.Delta.Text})
					}
				case "thinking_delta":
					if open != nil {
						open.text.WriteString(ev.Delta.Thinking)
					}
					if ev.Delta.Thinking != "" {
						return emit(core.ChatEvent{Kind: core.ChatThinkingDelta, Text: ev.Delta.Thinking})
					}
				case "signature_delta":
					if open != nil {
						open.signature += ev.Delta.Signature
					}
				case "input_json_delta":
					if open != nil {
						open.input.WriteString(ev.Delta.PartialJSON)
					}
				}
			case "content_block_stop":
				open := blocks[ev.Index]
				delete(blocks, ev.Index)
				if open == nil {
					return true
				}
				if b, ok := closedBlock(open); ok {
					return emit(core.ChatEvent{Kind: core.ChatBlock, Block: b})
				}
			case "message_delta":
				if ev.Delta.StopReason != "" {
					stop = ev.Delta.StopReason
				}
				if ev.Usage != nil {
					usage.merge(*ev.Usage)
				}
			case "message_stop":
				done = true
				return false
			case "error":
				fail(classifyStreamError(ev.Error.Type, ev.Error.Message, key))
				return false
			}
			// ping and unknown events are ignored.
			return true
		}

		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				fail(err)
				return
			}
			line := strings.TrimSuffix(scanner.Text(), "\r")
			if line == "" {
				if data.Len() > 0 {
					payload := data.String()
					data.Reset()
					if !handle(payload) {
						break
					}
				}
				continue
			}
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(value, " "))
			}
			// "event:" lines repeat the data type; comments are ignored.
		}
		if aborted {
			return
		}
		if !done && data.Len() > 0 {
			// A final event without the trailing blank line.
			handle(data.String())
			if aborted {
				return
			}
		}
		if !done {
			switch {
			case ctx.Err() != nil:
				fail(ctx.Err())
				return
			case scanner.Err() != nil:
				fail(&core.ProviderError{Class: "network", Message: scanner.Err().Error()})
				return
			case stop == "":
				fail(&core.ProviderError{Class: "network", Message: "stream disconnected before message_stop"})
				return
			}
		}
		if usage.seen && !emit(core.ChatEvent{Kind: core.ChatUsage, Usage: usage.core()}) {
			return
		}
		emit(core.ChatEvent{Kind: core.ChatStop, StopReason: mapStop(stop)})
	}
}

// closedBlock returns the chat block of a closed content block, if it
// becomes one. A tool_use whose input is not valid JSON (cut by max_tokens
// or a refusal) is dropped so the agent never runs a broken call.
func closedBlock(open *openBlock) (core.ContentBlock, bool) {
	switch open.kind {
	case "thinking":
		if open.signature == "" && open.text.Len() == 0 {
			return core.ContentBlock{}, false
		}
		return core.ContentBlock{Kind: core.BlockThinking, Text: open.text.String(), Signature: open.signature}, true
	case "redacted_thinking":
		if open.data == "" {
			return core.ContentBlock{}, false
		}
		return core.ContentBlock{Kind: core.BlockThinking, Signature: core.RedactedThinkingPrefix + open.data}, true
	case "tool_use":
		input := strings.TrimSpace(open.input.String())
		if input == "" {
			input = "{}"
		}
		if !json.Valid([]byte(input)) {
			return core.ContentBlock{}, false
		}
		callID := open.id
		if callID == "" {
			callID = id.New()
		}
		return core.ContentBlock{Kind: core.BlockToolUse, ID: callID, Name: open.name, Input: json.RawMessage(input)}, true
	}
	return core.ContentBlock{}, false
}

// mapStop maps an Anthropic stop reason to lana's vocabulary: end_turn,
// tool_use, and max_tokens pass through; stop_sequence ends the turn; a
// context-window stop counts as max_tokens; refusal and pause_turn pass
// through as they are.
func mapStop(reason string) string {
	switch reason {
	case "", "end_turn", "stop_sequence":
		return "end_turn"
	case "model_context_window_exceeded":
		return "max_tokens"
	default:
		return reason
	}
}

// idleReader wraps a response body so a read that stays idle longer than the
// timeout fails instead of blocking forever; readSSE maps the error to a
// network class error. Each Read arms a fresh timer.
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

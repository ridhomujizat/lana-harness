package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

type chunkDelta struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type chunkChoice struct {
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptCacheHitToken int64 `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// cachedTokens returns the cached prompt tokens from either provider shape.
func (u chunkUsage) cachedTokens() int64 {
	if u.PromptCacheHitToken > 0 {
		return u.PromptCacheHitToken
	}
	return u.PromptTokensDetails.CachedTokens
}

type chunk struct {
	Choices []chunkChoice `json:"choices"`
	Usage   *chunkUsage   `json:"usage"`
}

type toolAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

func (a *Adapter) readSSE(ctx context.Context, body io.ReadCloser) core.ChatStream {
	return func(yield func(core.ChatEvent, error) bool) {
		defer func() { _ = body.Close() }()
		reader := newIdleReader(body, a.streamIdle)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		acc := map[int]*toolAccumulator{}
		var finish string
		var usage *chunkUsage
		sawDone := false
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				yield(core.ChatEvent{}, err)
				return
			}
			line := strings.TrimSuffix(scanner.Text(), "\r")
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				sawDone = true
				break
			}
			var c chunk
			if err := json.Unmarshal([]byte(data), &c); err != nil {
				yield(core.ChatEvent{}, &core.ProviderError{Class: "server", Message: "invalid response"})
				return
			}
			if c.Usage != nil {
				usage = c.Usage
			}
			for _, choice := range c.Choices {
				if choice.Delta.Content != "" {
					if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: choice.Delta.Content}, nil) {
						return
					}
				}
				for _, call := range choice.Delta.ToolCalls {
					entry := acc[call.Index]
					if entry == nil {
						entry = &toolAccumulator{}
						acc[call.Index] = entry
					}
					if call.ID != "" {
						entry.id = call.ID
					}
					if call.Function.Name != "" {
						entry.name = call.Function.Name
					}
					entry.arguments.WriteString(call.Function.Arguments)
				}
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					finish = *choice.FinishReason
				}
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
		if !sawDone && finish == "" {
			yield(core.ChatEvent{}, &core.ProviderError{Class: "network", Message: "stream disconnected before finish_reason"})
			return
		}
		indexes := make([]int, 0, len(acc))
		for index := range acc {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			entry := acc[index]
			callID := entry.id
			if callID == "" {
				callID = id.New()
			}
			args := entry.arguments.String()
			if args == "" {
				args = "{}"
			}
			block := core.ContentBlock{Kind: core.BlockToolUse, ID: callID, Name: entry.name, Input: json.RawMessage(args)}
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: block}, nil) {
				return
			}
		}
		if usage != nil {
			coreUsage := core.Usage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, CachedInputTokens: usage.cachedTokens()}
			if !yield(core.ChatEvent{Kind: core.ChatUsage, Usage: coreUsage}, nil) {
				return
			}
		}
		if finish != "" || sawDone {
			if !yield(core.ChatEvent{Kind: core.ChatStop, StopReason: mapStop(finish)}, nil) {
				return
			}
		}
	}
}

func mapStop(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "":
		return "end_turn"
	default:
		return reason
	}
}

// idleReader wraps a response body so a read that stays idle longer than the
// timeout fails instead of blocking forever; the error from Read is mapped to
// a network class error by readSSE. Each Read arms a fresh timer, so any
// arriving byte resets the idle window.
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
			if r.err != nil {
				return 0, r.err
			}
			return 0, nil
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

func (r *idleReader) Close() error { return r.body.Close() }

var _ = errors.Is
var _ = fmt.Sprintf

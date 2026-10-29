// Package mock menyediakan provider LLM palsu untuk test.
package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Script adalah daftar respons untuk setiap panggilan Stream.
type Script struct {
	Responses []Response `json:"responses"`
}

// Response adalah satu respons skrip.
type Response struct {
	Events []Event      `json:"events,omitempty"`
	Error  *ScriptError `json:"error,omitempty"`
}

// ScriptError adalah error terminal di skrip.
type ScriptError struct {
	Class   string `json:"class"`
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
}

// ToolUse adalah tool call di skrip.
type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Event adalah satu event skrip; tepat satu field terisi.
type Event struct {
	Text    *string     `json:"text,omitempty"`
	DelayMS *int        `json:"delay_ms,omitempty"`
	ToolUse *ToolUse    `json:"tool_use,omitempty"`
	Usage   *core.Usage `json:"usage,omitempty"`
	Stop    *string     `json:"stop,omitempty"`
}

var validClasses = map[string]bool{
	"auth": true, "rate_limit": true, "server": true, "network": true,
	"context_length": true, "client": true, "missing_secret": true,
}

// Llm adalah provider palsu.
type Llm struct {
	mu     sync.Mutex
	next   int
	script Script
}

// New membuat Llm dari skrip.
func New(script Script) *Llm { return &Llm{script: script} }

// LoadScript membaca skrip JSON dan menolak key tidak dikenal.
func LoadScript(path string) (Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Script{}, err
	}
	var script Script
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&script); err != nil {
		return Script{}, err
	}
	for i := range script.Responses {
		resp := &script.Responses[i]
		for j := range resp.Events {
			ev := &resp.Events[j]
			count := 0
			if ev.Text != nil {
				count++
			}
			if ev.DelayMS != nil {
				count++
			}
			if ev.ToolUse != nil {
				count++
			}
			if ev.Usage != nil {
				count++
			}
			if ev.Stop != nil {
				count++
			}
			if count != 1 {
				return Script{}, fmt.Errorf("events[%d] harus punya tepat satu key", j)
			}
			if ev.DelayMS != nil && *ev.DelayMS < 0 {
				return Script{}, fmt.Errorf("delay_ms tidak boleh negatif")
			}
		}
		if resp.Error != nil && !validClasses[resp.Error.Class] {
			return Script{}, fmt.Errorf("class '%s' tidak dikenal", resp.Error.Class)
		}
	}
	return script, nil
}

// Stream memutar respons berikutnya.
func (l *Llm) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	l.mu.Lock()
	if l.next >= len(l.script.Responses) {
		l.mu.Unlock()
		return nil, fmt.Errorf("skrip habis")
	}
	resp := l.script.Responses[l.next]
	l.next++
	l.mu.Unlock()
	return func(yield func(core.ChatEvent, error) bool) {
		for _, ev := range resp.Events {
			if ev.DelayMS != nil {
				timer := time.NewTimer(time.Duration(*ev.DelayMS) * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					yield(core.ChatEvent{}, ctx.Err())
					return
				case <-timer.C:
				}
				continue
			}
			if ev.Text != nil {
				if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: *ev.Text}, nil) {
					return
				}
			}
			if ev.ToolUse != nil {
				input := ev.ToolUse.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				callID := ev.ToolUse.ID
				if callID == "" {
					callID = "call_mock"
				}
				block := core.ContentBlock{Kind: core.BlockToolUse, ID: callID, Name: ev.ToolUse.Name, Input: input}
				if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: block}, nil) {
					return
				}
			}
			if ev.Usage != nil {
				if !yield(core.ChatEvent{Kind: core.ChatUsage, Usage: *ev.Usage}, nil) {
					return
				}
			}
			if ev.Stop != nil {
				if !yield(core.ChatEvent{Kind: core.ChatStop, StopReason: *ev.Stop}, nil) {
					return
				}
			}
		}
		if resp.Error != nil {
			yield(core.ChatEvent{}, &core.ProviderError{Class: resp.Error.Class, Status: resp.Error.Status, Message: resp.Error.Message})
		}
	}, nil
}

var _ core.Provider = (*Llm)(nil)

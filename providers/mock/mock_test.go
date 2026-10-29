package mock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestMockScriptOrder(t *testing.T) {
	script := Script{Responses: []Response{
		{Events: []Event{
			{Text: ptr("Hello")},
			{DelayMS: ptr(5)},
			{Text: ptr(" world")},
			{ToolUse: &ToolUse{ID: "call_1", Name: "read_file", Input: []byte(`{"path":"a.txt"}`)}},
			{Usage: &core.Usage{InputTokens: 10, OutputTokens: 4}},
			{Stop: ptr("tool_use")},
		}},
		{Error: &ScriptError{Class: "rate_limit", Message: "429 from mock"}},
	}}
	llm := New(script)
	var kinds []core.ChatEventKind
	stream, err := llm.Stream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for ev, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, ev.Kind)
		if ev.Kind == core.ChatBlock && ev.Block.Kind != core.BlockToolUse {
			t.Fatalf("block=%+v", ev.Block)
		}
	}
	want := []core.ChatEventKind{core.ChatTextDelta, core.ChatTextDelta, core.ChatBlock, core.ChatUsage, core.ChatStop}
	if len(kinds) != len(want) {
		t.Fatalf("kinds=%v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds=%v", kinds)
		}
	}
	stream, err = llm.Stream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, err := range stream {
		if err != nil {
			gotErr = err
		}
	}
	var pe *core.ProviderError
	if !errors.As(gotErr, &pe) || pe.Class != "rate_limit" {
		t.Fatalf("error=%v", gotErr)
	}
}

func TestMockScriptExhausted(t *testing.T) {
	llm := New(Script{})
	if _, err := llm.Stream(context.Background(), core.ChatRequest{}); err == nil || !strings.Contains(err.Error(), "script exhausted") {
		t.Fatalf("error=%v", err)
	}
}

func TestMockDelayHonorsContext(t *testing.T) {
	llm := New(Script{Responses: []Response{{Events: []Event{{DelayMS: ptr(5000)}}}}})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := llm.Stream(ctx, core.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for _, err := range stream {
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("delay did not stop when ctx was cancelled")
	}
}

func TestLoadScriptRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := write(path, `{"responses":[{"events":[{"txt":"x"}]}]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScript(path); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func ptr[T any](v T) *T { return &v }

func write(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }

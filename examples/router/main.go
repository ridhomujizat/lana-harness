// Command router shows the router package used as a library: a router built
// from a Go struct with a mock kind, a stream by combo name, and the call
// record (AC-RTR-023). It runs offline: go run ./examples/router
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// mockAdapter answers every request with a fixed reply and usage.
type mockAdapter struct{ name string }

func (m mockAdapter) Stream(_ context.Context, req core.ChatRequest) (core.ChatStream, error) {
	reply := fmt.Sprintf("Hello from %s/%s.", m.name, req.Model)
	return func(yield func(core.ChatEvent, error) bool) {
		for _, word := range strings.SplitAfter(reply, " ") {
			if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: word}, nil) {
				return
			}
		}
		usage := core.Usage{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 200}
		if !yield(core.ChatEvent{Kind: core.ChatUsage, Usage: usage}, nil) {
			return
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (mockAdapter) ListModels(context.Context) ([]router.ModelInfo, error) {
	return []router.ModelInfo{{ID: "gpt-5-mini"}}, nil
}

// printRecorder keeps the records; Record must not block.
type printRecorder struct {
	mu      sync.Mutex
	records []router.CallRecord
}

func (p *printRecorder) Record(rec router.CallRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, rec)
}

func main() {
	router.RegisterKind("mock", func(name string, _ core.ProviderConfig, _ router.Deps) (router.Adapter, error) {
		return mockAdapter{name: name}, nil
	})
	rec := &printRecorder{}
	r, err := router.New(router.Config{
		Providers: map[string]core.ProviderConfig{"local": {Kind: "mock"}},
		Combos:    map[string]router.Combo{"fast": {Description: "the mock model", Models: []string{"local/gpt-5-mini"}}},
	}, router.WithRecorder(rec))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = r.Close() }()

	req := core.ChatRequest{Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}}}}
	stream, err := r.Stream(context.Background(), "fast", req, router.CallMeta{Source: "example"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var reply strings.Builder
	for event, err := range stream {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if event.Kind == core.ChatTextDelta {
			reply.WriteString(event.Text)
		}
	}
	fmt.Println("reply:", reply.String())
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, record := range rec.records {
		data, _ := json.Marshal(record)
		fmt.Println("record:", string(data))
	}
}

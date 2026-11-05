// Package memcapture runs one background memory-capture pass after a turn.
package memcapture

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	"github.com/ridhomujizat/lana-harness/internal/secretlike"
)

// captureTimeout bounds one pass; a pass still running at daemon shutdown is
// cancelled through the service base context (REQ-MEM-015).
const captureTimeout = 90 * time.Second

// captureMaxTokens caps the capture reply.
const captureMaxTokens = 1024

// maxOps is the most operations a reply may contribute (REQ-MEM-012).
const maxOps = 5

// maxContentRunes is the longest add/replace content (REQ-MEM-013).
const maxContentRunes = 300

// Service implements core.MemoryCapturer.
type Service struct {
	base  context.Context
	Store core.SessionStore
	LLM   core.LLMRegistry
	// LookupAgent returns the live agent config. It mirrors the compaction
	// service so both bind to the registry the same way.
	LookupAgent func(name string) (core.AgentConfig, bool)

	mu       sync.Mutex
	inFlight map[string]bool
}

// NewService creates a Service whose passes derive from base.
func NewService(base context.Context, store core.SessionStore, llm core.LLMRegistry) *Service {
	if base == nil {
		base = context.Background()
	}
	return &Service{base: base, Store: store, LLM: llm, inFlight: map[string]bool{}}
}

// Capture starts one background pass and returns at once. A pass already running
// for the session makes this a no-op (REQ-MEM-015).
func (s *Service) Capture(req core.CaptureRequest) {
	if req.SessionID == "" || req.Home == "" {
		return
	}
	s.mu.Lock()
	if s.inFlight == nil {
		s.inFlight = map[string]bool{}
	}
	if s.inFlight[req.SessionID] {
		s.mu.Unlock()
		return
	}
	s.inFlight[req.SessionID] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inFlight, req.SessionID)
			s.mu.Unlock()
		}()
		s.run(req)
	}()
}

// run executes one pass. Every failure is logged and dropped; it never surfaces
// as a turn error (REQ-MEM-013).
func (s *Service) run(req core.CaptureRequest) {
	ctx, cancel := context.WithTimeout(s.base, captureTimeout)
	defer cancel()

	events, err := s.Store.ChainEvents(ctx, req.SessionID)
	if err != nil {
		s.warn("read failed", req, err)
		return
	}
	history, err := core.DeriveMessages(events)
	if err != nil {
		s.warn("derive failed", req, err)
		return
	}
	// REQ-ATT-007: the capture transcript shows an image as its placeholder
	// text, never as image bytes.
	history = withoutImages(history)
	provider, ok := s.LLM.Provider(req.Provider)
	if !ok {
		s.warn("provider is not registered", req, nil)
		return
	}
	userEntries, _ := agenthome.ReadEntries(agenthome.SharedUser(req.Home))
	memoryEntries, _ := agenthome.ReadEntries(agenthome.Paths(req.Home, req.AgentName).Memory)
	messages := append(history, core.Message{Role: core.RoleUser, Content: []core.ContentBlock{
		{Kind: core.BlockText, Text: captureInstruction(userEntries, memoryEntries)},
	}})
	stream, err := provider.Stream(ctx, core.ChatRequest{
		Model: req.Model, System: req.System, Messages: messages, Tools: req.Tools, MaxTokens: captureMaxTokens, ReasoningEffort: req.ReasoningEffort,
	})
	if err != nil {
		s.warn("stream failed", req, err)
		return
	}
	text, usage, toolUse, err := collectReply(stream)
	if err != nil {
		s.warn("stream failed", req, err)
		return
	}
	if toolUse {
		slog.Debug("memory capture dropped: reply used a tool", "agent", req.AgentName, "session_id", req.SessionID, "turn_id", req.TurnID)
		return
	}
	ops, ok := parseOps(text)
	if !ok {
		slog.Debug("memory capture dropped: reply is not a JSON array", "agent", req.AgentName, "session_id", req.SessionID, "turn_id", req.TurnID)
		return
	}
	valid := make([]core.MemoryOp, 0, len(ops))
	for _, op := range ops {
		if !validOp(op) {
			slog.Debug("memory capture op skipped: invalid", "agent", req.AgentName, "session_id", req.SessionID, "turn_id", req.TurnID, "target", op.Target, "action", op.Action)
			continue
		}
		valid = append(valid, op)
	}
	if len(valid) > maxOps {
		valid = valid[:maxOps]
	}
	applied := make([]core.MemoryOp, 0, len(valid))
	for _, op := range valid {
		if !s.apply(req, op) {
			continue
		}
		applied = append(applied, op)
	}
	if len(applied) == 0 {
		return
	}
	payload := core.MemoryCaptured{V: core.PayloadVersion, TurnID: req.TurnID, Ops: applied, Usage: usage}
	if _, err := s.Store.Append(ctx, req.SessionID, []core.EventDraft{{Kind: core.KindMemoryCaptured, TurnID: req.TurnID, Payload: payload}}); err != nil {
		s.warn("append failed", req, err)
		return
	}
	slog.Info("memory captured", "agent", req.AgentName, "session_id", req.SessionID, "turn_id", req.TurnID, "count", len(applied),
		"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "cached_input_tokens", usage.CachedInputTokens)
}

// apply writes one validated op to its memory file. An error is logged and the
// op is skipped (REQ-MEM-013).
func (s *Service) apply(req core.CaptureRequest, op core.MemoryOp) bool {
	path, limit, ok := memoryTarget(req.Home, req.AgentName, op.Target)
	if !ok {
		return false
	}
	if _, err := agenthome.Update(path, func(entries []string) ([]string, error) {
		return agenthome.Apply(entries, op.Action, op.Content, op.OldText, limit)
	}); err != nil {
		s.warn("op skipped", req, err)
		return false
	}
	return true
}

// warn logs one dropped pass with the shared attribute keys.
func (s *Service) warn(message string, req core.CaptureRequest, err error) {
	slog.Warn("memory capture "+message, "agent", req.AgentName, "session_id", req.SessionID, "turn_id", req.TurnID, "error", err)
}

// memoryTarget returns the path and limit of target, using the same files and
// limits as the memory_write tool.
func memoryTarget(home, agent, target string) (string, int, bool) {
	switch target {
	case "user":
		return agenthome.SharedUser(home), agenthome.UserLimit, true
	case "memory":
		return agenthome.Paths(home, agent).Memory, agenthome.MemoryLimit, true
	}
	return "", 0, false
}

// validOp reports whether one operation passes the capture validation
// (REQ-MEM-013).
func validOp(op core.MemoryOp) bool {
	if op.Target != "user" && op.Target != "memory" {
		return false
	}
	switch op.Action {
	case "add", "replace":
		if strings.TrimSpace(op.Content) == "" || utf8.RuneCountInString(op.Content) > maxContentRunes {
			return false
		}
	case "remove":
	default:
		return false
	}
	if strings.Contains(op.Content, agenthome.Separator) || strings.Contains(op.OldText, agenthome.Separator) {
		return false
	}
	return !secretlike.Match(op.Content) && !secretlike.Match(op.OldText)
}

// collectReply reads the reply text and usage, and reports whether the reply
// held any tool-use block. A tool-use block drops the pass (REQ-MEM-013).
func collectReply(stream core.ChatStream) (string, core.Usage, bool, error) {
	var text strings.Builder
	var usage core.Usage
	toolUse := false
	for event, err := range stream {
		if err != nil {
			return "", usage, false, err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			text.WriteString(event.Text)
		case core.ChatBlock:
			switch event.Block.Kind {
			case core.BlockToolUse:
				toolUse = true
			case core.BlockText:
				text.WriteString(event.Block.Text)
			}
		case core.ChatUsage:
			usage = event.Usage
		}
	}
	return text.String(), usage, toolUse, nil
}

// withoutImages returns a copy of messages with every image block replaced by
// its placeholder text and every tool-result image appended as a placeholder
// line, so the capture transcript carries no image bytes (REQ-ATT-007). The
// input and the stored events are not changed.
func withoutImages(messages []core.Message) []core.Message {
	out := make([]core.Message, len(messages))
	for i, message := range messages {
		blocks := make([]core.ContentBlock, len(message.Content))
		for j, block := range message.Content {
			switch block.Kind {
			case core.BlockImage:
				blocks[j] = core.ContentBlock{Kind: core.BlockText, Text: attach.Placeholder(block)}
			case core.BlockToolResult:
				if len(block.Images) > 0 {
					lines := make([]string, 0, len(block.Images))
					for _, image := range block.Images {
						lines = append(lines, attach.Placeholder(image))
					}
					extra := strings.Join(lines, "\n")
					if block.Output == "" {
						block.Output = extra
					} else {
						block.Output += "\n" + extra
					}
					block.Images = nil
				}
				blocks[j] = block
			default:
				blocks[j] = block
			}
		}
		out[i] = core.Message{Role: message.Role, Content: blocks}
	}
	return out
}

// parseOps strips a ```/```json fence and parses a JSON array of operations. It
// reports false when the reply is not a JSON array (REQ-MEM-013).
func parseOps(text string) ([]core.MemoryOp, bool) {
	trimmed := stripFence(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var ops []core.MemoryOp
	if err := json.Unmarshal([]byte(trimmed), &ops); err != nil {
		return nil, false
	}
	return ops, true
}

// stripFence removes one leading ```/```json fence and its closing fence.
func stripFence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	if i := strings.LastIndex(text, "```"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

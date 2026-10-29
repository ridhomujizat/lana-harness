// Package compaction meringkas riwayat session.
package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// ErrTurnActive dan ErrNothingToCompact berasal dari package store.
var (
	ErrTurnActive       = errors.New("masih ada turn yang berjalan")
	ErrNothingToCompact = errors.New("tidak ada yang bisa diringkas")
	ErrSessionChanged   = errors.New("session berubah saat compaction")
)

// Service mengimplementasikan core.Compactor.
type Service struct {
	Store  core.SessionStore
	LLM    core.LLMRegistry
	Config core.Config
}

// NewService membuat Service.
func NewService(s core.SessionStore, llm core.LLMRegistry, config core.Config) *Service {
	return &Service{Store: s, LLM: llm, Config: config}
}

// CompactNow meringkas session.
func (s *Service) CompactNow(ctx context.Context, sessionID string) (core.CompactionResult, error) {
	return s.compact(ctx, sessionID, false)
}

func (s *Service) compact(ctx context.Context, sessionID string, allowActive bool) (core.CompactionResult, error) {
	info, err := s.Store.Info(ctx, sessionID)
	if err != nil {
		return core.CompactionResult{}, err
	}
	if info.Status == "running" && !allowActive {
		return core.CompactionResult{}, ErrTurnActive
	}
	events, err := s.Store.ChainEvents(ctx, sessionID)
	if err != nil {
		return core.CompactionResult{}, err
	}
	if len(events) <= 1 {
		return core.CompactionResult{}, ErrNothingToCompact
	}
	history, err := core.DeriveMessages(events)
	if err != nil {
		return core.CompactionResult{}, err
	}
	providerName, model := s.resolveModel(info.Agent, events)
	if providerName == "" || model == "" {
		return core.CompactionResult{}, fmt.Errorf("model untuk compaction tidak ditemukan")
	}
	provider, ok := s.LLM.Provider(providerName)
	if !ok {
		return core.CompactionResult{}, fmt.Errorf("provider tidak dikenal: %s", providerName)
	}
	req := core.ChatRequest{
		Model:    model,
		System:   "Ringkas riwayat percakapan berikut secara padat, pertahankan keputusan dan fakta penting.",
		Messages: history,
	}
	stream, err := provider.Stream(ctx, req)
	if err != nil {
		return core.CompactionResult{}, err
	}
	var summary strings.Builder
	var usage core.Usage
	for event, err := range stream {
		if err != nil {
			return core.CompactionResult{}, err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			summary.WriteString(event.Text)
		case core.ChatBlock:
			if event.Block.Kind == core.BlockText {
				summary.WriteString(event.Block.Text)
			}
		case core.ChatUsage:
			usage = event.Usage
		}
	}
	text := strings.TrimSpace(summary.String())
	if text == "" {
		return core.CompactionResult{}, errors.New("ringkasan kosong")
	}
	before := info.LastSeq
	if current, err := s.Store.Info(ctx, sessionID); err != nil {
		return core.CompactionResult{}, err
	} else if current.LastSeq != before {
		return core.CompactionResult{}, ErrSessionChanged
	}
	tokensBefore := estimateTokens(history)
	tokensAfter := estimateTokens([]core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}}})
	if usage.InputTokens > 0 {
		tokensBefore = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		tokensAfter = usage.OutputTokens
	}
	payload := core.CompactionSummary{V: core.PayloadVersion, Summary: text, UptoSeq: before, TokensBefore: tokensBefore, TokensAfter: tokensAfter, Model: model}
	if _, err := s.Store.Append(ctx, sessionID, []core.EventDraft{{Kind: core.KindCompactionSummary, Payload: payload}}); err != nil {
		return core.CompactionResult{}, err
	}
	return core.CompactionResult{UptoSeq: before, TokensBefore: tokensBefore, TokensAfter: tokensAfter}, nil
}

func (s *Service) resolveModel(agentName string, events []core.SessionEvent) (string, string) {
	provider, model := "", ""
	if agent, ok := s.Config.Agents[agentName]; ok {
		provider, model = agent.Provider, agent.Model
	}
	for _, event := range events {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		if payload.Agent != "" {
			if agent, ok := s.Config.Agents[payload.Agent]; ok {
				provider, model = agent.Provider, agent.Model
			}
		}
		if payload.Provider != "" {
			provider = payload.Provider
		}
		if payload.Model != "" {
			model = payload.Model
		}
	}
	return provider, model
}

func estimateTokens(messages []core.Message) int64 {
	var count int64
	for _, message := range messages {
		for _, block := range message.Content {
			count += int64(len(block.Text) + len(block.Output))
		}
	}
	return count / 4
}

// Install memasang listener otomatis.
func (s *Service) Install(hooks *core.Hooks) {
	hooks.AgentPreStep.Use(s.preStep)
	hooks.LLMStream.Use(s.stream, core.WithPrepend())
}

func (s *Service) preStep(ctx context.Context, in core.PreStepInput, next core.Next[core.PreStepInput, core.PreStepDecision]) (core.PreStepDecision, error) {
	events, err := s.Store.ChainEvents(ctx, in.SessionID)
	if err != nil {
		return next(ctx, in)
	}
	var usage core.Usage
	for _, event := range events {
		if event.Kind != core.KindStepEnd {
			continue
		}
		var payload core.StepEnd
		if json.Unmarshal(event.Payload, &payload) == nil {
			usage = payload.Usage
		}
	}
	window := int64(128000)
	if agent, ok := s.Config.Agents[in.AgentName]; ok && agent.ContextWindow > 0 {
		window = int64(agent.ContextWindow)
	}
	if usage.InputTokens > 0 && usage.InputTokens*100 > window*70 {
		if _, err := s.compact(ctx, in.SessionID, true); err != nil {
			slog.Warn("compaction otomatis gagal", "session_id", in.SessionID, "error", err)
		}
	}
	return next(ctx, in)
}

func (s *Service) stream(ctx context.Context, in core.LLMStreamInput, next core.Next[core.LLMStreamInput, core.ChatStream]) (core.ChatStream, error) {
	stream, err := next(ctx, in)
	if err == nil || !errors.Is(err, core.ErrContextTooLong) {
		return stream, err
	}
	if _, compactErr := s.compact(ctx, in.SessionID, true); compactErr != nil {
		slog.Warn("compaction retry gagal", "session_id", in.SessionID, "error", compactErr)
		return nil, err
	}
	events, chainErr := s.Store.ChainEvents(ctx, in.SessionID)
	if chainErr != nil {
		return nil, err
	}
	messages, deriveErr := core.DeriveMessages(events)
	if deriveErr != nil {
		return nil, err
	}
	in.Request.Messages = messages
	return next(ctx, in)
}

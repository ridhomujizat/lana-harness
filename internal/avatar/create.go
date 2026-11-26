package avatar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// Result sources (API-PXA-002).
const (
	SourceModel = "model"
	SourceLocal = "local"
)

// CallSource is the router call-record source of a generation (REQ-PXA-007).
const CallSource = llmcall.SourceAvatar

// AI modes of a generation (API-PXA-002).
const (
	// ModeAIParts asks a model to pick parts from a description.
	ModeAIParts = "ai_parts"
	// ModeAIDraw asks a model to draw a sprite pixel by pixel.
	ModeAIDraw = "ai_draw"
)

// Bounds of a model generation (REQ-PXA-007).
const (
	// attemptTimeout bounds one model call. A Claude Code or Antigravity
	// target starts its CLI first (spec 78 REQ-RPR-008), so it gets 120 s.
	attemptTimeout = 120 * time.Second
	// partsMaxTokens caps the answer of the parts mode.
	partsMaxTokens = 1200
	// drawMaxTokens caps the answer of the free-draw mode.
	drawMaxTokens = 4000
	// maxReply refuses a reply larger than this (64 KB).
	maxReply = 64 << 10
	// maxRetryReply bounds the refused reply echoed back to the model on a
	// retry (12 KB, REQ-PXA-007).
	maxRetryReply = 12 << 10
)

// ErrUnknownModel is returned when the requested model names no combo or known
// provider address.
var ErrUnknownModel = llmcall.ErrUnknownModel

// ErrInvalidMode is returned when Request.Mode is neither ModeAIParts nor
// ModeAIDraw (API-PXA-002).
var ErrInvalidMode = errors.New("unknown avatar generation mode")

// Request is one generation.
type Request struct {
	// Agent is the agent name, for the call record.
	Agent string
	// Config is the agent's config; its background chain is used when Model
	// is empty.
	Config core.AgentConfig
	// Mode is ModeAIParts or ModeAIDraw.
	Mode string
	// Model is a combo or "provider/model" address, or "" for the agent's
	// background model.
	Model string
	// Prompt is the owner's description, may be empty.
	Prompt string
	// Seed picks the local parts and, later, the model answer.
	Seed int64
	// Color is the agent colour, a token or "#rrggbb"; it overrides the seed
	// pick when set.
	Color string
}

// Result is a generated avatar (API-PXA-002).
type Result struct {
	Avatar Document
	// Model is the address that answered, "" for a local result.
	Model  string
	Seed   int64
	Source string
}

// Create generates an avatar (REQ-PXA-005..007). An unknown mode is refused
// and a seed of 0 draws a fresh one. It asks the model (or the agent's
// background chain) for parts or a drawn sprite; a failed call or an unusable
// answer is retried once, and then the local parts answer. A retry after an
// answer that was refused hands the reply and the reason back to the model; a
// retry after a call error sends a fresh prompt with a new seed. Without any
// non-Claude model the local parts answer at once. Only a requested model that
// is Claude Code or unknown is an error.
func Create(ctx context.Context, llm core.LLMRegistry, req Request) (Result, error) {
	if req.Mode != ModeAIParts && req.Mode != ModeAIDraw {
		return Result{}, ErrInvalidMode
	}
	if req.Seed == 0 {
		req.Seed = rand.Int64N(int64(maxSeed)) + 1
	}
	chain, requested, err := chainFor(llm, req)
	if err != nil {
		if req.Model != "" {
			return Result{}, err
		}
		slog.Debug("avatar: no background model, using the local parts", "agent", req.Agent, "error", err)
		return localResult(req.Color, req.Seed)
	}
	seed := req.Seed
	var retry *retryState
	for attempt := 0; attempt < 2; attempt++ {
		var res Result
		var reply string
		if req.Mode == ModeAIDraw {
			res, reply, err = drawOnce(ctx, llm, chain, requested, req, seed, retry)
		} else {
			res, reply, err = partsOnce(ctx, llm, chain, requested, req, seed, retry)
		}
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		slog.Warn("avatar: model attempt failed", "agent", req.Agent, "mode", req.Mode, "attempt", attempt+1, "error", err)
		if reply != "" {
			retry = &retryState{prompt: modeUserPrompt(req, seed), reply: reply, reason: err.Error()}
		} else {
			retry = nil
		}
		seed = NextSeed(seed)
	}
	slog.Warn("avatar: no usable model answer, using the local parts", "agent", req.Agent, "mode", req.Mode)
	return localResult(req.Color, req.Seed)
}

// retryState carries a refused model answer into the retry (REQ-PXA-006,
// REQ-PXA-007): the prompt of the failed attempt, its reply, and the reason it
// was refused.
type retryState struct {
	prompt string
	reply  string
	reason string
}

// attemptMessages is the conversation of one model call: the prompt alone on
// the first attempt, else the failed prompt, the refused reply, and the
// refusal (REQ-PXA-006, REQ-PXA-007).
func attemptMessages(req Request, seed int64, retry *retryState) []core.Message {
	if retry == nil {
		return []core.Message{textMessage(core.RoleUser, modeUserPrompt(req, seed))}
	}
	return []core.Message{
		textMessage(core.RoleUser, retry.prompt),
		textMessage(core.RoleAssistant, trimReply(retry.reply)),
		textMessage(core.RoleUser, refusalPrompt(retry.reason)),
	}
}

// textMessage wraps text in a one-block message.
func textMessage(role core.Role, text string) core.Message {
	return core.Message{Role: role, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}}
}

// trimReply bounds the reply echoed back to the model on a retry.
func trimReply(reply string) string {
	if len(reply) > maxRetryReply {
		return reply[:maxRetryReply]
	}
	return reply
}

// partsOnce asks the model for parts and fills them from RandomParts(seed)
// (REQ-PXA-005). The owner's colour overrides the seed's. It returns the reply
// so the caller can hand it back on a retry.
func partsOnce(ctx context.Context, llm core.LLMRegistry, chain []core.LLMTarget, requested string, req Request, seed int64, retry *retryState) (Result, string, error) {
	reply, served, err := streamOnce(ctx, llm, chain, requested, req.Agent, core.ChatRequest{
		System:          partsSystemPrompt(),
		Messages:        attemptMessages(req, seed, retry),
		MaxTokens:       partsMaxTokens,
		ReasoningEffort: "off",
	})
	if err != nil {
		return Result{}, "", err
	}
	p, err := parseParts(reply, seed)
	if err != nil {
		return Result{}, reply, err
	}
	if req.Color != "" {
		p.Color = req.Color
	}
	sprite, err := Render(p)
	if err != nil {
		return Result{}, reply, err
	}
	return Result{
		Avatar: Document{Kind: KindParts, Parts: &p, Sprite: sprite},
		Model:  served,
		Seed:   seed,
		Source: SourceModel,
	}, reply, nil
}

// drawOnce asks the model for a sprite and validates it (REQ-PXA-006). It
// returns the reply so the caller can hand it back on a retry.
func drawOnce(ctx context.Context, llm core.LLMRegistry, chain []core.LLMTarget, requested string, req Request, seed int64, retry *retryState) (Result, string, error) {
	reply, served, err := streamOnce(ctx, llm, chain, requested, req.Agent, core.ChatRequest{
		System:          drawSystemPrompt(req.Agent, req.Color),
		Messages:        attemptMessages(req, seed, retry),
		MaxTokens:       drawMaxTokens,
		ReasoningEffort: "off",
	})
	if err != nil {
		return Result{}, "", err
	}
	sprite, err := parseDrawn(reply, DefaultFor(req.Agent, req.Color).Sprite.Palette)
	if err != nil {
		return Result{}, reply, err
	}
	return Result{
		Avatar: Document{Kind: KindDrawn, Sprite: sprite, Prompt: trimPrompt(req.Prompt)},
		Model:  served,
		Seed:   seed,
		Source: SourceModel,
	}, reply, nil
}

// streamOnce makes one model call and collects its text (REQ-PXA-007). It
// returns the reply and the address that answered.
func streamOnce(ctx context.Context, llm core.LLMRegistry, chain []core.LLMTarget, requested, agent string, req core.ChatRequest) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	stream, target, err := llmcall.Stream(ctx, llm, chain, req,
		core.LLMCallMeta{Source: CallSource, Agent: agent, Requested: requested}, nil)
	if err != nil {
		return "", "", err
	}
	var text strings.Builder
	for event, err := range stream {
		if err != nil {
			return "", "", err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			text.WriteString(event.Text)
		case core.ChatBlock:
			if event.Block.Kind == core.BlockText {
				text.WriteString(event.Block.Text)
			}
		}
		if text.Len() > maxReply {
			return "", "", errors.New("the reply is too long")
		}
	}
	return text.String(), llmcall.Address(target), nil
}

// localResult is the local fallback: the seeded parts rendered, source local.
func localResult(color string, seed int64) (Result, error) {
	p := RandomParts(seed)
	if color != "" {
		p.Color = color
	}
	sprite, err := Render(p)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Avatar: Document{Kind: KindParts, Parts: &p, Sprite: sprite},
		Seed:   seed,
		Source: SourceLocal,
	}, nil
}

// chainFor resolves the chain of a request: the requested model's chain as it
// is, Claude Code and Antigravity targets included (spec 78 REQ-RPR-008: the
// router runs them through their CLIs), else the agent's background chain.
func chainFor(llm core.LLMRegistry, req Request) ([]core.LLMTarget, string, error) {
	if req.Model == "" {
		return llmcall.AgentBackgroundChain(llm, req.Config)
	}
	chain, err := llmcall.Chain(llm, "", req.Model, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %q", ErrUnknownModel, req.Model)
	}
	return chain, req.Model, nil
}

// NextSeed derives a new seed from seed (splitmix64), kept in 1..2^31-1 so it
// survives a JSON number in any client.
func NextSeed(seed int64) int64 {
	z := uint64(seed) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	return int64(z%(1<<31-1)) + 1
}

package lana

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/internal/avatar"
)

// AvatarResult is a generated agent avatar (spec 43, API-AVT-001): the
// sanitized SVG, the model address that drew it ("" for the local
// generator), the seed, and the source ("model" or "local").
type AvatarResult = avatar.Result

// Avatar generation errors (spec 43).
var (
	// ErrAvatarClaudeCode is returned when the requested model runs in
	// Claude Code (REQ-AVT-002).
	ErrAvatarClaudeCode = avatar.ErrClaudeCodeModel
	// ErrAvatarUnknownModel is returned when the requested model names no
	// combo or known provider address.
	ErrAvatarUnknownModel = avatar.ErrUnknownModel
)

// GenerateAvatar draws an avatar for an agent (REQ-AVT-002, REQ-AVT-005):
// model is a combo or address, or "" for the agent's background chain. The
// call goes through the router with source "avatar". A failed or invalid
// model answer falls back to the local generator; nothing is stored.
func (rt *Runtime) GenerateAvatar(ctx context.Context, agentName, model string, seed int64) (AvatarResult, error) {
	cfg, ok := rt.agents()[agentName]
	if !ok {
		return AvatarResult{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	return avatar.Create(ctx, rt.coreCtx.LLM, avatar.Request{Agent: agentName, Config: cfg, Model: model, Seed: seed})
}

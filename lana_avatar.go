package lana

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/internal/avatar"
)

// AvatarResult is a generated agent avatar (spec 62, API-PXA-002): the avatar
// document, the model address that drew it ("" for the local generator), the
// seed, and the source ("model" or "local").
type AvatarResult = avatar.Result

// Avatar generation errors (spec 62).
var (
	// ErrAvatarClaudeCode is returned when the requested model runs in
	// Claude Code (REQ-PXA-007).
	ErrAvatarClaudeCode = avatar.ErrClaudeCodeModel
	// ErrAvatarUnknownModel is returned when the requested model names no
	// combo or known provider address.
	ErrAvatarUnknownModel = avatar.ErrUnknownModel
)

// GenerateAvatar draws an avatar for an agent (REQ-PXA-005..007): mode is
// avatar.ModeAIParts or avatar.ModeAIDraw, model is a combo or address, or ""
// for the agent's background chain, and prompt is the owner's description.
// color is the agent colour the sprite renders with. The call goes through
// the router with source "avatar"; a failed or invalid model answer falls back
// to the local generator. Nothing is stored.
func (rt *Runtime) GenerateAvatar(ctx context.Context, agentName, mode, model, prompt string, seed int64, color string) (AvatarResult, error) {
	cfg, ok := rt.agents()[agentName]
	if !ok {
		return AvatarResult{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	return avatar.Create(ctx, rt.coreCtx.LLM, avatar.Request{
		Agent: agentName, Config: cfg, Mode: mode, Model: model, Prompt: prompt, Seed: seed, Color: color,
	})
}

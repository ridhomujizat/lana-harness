package main

import (
	"context"
	"errors"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/internal/avatar"
	"github.com/ridhomujizat/lana-harness/web"
)

var _ web.AvatarBackend = (*webBackend)(nil)

// GenerateAvatar implements web.AvatarBackend (spec 62, API-PXA-002).
func (b *webBackend) GenerateAvatar(ctx context.Context, agent, mode, model, prompt string, seed int64, color string) (avatar.Result, error) {
	result, err := b.rt.GenerateAvatar(ctx, agent, mode, model, prompt, seed, color)
	if errors.Is(err, lana.ErrUnknownAgent) {
		return avatar.Result{}, web.ErrUnknownAgent
	}
	return result, err
}

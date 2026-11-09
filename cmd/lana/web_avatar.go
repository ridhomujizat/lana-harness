package main

import (
	"context"
	"errors"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/internal/avatar"
	"github.com/ridhomujizat/lana-harness/web"
)

var _ web.AvatarBackend = (*webBackend)(nil)

// GenerateAvatar implements web.AvatarBackend (spec 43, API-AVT-001).
func (b *webBackend) GenerateAvatar(ctx context.Context, agent, model string, seed int64) (avatar.Result, error) {
	result, err := b.rt.GenerateAvatar(ctx, agent, model, seed)
	if errors.Is(err, lana.ErrUnknownAgent) {
		return avatar.Result{}, web.ErrUnknownAgent
	}
	return result, err
}

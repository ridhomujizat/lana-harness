package main

import (
	"context"
	"errors"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/web"
)

// SetSessionTitle implements web.SessionTitleBackend (spec 40, REQ-TTL-008).
func (b *webBackend) SetSessionTitle(ctx context.Context, id, title string) error {
	_, err := b.rt.SetSessionTitle(ctx, id, title)
	return titleError(err)
}

// RegenerateSessionTitle implements web.SessionTitleBackend (REQ-TTL-010).
func (b *webBackend) RegenerateSessionTitle(ctx context.Context, id string) error {
	_, err := b.rt.RegenerateSessionTitle(ctx, id)
	return titleError(err)
}

// TitlePending implements web.SessionTitleBackend (REQ-TTL-012).
func (b *webBackend) TitlePending(id string) bool {
	return b.rt.TitlePending(id)
}

// SessionTitle implements web.SessionTitleBackend (REQ-TTL-011).
func (b *webBackend) SessionTitle(ctx context.Context, id string) (string, string, error) {
	title, source, err := b.rt.SessionTitle(ctx, id)
	if err != nil {
		return "", "", sessionLookupError(err)
	}
	return title, source, nil
}

// titleError maps the runtime title errors to the web ones.
func titleError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, lana.ErrTitleBusy):
		return web.ErrTitleBusy
	case errors.Is(err, lana.ErrNothingToTitle):
		return web.ErrNothingToTitle
	case errors.Is(err, lana.ErrInvalidTitle):
		return web.ErrInvalidTitle
	default:
		return sessionLookupError(err)
	}
}

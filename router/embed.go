package router

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

// Embed embeds inputs with the model at address (REQ-RTR-007). A combo uses
// its first model only: vectors of different models do not mix, so there is
// no fallback. rate_limit, server, and network errors are retried like the
// last target of a chain. Each attempt is recorded with meta.Source, or
// "embedding" when it is empty; embedders report no usage, so the record has
// no tokens and no cost.
func (r *Router) Embed(ctx context.Context, address string, inputs []string, meta CallMeta) ([][]float32, error) {
	snap := r.snap.Load()
	chain, err := snap.resolve(address)
	if err != nil {
		return nil, err
	}
	target := chain[0]
	if meta.Source == "" {
		meta.Source = "embedding"
	}
	entry, ok := snap.providers[target.Provider]
	if !ok {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is not configured", target.Provider)}
	}
	embedder, ok := embedderOf(entry)
	if !ok {
		return nil, &core.ProviderError{Class: "client", Message: fmt.Sprintf("provider %q does not support embeddings", target.Provider)}
	}
	for n := 1; ; n++ {
		start := r.now()
		vectors, err := embedder.Embed(ctx, target.Model, inputs)
		rec := r.baseRecord(address, target, n, start, meta)
		rec.DurationMs = r.now().Sub(start).Milliseconds()
		if err == nil {
			rec.Outcome = "ok"
			r.emit(rec)
			return vectors, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pe := snap.redactError(r.opts.secrets, target.Provider, asProviderError(err))
		rec.Outcome = effectiveClass(pe)
		rec.Error = truncate(pe.Error(), maxRecordError)
		r.emit(rec)
		switch rec.Outcome {
		case "rate_limit", "server", "network":
			if n > retriesOnLast {
				return nil, pe
			}
			if err := r.sleep(ctx, r.backoff(n, pe)); err != nil {
				return nil, err
			}
		default:
			return nil, pe
		}
	}
}

func embedderOf(entry *providerEntry) (core.Embedder, bool) {
	if e, ok := entry.adapter.(core.Embedder); ok {
		return e, true
	}
	e, ok := entry.raw.(core.Embedder)
	return e, ok
}

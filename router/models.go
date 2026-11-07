package router

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/models"
)

const (
	// modelListTTL is how long a provider's model list is cached.
	modelListTTL = 24 * time.Hour
	// modelListErrorTTL is how long a failed model list fetch is remembered.
	modelListErrorTTL = 5 * time.Minute
)

// ModelInfo is one model of a provider (API-RTR-009).
type ModelInfo struct {
	ID            string `json:"id"`
	Address       string `json:"address"`
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
	MaxOutput     int    `json:"max_output"`
	Vision        bool   `json:"vision"`
	Thinking      bool   `json:"thinking"`
	// Price is nil when unknown.
	Price *Price `json:"price"`
	// Source is "catalog", "provider", or "both".
	Source string `json:"source"`
}

type modelCacheEntry struct {
	entry  *providerEntry
	at     time.Time
	models []ModelInfo
	err    error
}

type modelCache struct {
	mu      sync.Mutex
	entries map[string]modelCacheEntry
}

// cachedPrice returns the price the provider's own model list gave for
// model, or nil.
func (c *modelCache) cachedPrice(entry *providerEntry, model string) *Price {
	if entry == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, ok := c.entries[entry.name]
	if !ok || cached.entry != entry {
		return nil
	}
	for _, m := range cached.models {
		if m.ID == model && m.Price != nil {
			p := *m.Price
			return &p
		}
	}
	return nil
}

// Models lists the models of a provider: the catalog models of its kind
// merged with the models its models endpoint returns, cached for 24 hours
// (REQ-RTR-013). refresh fetches the list again. When the fetch fails, the
// catalog models are returned with the error.
func (r *Router) Models(ctx context.Context, provider string, refresh bool) ([]ModelInfo, error) {
	entry, ok := r.snap.Load().providers[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAddress, provider)
	}
	listed, fetchErr := r.listModels(ctx, entry, refresh)
	var catalog []models.Model
	if entry.kind == "openai_compat" {
		catalog = models.ForProviderKind(entry.cfg.BaseURL)
	} else {
		catalog = models.ForKind(entry.kind)
	}
	out := make([]ModelInfo, 0, len(catalog)+len(listed))
	index := map[string]int{}
	for _, m := range catalog {
		info := fromCatalog(m)
		info.Address = provider + "/" + m.ID
		info.Source = "catalog"
		index[m.ID] = len(out)
		out = append(out, info)
	}
	for _, m := range listed {
		if m.ID == "" {
			continue
		}
		if i, ok := index[m.ID]; ok {
			merged := out[i]
			merged.Source = "both"
			if m.Price != nil {
				merged.Price = m.Price
			}
			if merged.ContextWindow == 0 {
				merged.ContextWindow = m.ContextWindow
			}
			if merged.MaxOutput == 0 {
				merged.MaxOutput = m.MaxOutput
			}
			if merged.Name == "" {
				merged.Name = m.Name
			}
			merged.Vision = merged.Vision || m.Vision
			merged.Thinking = merged.Thinking || m.Thinking
			out[i] = merged
			continue
		}
		info := m
		info.Address = provider + "/" + m.ID
		info.Source = "provider"
		if known, ok := models.Lookup(m.ID); ok {
			fill := fromCatalog(known)
			if info.Name == "" {
				info.Name = fill.Name
			}
			if info.ContextWindow == 0 {
				info.ContextWindow = fill.ContextWindow
			}
			if info.MaxOutput == 0 {
				info.MaxOutput = fill.MaxOutput
			}
			if info.Price == nil {
				info.Price = fill.Price
			}
			info.Vision = info.Vision || fill.Vision
			info.Thinking = info.Thinking || fill.Thinking
		}
		if info.Name == "" {
			info.Name = m.ID
		}
		index[m.ID] = len(out)
		out = append(out, info)
	}
	return out, fetchErr
}

func (r *Router) listModels(ctx context.Context, entry *providerEntry, refresh bool) ([]ModelInfo, error) {
	now := r.now()
	c := &r.models
	c.mu.Lock()
	cached, ok := c.entries[entry.name]
	c.mu.Unlock()
	if ok && cached.entry == entry && !refresh {
		ttl := modelListTTL
		if cached.err != nil {
			ttl = modelListErrorTTL
		}
		if now.Sub(cached.at) < ttl {
			return cached.models, cached.err
		}
	}
	listed, err := entry.adapter.ListModels(ctx)
	if errors.Is(err, ErrNotSupported) {
		listed, err = nil, nil
	}
	if err != nil {
		listed = nil
	}
	c.mu.Lock()
	c.entries[entry.name] = modelCacheEntry{entry: entry, at: now, models: listed, err: err}
	c.mu.Unlock()
	return listed, err
}

func fromCatalog(m models.Model) ModelInfo {
	info := ModelInfo{
		ID:            m.ID,
		Name:          m.Name,
		ContextWindow: m.ContextWindow,
		MaxOutput:     m.MaxOutput,
		Vision:        m.Vision,
		Thinking:      m.Thinking != "",
	}
	if m.Price != nil {
		p := *m.Price
		info.Price = &p
	}
	return info
}

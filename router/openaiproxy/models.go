package openaiproxy

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"
)

// modelListTimeout bounds the provider model list fetches of GET /models.
const modelListTimeout = 15 * time.Second

type modelList struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// models serves GET /models: the combos first (owned_by "combo"), then every
// enabled provider's models as "provider/model" addresses. A provider whose model
// list cannot be fetched still lists its catalog models.
func (h *handler) models(w http.ResponseWriter, req *http.Request) {
	out := modelList{Object: "list", Data: []modelObject{}}
	seen := map[string]bool{}
	combos := h.r.Config().Combos
	names := make([]string, 0, len(combos))
	for name := range combos {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		seen[name] = true
		out.Data = append(out.Data, modelObject{ID: name, Object: "model", OwnedBy: "combo"})
	}

	ctx, cancel := context.WithTimeout(req.Context(), modelListTimeout)
	defer cancel()
	var providers []string
	for _, name := range h.r.Names() {
		// A disabled provider serves no calls, so it lists no models.
		if !h.r.Disabled(name) {
			providers = append(providers, name)
		}
	}
	lists := make([][]string, len(providers))
	var wg sync.WaitGroup
	for i, provider := range providers {
		wg.Go(func() {
			infos, err := h.r.Models(ctx, provider, false)
			if err != nil {
				h.cfg.logger.Debug("router proxy model list incomplete", "provider", provider, "error", err)
			}
			for _, info := range infos {
				address := info.Address
				if address == "" {
					address = provider + "/" + info.ID
				}
				lists[i] = append(lists[i], address)
			}
		})
	}
	wg.Wait()
	for i, provider := range providers {
		for _, address := range lists[i] {
			if seen[address] {
				continue
			}
			seen[address] = true
			out.Data = append(out.Data, modelObject{ID: address, Object: "model", OwnedBy: provider})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

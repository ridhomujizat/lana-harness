package llmcall

import (
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// REQ-MDL-010: ContextWindow resolves a chain model the static catalog does not
// know through the registered fallback (the provider's own model list) instead
// of the default 128000.
func TestContextWindowCatalogFallback(t *testing.T) {
	t.Cleanup(func() { models.SetFallback(nil) })
	models.SetFallback(func(address string) (models.Model, bool) {
		if address == "commandcode/vendor/some-model" {
			return models.Model{ID: "vendor/some-model", ContextWindow: 1000000, MaxOutput: 384000}, true
		}
		return models.Model{}, false
	})

	chain := []core.LLMTarget{{Provider: "commandcode", Model: "vendor/some-model"}}
	if got := ContextWindow(0, chain); got != 1000000 {
		t.Fatalf("ContextWindow = %d, want 1000000 (the fallback window)", got)
	}
	// The agent's own window still wins over the fallback.
	if got := ContextWindow(200000, chain); got != 200000 {
		t.Fatalf("agent window = %d, want 200000", got)
	}
	// Without a fallback the default applies.
	models.SetFallback(nil)
	if got := ContextWindow(0, chain); got != DefaultContextWindow {
		t.Fatalf("default window = %d, want %d", got, DefaultContextWindow)
	}
}

// Package models is the catalog of known model names and their defaults.
package models

import (
	"net/url"
	"strings"
)

// Model is one catalog entry (REQ-MDL-010).
type Model struct {
	ID            string
	Name          string
	Description   string
	ContextWindow int
	MaxOutput     int
	// Thinking is the thinking format the model uses ("deepseek" or "").
	Thinking string
	// DefaultEffort is the effort used when the agent sets none.
	DefaultEffort string
	Legacy        bool
	Replacement   string
}

// catalog is keyed by model ID.
var catalog = map[string]Model{
	"deepseek-flash": {
		ID:            "deepseek-flash",
		Name:          "DeepSeek Flash",
		Description:   "Fast DeepSeek model with thinking and a 1M-token context.",
		ContextWindow: 1048576,
		MaxOutput:     384000,
		Thinking:      "deepseek",
		DefaultEffort: "high",
	},
	"deepseek-v4-pro": {
		ID:            "deepseek-v4-pro",
		Name:          "DeepSeek V4 Pro",
		Description:   "DeepSeek V4 Pro with thinking and a 1M-token context.",
		ContextWindow: 1048576,
		MaxOutput:     384000,
		Thinking:      "deepseek",
		DefaultEffort: "high",
	},
	"deepseek-v4-flash": {
		ID:            "deepseek-v4-flash",
		Name:          "DeepSeek V4 Flash",
		Description:   "Accepted alias of deepseek-flash.",
		ContextWindow: 1048576,
		MaxOutput:     384000,
		Thinking:      "deepseek",
		DefaultEffort: "high",
	},
	"deepseek-chat": {
		ID:            "deepseek-chat",
		Name:          "DeepSeek Chat",
		Description:   "Legacy DeepSeek model served without thinking.",
		ContextWindow: 128000,
		MaxOutput:     8192,
		Legacy:        true,
		Replacement:   "deepseek-flash",
	},
	"deepseek-reasoner": {
		ID:            "deepseek-reasoner",
		Name:          "DeepSeek Reasoner",
		Description:   "Legacy DeepSeek reasoning model.",
		ContextWindow: 128000,
		MaxOutput:     8192,
		Thinking:      "deepseek",
		DefaultEffort: "high",
		Legacy:        true,
		Replacement:   "deepseek-flash",
	},
}

// Lookup returns the catalog entry for a model. An unknown model returns false;
// callers keep their current defaults.
func Lookup(model string) (Model, bool) {
	entry, ok := catalog[model]
	return entry, ok
}

// deepseekPicker is the picker order of the DeepSeek models. The
// deepseek-v4-flash alias stays out of the list (REQ-MDL-010).
var deepseekPicker = []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner"}

// ForProviderKind returns the catalog models a provider should offer in its
// picker. It returns the DeepSeek list when the base URL host is
// api.deepseek.com, and nil otherwise (REQ-MDL-020).
func ForProviderKind(baseURL string) []Model {
	parsed, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "api.deepseek.com") {
		return nil
	}
	models := make([]Model, 0, len(deepseekPicker))
	for _, id := range deepseekPicker {
		models = append(models, catalog[id])
	}
	return models
}

// Effort is one selectable reasoning effort with its description (REQ-MDL-001).
type Effort struct {
	ID          string
	Description string
}

// Efforts returns the reasoning efforts in picker order.
func Efforts() []Effort {
	return []Effort{
		{ID: "off", Description: "Use for simple tasks that do not need reasoning."},
		{ID: "low", Description: "Prefer for routine or latency-sensitive tasks."},
		{ID: "high", Description: "The default balance for most tasks."},
		{ID: "max", Description: "Reserve for the hardest quality-first tasks."},
	}
}

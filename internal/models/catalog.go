// Package models is the catalog of known model names and their defaults.
package models

import (
	"net/url"
	"strings"
)

// PricesAsOf is the date the catalog prices were last checked. Prices go
// stale; OpenRouter prices come live from its models API instead
// (REQ-RTR-010).
const PricesAsOf = "2026-11-08"

// Price is the USD price per one million tokens (REQ-RTR-010).
type Price struct {
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	Output      float64 `json:"output"`
}

// Model is one catalog entry (REQ-MDL-010).
type Model struct {
	ID            string
	Name          string
	Description   string
	ContextWindow int
	MaxOutput     int
	// Kind is the provider kind that serves the model natively: openai,
	// anthropic, or deepseek (REQ-RTR-001).
	Kind string
	// Thinking is the thinking format the model uses ("deepseek", "openai",
	// "anthropic", or "" for a model without thinking).
	Thinking string
	// DefaultEffort is the effort used when the agent sets none.
	DefaultEffort string
	// Vision marks models that accept image input (REQ-ATT-003).
	Vision bool
	// Price is nil when the price is unknown; cost is then shown as "—",
	// never as 0 (REQ-RTR-010).
	Price       *Price
	Legacy      bool
	Replacement string
	// Embedding marks embedding models; they are not chat models.
	Embedding bool
}

func price(input, cachedInput, output float64) *Price {
	return &Price{Input: input, CachedInput: cachedInput, Output: output}
}

// catalog is keyed by model ID.
var catalog = map[string]Model{
	// DeepSeek. The prices of the current DeepSeek models are not in the
	// catalog yet; their cost shows as unknown.
	"deepseek-flash": {
		ID:            "deepseek-flash",
		Name:          "DeepSeek Flash",
		Description:   "Fast DeepSeek model with thinking and a 1M-token context.",
		ContextWindow: 1048576,
		MaxOutput:     384000,
		Kind:          "deepseek",
		Thinking:      "deepseek",
		DefaultEffort: "high",
		Vision:        true,
	},
	"deepseek-v4-pro": {
		ID:            "deepseek-v4-pro",
		Name:          "DeepSeek V4 Pro",
		Description:   "DeepSeek V4 Pro with thinking and a 1M-token context.",
		ContextWindow: 1048576,
		MaxOutput:     384000,
		Kind:          "deepseek",
		Thinking:      "deepseek",
		DefaultEffort: "high",
	},
	"deepseek-v4-flash": {
		ID:            "deepseek-v4-flash",
		Name:          "DeepSeek V4 Flash",
		Description:   "Accepted alias of deepseek-flash.",
		ContextWindow: 1048576,
		MaxOutput:     384000,
		Kind:          "deepseek",
		Thinking:      "deepseek",
		DefaultEffort: "high",
		Vision:        true,
	},
	"deepseek-chat": {
		ID:            "deepseek-chat",
		Name:          "DeepSeek Chat",
		Description:   "Legacy DeepSeek model served without thinking.",
		ContextWindow: 128000,
		MaxOutput:     8192,
		Kind:          "deepseek",
		Legacy:        true,
		Replacement:   "deepseek-flash",
	},
	"deepseek-reasoner": {
		ID:            "deepseek-reasoner",
		Name:          "DeepSeek Reasoner",
		Description:   "Legacy DeepSeek reasoning model.",
		ContextWindow: 128000,
		MaxOutput:     8192,
		Kind:          "deepseek",
		Thinking:      "deepseek",
		DefaultEffort: "high",
		Legacy:        true,
		Replacement:   "deepseek-flash",
	},

	// OpenAI.
	"gpt-5.1": {
		ID:            "gpt-5.1",
		Name:          "GPT-5.1",
		Description:   "OpenAI GPT-5.1 reasoning model.",
		ContextWindow: 400000,
		MaxOutput:     128000,
		Kind:          "openai",
		Thinking:      "openai",
		Vision:        true,
		Price:         price(1.25, 0.125, 10),
	},
	"gpt-5": {
		ID:            "gpt-5",
		Name:          "GPT-5",
		Description:   "OpenAI GPT-5 reasoning model.",
		ContextWindow: 400000,
		MaxOutput:     128000,
		Kind:          "openai",
		Thinking:      "openai",
		Vision:        true,
		Price:         price(1.25, 0.125, 10),
	},
	"gpt-5-mini": {
		ID:            "gpt-5-mini",
		Name:          "GPT-5 mini",
		Description:   "Faster, cheaper GPT-5 for well-defined tasks.",
		ContextWindow: 400000,
		MaxOutput:     128000,
		Kind:          "openai",
		Thinking:      "openai",
		Vision:        true,
		Price:         price(0.25, 0.025, 2),
	},
	"gpt-5-nano": {
		ID:            "gpt-5-nano",
		Name:          "GPT-5 nano",
		Description:   "Fastest, cheapest GPT-5 for simple tasks.",
		ContextWindow: 400000,
		MaxOutput:     128000,
		Kind:          "openai",
		Thinking:      "openai",
		Vision:        true,
		Price:         price(0.05, 0.005, 0.4),
	},
	"gpt-4.1": {
		ID:            "gpt-4.1",
		Name:          "GPT-4.1",
		Description:   "OpenAI GPT-4.1 without reasoning, 1M-token context.",
		ContextWindow: 1047576,
		MaxOutput:     32768,
		Kind:          "openai",
		Vision:        true,
		Price:         price(2, 0.5, 8),
	},
	"gpt-4.1-mini": {
		ID:            "gpt-4.1-mini",
		Name:          "GPT-4.1 mini",
		Description:   "Smaller GPT-4.1 without reasoning.",
		ContextWindow: 1047576,
		MaxOutput:     32768,
		Kind:          "openai",
		Vision:        true,
		Price:         price(0.4, 0.1, 1.6),
	},
	"text-embedding-3-small": {
		ID:            "text-embedding-3-small",
		Name:          "text-embedding-3-small",
		Description:   "OpenAI embedding model, 1536 dimensions.",
		ContextWindow: 8191,
		Kind:          "openai",
		Embedding:     true,
		Price:         price(0.02, 0.02, 0),
	},
	"text-embedding-3-large": {
		ID:            "text-embedding-3-large",
		Name:          "text-embedding-3-large",
		Description:   "OpenAI embedding model, 3072 dimensions.",
		ContextWindow: 8191,
		Kind:          "openai",
		Embedding:     true,
		Price:         price(0.13, 0.13, 0),
	},

	// Anthropic (claude-api skill, models table cached 2026-10-06). Cached
	// input is the cache-read price.
	"claude-fable-5-1": {
		ID:            "claude-fable-5-1",
		Name:          "Claude Fable 5.1",
		Description:   "Anthropic's most capable model; thinking is always on.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(10, 0.25, 50),
	},
	"claude-fable-5": {
		ID:            "claude-fable-5",
		Name:          "Claude Fable 5",
		Description:   "Previous Fable release; thinking is always on.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(10, 1, 50),
	},
	"claude-opus-5-5": {
		ID:            "claude-opus-5-5",
		Name:          "Claude Opus 5.5",
		Description:   "The current Opus for long-running agentic coding; thinking is always on.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(4, 0.2, 20),
	},
	"claude-opus-5": {
		ID:            "claude-opus-5",
		Name:          "Claude Opus 5",
		Description:   "Previous Opus with adaptive thinking.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(5, 0.5, 25),
	},
	"claude-opus-4-8": {
		ID:            "claude-opus-4-8",
		Name:          "Claude Opus 4.8",
		Description:   "Opus 4 series model with adaptive thinking.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(5, 0.5, 25),
	},
	"claude-opus-4-7": {
		ID:            "claude-opus-4-7",
		Name:          "Claude Opus 4.7",
		Description:   "Opus 4 series model with adaptive thinking.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(5, 0.5, 25),
	},
	"claude-opus-4-6": {
		ID:            "claude-opus-4-6",
		Name:          "Claude Opus 4.6",
		Description:   "Older Opus with adaptive thinking.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(5, 0.5, 25),
	},
	"claude-sonnet-5-5": {
		ID:            "claude-sonnet-5-5",
		Name:          "Claude Sonnet 5.5",
		Description:   "The current Sonnet: speed and capability for everyday coding and agents.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(2, 0.2, 10),
	},
	"claude-sonnet-5": {
		ID:            "claude-sonnet-5",
		Name:          "Claude Sonnet 5",
		Description:   "Previous Sonnet with adaptive thinking.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(2, 0.2, 10),
	},
	"claude-sonnet-4-6": {
		ID:            "claude-sonnet-4-6",
		Name:          "Claude Sonnet 4.6",
		Description:   "Previous-generation Sonnet with adaptive thinking.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(3, 0.3, 15),
	},
	"claude-haiku-5-5": {
		ID:            "claude-haiku-5-5",
		Name:          "Claude Haiku 5.5",
		Description:   "The current Haiku: fast and low-cost. The price is for prompts up to 100K tokens.",
		ContextWindow: 1000000,
		MaxOutput:     128000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(0.1, 0.01, 0.5),
	},
	"claude-haiku-4-5": {
		ID:            "claude-haiku-4-5",
		Name:          "Claude Haiku 4.5",
		Description:   "Previous Haiku with budget-based extended thinking.",
		ContextWindow: 200000,
		MaxOutput:     64000,
		Kind:          "anthropic",
		Thinking:      "anthropic",
		Vision:        true,
		Price:         price(1, 0.1, 5),
	},
}

// Lookup returns the catalog entry for a model. It accepts a bare model ID,
// a model address "provider/model", and an OpenRouter-style "vendor/model"
// ID: the prefix up to each "/" is dropped in turn until an entry matches.
// OpenRouter's dotted Claude IDs ("claude-sonnet-4.6") also match the dashed
// catalog ID. An unknown model returns false; callers keep their current
// defaults.
func Lookup(model string) (Model, bool) {
	for candidate := model; candidate != ""; {
		if entry, ok := catalog[candidate]; ok {
			return entry, true
		}
		if strings.HasPrefix(candidate, "claude-") && strings.Contains(candidate, ".") {
			if entry, ok := catalog[strings.ReplaceAll(candidate, ".", "-")]; ok {
				return entry, true
			}
		}
		_, rest, ok := strings.Cut(candidate, "/")
		if !ok {
			break
		}
		candidate = rest
	}
	return Model{}, false
}

// deepseekPicker is the picker order of the DeepSeek models. The
// deepseek-v4-flash alias stays out of the list (REQ-MDL-010).
var deepseekPicker = []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner"}

// openaiPicker is the picker order of the OpenAI chat models.
var openaiPicker = []string{"gpt-5.1", "gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-4.1", "gpt-4.1-mini"}

// anthropicPicker is the picker order of the Claude models.
var anthropicPicker = []string{
	"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1",
	"claude-fable-5", "claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6",
	"claude-sonnet-5", "claude-sonnet-4-6", "claude-haiku-4-5",
}

// ForKind returns the catalog chat models a provider of the given kind offers
// in its picker, in picker order. Kinds without a built-in list (openrouter,
// openai_compat) return nil; their models come from the provider's models
// endpoint.
func ForKind(kind string) []Model {
	var ids []string
	switch kind {
	case "deepseek":
		ids = deepseekPicker
	case "openai":
		ids = openaiPicker
	case "anthropic":
		ids = anthropicPicker
	default:
		return nil
	}
	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, catalog[id])
	}
	return models
}

// ForProviderKind returns the catalog models a provider should offer in its
// picker. It returns the DeepSeek list when the base URL host is
// api.deepseek.com, and nil otherwise (REQ-MDL-020).
func ForProviderKind(baseURL string) []Model {
	parsed, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "api.deepseek.com") {
		return nil
	}
	return ForKind("deepseek")
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

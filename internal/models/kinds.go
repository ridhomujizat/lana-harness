package models

// defaultBaseURLs are the public API base URLs of the preset provider kinds
// (API-RTR-001). openai_compat has none: it needs base_url. The adapters in
// providers/openai and providers/anthropic apply the same values.
var defaultBaseURLs = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"deepseek":   "https://api.deepseek.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"anthropic":  "https://api.anthropic.com",
}

// DefaultBaseURL returns the base URL a provider of kind uses when its
// base_url is empty, or "" for a kind without a preset.
func DefaultBaseURL(kind string) string {
	return defaultBaseURLs[kind]
}

// testModels are the cheapest chat models of each preset kind, used for a
// one-token connection test.
var testModels = map[string]string{
	"deepseek":   "deepseek-flash",
	"openai":     "gpt-5-mini",
	"anthropic":  "claude-haiku-5-5",
	"openrouter": "openai/gpt-5-mini",
}

// TestModel returns the model a connection test of a provider of kind uses
// when nothing better is known, or "" for a kind without one.
func TestModel(kind string) string {
	return testModels[kind]
}

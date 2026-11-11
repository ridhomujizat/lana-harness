package models

import (
	"net/url"
	"strings"
)

// defaultBaseURLs are the public API base URLs of the preset provider kinds
// (API-RTR-001). openai_compat has none: it needs base_url. The adapters in
// providers/openai and providers/anthropic apply the same values.
var defaultBaseURLs = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"deepseek":   "https://api.deepseek.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"anthropic":  "https://api.anthropic.com",
	// commandcode is the Command Code CLI API (providers/commandcode).
	"commandcode": "https://api.commandcode.ai",
}

// DefaultBaseURL returns the base URL a provider of kind uses when its
// base_url is empty, or "" for a kind without a preset.
func DefaultBaseURL(kind string) string {
	return defaultBaseURLs[kind]
}

// testModels are the cheapest chat models of each preset kind, used for a
// one-token connection test.
var testModels = map[string]string{
	"deepseek":    "deepseek-flash",
	"openai":      "gpt-5-mini",
	"anthropic":   "claude-haiku-5-5",
	"openrouter":  "openai/gpt-5-mini",
	"commandcode": "deepseek/deepseek-v4-flash",
}

// TestModel returns the model a connection test of a provider of kind uses
// when nothing better is known, or "" for a kind without one.
func TestModel(kind string) string {
	return testModels[kind]
}

// hostTestModels are cheap chat models of the OpenAI-compatible services the
// dashboard offers as presets (Router Providers, revision 2), keyed by base
// URL host. They are used for a connection test only when nothing better is
// known; the owner can always test another model from the Models list.
var hostTestModels = map[string]string{
	"generativelanguage.googleapis.com": "gemini-2.5-flash",
	"api.x.ai":                          "grok-3-mini",
	"api.groq.com":                      "llama-3.1-8b-instant",
	"api.mistral.ai":                    "mistral-small-latest",
	"api.together.xyz":                  "meta-llama/Llama-3.3-70B-Instruct-Turbo",
	"api.fireworks.ai":                  "accounts/fireworks/models/llama-v3p3-70b-instruct",
	"api.cerebras.ai":                   "llama3.1-8b",
}

// TestModelForBaseURL returns the connection test model of a known
// OpenAI-compatible host, or "" for any other base URL.
func TestModelForBaseURL(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return hostTestModels[strings.ToLower(parsed.Hostname())]
}

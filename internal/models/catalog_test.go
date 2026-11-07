package models

import (
	"reflect"
	"testing"
)

// REQ-MDL-010: the catalog carries the DeepSeek thinking models and the legacy
// names, each with its context window and default effort.
func TestLookup(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		ok            bool
		contextWindow int
		maxOutput     int
		thinking      string
		defaultEffort string
		legacy        bool
		replacement   string
	}{
		{
			name:          "deepseek-flash",
			model:         "deepseek-flash",
			ok:            true,
			contextWindow: 1048576,
			maxOutput:     384000,
			thinking:      "deepseek",
			defaultEffort: "high",
		},
		{
			name:          "deepseek-v4-pro",
			model:         "deepseek-v4-pro",
			ok:            true,
			contextWindow: 1048576,
			maxOutput:     384000,
			thinking:      "deepseek",
			defaultEffort: "high",
		},
		{
			name:          "deepseek-v4-flash alias",
			model:         "deepseek-v4-flash",
			ok:            true,
			contextWindow: 1048576,
			maxOutput:     384000,
			thinking:      "deepseek",
			defaultEffort: "high",
		},
		{
			name:          "deepseek-chat legacy",
			model:         "deepseek-chat",
			ok:            true,
			contextWindow: 128000,
			maxOutput:     8192,
			legacy:        true,
			replacement:   "deepseek-flash",
		},
		{
			name:          "deepseek-reasoner legacy",
			model:         "deepseek-reasoner",
			ok:            true,
			contextWindow: 128000,
			maxOutput:     8192,
			thinking:      "deepseek",
			defaultEffort: "high",
			legacy:        true,
			replacement:   "deepseek-flash",
		},
		{
			name:  "unknown",
			model: "unknown",
			ok:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Lookup(tc.model)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.ID != tc.model {
				t.Fatalf("id=%q want %q", got.ID, tc.model)
			}
			if got.ContextWindow != tc.contextWindow || got.MaxOutput != tc.maxOutput {
				t.Fatalf("window=%d/%d want %d/%d", got.ContextWindow, got.MaxOutput, tc.contextWindow, tc.maxOutput)
			}
			if got.Thinking != tc.thinking || got.DefaultEffort != tc.defaultEffort {
				t.Fatalf("thinking=%q effort=%q want %q/%q", got.Thinking, got.DefaultEffort, tc.thinking, tc.defaultEffort)
			}
			if got.Legacy != tc.legacy || got.Replacement != tc.replacement {
				t.Fatalf("legacy=%v replacement=%q want %v/%q", got.Legacy, got.Replacement, tc.legacy, tc.replacement)
			}
		})
	}
}

// REQ-MDL-010: the alias is a lookup entry but not a picker entry.
func TestForProviderKind(t *testing.T) {
	deepseek := ForProviderKind("https://api.deepseek.com/v1")
	ids := make([]string, 0, len(deepseek))
	for _, model := range deepseek {
		ids = append(ids, model.ID)
	}
	want := []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("deepseek picker = %v, want %v", ids, want)
	}
	for _, model := range deepseek {
		if model.ID == "deepseek-v4-flash" {
			t.Fatal("the deepseek-v4-flash alias must stay out of the picker")
		}
	}

	if got := ForProviderKind("https://API.DEEPSEEK.COM:443/v1"); !reflect.DeepEqual(got, deepseek) {
		t.Fatalf("host with port and case = %+v, want the deepseek picker", got)
	}
	if got := ForProviderKind("https://api.openai.com/v1"); got != nil {
		t.Fatalf("other host = %+v, want nil", got)
	}
	if got := ForProviderKind("not a url"); got != nil {
		t.Fatalf("unparsable base URL = %+v, want nil", got)
	}
}

// REQ-MDL-001: the effort list carries the four IDs and their descriptions.
func TestEfforts(t *testing.T) {
	want := []Effort{
		{ID: "off", Description: "Use for simple tasks that do not need reasoning."},
		{ID: "low", Description: "Prefer for routine or latency-sensitive tasks."},
		{ID: "high", Description: "The default balance for most tasks."},
		{ID: "max", Description: "Reserve for the hardest quality-first tasks."},
	}
	if got := Efforts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Efforts() = %+v, want %+v", got, want)
	}
}

// REQ-ATT-003: only deepseek-flash and its alias are vision models; an unknown
// model is not.
func TestVision(t *testing.T) {
	tests := []struct {
		model string
		ok    bool
		want  bool
	}{
		{model: "deepseek-flash", ok: true, want: true},
		{model: "deepseek-v4-flash", ok: true, want: true},
		{model: "deepseek-v4-pro", ok: true, want: false},
		{model: "deepseek-chat", ok: true, want: false},
		{model: "deepseek-reasoner", ok: true, want: false},
		{model: "unknown", ok: false, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			got, ok := Lookup(tc.model)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if got.Vision != tc.want {
				t.Fatalf("Vision=%v want %v", got.Vision, tc.want)
			}
		})
	}
}

// REQ-RTR-003, REQ-RTR-010: Lookup resolves addresses and OpenRouter-style
// IDs by suffix, and carries kinds and prices.
func TestLookupAddressesAndPrices(t *testing.T) {
	tests := []struct {
		in   string
		id   string
		kind string
	}{
		{in: "gpt-5-mini", id: "gpt-5-mini", kind: "openai"},
		{in: "oa/gpt-5-mini", id: "gpt-5-mini", kind: "openai"},
		{in: "openai/gpt-5-mini", id: "gpt-5-mini", kind: "openai"},
		{in: "openrouter/openai/gpt-5-mini", id: "gpt-5-mini", kind: "openai"},
		{in: "anthropic/claude-sonnet-5-5", id: "claude-sonnet-5-5", kind: "anthropic"},
		{in: "openrouter/anthropic/claude-sonnet-4.6", id: "claude-sonnet-4-6", kind: "anthropic"},
		{in: "ds/deepseek-flash", id: "deepseek-flash", kind: "deepseek"},
		{in: "gpt-5.1", id: "gpt-5.1", kind: "openai"},
	}
	for _, tc := range tests {
		got, ok := Lookup(tc.in)
		if !ok || got.ID != tc.id || got.Kind != tc.kind {
			t.Fatalf("Lookup(%q) = %q/%q ok=%v, want %q/%q", tc.in, got.ID, got.Kind, ok, tc.id, tc.kind)
		}
	}
	for _, in := range []string{"", "x/unknown", "unknown/", "/"} {
		if _, ok := Lookup(in); ok {
			t.Fatalf("Lookup(%q) found an entry", in)
		}
	}
	opus, _ := Lookup("claude-opus-5-5")
	if opus.Price == nil || *opus.Price != (Price{Input: 4, CachedInput: 0.2, Output: 20}) {
		t.Fatalf("opus price = %+v", opus.Price)
	}
	if opus.ContextWindow != 1000000 || opus.MaxOutput != 128000 || opus.Thinking == "" || !opus.Vision {
		t.Fatalf("opus = %+v", opus)
	}
	flash, _ := Lookup("deepseek-flash")
	if flash.Price != nil {
		t.Fatalf("deepseek-flash price = %+v, want unknown", flash.Price)
	}
	if PricesAsOf == "" {
		t.Fatal("PricesAsOf is empty")
	}
}

// REQ-RTR-013: ForKind returns the picker list of a kind.
func TestForKind(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic", "deepseek"} {
		list := ForKind(kind)
		if len(list) == 0 {
			t.Fatalf("ForKind(%q) is empty", kind)
		}
		for _, model := range list {
			if model.Kind != kind || model.ID == "" || model.Embedding {
				t.Fatalf("ForKind(%q) has %+v", kind, model)
			}
		}
	}
	if got := ForKind("deepseek"); !reflect.DeepEqual(got, ForProviderKind("https://api.deepseek.com/v1")) {
		t.Fatal("ForKind(deepseek) differs from ForProviderKind")
	}
	for _, kind := range []string{"openrouter", "openai_compat", ""} {
		if got := ForKind(kind); got != nil {
			t.Fatalf("ForKind(%q) = %+v, want nil", kind, got)
		}
	}
	if first := ForKind("anthropic")[0]; first.ID != "claude-opus-5-5" {
		t.Fatalf("first anthropic model = %q", first.ID)
	}
}

package models

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// REQ-MES-001 (revision 2026-11-26): any single token, or "" for the default.
func TestValidEffort(t *testing.T) {
	for _, v := range []string{"", "off", "low", "medium", "high", "xhigh", "max", "minimal", "none", "Level_2", "a-b", strings.Repeat("x", 20)} {
		if !ValidEffort(v) {
			t.Errorf("ValidEffort(%q) = false", v)
		}
	}
	for _, v := range []string{" ", "high ", "two words", "-high", "_x", "a/b", "x\ny", strings.Repeat("x", 21), "é"} {
		if ValidEffort(v) {
			t.Errorf("ValidEffort(%q) = true", v)
		}
	}
}

func TestPickEffort(t *testing.T) {
	gpt5 := []string{"minimal", "low", "medium", "high"}
	cases := []struct {
		requested string
		supported []string
		def       string
		want      string
	}{
		{"high", gpt5, "medium", "high"},
		{"max", gpt5, "medium", "medium"},
		{"xhigh", gpt5, "", ""},
		{"", gpt5, "medium", "medium"},
		{"medium", []string{"medium"}, "medium", "medium"},
		{"xhigh", []string{"medium"}, "medium", "medium"},
		{"high", nil, "", ""},
	}
	for _, tc := range cases {
		if got := PickEffort(tc.requested, tc.supported, tc.def); got != tc.want {
			t.Errorf("PickEffort(%q, %v, %q) = %q, want %q", tc.requested, tc.supported, tc.def, got, tc.want)
		}
	}
}

// REQ-MES-002: the catalog's own effort levels per model family.
func TestModelEfforts(t *testing.T) {
	cases := []struct {
		model string
		want  []string
		def   string
	}{
		{"gpt-5", []string{"minimal", "low", "medium", "high"}, "medium"},
		{"openai/gpt-5-mini", []string{"minimal", "low", "medium", "high"}, "medium"},
		{"gpt-5.1", []string{"none", "low", "medium", "high"}, "none"},
		{"gpt-4.1", nil, ""},
		{"deepseek-flash", []string{"off", "high", "max"}, "high"},
		{"deepseek-v4.1-flash", []string{"off", "low", "high", "max"}, "high"},
		{"deepseek-chat", nil, ""},
		{"claude-opus-5-5", []string{"low", "medium", "high", "xhigh", "max"}, "medium"},
		{"claude-sonnet-5-5", []string{"low", "medium", "high", "xhigh", "max"}, "high"},
		{"claude-opus-4-6", []string{"low", "medium", "high", "max"}, "high"},
		{"anthropic/claude-sonnet-4.6", []string{"low", "medium", "high", "max"}, "high"},
		{"claude-haiku-4-5", []string{"low", "medium", "high", "xhigh", "max"}, ""},
	}
	for _, tc := range cases {
		got, def, known := ModelEfforts(tc.model)
		if !known || !reflect.DeepEqual(got, tc.want) || def != tc.def {
			t.Errorf("ModelEfforts(%q) = %v %q known=%v, want %v %q", tc.model, got, def, known, tc.want, tc.def)
		}
	}
	if _, _, known := ModelEfforts("some-unknown-model"); known {
		t.Fatal("unknown model is known")
	}
	// A default is always one of the model's levels; only the DeepSeek
	// thinking models (REQ-MDL-001, before spec 76) send theirs, the others
	// are display-only.
	for id, m := range catalog {
		if def := ShownDefault(m); def != "" && !slices.Contains(m.Efforts, def) {
			t.Errorf("%s default %q not in %v", id, def, m.Efforts)
		}
		if m.DefaultEffort != "" && (m.Kind != "deepseek" || m.DisplayDefault != "") {
			t.Errorf("%s sends default %q (display %q)", id, m.DefaultEffort, m.DisplayDefault)
		}
	}
}

// The fallback never supplies effort levels: a provider-listed model is
// unknown to ModelEfforts, so its adapter's own levels apply.
func TestModelEffortsIgnoresFallback(t *testing.T) {
	SetFallback(func(string) (Model, bool) { return Model{ID: "x", Thinking: "provider"}, true })
	defer SetFallback(nil)
	if _, _, known := ModelEfforts("listed-model"); known {
		t.Fatal("fallback model reported as known")
	}
	if got := EffortFor("listed-model", "xhigh", nil); got != "xhigh" {
		t.Fatalf("EffortFor passthrough = %q", got)
	}
}

// REQ-MES-003 (revision 2026-11-26): a listed value as is, else the default.
func TestEffortFor(t *testing.T) {
	cases := []struct {
		model, requested string
		unknown          []string
		want             string
	}{
		{"gpt-5", "minimal", nil, "minimal"},
		{"gpt-5", "max", nil, "medium"},
		{"gpt-5.1", "xhigh", nil, "none"},
		{"gpt-4.1", "high", nil, ""},
		{"claude-opus-4-6", "xhigh", nil, "high"},
		{"claude-haiku-4-5", "minimal", nil, ""},
		{"claude-opus-5-5", "off", nil, "off"},
		{"mystery", "medium", []string{"low", "high"}, ""},
		{"mystery", "high", []string{"low", "high"}, "high"},
		{"mystery", "medium", nil, "medium"},
		{"gpt-5", "", nil, ""},
	}
	for _, tc := range cases {
		if got := EffortFor(tc.model, tc.requested, tc.unknown); got != tc.want {
			t.Errorf("EffortFor(%q, %q) = %q, want %q", tc.model, tc.requested, got, tc.want)
		}
	}
}

// REQ-MES-005: effort variants split into base and level.
func TestSplitEffortVariant(t *testing.T) {
	cases := []struct {
		id, base, level string
		ok              bool
	}{
		{"gemini-4-neon-high", "gemini-4-neon", "high", true},
		{"gemini-4-neon-xhigh", "gemini-4-neon", "xhigh", true},
		{"gpt-oss-120b-medium", "gpt-oss-120b", "medium", true},
		{"gemini-pro-orchestrator", "gemini-pro-orchestrator", "", false},
		{"gemini-pro-orchestrator-neon", "gemini-pro-orchestrator-neon", "", false},
		{"thing-off", "thing-off", "", false},
		{"high", "high", "", false},
		{"-high", "-high", "", false},
	}
	for _, tc := range cases {
		base, level, ok := SplitEffortVariant(tc.id)
		if base != tc.base || level != tc.level || ok != tc.ok {
			t.Errorf("SplitEffortVariant(%q) = %q %q %v", tc.id, base, level, ok)
		}
	}
	if got := OrderVariantEfforts([]string{"xhigh", "low", "high", "medium", "high"}); !reflect.DeepEqual(got, []string{"low", "medium", "high", "xhigh"}) {
		t.Fatalf("OrderVariantEfforts = %v", got)
	}
	if got := VariantDefault([]string{"low", "medium", "high", "xhigh"}); got != "high" {
		t.Fatalf("VariantDefault = %q", got)
	}
	if got := VariantDefault([]string{"medium"}); got != "medium" {
		t.Fatalf("VariantDefault = %q", got)
	}
}

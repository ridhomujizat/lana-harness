package models

import (
	"strings"
	"testing"
)

// REQ-MDL-010: the fallback resolves a model the static catalog does not know,
// with the original argument, and the static catalog always wins.
func TestLookupFallback(t *testing.T) {
	t.Cleanup(func() { SetFallback(nil) })
	var got string
	SetFallback(func(address string) (Model, bool) {
		got = address
		if address == "provider/unknown-model" {
			return Model{ID: "unknown-model", ContextWindow: 1000000, MaxOutput: 384000}, true
		}
		return Model{}, false
	})

	entry, ok := Lookup("provider/unknown-model")
	if !ok || entry.ContextWindow != 1000000 || entry.MaxOutput != 384000 {
		t.Fatalf("fallback lookup = %+v ok=%v", entry, ok)
	}
	if got != "provider/unknown-model" {
		t.Fatalf("fallback got %q, want the original argument", got)
	}

	// The static catalog wins: the fallback is not consulted for a model it
	// knows, even behind an unknown provider prefix.
	got = ""
	entry, ok = Lookup("somewhere/deepseek-flash")
	if !ok || entry.ID != "deepseek-flash" || entry.ContextWindow != 1048576 {
		t.Fatalf("static lookup = %+v ok=%v", entry, ok)
	}
	if got != "" {
		t.Fatalf("fallback called for a static hit: %q", got)
	}

	// A model neither the catalog nor the fallback knows stays unknown.
	if _, ok := Lookup("provider/other"); ok {
		t.Fatal("fallback matched a model it does not know")
	}
}

// REQ-MDL-010: a nil fallback keeps the catalog-only behavior.
func TestLookupNilFallback(t *testing.T) {
	SetFallback(nil)
	if _, ok := Lookup("provider/unknown-model"); ok {
		t.Fatal("unknown model found without a fallback")
	}
	if _, ok := Lookup("deepseek-flash"); !ok {
		t.Fatal("static lookup failed without a fallback")
	}
}

// REQ-MDL-010: the V4.1 flash entries carry their context window and max
// output, resolve behind a provider address, and stay out of the picker.
func TestV41FlashEntries(t *testing.T) {
	t.Cleanup(func() { SetFallback(nil) })
	tests := []struct {
		model  string
		window int
	}{
		{"deepseek-v4.1-flash", 1000000},
		{"deepseek-v4.1-flash-fast", 1000000},
		{"deepseek-v4.1-flash-ultrafast", 1048576},
	}
	for _, tc := range tests {
		entry, ok := Lookup(tc.model)
		if !ok || entry.ContextWindow != tc.window || entry.MaxOutput != 384000 || entry.Kind != "deepseek" {
			t.Fatalf("Lookup(%q) = %+v ok=%v", tc.model, entry, ok)
		}
		addr, ok := Lookup("commandcode/deepseek/" + tc.model)
		if !ok || addr.ID != tc.model {
			t.Fatalf("address lookup = %+v ok=%v", addr, ok)
		}
	}
	for _, m := range ForKind("deepseek") {
		if strings.HasPrefix(m.ID, "deepseek-v4.1-") {
			t.Fatalf("a v4.1 entry is in the deepseek picker: %q", m.ID)
		}
	}
}

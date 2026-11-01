package knowledge

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePageNoFrontmatter(t *testing.T) {
	raw := "Just text\nmore\n"
	p := ParsePage("wiki/Catatan.md", raw)
	if p.Title != "Catatan" {
		t.Errorf("Title = %q, want Catatan", p.Title)
	}
	if !reflect.DeepEqual(p.Meta, PageMeta{}) {
		t.Errorf("Meta = %+v, want zero", p.Meta)
	}
	if p.Body != raw {
		t.Errorf("Body = %q, want the whole file", p.Body)
	}
}

func TestParsePageBrokenYAML(t *testing.T) {
	raw := "---\nfoo: [unclosed\n---\nBody here.\n"
	p := ParsePage("wiki/Broken.md", raw)
	if !reflect.DeepEqual(p.Meta, PageMeta{}) {
		t.Errorf("Meta = %+v, want zero for invalid frontmatter", p.Meta)
	}
	if p.Body != raw {
		t.Errorf("Body = %q, want the whole file", p.Body)
	}
}

func TestParsePageSingleStringList(t *testing.T) {
	raw := "---\ntype: person\naliases: Budi\ntags: klien\n---\n\nBody.\n"
	p := ParsePage("wiki/Pak Budi.md", raw)
	if !reflect.DeepEqual(p.Meta.Aliases, []string{"Budi"}) {
		t.Errorf("Aliases = %v, want [Budi]", p.Meta.Aliases)
	}
	if !reflect.DeepEqual(p.Meta.Tags, []string{"klien"}) {
		t.Errorf("Tags = %v, want [klien]", p.Meta.Tags)
	}
	if p.Body != "Body.\n" {
		t.Errorf("Body = %q, want \"Body.\\n\"", p.Body)
	}
}

func TestParsePageInvalidListType(t *testing.T) {
	raw := "---\naliases:\n  nested: map\n---\nBody.\n"
	p := ParsePage("wiki/Bad.md", raw)
	if !reflect.DeepEqual(p.Meta, PageMeta{}) {
		t.Errorf("Meta = %+v, want zero for invalid alias list", p.Meta)
	}
	if p.Body != raw {
		t.Errorf("Body = %q, want the whole file", p.Body)
	}
}

func TestFormatPageRoundTrip(t *testing.T) {
	meta := PageMeta{
		Type:        "person",
		Description: "Klien: utama proyek X",
		Aliases:     []string{"Budi", "Pak Budi"},
		Tags:        []string{"klien"},
		Sources:     []string{"[[sources/2026-11-01 Rapat.pdf]]"},
		Created:     "2026-11-01",
		Updated:     "2026-11-02",
		Extra:       map[string]any{"custom": "keep"},
	}
	text := FormatPage(meta, "Ringkasan.\n")
	if !strings.HasPrefix(text, "---\ntype: person\n") {
		t.Fatalf("frontmatter does not start with type:\n%s", text)
	}
	if !strings.HasSuffix(text, "Ringkasan.\n") {
		t.Fatalf("text does not end with the body:\n%s", text)
	}
	got := ParsePage("wiki/Pak Budi.md", text)
	if got.Meta.Type != "person" || got.Meta.Description != meta.Description {
		t.Errorf("type/description round-trip: %+v", got.Meta)
	}
	if !reflect.DeepEqual(got.Meta.Aliases, meta.Aliases) {
		t.Errorf("aliases = %v, want %v", got.Meta.Aliases, meta.Aliases)
	}
	if !reflect.DeepEqual(got.Meta.Sources, meta.Sources) {
		t.Errorf("sources = %v, want %v", got.Meta.Sources, meta.Sources)
	}
	if got.Meta.Extra["custom"] != "keep" {
		t.Errorf("extra custom = %v, want keep", got.Meta.Extra["custom"])
	}
	if got.Body != "Ringkasan.\n" {
		t.Errorf("body = %q", got.Body)
	}
}

func TestFormatPageKeyOrder(t *testing.T) {
	meta := PageMeta{
		Type:        "topic",
		Description: "desc",
		Aliases:     []string{"a"},
		Tags:        []string{"t"},
		Sources:     []string{"s"},
		Created:     "2026-11-01",
		Updated:     "2026-11-01",
		Extra:       map[string]any{"zeta": 1, "alpha": 2},
	}
	text := FormatPage(meta, "body")
	order := []string{"type:", "description:", "aliases:", "tags:", "sources:", "created:", "updated:", "alpha:", "zeta:"}
	last := -1
	for _, key := range order {
		i := strings.Index(text, "\n"+key)
		if key == "type:" {
			i = strings.Index(text, key)
		}
		if i < 0 {
			t.Fatalf("key %q missing from:\n%s", key, text)
		}
		if i < last {
			t.Fatalf("key %q out of order:\n%s", key, text)
		}
		last = i
	}
}

func TestFormatPageOmitsEmpty(t *testing.T) {
	text := FormatPage(PageMeta{Type: "person", Created: "2026-11-01", Updated: "2026-11-01"}, "")
	for _, key := range []string{"description:", "aliases:", "tags:", "sources:"} {
		if strings.Contains(text, key) {
			t.Errorf("empty %q should be omitted:\n%s", key, text)
		}
	}
}

func TestValidTitle(t *testing.T) {
	valid := []string{"Pak Budi", "Proyek X", "a", strings.Repeat("a", 120)}
	for _, title := range valid {
		if err := ValidTitle(title); err != nil {
			t.Errorf("ValidTitle(%q) = %v, want nil", title, err)
		}
	}
	invalid := []string{"", strings.Repeat("a", 121), "a/b", ".hidden", "a[b", "a:b", `a\b`}
	for _, title := range invalid {
		if err := ValidTitle(title); err == nil {
			t.Errorf("ValidTitle(%q) = nil, want error", title)
		}
	}
}

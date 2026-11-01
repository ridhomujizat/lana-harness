package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func createPage(t *testing.T, v *Vault, title, typ, body string) {
	t.Helper()
	if _, err := v.Apply(Op{Action: "create", Title: title, Type: typ, Body: body}, "test"); err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
}

func TestAppendAtEnd(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Baris satu.")
	if _, err := v.Apply(Op{Action: "append", Title: "Pak Budi", Text: "Baris dua."}, "test"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := mustFind(t, v, "Pak Budi").Body; got != "Baris satu.\n\nBaris dua.\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestAppendUnderHeading(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Catatan", "topic", "# Top\n\n## Satu\nisi satu\n\n## Dua\nisi dua")
	if _, err := v.Apply(Op{Action: "append", Title: "Catatan", Heading: "Satu", Text: "tambahan"}, "test"); err != nil {
		t.Fatalf("append: %v", err)
	}
	want := "# Top\n\n## Satu\nisi satu\n\ntambahan\n\n## Dua\nisi dua\n"
	if got := mustFind(t, v, "Catatan").Body; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestAppendCreatesMissingHeading(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Awal.")
	if _, err := v.Apply(Op{Action: "append", Title: "Pak Budi", Heading: "Kontak", Text: "08123"}, "test"); err != nil {
		t.Fatalf("append: %v", err)
	}
	got := mustFind(t, v, "Pak Budi").Body
	if !strings.Contains(got, "## Kontak\n\n08123") {
		t.Fatalf("body = %q", got)
	}
}

func TestReplaceOnce(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Halo dunia.")
	if _, err := v.Apply(Op{Action: "replace", Title: "Pak Budi", OldText: "dunia", NewText: "semua"}, "test"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got := mustFind(t, v, "Pak Budi").Body; got != "Halo semua.\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestReplaceNotFound(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Halo dunia.")
	_, err := v.Apply(Op{Action: "replace", Title: "Pak Budi", OldText: "tidak ada", NewText: "x"}, "test")
	if err == nil || err.Error() != "old_text not found in Pak Budi" {
		t.Fatalf("err = %v, want the not-found error", err)
	}
}

func TestReplaceAmbiguous(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Pak Budi", "person", "alpha beta alpha.")
	_, err := v.Apply(Op{Action: "replace", Title: "Pak Budi", OldText: "alpha", NewText: "x"}, "test")
	if err == nil || err.Error() != "old_text matches 2 places in Pak Budi; use more specific text" {
		t.Fatalf("err = %v, want the ambiguous error", err)
	}
}

func TestReplaceEmptyOldText(t *testing.T) {
	v, _ := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Body.")
	if _, err := v.Apply(Op{Action: "replace", Title: "Pak Budi", OldText: "", NewText: "x"}, "test"); err == nil {
		t.Fatal("empty old_text should be an error")
	}
}

func TestMetaUpdate(t *testing.T) {
	v, dir := newVault(t)
	raw := "---\ntype: person\ndescription: old\naliases:\n  - Bud\ncustom: keep\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, wikiDir, "Pak Budi.md"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := v.Apply(Op{
		Action:      "meta",
		Title:       "Pak Budi",
		Description: "new",
		Aliases:     []string{"BUD", "budi"},
		Tags:        []string{"klien"},
		Sources:     []string{"[[sources/x.md]]"},
	}, "test")
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	page := mustFind(t, v, "Pak Budi")
	if page.Meta.Description != "new" {
		t.Errorf("description = %q", page.Meta.Description)
	}
	if got := strings.Join(page.Meta.Aliases, ","); got != "Bud,budi" {
		t.Errorf("aliases = %q, want Bud,budi (case-insensitive dedupe)", got)
	}
	if got := strings.Join(page.Meta.Tags, ","); got != "klien" {
		t.Errorf("tags = %q", got)
	}
	if got := strings.Join(page.Meta.Sources, ","); got != "[[sources/x.md]]" {
		t.Errorf("sources = %q", got)
	}
	if page.Meta.Extra["custom"] != "keep" {
		t.Errorf("unknown key lost: %+v", page.Meta.Extra)
	}
}

func TestCreateOnExistingTitleAppends(t *testing.T) {
	v, dir := newVault(t)
	createPage(t, v, "Pak Budi", "person", "A")
	res, err := v.Apply(Op{Action: "create", Title: "pak budi", Type: "person", Body: "B"}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Op != "append" || res.Path != "wiki/Pak Budi.md" {
		t.Fatalf("Result = %+v, want an append to the existing page", res)
	}
	if files := pageFiles(t, dir); len(files) != 1 {
		t.Fatalf("files = %v, want one page", files)
	}
	if got := mustFind(t, v, "Pak Budi").Body; got != "A\n\nB\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestCreateOnExistingAliasAppends(t *testing.T) {
	v, dir := newVault(t)
	if _, err := v.Apply(Op{Action: "create", Title: "Pak Budi", Type: "person", Body: "A", Aliases: []string{"Budi Santoso"}}, "test"); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := v.Apply(Op{Action: "create", Title: "BUDI SANTOSO", Type: "person", Body: "B"}, "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Op != "append" || res.Path != "wiki/Pak Budi.md" {
		t.Fatalf("Result = %+v, want an append to the alias page", res)
	}
	if files := pageFiles(t, dir); len(files) != 1 {
		t.Fatalf("files = %v, want one page", files)
	}
}

func TestCreateInvalidType(t *testing.T) {
	v, _ := newVault(t)
	if _, err := v.Apply(Op{Action: "create", Title: "X", Type: "nope", Body: "b"}, "test"); err == nil {
		t.Fatal("invalid type should be refused")
	}
}

func TestOverLimit(t *testing.T) {
	v, dir := newVault(t)
	createPage(t, v, "Big", "topic", "ok")
	file := filepath.Join(dir, wikiDir, "Big.md")
	before := readFile(t, file)
	_, err := v.Apply(Op{Action: "append", Title: "Big", Text: strings.Repeat("a", 40001)}, "test")
	if err == nil || !strings.Contains(err.Error(), "split the page") {
		t.Fatalf("err = %v, want the split-the-page error", err)
	}
	if after := readFile(t, file); after != before {
		t.Fatalf("file changed on a refused write:\n%s", after)
	}
}

func TestSecretRefused(t *testing.T) {
	v, dir := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Aman.")
	file := filepath.Join(dir, wikiDir, "Pak Budi.md")
	before := readFile(t, file)
	_, err := v.Apply(Op{Action: "append", Title: "Pak Budi", Text: "password: hunter22xyz"}, "test")
	if err == nil || !strings.Contains(err.Error(), "content looks like a secret; not saved") {
		t.Fatalf("err = %v, want the secret error", err)
	}
	if after := readFile(t, file); after != before {
		t.Fatalf("file changed on a refused write:\n%s", after)
	}
}

func TestInvalidTitlesRefused(t *testing.T) {
	v, _ := newVault(t)
	for _, title := range []string{"", strings.Repeat("a", 121), "a/b", ".hidden", "a[b"} {
		if _, err := v.Apply(Op{Action: "create", Title: title, Type: "person", Body: "b"}, "test"); err == nil {
			t.Errorf("title %q should be refused", title)
		}
	}
}

// AC-KNW-002: an on-disk edit between read and write is kept and the op is
// re-applied to the re-read content.
func TestConcurrentOnDiskEdit(t *testing.T) {
	v, dir := newVault(t)
	createPage(t, v, "Pak Budi", "person", "Lini satu.")

	var once sync.Once
	v.beforeCommit = func(path string) {
		once.Do(func() {
			cur := readFile(t, path)
			if err := os.WriteFile(path, []byte(cur+"Lini luar.\n"), 0o600); err != nil {
				t.Fatalf("external edit: %v", err)
			}
			future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := os.Chtimes(path, future, future); err != nil {
				t.Fatalf("chtimes: %v", err)
			}
		})
	}
	defer func() { v.beforeCommit = nil }()

	if _, err := v.Apply(Op{Action: "append", Title: "Pak Budi", Text: "Lini lana."}, "test"); err != nil {
		t.Fatalf("append: %v", err)
	}

	body := readFile(t, filepath.Join(dir, wikiDir, "Pak Budi.md"))
	for _, want := range []string{"Lini satu.", "Lini luar.", "Lini lana."} {
		if !strings.Contains(body, want) {
			t.Fatalf("final file missing %q:\n%s", want, body)
		}
	}
}

func TestAppendMissingPage(t *testing.T) {
	v, _ := newVault(t)
	if _, err := v.Apply(Op{Action: "append", Title: "Nope", Text: "x"}, "test"); err == nil {
		t.Fatal("append to a missing page should be an error")
	}
}

// A heading sent with its markdown hashes appends under the existing section
// instead of creating "## ## Heading".
func TestAppendTextHeadingWithHashes(t *testing.T) {
	body := "Intro.\n\n## Peran\n- satu\n\n## Lain\n- x\n"
	got := appendText(body, "## Peran", "- dua")
	if strings.Contains(got, "## ## Peran") {
		t.Fatalf("hashes kept in heading:\n%s", got)
	}
	if !strings.Contains(got, "- satu\n\n- dua") && !strings.Contains(got, "- satu\n- dua") {
		t.Fatalf("text not under the existing heading:\n%s", got)
	}
}

package knowledge

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// AC-KNU-006 (REQ-KNU-022, -023): a fixture vault with a broken link, an
// orphan, a duplicate pair, a page without a description, and a **Conflict:**
// line yields a report listing each, and no file under wiki/ changes.
func TestLintReportsEveryProblem(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeWiki("Proyek X.md", "---\ntype: project\ndescription: Proyek klien\n---\nTerkait [[Proyek Y]].\n**Conflict:** deadline 15 Nov vs 20 Nov\n")
	env.writeWiki("Catatan lama.md", "---\ntype: topic\ndescription: catatan\n---\nTidak ada yang menautkan halaman ini.\n")
	env.writeWiki("Budi Santoso.md", "---\ntype: person\ndescription: klien\n---\nIsi halaman.\n")
	env.writeWiki("BudiSantoso.md", "---\ntype: person\ndescription: klien\n---\nIsi halaman.\n")
	env.writeWiki("Kantor.md", "---\ntype: place\n---\nKantor pusat.\n")

	before := wikiMtimes(t, env.vaultDir)
	path, err := env.svc.lint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := wikiMtimes(t, env.vaultDir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a file under wiki/ changed during lint:\nbefore=%v\nafter=%v", before, after)
	}

	if !strings.HasPrefix(path, "reports/lint-") || !strings.HasSuffix(path, ".md") {
		t.Fatalf("report path = %q", path)
	}
	data, err := os.ReadFile(filepath.Join(env.vaultDir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	for _, want := range []string{
		"# Knowledge lint ",
		"## Broken links", "[[Proyek X]] → [[Proyek Y]]",
		"## Orphan pages", "[[Catatan lama]]",
		"## Likely duplicates", "[[Budi Santoso]] and [[BudiSantoso]]",
		"## Missing details", "[[Kantor]]: no description",
		"## Marked conflicts", "[[Proyek X]] line", "**Conflict:**",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report is missing %q:\n%s", want, report)
		}
	}
}

// AC-KNU-006: lint only reads the vault; it never writes under wiki/.
func TestLintLeavesWikiUntouched(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeWiki("Satu.md", "---\ntype: topic\ndescription: satu\n---\nTautan ke [[Dua]].\n")
	env.writeWiki("Dua.md", "---\ntype: topic\ndescription: dua\n---\nBalik ke [[Satu]].\n")

	before := wikiMtimes(t, env.vaultDir)
	if _, err := env.svc.lint(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := wikiMtimes(t, env.vaultDir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("wiki/ changed during lint:\nbefore=%v\nafter=%v", before, after)
	}
}

// AC-KNU-006: the contradiction pass reports what the model returns about a
// changed page and the pages it links (design §8).
func TestLintContradictionsReported(t *testing.T) {
	provider := &fakeProvider{text: `[{"page":"Proyek X","claim":"deadline 15 Nov","contradicts":"deadline 20 Nov","where":"[[Rapat Q4]]"}]`}
	env := newVaultEnv(t, provider)
	env.writeWiki("Proyek X.md", "---\ntype: project\ndescription: proyek\n---\nDeadline 15 Nov. Lihat [[Rapat Q4]].\n")
	env.writeWiki("Rapat Q4.md", "---\ntype: source\ndescription: rapat\n---\nDeadline 20 Nov.\n")

	path, err := env.svc.lint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(env.vaultDir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	if !strings.Contains(report, "## Contradictions") {
		t.Fatalf("report has no contradictions section:\n%s", report)
	}
	if want := `- [[Proyek X]]: "deadline 15 Nov" contradicts "deadline 20 Nov" ([[Rapat Q4]])`; !strings.Contains(report, want) {
		t.Fatalf("report missing %q:\n%s", want, report)
	}
}

// design §8: a vault with no problems gets "No problems found.".
func TestLintNoProblems(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	env.writeWiki("Satu.md", "---\ntype: topic\ndescription: satu\n---\nLihat [[Dua]].\n")
	env.writeWiki("Dua.md", "---\ntype: topic\ndescription: dua\n---\nLihat [[Satu]].\n")

	path, err := env.svc.lint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(env.vaultDir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	if !strings.Contains(report, "Problems: 0.") || !strings.Contains(report, "No problems found.") {
		t.Fatalf("report is not the clean report:\n%s", report)
	}
}

// One malformed item must not drop the whole contradiction reply.
func TestParseLintReplySkipsMalformedItem(t *testing.T) {
	reply := `[{"page":"A","claim":"x","contradicts":"y","where":"B"},{"page":"C","claim":42}]`
	items, err := parseLintReply(reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Page != "A" {
		t.Fatalf("items = %+v, want only the well-formed item", items)
	}
}

// wikiMtimes records the mtime of every file under wiki/.
func wikiMtimes(t *testing.T, root string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	dir := filepath.Join(root, "wiki")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[p] = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

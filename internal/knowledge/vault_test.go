package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// newVault opens a vault in a temp dir with a fixed clock.
func newVault(t *testing.T) (*Vault, string) {
	t.Helper()
	dir := t.TempDir()
	v, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	v.now = func() time.Time { return testNow }
	return v, dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustFind(t *testing.T, v *Vault, title string) Page {
	t.Helper()
	p, ok, err := v.FindPage(title)
	if err != nil {
		t.Fatalf("FindPage(%q): %v", title, err)
	}
	if !ok {
		t.Fatalf("page %q not found", title)
	}
	return p
}

func pageFiles(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, wikiDir, "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return paths
}

func TestOpenSeedsLayout(t *testing.T) {
	v, dir := newVault(t)
	for _, d := range []string{sourcesDir, wikiDir} {
		fi, err := os.Stat(filepath.Join(dir, d))
		if err != nil || !fi.IsDir() {
			t.Fatalf("directory %s missing: %v", d, err)
		}
	}
	if got := readFile(t, filepath.Join(dir, "KNOWLEDGE.md")); got != knowledgeTemplate {
		t.Fatalf("KNOWLEDGE.md does not match the template:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, indexFile)); !strings.Contains(got, "# Knowledge index") {
		t.Fatalf("index.md missing header:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, logFile)); !strings.Contains(got, "# Knowledge log") {
		t.Fatalf("log.md missing header:\n%s", got)
	}
	_ = v
}

func TestOpenKeepsEditedKnowledge(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	edited := "# my conventions\nkeep me\n"
	if err := os.WriteFile(filepath.Join(dir, "KNOWLEDGE.md"), []byte(edited), 0o600); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "KNOWLEDGE.md")); got != edited {
		t.Fatalf("KNOWLEDGE.md edit lost:\n%s", got)
	}
}

// AC-KNW-001: a write regenerates index.md and appends to log.md, keeping the
// earlier entries.
func TestApplyRegeneratesIndexAndLog(t *testing.T) {
	v, dir := newVault(t)
	res, err := v.Apply(Op{
		Action:      "create",
		Title:       "Pak Budi",
		Type:        "person",
		Description: "Klien utama proyek X",
		Body:        "Pak Budi adalah klien.",
	}, "agent lana")
	if err != nil {
		t.Fatalf("Apply create: %v", err)
	}
	if res.Op != "create" || res.Path != "wiki/Pak Budi.md" {
		t.Fatalf("Result = %+v", res)
	}

	idx := readFile(t, filepath.Join(dir, indexFile))
	if want := "## People\n- [[Pak Budi]] — Klien utama proyek X\n"; !strings.Contains(idx, want) {
		t.Fatalf("index.md missing the page line:\n%s", idx)
	}

	log := readFile(t, filepath.Join(dir, logFile))
	if want := "## 2026-11-01 09:00 agent lana\n- created [[Pak Budi]]\n"; !strings.Contains(log, want) {
		t.Fatalf("log.md missing the entry:\n%s", log)
	}

	if _, err := v.Apply(Op{Action: "append", Title: "Pak Budi", Text: "Baris kedua."}, "agent lana"); err != nil {
		t.Fatalf("Apply append: %v", err)
	}
	log = readFile(t, filepath.Join(dir, logFile))
	if n := strings.Count(log, "## 2026-11-01 09:00 agent lana"); n != 2 {
		t.Fatalf("log entry count = %d, want 2:\n%s", n, log)
	}
	if !strings.Contains(log, "- created [[Pak Budi]]") || !strings.Contains(log, "- appended to [[Pak Budi]]") {
		t.Fatalf("log lost an entry:\n%s", log)
	}
}

func TestFindHandWrittenPages(t *testing.T) {
	v, dir := newVault(t)
	plain := "Just text\nmore\n"
	if err := os.WriteFile(filepath.Join(dir, wikiDir, "Catatan.md"), []byte(plain), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	broken := "---\nfoo: [unclosed\n---\nBody here.\n"
	if err := os.WriteFile(filepath.Join(dir, wikiDir, "Broken.md"), []byte(broken), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, tc := range []struct{ title, body string }{
		{"Catatan", plain},
		{"Broken", broken},
	} {
		p, ok, err := v.FindPage(tc.title)
		if err != nil || !ok {
			t.Fatalf("FindPage(%q) = %v, %v", tc.title, ok, err)
		}
		if p.Meta.Type != "" || p.Meta.Description != "" {
			t.Errorf("%s: meta not empty: %+v", tc.title, p.Meta)
		}
		if p.Body != tc.body {
			t.Errorf("%s: body = %q, want the whole file", tc.title, p.Body)
		}
	}
}

func TestApplyLogsSources(t *testing.T) {
	v, dir := newVault(t)
	_, err := v.Apply(Op{
		Action:  "create",
		Title:   "Rapat Q4",
		Type:    "source",
		Body:    "Ringkasan rapat.",
		Sources: []string{"[[sources/2026-11-01 Rapat.pdf]]"},
	}, "compile Rapat.pdf")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	log := readFile(t, filepath.Join(dir, logFile))
	if !strings.Contains(log, "- from [[sources/2026-11-01 Rapat.pdf]]") {
		t.Fatalf("log missing the source line:\n%s", log)
	}
}

// REQ-KNU-004: AppendJournal creates journal/<day>.md with a header and appends
// "\n"+block on each call.
func TestVaultAppendJournal(t *testing.T) {
	v, dir := newVault(t)

	rel, err := v.AppendJournal("2026-11-01", "## 14:05 · lana\n- note one")
	if err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	if rel != "journal/2026-11-01.md" {
		t.Fatalf("rel = %q, want journal/2026-11-01.md", rel)
	}
	first := readFile(t, filepath.Join(dir, journalDir, "2026-11-01.md"))
	if first != "# 2026-11-01\n\n## 14:05 · lana\n- note one\n" {
		t.Fatalf("first content = %q", first)
	}

	rel, err = v.AppendJournal("2026-11-01", "## 15:00 · lana\n- note two")
	if err != nil {
		t.Fatalf("AppendJournal second: %v", err)
	}
	if rel != "journal/2026-11-01.md" {
		t.Fatalf("rel = %q, want journal/2026-11-01.md", rel)
	}
	second := readFile(t, filepath.Join(dir, journalDir, "2026-11-01.md"))
	if second != first+"\n## 15:00 · lana\n- note two\n" {
		t.Fatalf("second content = %q", second)
	}
}

// REQ-KNU-010: Open creates inbox/ and journal/ on an existing vault that lacks
// them.
func TestOpenCreatesInboxAndJournal(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	for _, d := range []string{inboxDir, journalDir} {
		if err := os.RemoveAll(filepath.Join(dir, d)); err != nil {
			t.Fatalf("remove %s: %v", d, err)
		}
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	for _, d := range []string{inboxDir, journalDir} {
		fi, err := os.Stat(filepath.Join(dir, d))
		if err != nil || !fi.IsDir() {
			t.Fatalf("directory %s missing after reopen: %v", d, err)
		}
	}
}

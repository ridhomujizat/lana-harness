package knowledge

import (
	"context"
	"strings"
	"testing"
)

// Search returns one hit per file, best first, and can filter by kind.
func TestSearchKeywordAndFilter(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Satu.md", "# Satu\n\nKontrak proyek besar senilai tinggi.\n")
	writeVaultFile(t, dir, "wiki/Dua.md", "# Dua\n\nKontrak kecil di kantor.\n")
	writeVaultFile(t, dir, "sources/lain.txt", "Kontrak baru ditandatangani hari ini.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	hits, err := idx.Search(ctx, Query{Text: "kontrak proyek", Limit: 8})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %+v, want 3 files, one each", hits)
	}
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.Path] {
			t.Fatalf("duplicate file in results: %+v", hits)
		}
		seen[h.Path] = true
	}
	if hits[0].Path != "wiki/Satu.md" {
		t.Fatalf("best hit = %q, want wiki/Satu.md (matches both terms)", hits[0].Path)
	}
	if hits[0].Snippet == "" {
		t.Fatalf("no snippet on the best hit")
	}

	only, err := idx.Search(ctx, Query{Text: "kontrak", Kind: "source"})
	if err != nil {
		t.Fatalf("filtered Search: %v", err)
	}
	if len(only) != 1 || only[0].Kind != "source" || only[0].Path != "sources/lain.txt" {
		t.Fatalf("kind filter = %+v", only)
	}
}

// The recall rule: a page qualifies with at least two distinct query tokens,
// pages only, and one token is not enough.
func TestRecallRule(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Alpha.md", "# Alpha\n\nKontrak proyek besar.\n")
	writeVaultFile(t, dir, "wiki/Beta.md", "# Beta\n\nKontrak saja.\n")
	writeVaultFile(t, dir, "sources/notes.txt", "Kontrak proyek dari sumber.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	hits, err := idx.Recall(ctx, "kontrak proyek", 3, nil, "")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Alpha.md" {
		t.Fatalf("recall = %+v, want only the page matching two tokens", hits)
	}
	if none, _ := idx.Recall(ctx, "kontrak", 3, nil, ""); len(none) != 0 {
		t.Fatalf("a one-token overlap should not qualify: %+v", none)
	}
}

// With an embedder, a synonym page with no shared token still qualifies for
// recall through cosine >= 0.45.
func TestRecallVectorQualifies(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Kendaraan.md", "# Kendaraan\n\nKendaraan roda empat milik klien.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	idx.SetEmbedder(&fakeEmbedder{}, "fake")
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	hits, err := idx.Recall(ctx, "mobil", 3, nil, "")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Kendaraan.md" {
		t.Fatalf("vector recall = %+v", hits)
	}
}

// REQ-DKN-010: Search filters files by scope, and a hit names its scope.
func TestSearchScopeFilter(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Pajak.md", "# Pajak\n\nPajak tahunan bersama.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/Pajak.md", "# Pajak\n\nPajak dana khusus.\n")
	writeVaultFile(t, dir, "agents/rio/wiki/Pajak.md", "# Pajak\n\nPajak rio khusus.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	all, err := idx.Search(ctx, Query{Text: "pajak"})
	if err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all scopes = %+v, want 3", all)
	}

	dana, err := idx.Search(ctx, Query{Text: "pajak", Scopes: []string{"dana"}})
	if err != nil {
		t.Fatalf("Search dana: %v", err)
	}
	if len(dana) != 1 || dana[0].Scope != "dana" || dana[0].Path != "agents/dana/wiki/Pajak.md" {
		t.Fatalf("dana scopes = %+v", dana)
	}

	both, err := idx.Search(ctx, Query{Text: "pajak", Scopes: []string{ScopeShared, "dana"}})
	if err != nil {
		t.Fatalf("Search shared+dana: %v", err)
	}
	if len(both) != 2 {
		t.Fatalf("shared+dana scopes = %+v, want 2", both)
	}
	for _, h := range both {
		if h.Scope == "rio" {
			t.Fatalf("rio leaked into a filtered search: %+v", both)
		}
	}
}

// REQ-DKN-012: Recall filters by scope and, on an equal score, puts a hit whose
// scope is prefer first.
func TestRecallScopesAndPrefer(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Kontrak.md", "# Kontrak\n\nKontrak proyek bersama.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/Kontrak.md", "# Kontrak\n\nKontrak proyek dana.\n")
	writeVaultFile(t, dir, "agents/rio/wiki/Kontrak.md", "# Kontrak\n\nKontrak proyek rio.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	hits, err := idx.Recall(ctx, "kontrak proyek", 5, []string{"dana", ScopeShared}, "dana")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("recall = %+v, want dana and shared only", hits)
	}
	for _, h := range hits {
		if h.Scope == "rio" {
			t.Fatalf("rio leaked into recall: %+v", hits)
		}
	}

	// The two pages tie on score; prefer overrides the path tie-break, so the
	// shared page (which sorts after dana by path) comes first here.
	preferred, err := idx.Recall(ctx, "kontrak proyek", 5, []string{"dana", ScopeShared}, ScopeShared)
	if err != nil {
		t.Fatalf("Recall prefer shared: %v", err)
	}
	if len(preferred) != 2 || preferred[0].Scope != ScopeShared || preferred[1].Scope != "dana" {
		t.Fatalf("prefer=%s order = %+v", ScopeShared, preferred)
	}
}

// REQ-DKN-026: a superseded fact is stripped from the indexed text, so it is
// not findable while the new text is, and start_line still points at the
// section heading.
func TestSupersededTextStripped(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	raw := "---\ntype: topic\n---\n# Gaji\n\nGaji pokok ~~5000~~ *(until 2026-11-01)* 7000 per bulan.\n"
	writeVaultFile(t, dir, "wiki/Gaji.md", raw)

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if old, _ := idx.Search(ctx, Query{Text: "5000"}); len(old) != 0 {
		t.Fatalf("superseded text is still indexed: %+v", old)
	}
	hits, err := idx.Search(ctx, Query{Text: "7000"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Gaji.md" {
		t.Fatalf("current text not found: %+v", hits)
	}
	if hits[0].StartLine != 4 {
		t.Fatalf("start line = %d, want 4", hits[0].StartLine)
	}
	if strings.Contains(hits[0].Snippet, "5000") || strings.Contains(hits[0].Snippet, "until") {
		t.Fatalf("snippet shows the superseded fact: %q", hits[0].Snippet)
	}
}

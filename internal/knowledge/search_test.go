package knowledge

import (
	"context"
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

	hits, err := idx.Recall(ctx, "kontrak proyek", 3)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Alpha.md" {
		t.Fatalf("recall = %+v, want only the page matching two tokens", hits)
	}
	if none, _ := idx.Recall(ctx, "kontrak", 3); len(none) != 0 {
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

	hits, err := idx.Recall(ctx, "mobil", 3)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Kendaraan.md" {
		t.Fatalf("vector recall = %+v", hits)
	}
}

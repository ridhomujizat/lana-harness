package knowledge

import (
	"path/filepath"
	"strings"
	"testing"
)

// AC-KNW-003 (save and dedup): a source is stored once and found again by hash.
func TestSaveSourceDedup(t *testing.T) {
	v, dir := newVault(t)
	pdf := []byte("%PDF-1.4 fake pdf bytes")

	path, dup, err := v.SaveSource("Rapat", "pdf", pdf)
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if path != "sources/2026-11-01 Rapat.pdf" || dup {
		t.Fatalf("path = %q dup = %v", path, dup)
	}

	again, dup, err := v.SaveSource("Rapat", "pdf", pdf)
	if err != nil {
		t.Fatalf("SaveSource again: %v", err)
	}
	if again != path || !dup {
		t.Fatalf("second save = %q dup = %v, want the same path and dup=true", again, dup)
	}
	files, err := filepath.Glob(filepath.Join(dir, sourcesDir, "*.pdf"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("pdf files = %v, want one", files)
	}

	txt, dup, err := v.SaveSource("Rapat", "txt", []byte("different bytes"))
	if err != nil {
		t.Fatalf("SaveSource txt: %v", err)
	}
	if txt != "sources/2026-11-01 Rapat.txt" || dup {
		t.Fatalf("txt = %q dup = %v", txt, dup)
	}

	txt2, dup, err := v.SaveSource("Rapat", "txt", []byte("yet another body"))
	if err != nil {
		t.Fatalf("SaveSource txt2: %v", err)
	}
	if txt2 != "sources/2026-11-01 Rapat (2).txt" || dup {
		t.Fatalf("txt2 = %q dup = %v", txt2, dup)
	}
}

func TestSaveSourceTooLarge(t *testing.T) {
	v, _ := newVault(t)
	data := make([]byte, maxSourceBytes+1)
	if _, _, err := v.SaveSource("Big", "pdf", data); err == nil {
		t.Fatal("a 30 MB+1 source should be refused")
	}
}

func TestSaveSourceBadExt(t *testing.T) {
	v, _ := newVault(t)
	if _, _, err := v.SaveSource("Page", "html", []byte("x")); err == nil {
		t.Fatal("an unsupported extension should be refused")
	}
}

func TestSaveSourceSanitizesTitle(t *testing.T) {
	v, _ := newVault(t)
	path, dup, err := v.SaveSource("a/b:c", "txt", []byte("hello"))
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if dup || path != "sources/2026-11-01 a-b-c.txt" {
		t.Fatalf("path = %q dup = %v", path, dup)
	}
}

func TestSaveSourceEmptyTitle(t *testing.T) {
	v, _ := newVault(t)
	path, _, err := v.SaveSource("   ", "txt", []byte("hello"))
	if err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if !strings.HasSuffix(path, " Source.txt") {
		t.Fatalf("path = %q, want the Source fallback", path)
	}
}

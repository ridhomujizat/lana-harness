package knowledge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// AC-KNW-005 (chunking): markdown sections split at heading levels 1-3, the
// first chunk starts with "title — description", and start_line points at the
// file line where the chunk text begins.
func TestChunkMarkdownSectionsAndLines(t *testing.T) {
	raw := "---\ntype: person\n---\n# Pak Budi\n\nKlien utama.\n\n## Kontrak\n\nDitandatangani 12 Okt.\n"
	chunks := ChunkMarkdown("Pak Budi", "Klien utama proyek X", raw)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2: %+v", len(chunks), chunks)
	}
	if chunks[0].Heading != "Pak Budi" || chunks[0].StartLine != 4 {
		t.Fatalf("first chunk = %+v", chunks[0])
	}
	if !strings.HasPrefix(chunks[0].Text, "Pak Budi — Klien utama proyek X\n\n# Pak Budi") {
		t.Fatalf("first chunk text = %q", chunks[0].Text)
	}
	if strings.Contains(chunks[0].Text, "type: person") {
		t.Fatalf("frontmatter leaked into the chunk: %q", chunks[0].Text)
	}
	if chunks[1].Heading != "Kontrak" || chunks[1].StartLine != 8 {
		t.Fatalf("second chunk = %+v", chunks[1])
	}
	if !strings.Contains(chunks[1].Text, "Ditandatangani 12 Okt.") {
		t.Fatalf("second chunk text = %q", chunks[1].Text)
	}
}

// A section over 1,500 runes splits at blank lines, and the second chunk keeps
// the right start line.
func TestChunkMarkdownSplitsLongSection(t *testing.T) {
	para := strings.Repeat("kata ", 100)
	raw := "---\n---\n# Judul\n\n" + strings.Join([]string{para, para, para, para}, "\n\n") + "\n"
	chunks := ChunkMarkdown("Judul", "", raw)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2: %+v", len(chunks), chunks)
	}
	if chunks[0].StartLine != 3 {
		t.Fatalf("first start line = %d, want 3", chunks[0].StartLine)
	}
	if chunks[1].StartLine != 9 {
		t.Fatalf("second start line = %d, want 9", chunks[1].StartLine)
	}
	for i, c := range chunks {
		if utf8.RuneCountInString(c.Text) > chunkRunes+64 {
			t.Fatalf("chunk %d is %d runes, over the cap", i, utf8.RuneCountInString(c.Text))
		}
	}
}

// A markdown page with no body still yields one chunk holding the header.
func TestChunkMarkdownEmptyBody(t *testing.T) {
	chunks := ChunkMarkdown("Kosong", "Deskripsi", "---\ntype: topic\n---\n")
	if len(chunks) != 1 || chunks[0].Text != "Kosong — Deskripsi" {
		t.Fatalf("chunks = %+v", chunks)
	}
}

// Plain text splits at blank lines into chunks of at most 1,500 runes.
func TestChunkTextSplitsAtBlankLines(t *testing.T) {
	para := strings.Repeat("x", 1000)
	chunks := ChunkText(para + "\n\n" + para)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2: %+v", len(chunks), chunks)
	}
	if chunks[0].StartLine != 1 || chunks[1].StartLine != 3 {
		t.Fatalf("start lines = %d, %d; want 1, 3", chunks[0].StartLine, chunks[1].StartLine)
	}
}

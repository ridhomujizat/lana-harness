package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock drives the index throttle deterministically.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newIndex opens an index over dir with a silent logger.
func newIndex(t *testing.T, dir string) *Index {
	t.Helper()
	idx, err := OpenIndex(filepath.Join(dir, "knowledge.db"), dir)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	idx.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return idx
}

// writeVaultFile writes a vault file, creating its folder.
func writeVaultFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// fakeEmbedder maps synonyms to a shared vector. When err is set every call
// fails.
type fakeEmbedder struct {
	err error
}

func (f *fakeEmbedder) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		out[i] = conceptVector(in)
	}
	return out, nil
}

// conceptVector maps a text to one of two orthogonal concepts so a synonym
// query can only match through the vector list.
func conceptVector(s string) []float32 {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "kendaraan") || strings.Contains(lower, "mobil") {
		return []float32{1, 0, 0}
	}
	return []float32{0, 1, 0}
}

// batchEmbedder records the largest batch it is asked to embed.
type batchEmbedder struct {
	mu    sync.Mutex
	max   int
	total int
}

func (b *batchEmbedder) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(inputs) > b.max {
		b.max = len(inputs)
	}
	b.total += len(inputs)
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

// AC-KNW-005: a file edited outside lana is found by its new words after Sync,
// a deleted file is gone, and a second Sync inside the throttle window is a
// no-op.
func TestIndexSyncExternalEditAndDelete(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Pak Budi.md", "---\ntype: person\ndescription: Klien\n---\nPak Budi memelihara kucing.\n")
	writeVaultFile(t, dir, "sources/catatan.txt", "catatan biasa\n")

	idx := newIndex(t, dir)
	clock := &fakeClock{t: testNow}
	idx.now = clock.now
	ctx := context.Background()

	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	hits, err := idx.Search(ctx, Query{Text: "kucing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Pak Budi.md" {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].StartLine == 0 {
		t.Fatalf("no start line: %+v", hits[0])
	}

	// A second Sync inside the window ignores the change below.
	clock.advance(2 * time.Second)
	writeVaultFile(t, dir, "wiki/Pak Budi.md", "---\ntype: person\ndescription: Klien\n---\nPak Budi memelihara kelinci besar.\n")
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("throttled Sync: %v", err)
	}
	if stale, _ := idx.Search(ctx, Query{Text: "kelinci"}); len(stale) != 0 {
		t.Fatalf("throttled Sync indexed the edit: %+v", stale)
	}

	// After the window the edit is picked up and the old word is gone.
	clock.advance(9 * time.Second)
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync after window: %v", err)
	}
	hits, _ = idx.Search(ctx, Query{Text: "kelinci"})
	if len(hits) != 1 {
		t.Fatalf("new word not indexed: %+v", hits)
	}
	if old, _ := idx.Search(ctx, Query{Text: "kucing"}); len(old) != 0 {
		t.Fatalf("stale word still found: %+v", old)
	}

	// A deleted file is gone after the next Sync.
	clock.advance(11 * time.Second)
	if err := os.Remove(filepath.Join(dir, "wiki", "Pak Budi.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync after delete: %v", err)
	}
	if hits, _ := idx.Search(ctx, Query{Text: "kelinci"}); len(hits) != 0 {
		t.Fatalf("deleted file still found: %+v", hits)
	}
}

// AC-KNW-005: with a fake embedder a synonym-only query finds the page through
// the vector list.
func TestIndexVectorSynonymSearch(t *testing.T) {
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

	hits, err := idx.Search(ctx, Query{Text: "mobil"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Kendaraan.md" {
		t.Fatalf("synonym query did not find the page through vectors: %+v", hits)
	}
	if hits[0].Snippet == "" {
		t.Fatalf("vector-only hit has no snippet")
	}
}

// AC-KNW-005: an embedder error still returns the keyword hits.
func TestIndexEmbedderErrorKeepsKeyword(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Kucing.md", "# Kucing\n\nKucing oren di rumah.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	idx.SetEmbedder(&fakeEmbedder{err: errors.New("boom")}, "fake")
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	hits, err := idx.Search(ctx, Query{Text: "kucing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/Kucing.md" {
		t.Fatalf("keyword fallback failed: %+v", hits)
	}
}

// Chunks are embedded in batches of at most 64.
func TestIndexEmbedBatchSize(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	para := strings.Repeat("z", 1000)
	var b strings.Builder
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&b, "%s %d\n\n", para, i)
	}
	writeVaultFile(t, dir, "sources/big.txt", b.String())

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	emb := &batchEmbedder{}
	idx.SetEmbedder(emb, "fake")
	if err := idx.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if emb.max == 0 || emb.max > embedBatchSize {
		t.Fatalf("max batch = %d, want 1..%d", emb.max, embedBatchSize)
	}
	if emb.total <= embedBatchSize {
		t.Fatalf("total embedded = %d, expected more than one batch", emb.total)
	}
}

// The catalog lists pages and sources_state round-trips.
func TestIndexCatalogAndSourceState(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Proyek X.md", "---\ntype: project\ndescription: Proyek klien\n---\nIsi.\n")
	writeVaultFile(t, dir, "wiki/Pak Budi.md", "---\ntype: person\ndescription: Klien utama\n---\nIsi.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	lines, err := idx.Catalog(0)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Pak Budi — person — Klien utama") {
		t.Fatalf("catalog = %v", lines)
	}

	st := SourceState{Path: "sources/a.pdf", Status: "pending", SessionID: "s1", TurnID: "t1", Agent: "lana"}
	if err := idx.SetSourceState(st); err != nil {
		t.Fatalf("SetSourceState: %v", err)
	}
	got, ok, err := idx.GetSourceState("sources/a.pdf")
	if err != nil || !ok {
		t.Fatalf("GetSourceState: %v ok=%v", err, ok)
	}
	if got.Status != "pending" || got.SessionID != "s1" || got.Agent != "lana" || got.UpdatedAt == 0 {
		t.Fatalf("state = %+v", got)
	}
	if list, _ := idx.ListSourceStates("pending"); len(list) != 1 {
		t.Fatalf("pending = %v", list)
	}
	st.Status = "done"
	if err := idx.SetSourceState(st); err != nil {
		t.Fatalf("SetSourceState update: %v", err)
	}
	if list, _ := idx.ListSourceStates("pending"); len(list) != 0 {
		t.Fatalf("pending after done = %v", list)
	}
}

// SyncFile indexes one file without waiting for the throttle.
func TestIndexSyncFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	rel := filepath.Join("wiki", "Baru.md")
	writeVaultFile(t, dir, "wiki/Baru.md", "# Baru\n\nIsi pertama.\n")
	if err := idx.SyncFile(rel); err != nil {
		t.Fatalf("SyncFile: %v", err)
	}
	if hits, _ := idx.Search(ctx, Query{Text: "pertama"}); len(hits) != 1 || hits[0].Path != "wiki/Baru.md" {
		t.Fatalf("SyncFile did not index the file: %+v", hits)
	}
}

// SaveSource consults the index's source hashes before falling back to hashing
// the files (design §2).
func TestSaveSourceDedupViaIndex(t *testing.T) {
	v, dir := newVault(t)
	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	v.SetIndex(idx)
	ctx := context.Background()

	path, dup, err := v.SaveSource("Rapat", "txt", []byte("indexed body"))
	if err != nil || dup {
		t.Fatalf("SaveSource: %v dup=%v", err, dup)
	}
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	again, dup, err := v.SaveSource("Rapat", "txt", []byte("indexed body"))
	if err != nil {
		t.Fatalf("SaveSource again: %v", err)
	}
	if again != path || !dup {
		t.Fatalf("second save = %q dup = %v, want %q and dup=true", again, dup, path)
	}
}

// PDF text is indexed when a fixture exists under internal/attach/testdata.
func TestIndexPDFText(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join("..", "attach", "testdata", "*.pdf"))
	if len(matches) == 0 {
		t.Skip("no PDF fixture under internal/attach/testdata")
	}
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	writeVaultFile(t, dir, "sources/doc.pdf", string(data))

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	if err := idx.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	var count int
	if err := idx.db.QueryRow("SELECT COUNT(*) FROM chunks WHERE path = ?", "sources/doc.pdf").Scan(&count); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if count == 0 {
		t.Fatalf("no chunks indexed for the PDF")
	}
}

// Design §2: a v1 index is upgraded to version 2 in place, keeping its data and
// gaining the upkeep tables.
func TestIndexMigrateV1ToV2(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "knowledge.db")

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(indexSchema); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set version 1: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO files (path, kind, title, type, description, aliases, updated, mtime, size, hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"wiki/A.md", "page", "A", "", "", "[]", "", 1, 2, "h"); err != nil {
		t.Fatalf("insert v1 row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	idx, err := OpenIndex(dbPath, dir)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	var version int
	if err := idx.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}

	var title string
	if err := idx.db.QueryRow("SELECT title FROM files WHERE path = ?", "wiki/A.md").Scan(&title); err != nil {
		t.Fatalf("v1 data lost: %v", err)
	}
	if title != "A" {
		t.Fatalf("title = %q, want A", title)
	}

	for _, table := range []string{"journal_marks", "promote_marks", "upkeep"} {
		var name string
		if err := idx.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing after migration: %v", table, err)
		}
	}
}

// Design §2: the mark and upkeep state round-trips, overwrites, and reports the
// absent case.
func TestIndexMarksAndUpkeep(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now

	if seq, err := idx.JournalMark("s1"); err != nil || seq != 0 {
		t.Fatalf("JournalMark absent = %d, %v; want 0, nil", seq, err)
	}
	if err := idx.SetJournalMark("s1", 42); err != nil {
		t.Fatalf("SetJournalMark: %v", err)
	}
	if seq, err := idx.JournalMark("s1"); err != nil || seq != 42 {
		t.Fatalf("JournalMark = %d, %v; want 42", seq, err)
	}
	if err := idx.SetJournalMark("s1", 100); err != nil {
		t.Fatalf("SetJournalMark overwrite: %v", err)
	}
	if seq, _ := idx.JournalMark("s1"); seq != 100 {
		t.Fatalf("JournalMark after overwrite = %d, want 100", seq)
	}

	if off, err := idx.PromoteOffset("journal/2026-11-01.md"); err != nil || off != 0 {
		t.Fatalf("PromoteOffset absent = %d, %v; want 0, nil", off, err)
	}
	if err := idx.SetPromoteOffset("journal/2026-11-01.md", 512); err != nil {
		t.Fatalf("SetPromoteOffset: %v", err)
	}
	if off, err := idx.PromoteOffset("journal/2026-11-01.md"); err != nil || off != 512 {
		t.Fatalf("PromoteOffset = %d, %v; want 512", off, err)
	}
	if err := idx.SetPromoteOffset("journal/2026-11-01.md", 900); err != nil {
		t.Fatalf("SetPromoteOffset overwrite: %v", err)
	}
	if off, _ := idx.PromoteOffset("journal/2026-11-01.md"); off != 900 {
		t.Fatalf("PromoteOffset after overwrite = %d, want 900", off)
	}

	if v, ok, err := idx.UpkeepValue("last_report"); err != nil || ok || v != "" {
		t.Fatalf("UpkeepValue absent = %q, %v, %v; want empty and absent", v, ok, err)
	}
	if err := idx.SetUpkeepValue("last_report", "reports/lint-2026-11-01.md"); err != nil {
		t.Fatalf("SetUpkeepValue: %v", err)
	}
	if v, ok, err := idx.UpkeepValue("last_report"); err != nil || !ok || v != "reports/lint-2026-11-01.md" {
		t.Fatalf("UpkeepValue = %q, %v, %v", v, ok, err)
	}
	if err := idx.SetUpkeepValue("last_report", "reports/lint-2026-11-02.md"); err != nil {
		t.Fatalf("SetUpkeepValue overwrite: %v", err)
	}
	if v, _, _ := idx.UpkeepValue("last_report"); v != "reports/lint-2026-11-02.md" {
		t.Fatalf("UpkeepValue after overwrite = %q", v)
	}
}

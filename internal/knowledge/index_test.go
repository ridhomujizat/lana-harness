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

// Design §2, REQ-DKN-004: a v1 index is upgraded to version 3 in place, gains
// the v3 tables, and drops its file rows so the next Sync reindexes the vault
// with scopes. The vault file is untouched.
func TestIndexMigrateV1ToV3(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "knowledge.db")
	writeVaultFile(t, dir, "wiki/A.md", "---\ntype: topic\n---\n# A\n\nIsi halaman A.\n")

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

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()

	var version int
	if err := idx.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 3 {
		t.Fatalf("version = %d, want 3", version)
	}

	for _, table := range []string{"journal_marks", "promote_marks", "upkeep", "proposals"} {
		var name string
		if err := idx.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing after migration: %v", table, err)
		}
	}

	var count int
	if err := idx.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if count != 0 {
		t.Fatalf("files not dropped after migration: %d", count)
	}

	// The vault file is untouched and reindexes with its scope on the next Sync.
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	hits, err := idx.Search(ctx, Query{Text: "halaman"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "wiki/A.md" || hits[0].Scope != ScopeShared {
		t.Fatalf("reindex after migration = %+v", hits)
	}
}

// Design §2, REQ-DKN-004: a v2 index migrates to v3, drops its rows, and after
// Sync the vault files carry their scopes; only domain wiki/journal files are
// indexed.
func TestIndexMigrateV2ToV3(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "knowledge.db")
	writeVaultFile(t, dir, "wiki/Shared.md", "# Shared\n\nIsi bersama.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/Gaji.md", "# Gaji\n\nGaji domain dana.\n")
	writeVaultFile(t, dir, "agents/dana/journal/2026-11-01.md", "# Journal\n\nCatatan dana.\n")
	writeVaultFile(t, dir, "agents/dana/index.md", "# Index\n\nGenerated.\n")

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(indexSchema); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := db.Exec(indexSchemaV2); err != nil {
		t.Fatalf("create v2 schema: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatalf("set version 2: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO files (path, kind, title, type, description, aliases, updated, mtime, size, hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"wiki/Old.md", "page", "Old", "", "", "[]", "", 1, 2, "h"); err != nil {
		t.Fatalf("insert v2 row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()

	var version int
	if err := idx.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 3 {
		t.Fatalf("version = %d, want 3", version)
	}
	var count int
	if err := idx.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if count != 0 {
		t.Fatalf("files not dropped after migration: %d", count)
	}

	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	kindScope := func(rel string) (kind, scope string, ok bool) {
		err := idx.db.QueryRow("SELECT kind, scope FROM files WHERE path = ?", rel).Scan(&kind, &scope)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return kind, scope, true
	}

	if kind, scope, ok := kindScope("agents/dana/wiki/Gaji.md"); !ok || kind != kindPage || scope != "dana" {
		t.Fatalf("dana wiki row = %q %q %v", kind, scope, ok)
	}
	if kind, scope, ok := kindScope("agents/dana/journal/2026-11-01.md"); !ok || kind != kindJournal || scope != "dana" {
		t.Fatalf("dana journal row = %q %q %v", kind, scope, ok)
	}
	if _, _, ok := kindScope("agents/dana/index.md"); ok {
		t.Fatalf("agents/dana/index.md must not be indexed")
	}
	if kind, scope, ok := kindScope("wiki/Shared.md"); !ok || kind != kindPage || scope != ScopeShared {
		t.Fatalf("shared row = %q %q %v", kind, scope, ok)
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

// Design §2, REQ-DKN-001: a domain wiki file is indexed as a page in its domain
// scope, a domain journal file as a journal, and other files under agents/ are
// not indexed.
func TestIndexDomainScopeKinds(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Shared.md", "# Shared\n\nIsi bersama.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/Gaji.md", "# Gaji\n\nGaji domain.\n")
	writeVaultFile(t, dir, "agents/dana/journal/2026-11-01.md", "# Journal\n\nCatatan.\n")
	writeVaultFile(t, dir, "agents/dana/index.md", "# Index\n\nGenerated.\n")
	writeVaultFile(t, dir, "agents/dana/sources/raw.txt", "bahan dana\n")
	writeVaultFile(t, dir, "agents/Bad/wiki/x.md", "# Bad\n\nbad scope name.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	if err := idx.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	kindScope := func(rel string) (kind, scope string, ok bool) {
		err := idx.db.QueryRow("SELECT kind, scope FROM files WHERE path = ?", rel).Scan(&kind, &scope)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return kind, scope, true
	}

	if kind, scope, ok := kindScope("agents/dana/wiki/Gaji.md"); !ok || kind != kindPage || scope != "dana" {
		t.Fatalf("dana wiki row = %q %q %v", kind, scope, ok)
	}
	if kind, scope, ok := kindScope("agents/dana/journal/2026-11-01.md"); !ok || kind != kindJournal || scope != "dana" {
		t.Fatalf("dana journal row = %q %q %v", kind, scope, ok)
	}
	if kind, scope, ok := kindScope("wiki/Shared.md"); !ok || kind != kindPage || scope != ScopeShared {
		t.Fatalf("shared row = %q %q %v", kind, scope, ok)
	}
	for _, rel := range []string{"agents/dana/index.md", "agents/dana/sources/raw.txt", "agents/Bad/wiki/x.md"} {
		if _, _, ok := kindScope(rel); ok {
			t.Fatalf("%s must not be indexed", rel)
		}
	}
}

// Design §2: ScopeStats reports shared first (even empty), then the other
// scopes in name order, with page and journal-day counts and the newest mtime.
func TestIndexScopeStats(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/S.md", "# S\n\nIsi.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/G1.md", "# G1\n\nIsi.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/G2.md", "# G2\n\nIsi.\n")
	writeVaultFile(t, dir, "agents/dana/journal/2026-11-01.md", "# J\n\nCatatan.\n")
	writeVaultFile(t, dir, "agents/rio/wiki/R.md", "# R\n\nIsi.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	if err := idx.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	stats, err := idx.ScopeStats()
	if err != nil {
		t.Fatalf("ScopeStats: %v", err)
	}
	if len(stats) != 3 || stats[0].Scope != ScopeShared || stats[1].Scope != "dana" || stats[2].Scope != "rio" {
		t.Fatalf("scopes = %+v", stats)
	}
	if stats[0].Pages != 1 || stats[0].JournalDays != 0 || stats[0].UpdatedAt == 0 {
		t.Fatalf("shared stat = %+v", stats[0])
	}
	if stats[1].Pages != 2 || stats[1].JournalDays != 1 || stats[1].UpdatedAt == 0 {
		t.Fatalf("dana stat = %+v", stats[1])
	}
	if stats[2].Pages != 1 || stats[2].JournalDays != 0 {
		t.Fatalf("rio stat = %+v", stats[2])
	}

	// An index with no files still reports shared with zero counts.
	empty := newIndex(t, t.TempDir())
	empty.now = (&fakeClock{t: testNow}).now
	st, err := empty.ScopeStats()
	if err != nil {
		t.Fatalf("empty ScopeStats: %v", err)
	}
	if len(st) != 1 || st[0].Scope != ScopeShared || st[0].Pages != 0 || st[0].UpdatedAt != 0 {
		t.Fatalf("empty stats = %+v", st)
	}
}

// Design §2: ScopeStats runs Sync first, so a file added outside lana is counted
// without any other call.
func TestIndexScopeStatsSyncsFirst(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "agents/dana/wiki/Gaji.md", "# Gaji\n\nIsi.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	stats, err := idx.ScopeStats()
	if err != nil {
		t.Fatalf("ScopeStats: %v", err)
	}
	byScope := map[string]ScopeStat{}
	for _, s := range stats {
		byScope[s.Scope] = s
	}
	if byScope["dana"].Pages != 1 {
		t.Fatalf("dana stat = %+v, want 1 page without an explicit Sync", byScope["dana"])
	}
}

// Design §2: Pages lists only kind=page, filters by scope and a case-insensitive
// substring, orders newest first, and reports the total before the limit.
func TestIndexPagesFilterLimitTotal(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/Rencana.md", "---\ntype: project\ndescription: Rencana tahunan\n---\n# Rencana\n\nIsi.\n")
	writeVaultFile(t, dir, "wiki/Gaji.md", "---\ntype: topic\ndescription: Gaji karyawan\n---\n# Gaji\n\nIsi.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/Gaji.md", "---\ntype: topic\ndescription: Gaji dana\n---\n# Gaji\n\nIsi.\n")
	writeVaultFile(t, dir, "agents/dana/journal/2026-11-01.md", "# J\n\nCatatan.\n")

	base := testNow
	for rel, at := range map[string]time.Time{
		"wiki/Rencana.md":          base.Add(-2 * time.Hour),
		"wiki/Gaji.md":             base.Add(-1 * time.Hour),
		"agents/dana/wiki/Gaji.md": base,
	} {
		if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(rel)), at, at); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now

	rows, total, err := idx.Pages("", "", 0)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("all pages = %d rows, total %d; want 3, 3", len(rows), total)
	}
	if rows[0].Path != "agents/dana/wiki/Gaji.md" || rows[0].Scope != "dana" || rows[1].Path != "wiki/Gaji.md" || rows[2].Path != "wiki/Rencana.md" {
		t.Fatalf("page order = %+v", rows)
	}
	if rows[0].Mtime != base.UnixMilli() {
		t.Fatalf("mtime = %d, want %d", rows[0].Mtime, base.UnixMilli())
	}
	if rows[0].Type != "topic" || rows[0].Description != "Gaji dana" {
		t.Fatalf("page row = %+v", rows[0])
	}

	shared, sTotal, err := idx.Pages(ScopeShared, "", 0)
	if err != nil {
		t.Fatalf("Pages(shared): %v", err)
	}
	if sTotal != 2 || len(shared) != 2 {
		t.Fatalf("shared pages = %d rows, total %d; want 2, 2", len(shared), sTotal)
	}

	// q is a case-insensitive substring of title, aliases, or description.
	matched, mTotal, err := idx.Pages("all", "gaji", 0)
	if err != nil {
		t.Fatalf("Pages(q): %v", err)
	}
	if mTotal != 2 || len(matched) != 2 {
		t.Fatalf("q pages = %d rows, total %d; want 2, 2", len(matched), mTotal)
	}

	one, oTotal, err := idx.Pages("", "", 1)
	if err != nil {
		t.Fatalf("Pages(limit): %v", err)
	}
	if oTotal != 3 || len(one) != 1 || one[0].Path != "agents/dana/wiki/Gaji.md" {
		t.Fatalf("limited pages = %+v, total %d", one, oTotal)
	}
}

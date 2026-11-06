package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	"github.com/ridhomujizat/lana-harness/internal/sqlitex"
)

// Index sizing and schema constants (design §3).
const (
	indexSchemaVersion = 2
	indexSyncInterval  = 10 * time.Second
	embedBatchSize     = 64
	indexCatalogMax    = 400
)

// Index kinds for the top-level vault folders (design §3).
const (
	kindPage    = "page"
	kindSource  = "source"
	kindJournal = "journal"
	journalDir  = "journal"
)

// Per-extension size caps of the indexer (design §3).
const (
	maxTextIndexBytes = 5 << 20
	maxPDFIndexBytes  = 30 << 20
)

// indexKindByDir maps a top-level vault folder to its index kind. Other
// folders, dot entries, and root files are not indexed (design §3).
var indexKindByDir = map[string]string{
	wikiDir:    kindPage,
	sourcesDir: kindSource,
	journalDir: kindJournal,
}

// indexSchema is schema version 1 of knowledge.db (design §3).
const indexSchema = `
CREATE TABLE files (
  path TEXT PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL, type TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', aliases TEXT NOT NULL DEFAULT '[]', updated TEXT NOT NULL DEFAULT '',
  mtime INTEGER NOT NULL, size INTEGER NOT NULL, hash TEXT NOT NULL
);
CREATE TABLE chunks (
  id INTEGER PRIMARY KEY, path TEXT NOT NULL, ord INTEGER NOT NULL, title TEXT NOT NULL,
  heading TEXT NOT NULL DEFAULT '', start_line INTEGER NOT NULL, text TEXT NOT NULL,
  UNIQUE (path, ord)
);
CREATE VIRTUAL TABLE chunks_fts USING fts5(
  title, heading, text, content = 'chunks', content_rowid = 'id',
  tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
  INSERT INTO chunks_fts (rowid, title, heading, text) VALUES (new.id, new.title, new.heading, new.text);
END;
CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
  INSERT INTO chunks_fts (chunks_fts, rowid, title, heading, text) VALUES ('delete', old.id, old.title, old.heading, old.text);
END;
CREATE TABLE vectors (chunk_id INTEGER PRIMARY KEY, model TEXT NOT NULL, dim INTEGER NOT NULL, vec BLOB NOT NULL);
CREATE TABLE sources_state (path TEXT PRIMARY KEY, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '', turn_id TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL);
`

// indexSchemaV2 is the schema version 2 migration: the knowledge upkeep state
// tables (design §2). A fresh database runs it right after indexSchema.
const indexSchemaV2 = `
CREATE TABLE journal_marks (session_id TEXT PRIMARY KEY, upto_seq INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE promote_marks (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE upkeep (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Index is the derived SQLite index of one vault (REQ-KNW-030). It lives
// outside the vault and can be rebuilt from the files at any time.
type Index struct {
	db     *sql.DB
	root   string
	now    func() time.Time
	logger *slog.Logger

	// Embedder and EmbeddingModel are optional. When set, Sync embeds chunks
	// that have no vector for EmbeddingModel in batches of embedBatchSize
	// (REQ-KNW-031).
	Embedder       core.Embedder
	EmbeddingModel string

	syncMu   sync.Mutex
	lastSync time.Time

	vecMu    sync.Mutex
	vecCache map[string]*vectorMatrix
}

// OpenIndex opens or creates the index database and applies the schema up to
// version 2 (design §2, §3). vaultRoot is the folder the index describes.
func OpenIndex(dbPath, vaultRoot string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
	} else if err != nil {
		return nil, err
	}
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sqlitex.Open(dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	idx := &Index{db: db, root: vaultRoot, now: time.Now, logger: slog.Default()}
	if err := idx.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

// migrate applies the schema migrations up to indexSchemaVersion. A fresh
// database is created at the newest version; an existing one is upgraded in
// order, keeping its data. A database from a newer build is refused rather than
// guessed at.
func (i *Index) migrate(ctx context.Context) error {
	var version int
	if err := i.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > indexSchemaVersion {
		return fmt.Errorf("knowledge index schema version %d is newer than this build (%d)", version, indexSchemaVersion)
	}
	if version == indexSchemaVersion {
		return nil
	}
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if version < 1 {
		if _, err := tx.ExecContext(ctx, indexSchema); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if version < 2 {
		if _, err := tx.ExecContext(ctx, indexSchemaV2); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", indexSchemaVersion)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Close closes the index database.
func (i *Index) Close() error { return i.db.Close() }

// SetEmbedder sets the optional embedding backend and the model whose vectors
// are stored (REQ-KNW-031).
func (i *Index) SetEmbedder(embedder core.Embedder, model string) {
	i.Embedder = embedder
	i.EmbeddingModel = model
}

// Sync re-indexes the vault, at most once per 10 s (REQ-KNW-030). Files the
// owner adds, edits, or deletes outside lana are picked up on the next run.
func (i *Index) Sync(ctx context.Context) error {
	i.syncMu.Lock()
	defer i.syncMu.Unlock()
	now := i.now()
	if !i.lastSync.IsZero() && now.Sub(i.lastSync) < indexSyncInterval {
		return nil
	}
	i.lastSync = now
	return i.syncAll(ctx)
}

// SyncFile re-indexes one file immediately; a lana write calls it instead of
// waiting for the throttle (design §3). path is absolute or vault-relative. A
// missing file drops its rows.
func (i *Index) SyncFile(path string) error {
	ctx := context.Background()
	rel, err := i.relPath(path)
	if err != nil {
		return err
	}
	i.syncMu.Lock()
	defer i.syncMu.Unlock()
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := i.syncOne(ctx, tx, rel); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	i.dropVectors()
	return i.embedMissing(ctx)
}

// syncAll walks the vault, re-chunks changed files, drops rows of missing
// files, then embeds the chunks that still have no vector (design §3).
func (i *Index) syncAll(ctx context.Context) error {
	current, err := i.scan()
	if err != nil {
		return err
	}
	states, err := i.fileStates(ctx)
	if err != nil {
		return err
	}
	var changed []string
	for rel, file := range current {
		st, ok := states[rel]
		if ok && st.mtime == file.mtime && st.size == file.size {
			continue
		}
		changed = append(changed, rel)
	}
	sort.Strings(changed)

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	dirty := false
	for _, rel := range changed {
		if err := i.syncOne(ctx, tx, rel); err != nil {
			_ = tx.Rollback()
			return err
		}
		dirty = true
	}
	for rel := range states {
		if _, ok := current[rel]; ok {
			continue
		}
		if err := i.deleteFile(ctx, tx, rel); err != nil {
			_ = tx.Rollback()
			return err
		}
		dirty = true
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if dirty {
		i.dropVectors()
	}
	return i.embedMissing(ctx)
}

// vaultFile is one indexable file found by scan.
type vaultFile struct {
	path  string
	kind  string
	abs   string
	mtime int64
	size  int64
}

// fileState is the indexed mtime and size of a path.
type fileState struct {
	mtime int64
	size  int64
}

// scan walks the indexable top-level folders and returns the files that are
// within the extension and size caps (design §3).
func (i *Index) scan() (map[string]vaultFile, error) {
	out := map[string]vaultFile{}
	for dir, kind := range indexKindByDir {
		root := filepath.Join(i.root, dir)
		err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if abs != root && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(abs))
			limit := indexMaxBytes(ext)
			if limit < 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() > limit {
				return nil
			}
			rel := i.rel(abs)
			out[rel] = vaultFile{path: rel, kind: kind, abs: abs, mtime: info.ModTime().UnixNano(), size: info.Size()}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fileStates returns the indexed mtime and size of every file row.
func (i *Index) fileStates(ctx context.Context) (map[string]fileState, error) {
	rows, err := i.db.QueryContext(ctx, "SELECT path, mtime, size FROM files")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]fileState{}
	for rows.Next() {
		var rel string
		var st fileState
		if err := rows.Scan(&rel, &st.mtime, &st.size); err != nil {
			return nil, err
		}
		out[rel] = st
	}
	return out, rows.Err()
}

// syncOne indexes or drops one vault file inside tx.
func (i *Index) syncOne(ctx context.Context, tx *sql.Tx, rel string) error {
	abs := filepath.Join(i.root, filepath.FromSlash(rel))
	kind, ok := kindOf(rel)
	if !ok {
		return i.deleteFile(ctx, tx, rel)
	}
	info, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return i.deleteFile(ctx, tx, rel)
	}
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(rel))
	limit := indexMaxBytes(ext)
	if !info.Mode().IsRegular() || info.Size() > limit || limit < 0 {
		return i.deleteFile(ctx, tx, rel)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		i.logger.Warn("knowledge index: reading file failed", "path", rel, "error", err)
		return nil
	}
	raw := string(data)
	if ext == ".pdf" {
		pages, err := attach.PDFText(abs)
		if err != nil {
			i.logger.Warn("knowledge index: reading pdf failed", "path", rel, "error", err)
			return nil
		}
		raw = strings.Join(pages, "\n")
	}
	title, typ, desc, aliases, updated := fileMeta(rel, ext, raw)
	var chunks []Chunk
	if ext == ".md" {
		chunks = ChunkMarkdown(title, desc, raw)
	} else {
		chunks = ChunkText(raw)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM vectors WHERE chunk_id IN (SELECT id FROM chunks WHERE path = ?)", rel); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE path = ?", rel); err != nil {
		return err
	}
	for ord, chunk := range chunks {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO chunks (path, ord, title, heading, start_line, text) VALUES (?, ?, ?, ?, ?, ?)",
			rel, ord, title, chunk.Heading, chunk.StartLine, chunk.Text); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO files (path, kind, title, type, description, aliases, updated, mtime, size, hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  kind = excluded.kind, title = excluded.title, type = excluded.type,
  description = excluded.description, aliases = excluded.aliases, updated = excluded.updated,
  mtime = excluded.mtime, size = excluded.size, hash = excluded.hash`,
		rel, kind, title, typ, desc, aliases, updated, info.ModTime().UnixNano(), info.Size(), sha256Hex(data))
	return err
}

// deleteFile removes a file row and its chunks and vectors.
func (i *Index) deleteFile(ctx context.Context, tx *sql.Tx, rel string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM vectors WHERE chunk_id IN (SELECT id FROM chunks WHERE path = ?)", rel); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE path = ?", rel); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM files WHERE path = ?", rel)
	return err
}

// relPath normalizes an absolute or vault-relative path to the forward-slash
// form the index stores.
func (i *Index) relPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		r, err := filepath.Rel(i.root, p)
		if err != nil {
			return "", err
		}
		p = r
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("knowledge index: %q is outside the vault", p)
	}
	return p, nil
}

// rel returns the vault-relative path of abs with forward slashes.
func (i *Index) rel(abs string) string {
	r, err := filepath.Rel(i.root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

// kindOf returns the index kind of a vault-relative path, if its top-level
// folder is indexed.
func kindOf(rel string) (string, bool) {
	top, _, _ := strings.Cut(rel, "/")
	kind, ok := indexKindByDir[top]
	return kind, ok
}

// indexMaxBytes returns the size cap of an extension, or -1 when the extension
// is not indexed.
func indexMaxBytes(ext string) int64 {
	switch ext {
	case ".md", ".txt":
		return maxTextIndexBytes
	case ".pdf":
		return maxPDFIndexBytes
	}
	return -1
}

// fileMeta returns the title, frontmatter type and description, aliases JSON,
// and updated date of a file. Only markdown carries frontmatter.
func fileMeta(rel, ext, raw string) (title, typ, desc, aliases, updated string) {
	base := path.Base(rel)
	title = strings.TrimSuffix(base, filepath.Ext(base))
	aliases = "[]"
	if ext != ".md" {
		return title, "", "", aliases, ""
	}
	page := ParsePage(rel, raw)
	title = page.Title
	typ = page.Meta.Type
	desc = page.Meta.Description
	updated = page.Meta.Updated
	if len(page.Meta.Aliases) > 0 {
		if data, err := json.Marshal(page.Meta.Aliases); err == nil {
			aliases = string(data)
		}
	}
	return title, typ, desc, aliases, updated
}

// sha256Hex returns the lower-case hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sourceByHash returns the vault-relative path of an indexed source whose bytes
// hash to sum (design §2).
func (i *Index) sourceByHash(sum []byte) (string, bool, error) {
	var rel string
	err := i.db.QueryRow("SELECT path FROM files WHERE kind = ? AND hash = ? LIMIT 1", kindSource, hex.EncodeToString(sum)).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return rel, true, nil
}

// embedMissing embeds the chunks that have no vector for EmbeddingModel, 64 per
// batch. An embedding error is logged and left for the next sync (design §3).
func (i *Index) embedMissing(ctx context.Context) error {
	if i.Embedder == nil || i.EmbeddingModel == "" {
		return nil
	}
	rows, err := i.db.QueryContext(ctx,
		"SELECT c.id, c.text FROM chunks c LEFT JOIN vectors v ON v.chunk_id = c.id AND v.model = ? WHERE v.chunk_id IS NULL ORDER BY c.id",
		i.EmbeddingModel)
	if err != nil {
		return err
	}
	type pending struct {
		id   int64
		text string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.text); err != nil {
			_ = rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(todo) == 0 {
		return nil
	}

	for start := 0; start < len(todo); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(todo) {
			end = len(todo)
		}
		batch := todo[start:end]
		inputs := make([]string, len(batch))
		for k, p := range batch {
			inputs[k] = p.text
		}
		vectors, err := i.Embedder.Embed(ctx, i.EmbeddingModel, inputs)
		if err != nil {
			i.logger.Warn("knowledge index: embedding failed", "model", i.EmbeddingModel, "error", err)
			return nil
		}
		if len(vectors) != len(batch) {
			i.logger.Warn("knowledge index: embedding count mismatch", "model", i.EmbeddingModel, "want", len(batch), "got", len(vectors))
			return nil
		}
		tx, err := i.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for k, vec := range vectors {
			if _, err := tx.ExecContext(ctx,
				"INSERT OR REPLACE INTO vectors (chunk_id, model, dim, vec) VALUES (?, ?, ?, ?)",
				batch[k].id, i.EmbeddingModel, len(vec), encodeVec(vec)); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	i.dropVectors()
	return nil
}

// dropVectors clears the in-memory vector matrices after rows change
// (design §3).
func (i *Index) dropVectors() {
	i.vecMu.Lock()
	i.vecCache = nil
	i.vecMu.Unlock()
}

// Catalog returns at most limit page catalog lines formatted
// "title — type — description", ordered by title case-insensitively
// (design §5). limit <= 0 means indexCatalogMax.
func (i *Index) Catalog(limit int) ([]string, error) {
	if limit <= 0 {
		limit = indexCatalogMax
	}
	rows, err := i.db.Query("SELECT title, type, description FROM files WHERE kind = ? ORDER BY lower(title)", kindPage)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var title, typ, desc string
		if err := rows.Scan(&title, &typ, &desc); err != nil {
			return nil, err
		}
		parts := []string{title}
		if typ != "" {
			parts = append(parts, typ)
		}
		if desc != "" {
			parts = append(parts, desc)
		}
		out = append(out, strings.Join(parts, " — "))
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// SourceState is one row of sources_state (design §5).
type SourceState struct {
	Path      string
	Status    string
	Error     string
	SessionID string
	TurnID    string
	Agent     string
	UpdatedAt int64
}

// GetSourceState returns the compile state of a source.
func (i *Index) GetSourceState(path string) (SourceState, bool, error) {
	var st SourceState
	err := i.db.QueryRow(
		"SELECT path, status, error, session_id, turn_id, agent, updated_at FROM sources_state WHERE path = ?", path).
		Scan(&st.Path, &st.Status, &st.Error, &st.SessionID, &st.TurnID, &st.Agent, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceState{}, false, nil
	}
	if err != nil {
		return SourceState{}, false, err
	}
	return st, true, nil
}

// SetSourceState inserts or updates one compile state. A zero UpdatedAt is
// filled with now.
func (i *Index) SetSourceState(st SourceState) error {
	if st.UpdatedAt == 0 {
		st.UpdatedAt = i.now().UnixMilli()
	}
	_, err := i.db.Exec(`
INSERT INTO sources_state (path, status, error, session_id, turn_id, agent, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  status = excluded.status, error = excluded.error, session_id = excluded.session_id,
  turn_id = excluded.turn_id, agent = excluded.agent, updated_at = excluded.updated_at`,
		st.Path, st.Status, st.Error, st.SessionID, st.TurnID, st.Agent, st.UpdatedAt)
	return err
}

// ListSourceStates returns compile states ordered by updated_at, optionally
// filtered by status ("" means all).
func (i *Index) ListSourceStates(status string) ([]SourceState, error) {
	query := "SELECT path, status, error, session_id, turn_id, agent, updated_at FROM sources_state"
	args := []any{}
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY updated_at, path"
	rows, err := i.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SourceState
	for rows.Next() {
		var st SourceState
		if err := rows.Scan(&st.Path, &st.Status, &st.Error, &st.SessionID, &st.TurnID, &st.Agent, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// JournalMark returns the newest conversation text seq already journaled for a
// session, or 0 when the session has no mark (design §2).
func (i *Index) JournalMark(sessionID string) (int64, error) {
	var seq int64
	err := i.db.QueryRow("SELECT upto_seq FROM journal_marks WHERE session_id = ?", sessionID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// SetJournalMark records the newest conversation text seq already journaled for
// a session (design §2).
func (i *Index) SetJournalMark(sessionID string, seq int64) error {
	_, err := i.db.Exec(`
INSERT INTO journal_marks (session_id, upto_seq, updated_at) VALUES (?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET upto_seq = excluded.upto_seq, updated_at = excluded.updated_at`,
		sessionID, seq, i.now().Unix())
	return err
}

// PromoteOffset returns how many bytes of a journal file have been promoted
// into wiki pages, or 0 when the file has no mark (design §2).
func (i *Index) PromoteOffset(path string) (int64, error) {
	var offset int64
	err := i.db.QueryRow("SELECT offset FROM promote_marks WHERE path = ?", path).Scan(&offset)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return offset, nil
}

// SetPromoteOffset records how many bytes of a journal file have been promoted
// into wiki pages (design §2).
func (i *Index) SetPromoteOffset(path string, offset int64) error {
	_, err := i.db.Exec(`
INSERT INTO promote_marks (path, offset, updated_at) VALUES (?, ?, ?)
ON CONFLICT(path) DO UPDATE SET offset = excluded.offset, updated_at = excluded.updated_at`,
		path, offset, i.now().Unix())
	return err
}

// UpkeepValue returns an upkeep state value such as last_consolidation_at, and
// whether it is set (design §2).
func (i *Index) UpkeepValue(key string) (string, bool, error) {
	var value string
	err := i.db.QueryRow("SELECT value FROM upkeep WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetUpkeepValue inserts or updates an upkeep state value (design §2).
func (i *Index) SetUpkeepValue(key, value string) error {
	_, err := i.db.Exec(`
INSERT INTO upkeep (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

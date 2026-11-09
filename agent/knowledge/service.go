// Package knowledge runs lana's knowledge-vault service: it opens the vault and
// the derived index lazily, serves search, read, write, and ingest, and runs the
// background compile pass that turns one source into wiki pages
// (REQ-KNW-020–025, design §5).
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// queueSize is the depth of the per-service compile queue (design §5).
const queueSize = 256

// Compile states of one source (REQ-KNW-023).
const (
	statePending = "pending"
	stateDone    = "done"
	stateFailed  = "failed"
)

var _ core.Knowledge = (*Service)(nil)

// Service implements core.Knowledge. The vault and the index open on the first
// call, so an install that never uses knowledge creates nothing (design §5).
type Service struct {
	base  context.Context
	home  string
	cfg   core.KnowledgeConfig
	store core.SessionStore
	llm   core.LLMRegistry

	// LookupAgent returns the live config of one agent. The compile pass uses
	// the provider and model of the agent that ingested the source
	// (REQ-KNW-025). It is nil until the module binds it to the registry.
	LookupAgent func(name string) (core.AgentConfig, bool)

	// Config is the effective runtime config. The module binds it so the upkeep
	// loop can pick the knowledge agent (REQ-KNU-030, design §3).
	Config core.Config

	// compileMu serializes compile passes: the source queue worker and
	// promotion share it (REQ-KNW-023, design §7).
	compileMu sync.Mutex

	// upkeep is the state of the background upkeep loop (design §5).
	upkeep upkeepState

	// now is the service clock: time.Now in production, injectable in tests.
	now func() time.Time

	vaultPath string
	indexPath string

	mu    sync.Mutex
	vault *kv.Vault
	index *kv.Index

	startOnce sync.Once
	queue     chan string
	workers   sync.WaitGroup
}

// NewService creates a Service whose passes derive from base. The clock is
// time.Now; tests may replace Now or set a different now through this field.
func NewService(base context.Context, home string, cfg core.KnowledgeConfig, store core.SessionStore, llm core.LLMRegistry) *Service {
	if base == nil {
		base = context.Background()
	}
	return &Service{
		base:      base,
		home:      home,
		cfg:       cfg,
		store:     store,
		llm:       llm,
		now:       time.Now,
		vaultPath: kv.ResolvePath(home, cfg.Path),
		indexPath: filepath.Join(home, "knowledge.db"),
		queue:     make(chan string, queueSize),
	}
}

// IndexPath returns the derived index database path.
func (s *Service) IndexPath() string { return s.indexPath }

// VaultPath returns the resolved vault folder.
func (s *Service) VaultPath() string { return s.vaultPath }

// open returns the vault and index, opening them on first use. A failed open
// leaves both nil, so the next call retries (design §5).
func (s *Service) open() (*kv.Vault, *kv.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vault != nil && s.index != nil {
		return s.vault, s.index, nil
	}
	vault, err := kv.Open(s.vaultPath)
	if err != nil {
		return nil, nil, err
	}
	idx, err := kv.OpenIndex(s.indexPath, s.vaultPath)
	if err != nil {
		return nil, nil, err
	}
	vault.SetIndex(idx)
	s.attachEmbedder(idx)
	s.vault, s.index = vault, idx
	return vault, idx, nil
}

// attachEmbedder wires the optional embedding backend from
// knowledge.embedding_provider when that provider implements core.Embedder
// (REQ-KNW-031, API-KNW-004). With a router the calls go through
// LLMRouter.Embed, so they are retried and recorded (REQ-RTR-007).
func (s *Service) attachEmbedder(idx *kv.Index) {
	if embedder, ok := llmcall.Embedder(s.llm, s.cfg.EmbeddingProvider); ok {
		idx.SetEmbedder(embedder, s.cfg.EmbeddingModel)
	}
}

// start launches the single FIFO worker and, when the index already exists,
// re-queues the sources left pending by an earlier run (REQ-KNW-023). It is
// idempotent.
func (s *Service) start() {
	s.startOnce.Do(func() {
		s.workers.Add(2)
		go s.worker()
		go s.requeuePending()
	})
}

// drain waits for the worker and the startup requeue to finish. The caller
// cancels the base context first (design §5).
func (s *Service) drain() { s.workers.Wait() }

// requeuePending compiles the sources a previous run left pending.
func (s *Service) requeuePending() {
	defer s.workers.Done()
	if _, err := os.Stat(s.indexPath); err != nil {
		return
	}
	_, idx, err := s.open()
	if err != nil {
		return
	}
	states, err := idx.ListSourceStates(statePending)
	if err != nil {
		return
	}
	for _, st := range states {
		s.enqueue(st.Path)
	}
}

// worker drains the compile queue in ingest order.
func (s *Service) worker() {
	defer s.workers.Done()
	for {
		select {
		case <-s.base.Done():
			return
		case src := <-s.queue:
			s.compileSource(src)
		}
	}
}

// enqueue adds one source to the compile queue.
func (s *Service) enqueue(src string) {
	select {
	case s.queue <- src:
	case <-s.base.Done():
	}
}

// Close closes the index and releases the vault. The caller cancels the base
// context first so in-flight passes end (design §5).
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.index
	s.index = nil
	s.vault = nil
	if idx != nil {
		return idx.Close()
	}
	return nil
}

// Search returns files, best first, each with its best chunk as a snippet
// (REQ-KNW-030–032).
func (s *Service) Search(ctx context.Context, q core.KnowledgeQuery) ([]core.KnowledgeHit, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	if err := idx.Sync(ctx); err != nil {
		return nil, err
	}
	hits, err := idx.Search(ctx, kv.Query{Text: q.Text, Limit: q.Limit, Kind: q.Kind, Type: q.Type})
	if err != nil {
		return nil, err
	}
	return knowledgeHits(hits), nil
}

// Read returns the text of one vault file by path or by page title/alias, with
// line numbers and an optional line range. A PDF source reads as extracted
// text (REQ-KNW-041).
func (s *Service) Read(ctx context.Context, ref string, from, to int) (core.KnowledgeDoc, error) {
	vault, _, err := s.open()
	if err != nil {
		return core.KnowledgeDoc{}, err
	}
	rel, abs, err := s.resolveRead(vault, ref)
	if err != nil {
		return core.KnowledgeDoc{}, err
	}
	var raw string
	if strings.EqualFold(filepath.Ext(abs), ".pdf") {
		pages, err := attach.PDFText(abs)
		if err != nil {
			return core.KnowledgeDoc{}, err
		}
		raw = strings.Join(pages, "\n")
	} else {
		data, err := os.ReadFile(abs)
		if err != nil {
			return core.KnowledgeDoc{}, err
		}
		raw = string(data)
	}
	doc := newDoc(rel, raw, from, to)
	return doc, nil
}

// Write applies one page change with the given actor, then re-indexes the
// changed file (REQ-KNW-010).
func (s *Service) Write(ctx context.Context, w core.KnowledgeWrite) (core.KnowledgeWriteResult, error) {
	vault, idx, err := s.open()
	if err != nil {
		return core.KnowledgeWriteResult{}, err
	}
	actor := strings.TrimSpace(w.Actor)
	if actor == "" {
		actor = "agent"
	}
	res, err := vault.Apply(kv.Op{
		Action:      w.Action,
		Title:       w.Title,
		Type:        w.Type,
		Description: w.Description,
		Body:        w.Body,
		Text:        w.Text,
		Heading:     w.Heading,
		OldText:     w.OldText,
		NewText:     w.NewText,
		Aliases:     w.Aliases,
		Tags:        w.Tags,
	}, actor)
	if err != nil {
		return core.KnowledgeWriteResult{}, err
	}
	if err := idx.SyncFile(filepath.Join(vault.Root(), filepath.FromSlash(res.Path))); err != nil {
		slog.Warn("knowledge write: re-index failed", "path", res.Path, "error", err)
	}
	return core.KnowledgeWriteResult{Path: res.Path, Op: res.Op, Size: res.Size}, nil
}

// Ingest stores one source and queues its compile pass. A duplicate returns the
// existing path and is re-queued only when its state is failed or pending
// (REQ-KNW-020, -023, -042).
func (s *Service) Ingest(ctx context.Context, in core.KnowledgeIngest) (core.KnowledgeIngestResult, error) {
	vault, idx, err := s.open()
	if err != nil {
		return core.KnowledgeIngestResult{}, err
	}
	srcPath, duplicate, err := vault.SaveSource(in.Title, in.Ext, in.Data)
	if err != nil {
		return core.KnowledgeIngestResult{}, err
	}
	if duplicate {
		st, ok, err := idx.GetSourceState(srcPath)
		if err != nil {
			return core.KnowledgeIngestResult{}, err
		}
		if !ok || (st.Status != stateFailed && st.Status != statePending) {
			return core.KnowledgeIngestResult{Path: srcPath, Duplicate: true, Queued: false}, nil
		}
	}
	s.setPending(idx, srcPath, in)
	s.enqueue(srcPath)
	return core.KnowledgeIngestResult{Path: srcPath, Duplicate: duplicate, Queued: true}, nil
}

// Recall returns up to limit wiki pages relevant to text, skipping any path the
// caller already showed (REQ-KNW-050).
func (s *Service) Recall(ctx context.Context, text string, limit int, skip func(path string) bool) ([]core.KnowledgeHit, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	if err := idx.Sync(ctx); err != nil {
		return nil, err
	}
	hits, err := idx.Recall(ctx, text, limit, nil, "")
	if err != nil {
		return nil, err
	}
	out := make([]core.KnowledgeHit, 0, len(hits))
	for _, h := range hits {
		if skip != nil && skip(h.Path) {
			continue
		}
		out = append(out, knowledgeHit(h))
	}
	return out, nil
}

// setPending writes the pending state row of one source with its session, turn,
// and agent (REQ-KNW-023).
func (s *Service) setPending(idx *kv.Index, srcPath string, in core.KnowledgeIngest) {
	if err := idx.SetSourceState(kv.SourceState{
		Path:      srcPath,
		Status:    statePending,
		SessionID: in.SessionID,
		TurnID:    in.TurnID,
		Agent:     in.AgentName,
		UpdatedAt: s.now().UnixMilli(),
	}); err != nil {
		slog.Warn("knowledge ingest: saving pending state failed", "source", srcPath, "error", err)
	}
}

// resolveRead turns a read reference into a vault-relative and absolute path. A
// reference with a slash is a vault path; otherwise a page title or alias is
// tried first, then a root file name (REQ-KNW-041, -043).
func (s *Service) resolveRead(vault *kv.Vault, ref string) (rel, abs string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", errors.New("knowledge path is required")
	}
	if strings.ContainsAny(ref, "/\\") || filepath.IsAbs(ref) {
		rel, err = vaultRel(vault.Root(), ref)
		if err != nil {
			return "", "", err
		}
		abs = filepath.Join(vault.Root(), filepath.FromSlash(rel))
		if _, statErr := os.Stat(abs); statErr != nil {
			return "", "", statErr
		}
		return rel, abs, nil
	}
	if page, ok, err := vault.FindPage(ref); err != nil {
		return "", "", err
	} else if ok {
		rel = page.Path
		return rel, filepath.Join(vault.Root(), filepath.FromSlash(rel)), nil
	}
	rel, err = vaultRel(vault.Root(), ref)
	if err != nil {
		return "", "", err
	}
	abs = filepath.Join(vault.Root(), filepath.FromSlash(rel))
	if _, statErr := os.Stat(abs); statErr != nil {
		return "", "", fmt.Errorf("knowledge: %s not found", ref)
	}
	return rel, abs, nil
}

// knowledgeHits converts index hits to the core shape.
func knowledgeHits(hits []kv.Hit) []core.KnowledgeHit {
	out := make([]core.KnowledgeHit, len(hits))
	for i, h := range hits {
		out[i] = knowledgeHit(h)
	}
	return out
}

func knowledgeHit(h kv.Hit) core.KnowledgeHit {
	return core.KnowledgeHit{
		Path: h.Path, Title: h.Title, Kind: h.Kind, Type: h.Type, Description: h.Description,
		Heading: h.Heading, Snippet: h.Snippet, Updated: h.Updated, StartLine: h.StartLine,
	}
}

// newDoc builds a KnowledgeDoc for raw with the requested 1-based line range.
func newDoc(rel, raw string, from, to int) core.KnowledgeDoc {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	total := len(lines)
	if from < 1 {
		from = 1
	}
	if to <= 0 || to > total {
		to = total
	}
	if from > total {
		from = total
	}
	doc := core.KnowledgeDoc{Path: rel, Title: titleOfPath(rel), From: from, To: to, Total: total}
	if from <= to && total > 0 {
		doc.Lines = append([]string(nil), lines[from-1:to]...)
	}
	return doc
}

// vaultRel normalizes a reference to a vault-relative, forward-slash path and
// rejects one that escapes the vault (REQ-KNW-043).
func vaultRel(root, ref string) (string, error) {
	if filepath.IsAbs(ref) {
		r, err := filepath.Rel(root, ref)
		if err != nil {
			return "", err
		}
		ref = r
	}
	p := filepath.ToSlash(filepath.Clean(ref))
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("knowledge: %q is outside the vault", ref)
	}
	return p, nil
}

// titleOfPath is the page title of a vault-relative markdown path.
func titleOfPath(rel string) string {
	base := path.Base(rel)
	return strings.TrimSuffix(base, path.Ext(base))
}

// sourceTitle is the page title of a source path: its file name without the
// extension.
func sourceTitle(rel string) string { return titleOfPath(rel) }

// sourceLink is the wikilink a page uses to cite a source.
func sourceLink(rel string) string { return "[[" + rel + "]]" }

// firstRunes returns the first n runes of s.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

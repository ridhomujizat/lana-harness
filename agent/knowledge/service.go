// Package knowledge runs lana's knowledge-vault service: it opens the vault and
// the derived index lazily, serves search, read, write, and ingest, and runs the
// background compile pass that turns one source into wiki pages
// (REQ-KNW-020–025, design §5).
package knowledge

import (
	"context"
	"database/sql"
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
	"github.com/ridhomujizat/lana-harness/internal/id"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// queueSize is the depth of the per-service compile queue (design §5).
const queueSize = 256

// Proposal statuses (design §2) and the domain stats bounds (API-DKN-008).
const (
	proposalStatusPending  = "pending"
	proposalStatusApproved = "approved"
	proposalStatusApplied  = "applied"
	proposalStatusRejected = "rejected"

	domainStatsPageMax   = 500
	domainStatsRecentMax = 5
	domainStatBucket     = 7 * 24 * time.Hour
)

// Compile states of one source (REQ-KNW-023).
const (
	statePending = "pending"
	stateDone    = "done"
	stateFailed  = "failed"
)

var _ core.Knowledge = (*Service)(nil)
var _ core.KnowledgeDomains = (*Service)(nil)

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

	// promoteDomainFn runs the domain promotion of one agent (design §4.2). It
	// is nil until that pass is wired; PromoteSoon is nil-safe.
	promoteDomainFn func(ctx context.Context, agent string) error

	// promoteMu guards promoteFlight, the in-flight domain promotions per agent
	// (design §4). A second request for the same agent is dropped.
	promoteMu     sync.Mutex
	promoteFlight map[string]bool

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
	// A runtime without a home folder (a library user, a test) has no vault;
	// a relative path would write "knowledge/" into the working directory.
	if s.home == "" && !filepath.IsAbs(s.vaultPath) {
		return nil, nil, errors.New("knowledge needs a home folder")
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
	hits, err := idx.Search(ctx, kv.Query{Text: q.Text, Limit: q.Limit, Kind: q.Kind, Type: q.Type, Scopes: q.Scopes})
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
	rel, _, err := s.resolveRead(vault, ref)
	if err != nil {
		return core.KnowledgeDoc{}, err
	}
	return s.docForRel(vault, rel, from, to)
}

// ReadIn is Read with scope resolution: a path ref is read as Read; a title or
// alias resolves in caller's domain first, then the shared wiki, then the other
// domains in name order. A non-empty scope restricts title resolution to that
// scope ("shared" or an agent name) (REQ-DKN-011).
func (s *Service) ReadIn(ctx context.Context, ref, caller, scope string, from, to int) (core.KnowledgeDoc, error) {
	vault, _, err := s.open()
	if err != nil {
		return core.KnowledgeDoc{}, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return core.KnowledgeDoc{}, errors.New("knowledge path is required")
	}
	if strings.ContainsAny(ref, "/\\") || filepath.IsAbs(ref) {
		rel, _, err := s.resolveRead(vault, ref)
		if err != nil {
			return core.KnowledgeDoc{}, err
		}
		return s.docForRel(vault, rel, from, to)
	}
	for _, sc := range readScopes(vault.Root(), caller, scope) {
		page, ok, err := vault.FindPageIn(sc, ref)
		if err != nil {
			return core.KnowledgeDoc{}, err
		}
		if ok {
			return s.docForRel(vault, page.Path, from, to)
		}
	}
	if scope == "" {
		// Fall back to a root file name, like Read (REQ-KNW-041).
		rel, err := vaultRel(vault.Root(), ref)
		if err != nil {
			return core.KnowledgeDoc{}, err
		}
		abs := filepath.Join(vault.Root(), filepath.FromSlash(rel))
		if _, statErr := os.Stat(abs); statErr != nil {
			return core.KnowledgeDoc{}, fmt.Errorf("knowledge: %s not found", ref)
		}
		return s.docForRel(vault, rel, from, to)
	}
	return core.KnowledgeDoc{}, fmt.Errorf("knowledge: %s not found", ref)
}

// docForRel reads one vault-relative file into a KnowledgeDoc and sets its
// scope. A PDF reads as extracted text (REQ-KNW-041).
func (s *Service) docForRel(vault *kv.Vault, rel string, from, to int) (core.KnowledgeDoc, error) {
	abs := filepath.Join(vault.Root(), filepath.FromSlash(rel))
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
	doc.Scope = kv.ScopeOf(rel)
	return doc, nil
}

// Write applies one page change with the given actor, then re-indexes the
// changed file (REQ-KNW-010).
func (s *Service) Write(ctx context.Context, w core.KnowledgeWrite) (core.KnowledgeWriteResult, error) {
	scope, err := s.writeScope(w.Scope)
	if err != nil {
		return core.KnowledgeWriteResult{}, err
	}
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
		Scope:       scope,
		Source:      w.Source,
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

// Recall returns up to limit wiki pages relevant to text, from scopes only (nil
// or empty means every scope), skipping any path the caller already showed
// (REQ-KNW-050, REQ-DKN-012).
func (s *Service) Recall(ctx context.Context, text string, limit int, scopes []string, prefer string, skip func(path string) bool) ([]core.KnowledgeHit, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	if err := idx.Sync(ctx); err != nil {
		return nil, err
	}
	hits, err := idx.Recall(ctx, text, limit, scopes, prefer)
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
		Path: h.Path, Title: h.Title, Kind: h.Kind, Type: h.Type, Scope: h.Scope, Description: h.Description,
		Heading: h.Heading, Snippet: h.Snippet, Updated: h.Updated, StartLine: h.StartLine,
	}
}

// writeScope normalizes a KnowledgeWrite scope: "" or "shared" is the shared
// wiki; an agent name must be a domain agent (design §4). It returns the scope
// to hand the vault.
func (s *Service) writeScope(raw string) (string, error) {
	scope := strings.TrimSpace(raw)
	if scope == "" || scope == kv.ScopeShared {
		return kv.ScopeShared, nil
	}
	if !domainAgent(s.Config, scope) {
		return "", fmt.Errorf("agent %q has no knowledge domain", scope)
	}
	return scope, nil
}

// HasDomain reports whether agent owns a knowledge domain (design §4).
func (s *Service) HasDomain(agent string) bool {
	return domainAgent(s.Config, agent)
}

// Propose stores one requested change to another agent's domain (REQ-DKN-021).
// The target must be a domain agent other than the proposer.
func (s *Service) Propose(ctx context.Context, p core.KnowledgeProposal) (core.KnowledgeProposal, error) {
	if !domainAgent(s.Config, p.Agent) {
		return core.KnowledgeProposal{}, fmt.Errorf("agent %q has no knowledge domain", p.Agent)
	}
	if p.FromAgent != "" && p.FromAgent == p.Agent {
		return core.KnowledgeProposal{}, errors.New("cannot propose to your own domain; write it")
	}
	_, idx, err := s.open()
	if err != nil {
		return core.KnowledgeProposal{}, err
	}
	op, err := json.Marshal(p.Write)
	if err != nil {
		return core.KnowledgeProposal{}, err
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = p.Write.Title
	}
	now := s.now().UnixMilli()
	stored := kv.Proposal{
		ID:        "p_" + id.New(),
		Agent:     p.Agent,
		FromAgent: p.FromAgent,
		SessionID: p.SessionID,
		TurnID:    p.TurnID,
		OwnerTurn: p.OwnerTurn,
		Op:        string(op),
		Title:     title,
		Status:    proposalStatusPending,
		CreatedAt: now,
	}
	if err := idx.AddProposal(stored); err != nil {
		return core.KnowledgeProposal{}, err
	}
	p.ID = stored.ID
	p.Title = title
	p.Status = stored.Status
	p.CreatedAt = now
	return p, nil
}

// PendingProposals returns every pending proposal of every agent, newest first
// (REQ-DKN-060).
func (s *Service) PendingProposals(ctx context.Context) ([]core.KnowledgeProposal, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	stored, err := idx.PendingProposals()
	if err != nil {
		return nil, err
	}
	out := make([]core.KnowledgeProposal, 0, len(stored))
	for _, p := range stored {
		out = append(out, coreProposal(p))
	}
	return out, nil
}

// DecidedProposals returns the latest decided proposals (applied, approved,
// rejected), newest decided first, at most limit (API-KGR-003, REQ-KGR-030).
func (s *Service) DecidedProposals(ctx context.Context, limit int) ([]core.KnowledgeProposal, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	stored, err := idx.DecidedProposals([]string{proposalStatusApplied, proposalStatusApproved, proposalStatusRejected}, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.KnowledgeProposal, 0, len(stored))
	for _, p := range stored {
		out = append(out, coreProposal(p))
	}
	return out, nil
}

// DecideProposal approves or rejects one pending proposal (REQ-DKN-061). An
// unknown or already-decided id wraps os.ErrNotExist.
func (s *Service) DecideProposal(ctx context.Context, id string, approve bool) (core.KnowledgeProposal, error) {
	_, idx, err := s.open()
	if err != nil {
		return core.KnowledgeProposal{}, err
	}
	p, err := idx.GetProposal(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.KnowledgeProposal{}, fmt.Errorf("knowledge proposal %s: %w", id, os.ErrNotExist)
		}
		return core.KnowledgeProposal{}, err
	}
	if p.Status != proposalStatusPending {
		return core.KnowledgeProposal{}, fmt.Errorf("knowledge proposal %s: %w", id, os.ErrNotExist)
	}
	status := proposalStatusRejected
	if approve {
		status = proposalStatusApproved
	}
	now := s.now().UnixMilli()
	if err := idx.SetProposal(id, status, p.Result, now); err != nil {
		return core.KnowledgeProposal{}, err
	}
	p.Status = status
	p.DecidedAt = now
	if approve {
		s.PromoteSoon(p.Agent)
	}
	return coreProposal(p), nil
}

// PromoteSoon requests a domain promotion of agent in the background on the
// service base context (REQ-DKN-033, design §4). A request is dropped while a
// pass for the same agent is in flight; a nil hook is a no-op.
func (s *Service) PromoteSoon(agent string) {
	if s.promoteDomainFn == nil || agent == "" {
		return
	}
	if !s.promoteBegin(agent) {
		return
	}
	go func() {
		defer s.promoteEnd(agent)
		if err := s.promoteDomainFn(s.base, agent); err != nil {
			slog.Warn("knowledge promotion: domain pass failed", "agent", agent, "error", err)
		}
	}()
}

// promoteBegin claims the in-flight slot of one agent, reporting false when a
// pass for it is already running (design §4).
func (s *Service) promoteBegin(agent string) bool {
	s.promoteMu.Lock()
	defer s.promoteMu.Unlock()
	if s.promoteFlight[agent] {
		return false
	}
	if s.promoteFlight == nil {
		s.promoteFlight = map[string]bool{}
	}
	s.promoteFlight[agent] = true
	return true
}

// promoteEnd releases the in-flight slot of one agent.
func (s *Service) promoteEnd(agent string) {
	s.promoteMu.Lock()
	delete(s.promoteFlight, agent)
	s.promoteMu.Unlock()
}

// Pages returns a page of one scope, newest first, with the total before the
// limit (API-DKN-004).
func (s *Service) Pages(ctx context.Context, scope, q string, limit int) ([]core.KnowledgePageInfo, int, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := idx.Pages(scope, q, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]core.KnowledgePageInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, pageInfo(r))
	}
	return out, total, nil
}

// Page returns the document and raw markdown of one vault file by path
// (API-DKN-005), with the API-KGR-002 detail fields: type, description, updated,
// frontmatter sources, outgoing resolved links, unresolved targets, and
// backlinks with their line context. A path outside the wiki and journal folders
// is refused; a missing file wraps os.ErrNotExist.
func (s *Service) Page(ctx context.Context, ref string) (core.KnowledgeDoc, string, error) {
	vault, idx, err := s.open()
	if err != nil {
		return core.KnowledgeDoc{}, "", err
	}
	rel, err := validPagePath(ref)
	if err != nil {
		return core.KnowledgeDoc{}, "", err
	}
	abs := filepath.Join(vault.Root(), filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return core.KnowledgeDoc{}, "", fmt.Errorf("knowledge page %s: %w", rel, os.ErrNotExist)
		}
		return core.KnowledgeDoc{}, "", err
	}
	raw := string(data)
	doc := newDoc(rel, raw, 0, 0)
	doc.Scope = kv.ScopeOf(rel)
	meta := kv.ParsePage(rel, raw).Meta
	doc.Type = meta.Type
	doc.Description = meta.Description
	doc.Updated = meta.Updated
	doc.Sources = sourceTargets(meta.Sources)
	// The graph reads resolve links from the index, so index this file first.
	if err := idx.Sync(ctx); err != nil {
		return core.KnowledgeDoc{}, "", err
	}
	out, unresolved, in, err := idx.PageLinks(ctx, rel)
	if err != nil {
		return core.KnowledgeDoc{}, "", err
	}
	doc.LinksOut = linkRefs(out)
	doc.Unresolved = unresolved
	if doc.Unresolved == nil {
		doc.Unresolved = []string{}
	}
	doc.LinksIn = backlinks(in)
	return doc, raw, nil
}

// sourceTargets renders frontmatter sources for the API: a source stored as a
// wikilink is returned as its target, without the brackets or a display label
// (API-KGR-002).
func sourceTargets(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, sourceTarget(s))
	}
	return out
}

// sourceTarget returns one source as its link target.
func sourceTarget(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "[["), "]]")
	}
	if i := strings.IndexByte(s, '|'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// linkRefs maps resolved outgoing links to the core shape.
func linkRefs(refs []kv.FileRef) []core.KnowledgeLinkRef {
	out := make([]core.KnowledgeLinkRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, core.KnowledgeLinkRef{Path: ref.Path, Title: ref.Title, Scope: ref.Scope})
	}
	return out
}

// backlinks maps resolved inbound links to the core shape.
func backlinks(in []kv.Backlink) []core.KnowledgeBacklink {
	out := make([]core.KnowledgeBacklink, 0, len(in))
	for _, b := range in {
		out = append(out, core.KnowledgeBacklink{
			Path: b.Path, Title: b.Title, Scope: b.Scope, Context: b.Context,
		})
	}
	return out
}

// graphNodeMax is the node cap Graph asks the index for (REQ-KGR-012).
const graphNodeMax = 2000

// Graph returns the knowledge graph of the vault: page nodes (plus journals and
// sources when asked), resolved page-to-page edges, one hub per scope, and the
// page, link, and broken counts (API-KGR-001, REQ-KGR-010–012). It runs Sync
// first, like Pages, so files added outside lana are seen.
func (s *Service) Graph(ctx context.Context, scope string, journals, sources bool) (core.KnowledgeGraph, error) {
	graph := core.KnowledgeGraph{
		Nodes: []core.KnowledgeGraphNode{},
		Hubs:  []core.KnowledgeGraphHub{},
		Edges: []core.KnowledgeGraphEdge{},
	}
	_, idx, err := s.open()
	if err != nil {
		return graph, err
	}
	if err := idx.Sync(ctx); err != nil {
		return graph, err
	}
	g, err := idx.Graph(ctx, kv.GraphQuery{Scope: scope, Journals: journals, Sources: sources, Max: graphNodeMax})
	if err != nil {
		return graph, err
	}
	for _, n := range g.Nodes {
		graph.Nodes = append(graph.Nodes, core.KnowledgeGraphNode{
			ID: n.ID, Title: n.Title, Scope: n.Scope, Kind: n.Kind, Type: n.Type,
			Updated: n.Updated, Degree: n.Degree,
		})
	}
	for _, e := range g.Edges {
		graph.Edges = append(graph.Edges, core.KnowledgeGraphEdge{Source: e.Source, Target: e.Target})
	}
	for _, h := range g.Hubs {
		graph.Hubs = append(graph.Hubs, core.KnowledgeGraphHub{Scope: h.Scope, Pages: h.Pages})
	}
	graph.Counts = core.KnowledgeGraphCounts{Pages: g.Pages, Links: g.Links, Broken: g.Broken}
	graph.Truncated = g.Truncated
	return graph, nil
}

// DomainStats returns the "What it knows" stats of one agent's domain
// (API-DKN-008): the page count, the pages changed in the last 7 days, 8 weekly
// buckets (oldest first, the last ending now), and the 5 newest pages.
func (s *Service) DomainStats(ctx context.Context, agent string, now time.Time) (core.KnowledgeDomainStats, error) {
	_, idx, err := s.open()
	if err != nil {
		return core.KnowledgeDomainStats{}, err
	}
	rows, total, err := idx.Pages(agent, "", domainStatsPageMax)
	if err != nil {
		return core.KnowledgeDomainStats{}, err
	}
	stats := core.KnowledgeDomainStats{Pages: total, Recent: []core.KnowledgePageInfo{}}
	weekAgo := now.Add(-domainStatBucket)
	for _, r := range rows {
		t := time.UnixMilli(r.Mtime)
		if !t.Before(weekAgo) && !t.After(now) {
			stats.Changed7d++
		}
		for i := 0; i < len(stats.Weeks); i++ {
			start := now.Add(-time.Duration(len(stats.Weeks)-i) * domainStatBucket)
			end := now.Add(-time.Duration(len(stats.Weeks)-i-1) * domainStatBucket)
			if !t.Before(start) && t.Before(end) {
				stats.Weeks[i]++
				break
			}
		}
	}
	recent := rows
	if len(recent) > domainStatsRecentMax {
		recent = recent[:domainStatsRecentMax]
	}
	for _, r := range recent {
		stats.Recent = append(stats.Recent, pageInfo(r))
	}
	return stats, nil
}

// ScopeStats returns one stat per vault scope, shared first (API-DKN-003).
func (s *Service) ScopeStats(ctx context.Context) ([]core.KnowledgeScopeStat, error) {
	_, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	stats, err := idx.ScopeStats()
	if err != nil {
		return nil, err
	}
	out := make([]core.KnowledgeScopeStat, 0, len(stats))
	for _, st := range stats {
		out = append(out, core.KnowledgeScopeStat{
			Scope:       st.Scope,
			Pages:       st.Pages,
			JournalDays: st.JournalDays,
			UpdatedAt:   st.UpdatedAt,
		})
	}
	return out, nil
}

// coreProposal maps one stored proposal to the core shape, decoding its Op JSON
// into Write (design §2).
func coreProposal(p kv.Proposal) core.KnowledgeProposal {
	out := core.KnowledgeProposal{
		ID: p.ID, Agent: p.Agent, FromAgent: p.FromAgent, SessionID: p.SessionID, TurnID: p.TurnID,
		Title: p.Title, Status: p.Status, Result: p.Result, OwnerTurn: p.OwnerTurn,
		CreatedAt: p.CreatedAt, DecidedAt: p.DecidedAt,
	}
	if p.Op != "" {
		var w core.KnowledgeWrite
		if err := json.Unmarshal([]byte(p.Op), &w); err != nil {
			slog.Debug("knowledge proposal: decoding op failed", "id", p.ID, "error", err)
		} else {
			out.Write = w
		}
	}
	return out
}

// pageInfo maps one index page row to the core shape.
func pageInfo(r kv.PageRow) core.KnowledgePageInfo {
	return core.KnowledgePageInfo{
		Path: r.Path, Title: r.Title, Scope: r.Scope, Type: r.Type,
		Description: r.Description, Updated: r.Updated, Mtime: r.Mtime,
	}
}

// readScopes returns the title-resolution order of ReadIn: caller's domain,
// then shared, then every domain folder in name order; an explicit scope
// restricts it to that scope (REQ-DKN-011).
func readScopes(root, caller, scope string) []string {
	if scope != "" {
		return []string{scope}
	}
	var out []string
	seen := map[string]bool{}
	add := func(sc string) {
		if sc == "" || seen[sc] {
			return
		}
		seen[sc] = true
		out = append(out, sc)
	}
	add(caller)
	add(kv.ScopeShared)
	for _, name := range domainFolders(root) {
		add(name)
	}
	return out
}

// domainFolders returns the agent domain folders under agents/, sorted by name
// (design §1).
func domainFolders(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && kv.ValidScopeName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// validPagePath normalizes and validates a vault page path for Page
// (API-DKN-005): vault-relative, no "..", ending in .md, inside wiki/,
// journal/, agents/<name>/wiki/, or agents/<name>/journal/.
func validPagePath(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || filepath.IsAbs(ref) || strings.ContainsAny(ref, "\\") {
		return "", fmt.Errorf("invalid path %q", ref)
	}
	rel := path.Clean(ref)
	if rel != ref || rel == "." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("invalid path %q", ref)
	}
	if !strings.EqualFold(path.Ext(rel), ".md") {
		return "", fmt.Errorf("invalid path %q: expected a markdown file", ref)
	}
	if !insidePageFolders(rel) {
		return "", fmt.Errorf("invalid path %q: outside the wiki or journal folders", ref)
	}
	return rel, nil
}

// insidePageFolders reports whether a vault-relative path sits in a wiki or
// journal folder (shared or a domain).
func insidePageFolders(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return false
	}
	if parts[0] == "wiki" || parts[0] == "journal" {
		return true
	}
	return parts[0] == "agents" && len(parts) >= 4 && kv.ValidScopeName(parts[1]) &&
		(parts[2] == "wiki" || parts[2] == "journal")
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

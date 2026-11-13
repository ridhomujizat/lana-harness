package core

import (
	"context"
	"errors"
	"time"
)

// ErrKnowledgeBusy is returned by KnowledgeAdmin.Consolidate when a nightly run
// is already in progress (API-KNU-006, REQ-KNU-020).
var ErrKnowledgeBusy = errors.New("knowledge consolidation is already running")

// KnowledgeAdmin is the knowledge upkeep surface the runtime and the web layer
// use (design §5, API-KNU-003–006). The concrete implementation lives in
// agent/knowledge.
type KnowledgeAdmin interface {
	// StartUpkeep starts the background upkeep loop once; later calls no-op.
	StartUpkeep(ctx context.Context)
	// Status reports the vault state for GET /v1/knowledge (API-KNU-003).
	Status(ctx context.Context) (KnowledgeStatus, error)
	// Report returns the path and markdown of the latest lint report
	// (API-KNU-004). It wraps os.ErrNotExist when no report exists.
	Report(ctx context.Context) (path, markdown string, err error)
	// SaveInbox stores one uploaded file under inbox/ and requests a scan
	// (API-KNU-005).
	SaveInbox(ctx context.Context, name string, data []byte) (string, error)
	// Consolidate runs one nightly pass. It returns ErrKnowledgeBusy while a
	// run is in progress (API-KNU-006, REQ-KNU-020).
	Consolidate(ctx context.Context) error
	// ScanInboxNow requests an inbox scan on the next tick (REQ-KNU-011).
	ScanInboxNow()
}

// KnowledgeStatus mirrors the GET /v1/knowledge response (API-KNU-003). Times
// are Unix milliseconds; 0 means never. Lists are never null.
type KnowledgeStatus struct {
	Available        bool                      `json:"available"`
	Path             string                    `json:"path"`
	Exists           bool                      `json:"exists"`
	Counts           KnowledgeCounts           `json:"counts"`
	SourcesPending   []string                  `json:"sources_pending"`
	SourcesFailed    []KnowledgeSourceError    `json:"sources_failed"`
	InboxUnsupported []string                  `json:"inbox_unsupported"`
	Embedding        KnowledgeEmbeddingStatus  `json:"embedding"`
	Agent            string                    `json:"agent"`
	AgentError       string                    `json:"agent_error"`
	Consolidation    KnowledgeConsolidation    `json:"consolidation"`
	Log              []KnowledgeLogEntry       `json:"log"`
	Scopes           []KnowledgeScopeStat      `json:"scopes"`
	ReviewPending    int                       `json:"review_pending"`
	JournalFailed    []KnowledgeJournalFailure `json:"journal_failed"`
}

// KnowledgeJournalFailure is the most recent failed journal pass of one session
// shown by GET /v1/knowledge (REQ-KBG-012). At is Unix milliseconds; Failures
// counts the consecutive failures.
type KnowledgeJournalFailure struct {
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
	At        int64  `json:"at"`
	Failures  int    `json:"failures"`
}

// KnowledgeScopeStat is the page count, journal-day count, and newest mtime of
// one vault scope for GET /v1/knowledge (API-DKN-003). UpdatedAt is Unix
// milliseconds; 0 means no file.
type KnowledgeScopeStat struct {
	Scope       string `json:"scope"`
	Pages       int    `json:"pages"`
	JournalDays int    `json:"journal_days"`
	UpdatedAt   int64  `json:"updated_at"`
}

// KnowledgePageInfo is one row of the knowledge page list (API-DKN-004). Mtime
// is Unix milliseconds.
type KnowledgePageInfo struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Scope       string `json:"scope"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Updated     string `json:"updated"`
	Mtime       int64  `json:"mtime"`
}

// KnowledgeDomainStats is the "What it knows" block of an agent overview
// (API-DKN-008). Weeks holds 8 counts of domain pages by 7-day bucket, oldest
// first, the last bucket ending now. Recent holds at most 5 pages, newest
// first.
type KnowledgeDomainStats struct {
	Pages     int                 `json:"pages"`
	Changed7d int                 `json:"changed_7d"`
	Weeks     [8]int              `json:"weeks"`
	Recent    []KnowledgePageInfo `json:"recent"`
}

// KnowledgeCounts is the page, source, and journal-day count of the vault
// (API-KNU-003).
type KnowledgeCounts struct {
	Pages       int `json:"pages"`
	Sources     int `json:"sources"`
	JournalDays int `json:"journal_days"`
}

// KnowledgeSourceError is one failed source and the error of its compile pass
// (API-KNU-003).
type KnowledgeSourceError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// KnowledgeEmbeddingStatus describes the optional embedding index
// (API-KNU-003). Provider is "" when the index is keyword-only.
type KnowledgeEmbeddingStatus struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Chunks   int    `json:"chunks"`
	Vectors  int    `json:"vectors"`
}

// KnowledgeConsolidation is the last and next nightly run (API-KNU-003).
type KnowledgeConsolidation struct {
	Running   bool   `json:"running"`
	LastAt    int64  `json:"last_at"`
	LastError string `json:"last_error"`
	NextAt    int64  `json:"next_at"`
	Report    string `json:"report"`
}

// KnowledgeLogEntry is one log.md entry: its heading and its bullet lines
// (API-KNU-003).
type KnowledgeLogEntry struct {
	Heading string   `json:"heading"`
	Lines   []string `json:"lines"`
}

// Knowledge is the knowledge vault service a turn and the knowledge tools use
// (REQ-KNW-020–025, design §5). The concrete implementation lives in
// agent/knowledge; callers reach it through the agent registry or the
// ServiceKnowledge service key.
type Knowledge interface {
	Search(ctx context.Context, q KnowledgeQuery) ([]KnowledgeHit, error)
	Read(ctx context.Context, ref string, from, to int) (KnowledgeDoc, error)
	Write(ctx context.Context, w KnowledgeWrite) (KnowledgeWriteResult, error)
	Ingest(ctx context.Context, in KnowledgeIngest) (KnowledgeIngestResult, error)
	// Recall returns up to limit pages relevant to text, from scopes only (nil
	// or empty means every scope); on an equal score a hit whose scope is
	// prefer comes first (REQ-DKN-012).
	Recall(ctx context.Context, text string, limit int, scopes []string, prefer string, skip func(path string) bool) ([]KnowledgeHit, error)
}

// KnowledgeDomains is the scoped surface of the knowledge service: domains,
// proposals, page lists, and per-domain stats (design §3). The concrete
// implementation lives in agent/knowledge.
type KnowledgeDomains interface {
	Propose(ctx context.Context, p KnowledgeProposal) (KnowledgeProposal, error)
	PendingProposals(ctx context.Context) ([]KnowledgeProposal, error)
	DecideProposal(ctx context.Context, id string, approve bool) (KnowledgeProposal, error)
	// PromoteSoon requests a domain promotion of agent in the background. A
	// pass for the same agent already in flight is dropped.
	PromoteSoon(agent string)
	HasDomain(agent string) bool
	Pages(ctx context.Context, scope, q string, limit int) ([]KnowledgePageInfo, int, error)
	// Page returns the document and its raw markdown for a vault path
	// (API-DKN-005). The document also carries the API-KGR-002 detail fields:
	// type, description, updated, sources, and the resolved links.
	Page(ctx context.Context, path string) (KnowledgeDoc, string, error)
	// Graph returns the knowledge graph of the vault (API-KGR-001, REQ-KGR-010).
	Graph(ctx context.Context, scope string, journals, sources bool) (KnowledgeGraph, error)
	// Activity returns the newest log entries, newest first, strictly older than
	// before when it is non-zero (API-KGR-004, REQ-KGR-040).
	Activity(ctx context.Context, limit int, before time.Time, kind string) (KnowledgeActivityPage, error)
	// Health returns the upkeep health of the vault without memory; the runtime
	// adds the memory blocks (API-KGR-005, REQ-KGR-041).
	Health(ctx context.Context) (KnowledgeHealth, error)
	// DecidedProposals returns the latest decided proposals, newest decided
	// first (API-KGR-003, REQ-KGR-030).
	DecidedProposals(ctx context.Context, limit int) ([]KnowledgeProposal, error)
	DomainStats(ctx context.Context, agent string, now time.Time) (KnowledgeDomainStats, error)
	ScopeStats(ctx context.Context) ([]KnowledgeScopeStat, error)
	// ReadIn resolves a page title in caller's domain first, then shared, then
	// the other domains in name order; an explicit scope restricts it to that
	// scope (REQ-DKN-011).
	ReadIn(ctx context.Context, ref, caller, scope string, from, to int) (KnowledgeDoc, error)
}

// KnowledgeProposal is one requested change to another agent's domain, waiting
// for that agent's promotion or the owner (REQ-DKN-021, REQ-DKN-060). Write
// holds the requested change.
type KnowledgeProposal struct {
	ID        string
	Agent     string
	FromAgent string
	SessionID string
	TurnID    string
	Title     string
	Status    string
	Result    string
	OwnerTurn bool
	Write     KnowledgeWrite
	CreatedAt int64
	DecidedAt int64
}

// ReviewItem is one entry of the owner review queue (API-DKN-006): a knowledge
// proposal (Kind "knowledge") or a skill proposal (Kind "skill"). Action is the
// requested write action, or "create"/"update" for a skill. Body is a readable
// rendering of the change; Description is set for skill items only. Status,
// Result, DecidedAt, and OldText are set for decided items (API-KGR-003).
type ReviewItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Agent       string `json:"agent"`
	From        string `json:"from"`
	Title       string `json:"title"`
	Action      string `json:"action"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body"`
	OwnerTurn   bool   `json:"owner_turn"`
	SessionID   string `json:"session_id"`
	CreatedAt   int64  `json:"created_at"`
	Status      string `json:"status,omitempty"`
	Result      string `json:"result,omitempty"`
	DecidedAt   int64  `json:"decided_at,omitempty"`
	OldText     string `json:"old_text,omitempty"`
}

// KnowledgeQuery is one search request. Kind and Type optionally narrow the
// results; "" means any (REQ-KNW-040). Scopes narrows by file scope; nil or
// empty means every scope (REQ-DKN-010).
type KnowledgeQuery struct {
	Text   string
	Limit  int
	Kind   string
	Type   string
	Scopes []string
}

// KnowledgeHit is one search result: a file with its best matching chunk
// (REQ-KNW-032). Scope names where the file belongs: "shared" or an agent name
// (REQ-DKN-010).
type KnowledgeHit struct {
	Path        string
	Title       string
	Kind        string
	Type        string
	Scope       string
	Description string
	Heading     string
	Snippet     string
	Updated     string
	StartLine   int
}

// KnowledgeDoc is the text of one vault file, with the returned line range and
// the file's total line count (REQ-KNW-041). Lines are 1-based. Scope names
// where the file belongs: "shared" or an agent name. The detail fields below
// are filled by Page only (API-KGR-002, REQ-KGR-020).
type KnowledgeDoc struct {
	Path  string
	Title string
	From  int
	To    int
	Total int
	Lines []string
	Scope string

	Type        string
	Description string
	Updated     string
	Sources     []string
	LinksOut    []KnowledgeLinkRef
	Unresolved  []string
	LinksIn     []KnowledgeBacklink
}

// KnowledgeLinkRef is one resolved link target of a page (API-KGR-002).
type KnowledgeLinkRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Scope string `json:"scope"`
}

// KnowledgeBacklink is one resolved inbound link with the context of the
// linking line (API-KGR-002).
type KnowledgeBacklink struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Scope   string `json:"scope"`
	Context string `json:"context"`
}

// KnowledgeWrite is one page change, as the knowledge_write tool and the
// compile pass express it (REQ-KNW-010). Scope selects the target page folder:
// "" or "shared" is the shared wiki, an agent name is that agent's domain
// (REQ-DKN-020). Source is the wikilink title a supersede change cites
// (REQ-DKN-025).
type KnowledgeWrite struct {
	Action      string
	Title       string
	Type        string
	Description string
	Body        string
	Text        string
	Heading     string
	OldText     string
	NewText     string
	Actor       string
	Scope       string
	Source      string
	Aliases     []string
	Tags        []string
}

// KnowledgeWriteResult describes an applied change. Op is the effective action
// (create or append when a create hit an existing page).
type KnowledgeWriteResult struct {
	Path string
	Op   string
	Size int
}

// KnowledgeIngest is one ingest request (REQ-KNW-020). Exactly one of the data
// sources is set by the caller; the service stores it under sources/.
type KnowledgeIngest struct {
	Title     string
	Ext       string
	SourceURL string
	SessionID string
	TurnID    string
	AgentName string
	Data      []byte
}

// KnowledgeIngestResult reports where a source was stored. Duplicate is true
// when the same bytes were already stored; Queued is true when a compile pass
// was scheduled (REQ-KNW-022, -042).
type KnowledgeIngestResult struct {
	Path      string
	Duplicate bool
	Queued    bool
}

// KnowledgeGraph is the knowledge graph of a vault (API-KGR-001, REQ-KGR-010).
// Edges join nodes in Nodes only; hub edges are not listed.
type KnowledgeGraph struct {
	Nodes     []KnowledgeGraphNode `json:"nodes"`
	Hubs      []KnowledgeGraphHub  `json:"hubs"`
	Edges     []KnowledgeGraphEdge `json:"edges"`
	Counts    KnowledgeGraphCounts `json:"counts"`
	Truncated bool                 `json:"truncated"`
}

// KnowledgeGraphNode is one node of the knowledge graph. Kind is "page",
// "journal", or "source"; Degree is edges in plus out (API-KGR-001).
type KnowledgeGraphNode struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Scope   string `json:"scope"`
	Kind    string `json:"kind"`
	Type    string `json:"type"`
	Updated string `json:"updated"`
	Degree  int    `json:"degree"`
}

// KnowledgeGraphEdge is one resolved page-to-page link (API-KGR-001).
type KnowledgeGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// KnowledgeGraphHub is the page count of one scope (API-KGR-001).
type KnowledgeGraphHub struct {
	Scope string `json:"scope"`
	Pages int    `json:"pages"`
}

// KnowledgeGraphCounts are the vault totals of a graph (API-KGR-001).
type KnowledgeGraphCounts struct {
	Pages  int `json:"pages"`
	Links  int `json:"links"`
	Broken int `json:"broken"`
}

// KnowledgeActivityEntry is one parsed log.md entry (API-KGR-004). At is an
// RFC 3339 local time; Kind is one of run, promotion, write, review, lint,
// error, other; Lines drop the leading "- ".
type KnowledgeActivityEntry struct {
	At      string   `json:"at"`
	Heading string   `json:"heading"`
	Kind    string   `json:"kind"`
	Actor   string   `json:"actor"`
	Lines   []string `json:"lines"`
}

// KnowledgeActivityPage is one page of log entries, newest first. NextBefore is
// the time to pass as before for the next page, empty when nothing is older
// (API-KGR-004).
type KnowledgeActivityPage struct {
	Entries    []KnowledgeActivityEntry `json:"entries"`
	NextBefore string                   `json:"next_before"`
}

// KnowledgeHealth is the upkeep health of the vault (API-KGR-005). LastRun and
// Lint are null when there is none; Memory is filled by the runtime.
type KnowledgeHealth struct {
	LastRun       *KnowledgeHealthRun     `json:"last_run"`
	NextAt        string                  `json:"next_at"`
	Lint          *KnowledgeHealthLint    `json:"lint"`
	ReviewPending int                     `json:"review_pending"`
	Memory        []KnowledgeHealthMemory `json:"memory"`
}

// KnowledgeHealthRun is the last nightly run: its time, the sessions journaled,
// the ops applied and skipped across the run's promotions, and its error
// (API-KGR-005).
type KnowledgeHealthRun struct {
	At        string `json:"at"`
	Journaled int    `json:"journaled"`
	Applied   int    `json:"applied"`
	Skipped   int    `json:"skipped"`
	Error     string `json:"error"`
}

// KnowledgeHealthLint is the latest lint report with its per-section counts
// (API-KGR-005).
type KnowledgeHealthLint struct {
	Date           string `json:"date"`
	Path           string `json:"path"`
	Pages          int    `json:"pages"`
	Contradictions int    `json:"contradictions"`
	Conflicts      int    `json:"conflicts"`
	Duplicates     int    `json:"duplicates"`
	Broken         int    `json:"broken"`
	Orphans        int    `json:"orphans"`
	Missing        int    `json:"missing"`
}

// KnowledgeHealthMemory is the MEMORY.md size and limit of one agent
// (API-KGR-005, REQ-KGR-041).
type KnowledgeHealthMemory struct {
	Agent      string `json:"agent"`
	Characters int    `json:"characters"`
	Limit      int    `json:"limit"`
}

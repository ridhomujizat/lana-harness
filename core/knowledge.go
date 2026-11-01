package core

import (
	"context"
	"errors"
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
	Available        bool                     `json:"available"`
	Path             string                   `json:"path"`
	Exists           bool                     `json:"exists"`
	Counts           KnowledgeCounts          `json:"counts"`
	SourcesPending   []string                 `json:"sources_pending"`
	SourcesFailed    []KnowledgeSourceError   `json:"sources_failed"`
	InboxUnsupported []string                 `json:"inbox_unsupported"`
	Embedding        KnowledgeEmbeddingStatus `json:"embedding"`
	Agent            string                   `json:"agent"`
	AgentError       string                   `json:"agent_error"`
	Consolidation    KnowledgeConsolidation   `json:"consolidation"`
	Log              []KnowledgeLogEntry      `json:"log"`
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
	Recall(ctx context.Context, text string, limit int, skip func(path string) bool) ([]KnowledgeHit, error)
}

// KnowledgeQuery is one search request. Kind and Type optionally narrow the
// results; "" means any (REQ-KNW-040).
type KnowledgeQuery struct {
	Text  string
	Limit int
	Kind  string
	Type  string
}

// KnowledgeHit is one search result: a file with its best matching chunk
// (REQ-KNW-032).
type KnowledgeHit struct {
	Path        string
	Title       string
	Kind        string
	Type        string
	Description string
	Heading     string
	Snippet     string
	Updated     string
	StartLine   int
}

// KnowledgeDoc is the text of one vault file, with the returned line range and
// the file's total line count (REQ-KNW-041). Lines are 1-based.
type KnowledgeDoc struct {
	Path  string
	Title string
	From  int
	To    int
	Total int
	Lines []string
}

// KnowledgeWrite is one page change, as the knowledge_write tool and the
// compile pass express it (REQ-KNW-010).
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

package workflow

import (
	"context"
	"encoding/json"
)

// Store persists workflows, their versions, runs, node runs, decisions, state
// and chat. All methods are safe for concurrent use.
type Store interface {
	// CreateWorkflow stores a new workflow with its first version. It returns
	// ErrExists when the id is taken and sets Version to 1.
	CreateWorkflow(ctx context.Context, w Workflow, g Graph, author, summary string) (Workflow, error)
	// GetWorkflow returns one workflow by id, or ErrNotFound.
	GetWorkflow(ctx context.Context, id string) (Workflow, error)
	// ListWorkflows returns all workflows ordered by name.
	ListWorkflows(ctx context.Context) ([]Workflow, error)
	// UpdateWorkflow writes workflow metadata only; it never changes Version.
	UpdateWorkflow(ctx context.Context, w Workflow) error
	// SaveVersion appends a new graph version when base is the current version,
	// otherwise it returns ErrVersionConflict. It updates workflow.Version.
	SaveVersion(ctx context.Context, id string, base int, g Graph, author, summary string) (Version, error)
	// GetVersion returns one stored version.
	GetVersion(ctx context.Context, id string, version int) (Version, error)
	// ListVersions returns the versions of a workflow, newest first.
	ListVersions(ctx context.Context, id string) ([]Version, error)
	// DeleteWorkflow removes a workflow and cascades to all of its rows.
	DeleteWorkflow(ctx context.Context, id string) error

	// CreateRun stores a run and sets its Number to max(number)+1 for the workflow.
	CreateRun(ctx context.Context, r Run) (Run, error)
	// UpdateRun writes an existing run.
	UpdateRun(ctx context.Context, r Run) error
	// GetRun returns one run by id.
	GetRun(ctx context.Context, id string) (Run, error)
	// GetRunByNumber returns one run by its workflow and run number.
	GetRunByNumber(ctx context.Context, workflowID string, number int) (Run, error)
	// ListRuns returns the runs matching the filter, newest first.
	ListRuns(ctx context.Context, f RunFilter) ([]Run, error)

	// PutNodeRun upserts a node run by (run, node, item).
	PutNodeRun(ctx context.Context, nr NodeRun) error
	// ListNodeRuns returns every node run of one run.
	ListNodeRuns(ctx context.Context, runID string) ([]NodeRun, error)

	// PutDecision upserts a decision by id.
	PutDecision(ctx context.Context, d Decision) error
	// GetDecision returns one decision by id.
	GetDecision(ctx context.Context, id string) (Decision, error)
	// ListDecisions returns the decisions matching the filter, newest first.
	ListDecisions(ctx context.Context, f DecisionFilter) ([]Decision, error)

	// ListState returns the workflow state entries ordered by key.
	ListState(ctx context.Context, workflowID string) ([]StateEntry, error)
	// SetState writes one state entry.
	SetState(ctx context.Context, workflowID, key string, value json.RawMessage) error
	// DeleteState removes one state entry; an empty key clears all of them.
	DeleteState(ctx context.Context, workflowID, key string) error

	// AppendChat stores one chat message and sets its ID.
	AppendChat(ctx context.Context, m ChatMessage) (ChatMessage, error)
	// ListChat returns the last limit messages of a workflow, oldest first.
	ListChat(ctx context.Context, workflowID string, limit int) ([]ChatMessage, error)

	// CreateCandidate marks any open candidate of the workflow superseded and
	// stores c as the new open candidate; it sets ID, Status "open" and times.
	CreateCandidate(ctx context.Context, c Candidate) (Candidate, error)
	// GetCandidate returns one candidate by id, or ErrNotFound.
	GetCandidate(ctx context.Context, id int64) (Candidate, error)
	// OpenCandidate returns the workflow's open candidate, or ErrNotFound.
	OpenCandidate(ctx context.Context, workflowID string) (Candidate, error)
	// UpdateCandidate writes graph, summary, status, published_version, updated_at.
	UpdateCandidate(ctx context.Context, c Candidate) error
}

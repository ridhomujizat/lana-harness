// Package workflow implements lana workflows (spec #33): graph definitions,
// durable runs, the error ladder and the guardian assistant.
package workflow

import (
	"encoding/json"
	"errors"
	"strings"
)

// Status is the lifecycle status of a workflow.
type Status string

// Workflow statuses.
const (
	StatusDraft  Status = "draft"  // saved but not yet activated
	StatusActive Status = "active" // triggers may start runs
	StatusPaused Status = "paused" // starts blocked by the owner
	StatusError  Status = "error"  // too many consecutive failures
)

// RunStatus is the status of one run.
type RunStatus string

// Run statuses.
const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunWaiting   RunStatus = "waiting"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

// NodeStatus is the status of one node run.
type NodeStatus string

// Node run statuses.
const (
	NodePending   NodeStatus = "pending"
	NodeRunning   NodeStatus = "running"
	NodeSucceeded NodeStatus = "succeeded"
	NodeFailed    NodeStatus = "failed"
	NodeSkipped   NodeStatus = "skipped"
	NodeWaiting   NodeStatus = "waiting"
	NodeFallback  NodeStatus = "fallback"
)

// Node run via values (spec #44, REQ-SP-019; spec #45, REQ-DG-007).
const (
	ViaScript   = "script"
	ViaFallback = "fallback"
	ViaAudit    = "audit"
)

// Sentinel errors returned by the store and the service.
var (
	ErrNotFound        = errors.New("workflow: not found")
	ErrExists          = errors.New("workflow: already exists")
	ErrVersionConflict = errors.New("workflow: version conflict")
	ErrRunActive       = errors.New("workflow: a run is already running")
	ErrRunFinished     = errors.New("workflow: run already finished")
	ErrDecisionClosed  = errors.New("workflow: decision already resolved")
	ErrInvalidChoice   = errors.New("workflow: invalid choice")
	ErrLockRequired    = errors.New("workflow: permissions must be locked again")
	ErrBadToken        = errors.New("workflow: bad webhook token")
)

// InvalidGraphError carries every validation problem.
type InvalidGraphError struct{ Issues []Issue }

// Error renders the issues as "<node_id>: <message>" joined by "; ".
func (e *InvalidGraphError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.NodeID+": "+issue.Message)
	}
	return "invalid graph: " + strings.Join(parts, "; ")
}

// Issue is one graph validation problem.
type Issue struct {
	NodeID  string `json:"node_id"`
	Code    string `json:"code"` // unknown_type, duplicate_id, missing_node, cycle, unreachable, missing_param, bad_template, bad_cron, bad_parent, no_trigger, unknown_agent, bad_schema, bad_script
	Message string `json:"message"`
}

// Position is a node's place on the canvas.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// FailurePolicy is how a node reacts to a failure.
type FailurePolicy struct {
	Mode        string `json:"mode,omitempty"` // default|guardian|stop|continue
	Retries     *int   `json:"retries,omitempty"`
	BackoffSecs []int  `json:"backoff_secs,omitempty"`
	Fallback    any    `json:"fallback,omitempty"`
}

// Node is one step of a workflow graph.
type Node struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Parent    string         `json:"parent,omitempty"`
	Disabled  bool           `json:"disabled,omitempty"`
	Position  Position       `json:"position"`
	Params    map[string]any `json:"params"`
	OnFailure *FailurePolicy `json:"on_failure,omitempty"`
}

// Edge connects one node port to another node.
type Edge struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	FromPort string `json:"from_port,omitempty"` // "" means "out"
	To       string `json:"to"`
}

// Graph is the node and edge set of one workflow version.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// LockTool is one tool a locked workflow may run without approval.
type LockTool struct {
	Tool   string   `json:"tool"`
	UsedBy []string `json:"used_by"`
	Risk   string   `json:"risk"` // read|write|admin
}

// Lock is the owner-approved tool set of one workflow version.
type Lock struct {
	Version  int        `json:"version"`
	LockedAt int64      `json:"locked_at"`
	Tools    []LockTool `json:"tools"`
}

// Webhook is the inbound hook path and token of a workflow.
type Webhook struct {
	Path  string `json:"path"`
	Token string `json:"token"`
}

// Workflow is the metadata record of a workflow.
type Workflow struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	Description         string        `json:"description"`
	Guardian            string        `json:"guardian"`
	Status              Status        `json:"status"`
	Version             int           `json:"version"`
	Lock                *Lock         `json:"lock"`
	FailureDefault      FailurePolicy `json:"failure_default"`
	WebhookToken        string        `json:"-"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
	AssistantSession    string        `json:"assistant_session"` // guardian chat session (spec 51)
	CreatedAt           int64         `json:"created_at"`
	UpdatedAt           int64         `json:"updated_at"`
}

// Version is one saved graph of a workflow.
type Version struct {
	WorkflowID string `json:"-"`
	Version    int    `json:"version"`
	Graph      Graph  `json:"graph"`
	Author     string `json:"author"` // owner|guardian
	Summary    string `json:"summary"`
	CreatedAt  int64  `json:"created_at"`
}

// Run is one execution of a workflow version.
type Run struct {
	ID            string          `json:"id"`
	WorkflowID    string          `json:"workflow_id"`
	Number        int             `json:"number"`
	Version       int             `json:"version"`
	Trigger       string          `json:"trigger"`
	Input         json.RawMessage `json:"input"`
	Status        RunStatus       `json:"status"`
	WaitingReason string          `json:"waiting_reason"`
	WakeAt        int64           `json:"wake_at"`
	Error         string          `json:"error"`
	Tokens        int64           `json:"tokens"`
	NodesDone     int             `json:"nodes_done"`
	NodesTotal    int             `json:"nodes_total"`
	Summary       string          `json:"summary"`
	// Overrides holds "this run only" parameter patches by node id.
	Overrides map[string]map[string]any `json:"overrides,omitempty"`
	// CandidateID >0 marks a test run of that candidate (spec 51 REQ-WFC-005);
	// Version then holds the candidate's base version.
	CandidateID int64 `json:"candidate_id,omitempty"`
	StartedAt   int64 `json:"started_at"`
	EndedAt     int64 `json:"ended_at"`
}

// Attempt is one try of a node run.
type Attempt struct {
	N         int    `json:"n"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	Error     string `json:"error,omitempty"`
}

// NodeRun is the durable state of one node in one run.
type NodeRun struct {
	RunID     string          `json:"-"`
	NodeID    string          `json:"node_id"`
	ItemIndex int             `json:"item_index"`
	Status    NodeStatus      `json:"status"`
	Attempts  []Attempt       `json:"attempts"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	Port      string          `json:"port,omitempty"` // branch taken (if/switch/approval/loop)
	Error     string          `json:"error"`
	Via       string          `json:"via"` // "" | script | fallback (spec #44)
	Logs      []string        `json:"logs"`
	Tokens    int64           `json:"tokens"`
	SessionID string          `json:"session_id"`
	StartedAt int64           `json:"started_at"`
	EndedAt   int64           `json:"ended_at"`
}

// Decision is one guardian or approval decision.
type Decision struct {
	ID         string         `json:"id"`
	WorkflowID string         `json:"workflow_id"`
	RunID      string         `json:"run_id"`
	RunNumber  int            `json:"run_number"`
	NodeID     string         `json:"node_id"`
	ItemIndex  int            `json:"item_index"`
	Kind       string         `json:"kind"`   // guardian|approval
	Action     string         `json:"action"` // retry_fix|skip|fallback|ask|stop
	Reason     string         `json:"reason"`
	Fix        map[string]any `json:"fix,omitempty"`
	Persistent bool           `json:"persistent"`
	Confidence float64        `json:"confidence"`
	Tokens     int64          `json:"tokens"`
	Status     string         `json:"status"` // applied|pending|resolved|expired
	Choice     string         `json:"choice"`
	CreatedAt  int64          `json:"created_at"`
	ExpiresAt  int64          `json:"expires_at"`
	ResolvedAt int64          `json:"resolved_at"`
}

// StateEntry is one workflow key/value entry.
type StateEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt int64           `json:"updated_at"`
}

// ChatMessage is one message of the workflow assistant chat.
type ChatMessage struct {
	ID         int64        `json:"id"`
	WorkflowID string       `json:"-"`
	Role       string       `json:"role"` // owner|guardian
	Text       string       `json:"text"`
	Change     *GraphChange `json:"change,omitempty"`
	Issues     []Issue      `json:"issues,omitempty"`
	// SessionID and TurnID point at the guardian session turn that produced
	// this message (spec 51); both are empty on messages from before it.
	SessionID string `json:"session_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// Candidate statuses (spec 51 REQ-WFC-003).
const (
	CandidateOpen       = "open"
	CandidateSuperseded = "superseded"
	CandidatePublished  = "published"
	CandidateDiscarded  = "discarded"
)

// Candidate is a saved but unpublished graph (spec 51 REQ-WFC-003).
type Candidate struct {
	ID               int64  `json:"id"`
	WorkflowID       string `json:"-"`
	BaseVersion      int    `json:"base_version"`
	Graph            Graph  `json:"graph"`
	Summary          string `json:"summary"`
	Author           string `json:"author"`
	Status           string `json:"status"` // open|superseded|published|discarded
	PublishedVersion int    `json:"published_version,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// GraphChange summarizes what an assistant edit changed between two versions.
type GraphChange struct {
	FromVersion int      `json:"from_version"`
	ToVersion   int      `json:"to_version"`
	Summary     string   `json:"summary"`
	Added       []string `json:"added"`
	Removed     []string `json:"removed"`
	Changed     []string `json:"changed"`
}

// RunFilter selects runs in Store.ListRuns.
type RunFilter struct {
	WorkflowID string
	Statuses   []RunStatus
	Since      int64 // started_at >= Since (0 = any)
	Before     int   // number < Before (0 = any)
	Limit      int   // 0 = no limit
}

// DecisionFilter selects decisions in Store.ListDecisions.
type DecisionFilter struct {
	WorkflowID string
	RunID      string
	Status     string
}

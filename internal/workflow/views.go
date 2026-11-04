package workflow

// This file holds the view types of the Service API (spec #33, design
// §"Service and scheduler"). They are the JSON objects of api-contract.md:
// workflow_summary, stats, attention, workflow (detail), run detail, the
// assistant reply, the chat command reply and the stream change. Only types
// live here; the Service that produces them is in service.go.

// Summary is the workflow_summary object of API-WF-001 (REQ-WF-001,
// REQ-WF-011).
type Summary struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Guardian    string        `json:"guardian"`
	Status      Status        `json:"status"`
	Version     int           `json:"version"`
	NodeCount   int           `json:"node_count"`
	Triggers    []TriggerInfo `json:"triggers"`
	Runs7d      int           `json:"runs_7d"`
	// Success7d is null with no finished run in the last seven days.
	Success7d *float64 `json:"success_7d"`
	Running   bool     `json:"running"`
	Waiting   bool     `json:"waiting"`
	LastRun   *Run     `json:"last_run"`
	// Bots lists the guardian then the other agents used by bot/worker nodes.
	Bots      []string `json:"bots"`
	UpdatedAt int64    `json:"updated_at"`
}

// TriggerInfo is one trigger row of a workflow summary.
type TriggerInfo struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Detail  string `json:"detail"`
	Enabled bool   `json:"enabled"`
}

// Stats is the stats object of API-WF-001 (REQ-WF-011).
type Stats struct {
	Total            int      `json:"total"`
	Active           int      `json:"active"`
	Paused           int      `json:"paused"`
	Error            int      `json:"error"`
	WaitingOnYou     int      `json:"waiting_on_you"`
	Approvals        int      `json:"approvals"`
	Decisions        int      `json:"decisions"`
	Success7d        *float64 `json:"success_7d"`
	SuccessPrev7d    *float64 `json:"success_prev_7d"`
	RunsToday        int      `json:"runs_today"`
	RunsTodayOK      int      `json:"runs_today_ok"`
	RunsTodayFailed  int      `json:"runs_today_failed"`
	RunsTodayRunning int      `json:"runs_today_running"`
}

// Attention is one Needs attention row of API-WF-001 (REQ-WF-008,
// REQ-WF-011). It embeds the pending decision and adds the workflow and node
// names. A tool approval of a workflow session is a row with Kind
// "tool_approval"; Tool and Argument carry the pending tool call.
type Attention struct {
	Decision
	WorkflowName string `json:"workflow_name"`
	NodeName     string `json:"node_name"`
	Tool         string `json:"tool,omitempty"`
	Argument     string `json:"argument,omitempty"`
}

// Overview is the response of API-WF-001 (REQ-WF-011).
type Overview struct {
	Workflows []Summary   `json:"workflows"`
	Stats     Stats       `json:"stats"`
	Attention []Attention `json:"attention"`
}

// Detail is the workflow (detail) object of API-WF-003 (REQ-WF-001). It embeds
// the Workflow metadata and adds the current version and graph.
type Detail struct {
	Workflow
	VersionAuthor  string  `json:"version_author"`
	VersionSummary string  `json:"version_summary"`
	VersionAt      int64   `json:"version_at"`
	Graph          Graph   `json:"graph"`
	LockCurrent    bool    `json:"lock_current"`
	Webhook        Webhook `json:"webhook"`
}

// CreateInput is the body of API-WF-002 (REQ-WF-001, REQ-WF-002). A nil Graph
// makes the Service use a single manual trigger.
type CreateInput struct {
	Name        string
	Description string
	Guardian    string
	Graph       *Graph
}

// UpdateInput is the body of API-WF-004 (REQ-WF-001, REQ-WF-002, REQ-WF-009).
// A nil pointer leaves that field unchanged; BaseVersion must be the current
// version.
type UpdateInput struct {
	BaseVersion    int
	Name           *string
	Description    *string
	Guardian       *string
	Graph          *Graph
	FailureDefault *FailurePolicy
	Summary        string
}

// RunDetail is the response of API-WF-013 (REQ-WF-004, REQ-WF-007). Graph is
// the version the run used.
type RunDetail struct {
	Run       Run        `json:"run"`
	Graph     Graph      `json:"graph"`
	Nodes     []NodeRun  `json:"nodes"`
	Decisions []Decision `json:"decisions"`
}

// AssistResult is the response of API-WF-017 (REQ-WF-010).
type AssistResult struct {
	Message  ChatMessage `json:"message"`
	Workflow Detail      `json:"workflow"`
	Tokens   int64       `json:"tokens"`
}

// CommandResult is the command object of API-WF-021 (REQ-WF-003).
type CommandResult struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	Number     int    `json:"number"`
	Reply      string `json:"reply"`
}

// Change is one changed event of API-WF-019 (REQ-WF-011). RunID is empty when
// the change is not tied to one run.
type Change struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id,omitempty"`
}

// Package claudechat turns a Claude Code transcript into the chat items the
// Workbench chat view shows (spec 71). The reader walks the JSONL transcript
// incrementally and maps the lines Claude Code writes onto a small item model;
// lines it does not know are skipped and counted rather than failing the read,
// because the transcript format is not a public API.
package claudechat

import "encoding/json"

// Item is one chat entry. Only ID and Kind are always set; the other fields are
// filled per kind. IDs are stable across reads, so a re-poll never duplicates an
// item (spec 71 REQ-WC-012).
type Item struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"` // user, command, output, assistant, thinking, tool, question, plan, divider, turn_end
	At           string          `json:"at,omitempty"`
	Text         string          `json:"text,omitempty"`
	Images       []ImageRef      `json:"images,omitempty"`
	Name         string          `json:"name,omitempty"` // command name ("/compact") or tool name
	Args         string          `json:"args,omitempty"`
	Model        string          `json:"model,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Agent        *AgentRef       `json:"agent,omitempty"`
	Summary      *ToolSummary    `json:"summary,omitempty"`
	Diff         *Diff           `json:"diff,omitempty"`
	Status       string          `json:"status,omitempty"` // running, done, error (tool)
	Result       *ToolResult     `json:"result,omitempty"`
	Questions    []Question      `json:"questions,omitempty"`
	Answered     bool            `json:"answered,omitempty"`
	Answer       string          `json:"answer,omitempty"`
	Plan         string          `json:"plan,omitempty"`
	State        string          `json:"state,omitempty"` // plan: pending, approved, rejected
	DurationMS   int64           `json:"duration_ms,omitempty"`
	OutputTokens int64           `json:"output_tokens,omitempty"`
}

// ImageRef points at an image block in the transcript. Ref is "<line id>:<block
// index>"; the bytes are fetched on demand through ImageBytes, never inlined.
type ImageRef struct {
	Ref       string `json:"ref"`
	MediaType string `json:"media_type"`
}

// ToolSummary is the one-line description of a tool call.
type ToolSummary struct {
	Verb   string `json:"verb"`
	Target string `json:"target"`
	Meta   string `json:"meta,omitempty"`
}

// Diff carries the old and new text of an edit.
type Diff struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ToolResult is a tool call's output.
type ToolResult struct {
	Text      string `json:"text"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated"`
	IsError   bool   `json:"is_error"`
}

// AgentRef points at the subagent an Agent / Task tool call launched
// (REQ-WC-017). AgentID is empty until the tool result reveals it.
type AgentRef struct {
	AgentID    string `json:"agent_id"`
	AgentType  string `json:"agent_type,omitempty"`
	Background bool   `json:"background,omitempty"`
}

// Task is one background task of a conversation: a background shell or a
// subagent (REQ-WC-016). Status is running, completed, failed or killed; Title
// is the command's description or the agent's description.
type Task struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // shell, agent, other
	ToolUseID string `json:"tool_use_id"`
	Title     string `json:"title"`
	AgentType string `json:"agent_type"`
	Status    string `json:"status"`
	Summary   string `json:"summary,omitempty"`
	Started   string `json:"started"`
	Ended     string `json:"ended,omitempty"`
}

// SubagentMeta is the agent-<id>.meta.json file Claude Code writes beside a
// subagent transcript (REQ-WC-017).
type SubagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	Model       string `json:"model"`
	Finished    bool   `json:"finished"`
}

// Question is one question of an AskUserQuestion tool call.
type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
	Options     []QuestionOption `json:"options"`
}

// QuestionOption is one choice of a Question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Context is the model's context use from the last main-chain assistant line.
type Context struct {
	Used      int64 `json:"used"`
	Window    int64 `json:"window"`
	CacheRead int64 `json:"cache_read"`
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
}

// Meta is the transcript's model, effort, mode and context state (REQ-WC-013).
type Meta struct {
	Model        string  `json:"model"`
	Effort       string  `json:"effort"`
	Mode         string  `json:"mode"`
	Context      Context `json:"context"`
	UnknownLines int     `json:"unknown_lines"`
	Version      string  `json:"version"`
	Tasks        []Task  `json:"tasks,omitempty"`
}

package core

import "encoding/json"

// PayloadVersion is the value of the v field in every payload.
const PayloadVersion = 1

// SessionEvent is one session_events row.
type SessionEvent struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	TurnID    string          `json:"turn_id,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

// EventDraft is an event before it is written.
type EventDraft struct {
	Kind    string
	TurnID  string
	Payload any
}

const (
	KindSessionCreated    = "session/created"
	KindTurnStart         = "turn/start"
	KindTurnEnd           = "turn/end"
	KindStepStart         = "step/start"
	KindStepEnd           = "step/end"
	KindSystemMessage     = "system/message"
	KindUserMessage       = "user/message"
	KindAssistantMessage  = "assistant/message"
	KindAssistantAttempt  = "assistant/attempt"
	KindToolCall          = "tool/call"
	KindToolResult        = "tool/result"
	KindApprovalAsked     = "approval/asked"
	KindApprovalDecided   = "approval/decided"
	KindApprovalPolicy    = "approval/policy"
	KindQuestionAsked     = "question/asked"
	KindQuestionAnswered  = "question/answered"
	KindModelSelection    = "model/selection"
	KindCompactionSummary = "compaction/summary"
	KindToolResultPruned  = "tool_result_pruned"
	KindMemoryCaptured    = "memory/captured"
	KindKnowledgeCompiled = "knowledge/compiled"
	KindFileSent          = "file/sent"
)

// PayloadError is the error in turn/end and assistant/attempt payloads.
type PayloadError struct {
	Class   string `json:"class"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message"`
}

type SessionCreated struct {
	V               int    `json:"v"`
	Agent           string `json:"agent"`
	Channel         string `json:"channel"`
	Address         string `json:"address"`
	Workspace       string `json:"workspace"`
	ParentSessionID string `json:"parent_session_id"`
	ParentCallID    string `json:"parent_call_id"`
	InheritedSeq    int64  `json:"inherited_seq"`
}

type TurnStart struct {
	V      int    `json:"v"`
	TurnID string `json:"turn_id"`
}

type TurnEnd struct {
	V      int           `json:"v"`
	TurnID string        `json:"turn_id"`
	Reason string        `json:"reason"`
	Error  *PayloadError `json:"error"`
}

type StepStart struct {
	V        int    `json:"v"`
	Step     int    `json:"step"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type StepEnd struct {
	V        int    `json:"v"`
	Step     int    `json:"step"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Usage    Usage  `json:"usage"`
}

// PromptSection is one named part of a system prompt, kept so a later turn can
// diff without re-parsing the head (REQ-LTC-002).
type PromptSection struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type SystemMessage struct {
	V        int             `json:"v"`
	Text     string          `json:"text"`
	Sections []PromptSection `json:"sections,omitempty"`
	// Subtype is "" for the prompt head. SystemSubtypeRuntime marks the
	// session meta of an external runtime process (API-CCR-002); such an event
	// is not a prompt head and carries the fields below instead of Text.
	Subtype string `json:"subtype,omitempty"`
	// Runtime is the runtime name, for example "claude-code".
	Runtime string `json:"runtime,omitempty"`
	// ClaudeSessionID is the runtime's own session ID, resumed on later turns.
	ClaudeSessionID string   `json:"claude_session_id,omitempty"`
	Model           string   `json:"model,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	Version         string   `json:"version,omitempty"`
	// Account is the Claude Code account the runtime session belongs to
	// (REQ-CCT-010). Empty means the built-in account "claude-code"; a
	// session of one account is never resumed on another.
	Account string `json:"account,omitempty"`
}

// SystemSubtypeRuntime is the system/message subtype of an external runtime's
// session meta (API-CCR-002).
const SystemSubtypeRuntime = "runtime"

// SystemSubtypeNote is the system/message subtype of a note lana writes for
// the owner, for example that lana's tools were not available to a Claude
// Code turn (spec 39 failure cases). It is not a prompt head.
const SystemSubtypeNote = "note"

type UserMessage struct {
	V       int            `json:"v"`
	Content []ContentBlock `json:"content"`
	// Continue marks a message that resumes an abnormally ended turn
	// (API-TFB-004, REQ-TFB-005). ContinueReason is the previous turn/end
	// reason.
	Continue       bool   `json:"continue,omitempty"`
	ContinueReason string `json:"continue_reason,omitempty"`
	// PromptSections is the full section set in effect when the turn's first
	// user message carried a <system-update>. It is omitted when nothing
	// changed (REQ-LTC-002).
	PromptSections []PromptSection `json:"prompt_sections,omitempty"`
	// ContextBlocks is the number of leading content blocks lana injected
	// (<context> and, when it changed, <system-update>). They are hidden from
	// display surfaces but still sent to the model (REQ-LTC-001).
	ContextBlocks int `json:"context_blocks,omitempty"`
	// ReplyTo is set when the owner's message replies to an automation message
	// (an assistant/message with a Source). The quoted text rides the leading
	// injected block; the reference is kept for display surfaces.
	ReplyTo *ReplyRef `json:"reply_to,omitempty"`
	// Runtime marks a note lana wrote itself (the empty-answer, step-limit, or
	// tool-call-text note), not a message from the owner. Display surfaces hide
	// it and it is not an owner message (REQ-MEM-021).
	Runtime bool `json:"runtime,omitempty"`
}

type AssistantMessage struct {
	V          int            `json:"v"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	// Source marks a message posted out-of-band by a lana automation, such as a
	// workflow's Message node (e.g. "workflow"); empty means a normal model
	// reply. SourceName names the poster (e.g. the workflow name). Old payloads
	// decode unchanged.
	Source     string `json:"source,omitempty"`
	SourceName string `json:"source_name,omitempty"`
	// Group rooms: Passed marks an agent that had nothing to add (no
	// content), Failed one that could not answer; Tools, Notes and Files are
	// what the agent used and changed for the reply.
	Passed bool           `json:"passed,omitempty"`
	Failed bool           `json:"failed,omitempty"`
	Tools  map[string]int `json:"tools,omitempty"`
	Notes  []string       `json:"notes,omitempty"`
	Files  []string       `json:"files,omitempty"`
}

// ReplyRef is the automation message an owner reply points back to: the seq of
// the assistant/message event, its source and source name, and its quoted text.
type ReplyRef struct {
	Seq        int64  `json:"seq"`
	Source     string `json:"source,omitempty"`
	SourceName string `json:"source_name,omitempty"`
	Text       string `json:"text"`
}

// ReplyQuoteMax caps the quoted text of a reply, in runes.
const ReplyQuoteMax = 8000

// ReplyQuoteText returns text capped at ReplyQuoteMax runes with an ellipsis.
func ReplyQuoteText(text string) string {
	runes := []rune(text)
	if len(runes) <= ReplyQuoteMax {
		return text
	}
	return string(runes[:ReplyQuoteMax]) + "…"
}

type AssistantAttempt struct {
	V        int          `json:"v"`
	Step     int          `json:"step"`
	Attempt  int          `json:"attempt"`
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	Error    PayloadError `json:"error"`
}

type ToolCallPayload struct {
	V      int             `json:"v"`
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
	// External marks a call an external runtime ran itself, such as a Claude
	// Code tool (API-CCR-002). lana did not run it.
	External bool `json:"external,omitempty"`
}

type ToolResultPayload struct {
	V      int            `json:"v"`
	CallID string         `json:"call_id"`
	Status ToolStatus     `json:"status"`
	Output string         `json:"output"`
	Images []ContentBlock `json:"images,omitempty"`
}

type ApprovalAsked struct {
	V         int    `json:"v"`
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
	Tool      string `json:"tool"`
	Risk      Risk   `json:"risk"`
	Label     string `json:"label,omitempty"`
}

type ApprovalDecided struct {
	V         int             `json:"v"`
	RequestID string          `json:"request_id"`
	CallID    string          `json:"call_id"`
	Outcome   ApprovalOutcome `json:"outcome"`
	Reason    string          `json:"reason,omitempty"`
}

type ApprovalPolicy struct {
	V      int    `json:"v"`
	Policy Policy `json:"policy"`
}

// QuestionAsked is the payload of a question/asked event.
type QuestionAsked struct {
	V         int    `json:"v"`
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
	ExpiresAt int64  `json:"expires_at"`
}

// QuestionAnswered is the payload of a question/answered event.
type QuestionAnswered struct {
	V         int              `json:"v"`
	RequestID string           `json:"request_id"`
	CallID    string           `json:"call_id"`
	Outcome   string           `json:"outcome"`
	Responder string           `json:"responder"`
	Reason    string           `json:"reason,omitempty"`
	Answers   []QuestionAnswer `json:"answers,omitempty"`
}

// FileSentPayload is the payload of a file/sent event (API-AGF-001). Path is
// the workspace-relative attachments path API-ATT-003 serves; Size is bytes.
type FileSentPayload struct {
	V         int    `json:"v"`
	CallID    string `json:"call_id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Caption   string `json:"caption,omitempty"`
}

type ModelSelection struct {
	V        int    `json:"v"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Agent    string `json:"agent,omitempty"`
	// ReasoningEffort is the per-session thinking effort (REQ-MDL-001). Empty
	// means the agent or model default applies.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type CompactionSummary struct {
	V       int    `json:"v"`
	Summary string `json:"summary"`
	// FromSeq is the first event seq of the summarized span; UptoSeq is its
	// last. RetainedFromSeq is the first event seq kept verbatim after the
	// span. A summary written before REQ-LTC-010 has RetainedFromSeq 0 and
	// keeps the older "drop everything up to the summary" behavior.
	FromSeq         int64  `json:"from_seq"`
	UptoSeq         int64  `json:"upto_seq"`
	RetainedFromSeq int64  `json:"retained_from_seq"`
	TokensBefore    int64  `json:"tokens_before"`
	TokensAfter     int64  `json:"tokens_after"`
	Model           string `json:"model"`
	Usage           Usage  `json:"usage"`
}

// ToolResultPruned replaces the output of a tool result in the model view. The
// original tool/result event stays in the log and is cited through SourceSeq
// (REQ-LTC-012).
type ToolResultPruned struct {
	V         int    `json:"v"`
	CallID    string `json:"call_id"`
	SourceSeq int64  `json:"source_seq"`
	Output    string `json:"output"`
}

// MemoryOp is one validated operation of a capture pass.
type MemoryOp struct {
	Target  string `json:"target"`
	Action  string `json:"action"`
	Content string `json:"content"`
	OldText string `json:"old_text"`
}

// MemoryCaptured is the payload of a memory/captured event: the operations a
// capture pass applied and the usage of the pass (REQ-MEM-014). It is not part
// of the model history.
type MemoryCaptured struct {
	V      int        `json:"v"`
	TurnID string     `json:"turn_id"`
	Ops    []MemoryOp `json:"ops"`
	Usage  Usage      `json:"usage"`
}

// KnowledgeCompiledOp is one page operation a compile pass applied
// (API-KNW-002). Op is the effective action: create, append, replace, or meta.
type KnowledgeCompiledOp struct {
	Op    string `json:"op"`
	Title string `json:"title"`
}

// KnowledgeCompiled is the payload of a knowledge/compiled event: the source a
// compile pass processed, the page operations it applied, the summed usage, and
// an error when the pass failed (REQ-KNW-024, API-KNW-002). It is not part of
// the model history.
type KnowledgeCompiled struct {
	V      int                   `json:"v"`
	Source string                `json:"source"`
	Ops    []KnowledgeCompiledOp `json:"ops"`
	Usage  Usage                 `json:"usage"`
	Error  string                `json:"error"`
}

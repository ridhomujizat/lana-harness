package core

import "encoding/json"

// PayloadVersion adalah nilai field v di setiap payload.
const PayloadVersion = 1

// SessionEvent adalah satu baris session_events.
type SessionEvent struct {
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	TurnID    string          `json:"turn_id,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

// EventDraft adalah event sebelum ditulis.
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
	KindModelSelection    = "model/selection"
	KindCompactionSummary = "compaction/summary"
)

// PayloadError adalah error di payload turn/end dan assistant/attempt.
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
	V       int           `json:"v"`
	TurnID  string        `json:"turn_id"`
	Reason  string        `json:"reason"`
	Error   *PayloadError `json:"error"`
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

type SystemMessage struct {
	V    int    `json:"v"`
	Text string `json:"text"`
}

type UserMessage struct {
	V       int            `json:"v"`
	Content []ContentBlock `json:"content"`
}

type AssistantMessage struct {
	V          int            `json:"v"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
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
}

type ToolResultPayload struct {
	V      int        `json:"v"`
	CallID string     `json:"call_id"`
	Status ToolStatus `json:"status"`
	Output string     `json:"output"`
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

type ModelSelection struct {
	V        int    `json:"v"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Agent    string `json:"agent,omitempty"`
}

type CompactionSummary struct {
	V            int    `json:"v"`
	Summary      string `json:"summary"`
	UptoSeq      int64  `json:"upto_seq"`
	TokensBefore int64  `json:"tokens_before"`
	TokensAfter  int64  `json:"tokens_after"`
	Model        string `json:"model"`
}

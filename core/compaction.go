package core

import "context"

// CompactionResult is the result of a compaction.
type CompactionResult struct {
	UptoSeq      int64
	TokensBefore int64
	TokensAfter  int64
}

// CompactRequest is one automatic or manual compaction request. It carries the
// conversation prefix (System and Tools) so the summary call reuses the warm
// provider cache (REQ-LTC-014).
type CompactRequest struct {
	SessionID string
	TurnID    string
	AgentName string
	Provider  string
	Model     string
	// Targets is the agent's chain (primary first). When set it wins over
	// Provider and Model, so the summary gets the same fallback as the turn.
	Targets []LLMTarget
	// Requested is the combo or address the agent asked for.
	Requested string
	System    string
	Tools     []ToolSpec
	MaxTokens int
	// ReasoningEffort is the turn's resolved thinking effort; "" means the
	// provider or catalog default (REQ-MDL-001, REQ-MDL-005).
	ReasoningEffort string
	// Trigger is "pressure", "overflow", or "manual". "overflow" and "manual"
	// ignore the pressure threshold.
	Trigger string
}

// Compactor summarizes session history on request.
type Compactor interface {
	// CompactNow condenses the session now, without a pressure check.
	CompactNow(ctx context.Context, sessionID string) (CompactionResult, error)
	// CompactIfNeeded condenses the session when it is over the pressure
	// threshold, or always when the request trigger ignores the threshold.
	CompactIfNeeded(ctx context.Context, req CompactRequest) (CompactionResult, error)
}

// CheckpointPreamble frames a summary in the derived history. It is the
// deepseek-harness "Conversation checkpoint preamble" (REQ-LTC-014).
const CheckpointPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

// ContextWindowInfo is a session's context window and the size at which it
// compacts on its own.
type ContextWindowInfo struct {
	Window    int64
	CompactAt int64
}

// ContextWindower is an optional Compactor extension that reports a
// session's context window, for the chat's context meter.
type ContextWindower interface {
	ContextWindow(ctx context.Context, sessionID string) (ContextWindowInfo, error)
}

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
	System    string
	Tools     []ToolSpec
	MaxTokens int
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

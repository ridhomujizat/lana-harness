package core

// PreStepInput is the input to AgentPreStep.
type PreStepInput struct {
	SessionID string
	TurnID    string
	AgentName string
	Step      int
	Input     Message
}

// PreStepDecision is the result of AgentPreStep.
type PreStepDecision struct {
	Input  Message
	Reject bool
	Reason string
}

// RequestControl is the request control that AgentRequest can modify.
type RequestControl struct {
	MaxTokens   int
	Temperature *float64
}

// LLMTarget is one provider target.
type LLMTarget struct {
	Provider string
	Model    string
}

// LLMStreamInput is the input to LLMStream.
type LLMStreamInput struct {
	SessionID string
	TurnID    string
	Step      int
	AgentName string
	Targets   []LLMTarget
	Request   ChatRequest
	// Requested is the combo or address the agent asked for, copied into
	// the router's call records.
	Requested string
	// OnServed, when set, receives the target that answered, before the
	// stream is returned.
	OnServed func(LLMTarget)
}

// ToolsPreExecuteInput is the input to ToolsPreExecute.
type ToolsPreExecuteInput struct {
	AgentName string
	Call      ToolCall
}

// ToolDecision is the decision of ToolsPreExecute.
type ToolDecision struct {
	Allowed bool
	Outcome ApprovalOutcome
	Status  ToolStatus
	Reason  string
}

// ToolsExecuteInput is the input to ToolsExecute.
type ToolsExecuteInput struct {
	Ctx  ToolCtx
	Call ToolCall
	Tool Tool
}

// ToolsPostExecuteInput is the input to ToolsPostExecute.
type ToolsPostExecuteInput struct {
	Call   ToolCall
	Output ToolOutput
}

// TurnStopInput is the input to AgentTurnStopping.
type TurnStopInput struct {
	SessionID  string
	TurnID     string
	AgentName  string
	Steps      int
	StopReason string
}

// StopVote is the vote of one AgentTurnStopping listener.
type StopVote struct {
	Continue bool
	Reason   string
}

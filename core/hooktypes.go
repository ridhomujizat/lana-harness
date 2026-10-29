package core

// PreStepInput adalah masukan AgentPreStep.
type PreStepInput struct {
	SessionID string
	TurnID    string
	AgentName string
	Step      int
	Input     Message
}

// PreStepDecision adalah hasil AgentPreStep.
type PreStepDecision struct {
	Input  Message
	Reject bool
	Reason string
}

// RequestControl adalah kontrol request yang bisa diubah AgentRequest.
type RequestControl struct {
	MaxTokens   int
	Temperature *float64
}

// LLMTarget adalah satu target provider.
type LLMTarget struct {
	Provider string
	Model    string
}

// LLMStreamInput adalah masukan LLMStream.
type LLMStreamInput struct {
	SessionID string
	TurnID    string
	Step      int
	AgentName string
	Targets   []LLMTarget
	Request   ChatRequest
}

// ToolsPreExecuteInput adalah masukan ToolsPreExecute.
type ToolsPreExecuteInput struct {
	AgentName string
	Call      ToolCall
}

// ToolDecision adalah keputusan ToolsPreExecute.
type ToolDecision struct {
	Allowed bool
	Outcome ApprovalOutcome
	Status  ToolStatus
	Reason  string
}

// ToolsExecuteInput adalah masukan ToolsExecute.
type ToolsExecuteInput struct {
	Ctx  ToolCtx
	Call ToolCall
	Tool Tool
}

// ToolsPostExecuteInput adalah masukan ToolsPostExecute.
type ToolsPostExecuteInput struct {
	Call   ToolCall
	Output ToolOutput
}

// TurnStopInput adalah masukan AgentTurnStopping.
type TurnStopInput struct {
	SessionID  string
	TurnID     string
	AgentName  string
	Steps      int
	StopReason string
}

// StopVote adalah suara satu listener AgentTurnStopping.
type StopVote struct {
	Continue bool
	Reason   string
}

// Subscription didefinisikan lengkap bersama implementasi Observe di T-004.
type Subscription[T any] struct{}

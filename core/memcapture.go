package core

// MemoryCapturer runs one background memory-capture pass after a turn. Capture
// returns at once; the pass runs in the background and never delays the turn
// (REQ-MEM-010).
type MemoryCapturer interface {
	Capture(CaptureRequest)
}

// CaptureRequest is one background capture pass. It carries the conversation
// prefix (System and Tools) so the capture call reuses the warm provider cache
// (REQ-MEM-011).
type CaptureRequest struct {
	SessionID string
	TurnID    string
	AgentName string
	Home      string
	Provider  string
	Model     string
	System    string
	Tools     []ToolSpec
}

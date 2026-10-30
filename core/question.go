package core

import "context"

// Question statuses are the values of QuestionOutcome.Status.
const (
	// QuestionStatusAnswered means the owner answered or skipped every question.
	QuestionStatusAnswered = "answered"
	// QuestionStatusUnavailable means the request ended without answers.
	QuestionStatusUnavailable = "unavailable"
)

// Question reasons are the values of QuestionOutcome.Reason.
const (
	// QuestionReasonNoResponder means the session channel has no responder.
	QuestionReasonNoResponder = "no_responder"
	// QuestionReasonTimeout means the request expired before an answer.
	QuestionReasonTimeout = "timeout"
	// QuestionReasonCancelled means the turn or the daemon was cancelled.
	QuestionReasonCancelled = "cancelled"
	// QuestionReasonInterrupted means the session resumed with an open request.
	QuestionReasonInterrupted = "interrupted"
)

// QuestionOption is one answer option of a question.
type QuestionOption struct {
	Label       string
	Description string
}

// Question is one question in a request.
type Question struct {
	Text        string
	Header      string
	MultiSelect bool
	Options     []QuestionOption
}

// QuestionRequest is a request for the owner to answer one or more questions.
type QuestionRequest struct {
	SessionID string
	TurnID    string
	CallID    string
	RequestID string
	AgentName string
	Questions []Question
	ExpiresAt int64 // Unix ms
}

// QuestionAnswer is the answer to one question.
type QuestionAnswer struct {
	Question string   `json:"question"`           // Question.Text
	Selected []string `json:"selected,omitempty"` // option labels, in option order
	Other    string   `json:"other,omitempty"`    // empty unless typed
	Skipped  bool     `json:"skipped,omitempty"`
}

// QuestionOutcome is the result of a request.
type QuestionOutcome struct {
	Status  string           `json:"status"`            // QuestionStatusAnswered or QuestionStatusUnavailable
	Reason  string           `json:"reason,omitempty"`  // when unavailable
	Answers []QuestionAnswer `json:"answers,omitempty"` // when answered, one per question
}

// QuestionService asks the session's channel responder and waits for the answer.
type QuestionService interface {
	Ask(ctx context.Context, req QuestionRequest) (QuestionOutcome, error)
}

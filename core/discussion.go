package core

import "context"

// Session event kinds of a room discussion (spec 47 API-RDS-001..003).
const (
	KindDiscussionStarted = "discussion/started"
	KindDiscussionMessage = "discussion/message"
	KindDiscussionEnded   = "discussion/ended"
)

// Discussion message roles (API-RDS-002).
const (
	DiscussionRoleLead   = "lead"
	DiscussionRoleMember = "member"
	DiscussionRoleOwner  = "owner"
)

// Owner stop modes (API-RDS-011).
const (
	DiscussionStop   = "stop"
	DiscussionDirect = "direct"
)

// DiscussRequestNote is the hidden runtime note of a room message sent with
// Discuss on (REQ-RDS-020).
const DiscussRequestNote = "The owner turned on Discuss for this message: open a discussion with discuss_start before you answer."

// DiscussionMember is one member of a discussion and the angle it covers.
type DiscussionMember struct {
	Agent string `json:"agent"`
	Angle string `json:"angle,omitempty"`
}

// DiscussionStartedPayload is the payload of discussion/started.
type DiscussionStartedPayload struct {
	V           int                `json:"v"`
	ID          string             `json:"id"`
	CallID      string             `json:"call_id"`
	Topic       string             `json:"topic"`
	Opening     string             `json:"opening"`
	Lead        string             `json:"lead"`
	Members     []DiscussionMember `json:"members"`
	Rounds      int                `json:"rounds"`
	ExtraRounds string             `json:"extra_rounds"`
	Requested   bool               `json:"requested,omitempty"`
}

// DiscussionMessagePayload is the payload of discussion/message.
type DiscussionMessagePayload struct {
	V              int    `json:"v"`
	ID             string `json:"id"`
	CallID         string `json:"call_id,omitempty"`
	Role           string `json:"role"`
	Agent          string `json:"agent,omitempty"`
	Round          int    `json:"round,omitempty"`
	Text           string `json:"text"`
	ReplyTo        string `json:"reply_to,omitempty"`
	Quote          string `json:"quote,omitempty"`
	Passed         bool   `json:"passed,omitempty"`
	Failed         bool   `json:"failed,omitempty"`
	Extra          bool   `json:"extra,omitempty"`
	Stop           string `json:"stop,omitempty"`
	ChildSessionID string `json:"child_session_id,omitempty"`
}

// DiscussionPoint is one point of a summary and the member it came from.
type DiscussionPoint struct {
	Text  string `json:"text"`
	Agent string `json:"agent,omitempty"`
}

// DiscussionSummary is the structured summary of a discussion.
type DiscussionSummary struct {
	Conclusion string            `json:"conclusion"`
	Agreed     []DiscussionPoint `json:"agreed"`
	Different  []DiscussionPoint `json:"different"`
	Open       []DiscussionPoint `json:"open"`
}

// DiscussionEndedPayload is the payload of discussion/ended.
type DiscussionEndedPayload struct {
	V          int               `json:"v"`
	ID         string            `json:"id"`
	CallID     string            `json:"call_id"`
	Topic      string            `json:"topic"`
	Reason     string            `json:"reason"`
	Rounds     int               `json:"rounds"`
	Limit      int               `json:"limit"`
	Summary    DiscussionSummary `json:"summary"`
	Parts      map[string]string `json:"parts,omitempty"`
	NotesSaved int               `json:"notes_saved"`
	SharedWith []string          `json:"shared_with"`
	Learning   []string          `json:"learning"`
}

// DiscussionNote is one room memory note a discussion end saves.
type DiscussionNote struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Replaces string `json:"replaces,omitempty"`
}

// DiscussStart is the input of discuss_start.
type DiscussStart struct {
	Topic   string             `json:"topic"`
	Opening string             `json:"opening"`
	Members []DiscussionMember `json:"members"`
}

// DiscussAsk is the input of discuss_ask.
type DiscussAsk struct {
	Agent   string `json:"agent"`
	Message string `json:"message"`
	ReplyTo string `json:"reply_to"`
	Quote   string `json:"quote"`
}

// DiscussMore is the input of discuss_more.
type DiscussMore struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

// DiscussEnd is the input of discuss_end.
type DiscussEnd struct {
	Reason     string            `json:"reason"`
	Conclusion string            `json:"conclusion"`
	Agreed     []DiscussionPoint `json:"agreed"`
	Different  []DiscussionPoint `json:"different"`
	Open       []DiscussionPoint `json:"open"`
	Parts      map[string]string `json:"parts"`
	Notes      []DiscussionNote  `json:"notes"`
}

// DiscussionService runs the discussion of a team session for the discuss_*
// tools (REQ-RDS-010..016). It is set on ToolCtx only for a team lead's root
// turn.
type DiscussionService interface {
	Start(ctx context.Context, tc ToolCtx, in DiscussStart) (ToolOutput, error)
	Ask(ctx context.Context, tc ToolCtx, in DiscussAsk) (ToolOutput, error)
	More(ctx context.Context, tc ToolCtx, in DiscussMore) (ToolOutput, error)
	End(ctx context.Context, tc ToolCtx, in DiscussEnd) (ToolOutput, error)
}

// DiscussionShare is what the runtime does after a discussion ends: post the
// summary to each member's room (REQ-RDS-031) and deliver discussion
// messages to the session's channel (REQ-RDS-033). The runtime installs it on
// the registry; nil disables both.
type DiscussionShare interface {
	// PostMessage delivers one discussion message (name and text) to the
	// session's channel when the channel shows discussions (Telegram).
	PostMessage(ctx context.Context, sessionID, name, text string)
	// ShareSummary posts the summary text to member's own room.
	ShareSummary(ctx context.Context, team, member, text string) error
}

package core

import (
	"context"
	"errors"
)

// Secret request event kinds (spec 53). The events carry the secret's name,
// description, and outcome; never its value.
const (
	KindSecretRequested = "secret/requested"
	KindSecretResolved  = "secret/resolved"
)

// Secret request statuses are the values of SecretOutcome.Status and of the
// secret/resolved outcome (spec 53 API-SEC-001).
const (
	// SecretStatusSaved means the owner typed a value and it was written to
	// the secret store.
	SecretStatusSaved = "saved"
	// SecretStatusCancelled means the owner cancelled the card, or the turn
	// was cancelled while the card was open.
	SecretStatusCancelled = "cancelled"
	// SecretStatusExpired means the request timed out before an answer.
	SecretStatusExpired = "expired"
	// SecretStatusUnavailable means no responder could show the card.
	SecretStatusUnavailable = "unavailable"
)

// Secret request errors the tool reports before any card is shown.
var (
	// ErrSecretTelegram means the session is a Telegram chat: secrets are never
	// collected over a chat channel.
	ErrSecretTelegram = errors.New("secrets cannot be collected over Telegram; ask the owner to add it on the web dashboard (Settings > Secrets) or in a web chat, and never ask for the value in this chat")
	// ErrSecretNoWeb means the session's channel cannot show the secure card.
	ErrSecretNoWeb = errors.New("secret_request needs the web chat; ask the owner to add the secret in Settings > Secrets on the web dashboard")
	// ErrSecretBusy means the session already has an open secret request.
	ErrSecretBusy = errors.New("a secret request is already open in this session")
)

// SecretRequest asks the owner to type one secret value into a secure input
// card. It never carries the value.
type SecretRequest struct {
	SessionID   string
	TurnID      string
	CallID      string
	RequestID   string
	AgentName   string
	Name        string
	Description string
	Link        string
	// Replaces is true when the name already has a value; saving replaces it.
	Replaces  bool
	ExpiresAt int64 // Unix ms
}

// SecretOutcome is the result of a secret request. It never carries the value.
type SecretOutcome struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// SecretRequester opens a secret request in the session's channel and waits
// for the outcome. It is implemented by the secret request service; the turn
// fills ToolCtx.Secrets.
type SecretRequester interface {
	RequestSecret(ctx context.Context, req SecretRequest) (SecretOutcome, error)
}

// SecretRequested is the payload of a secret/requested event.
type SecretRequested struct {
	V           int    `json:"v"`
	RequestID   string `json:"request_id"`
	CallID      string `json:"call_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Link        string `json:"link,omitempty"`
	Replaces    bool   `json:"replaces"`
	ExpiresAt   int64  `json:"expires_at"`
}

// SecretResolved is the payload of a secret/resolved event: the outcome only.
type SecretResolved struct {
	V         int    `json:"v"`
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason,omitempty"`
}

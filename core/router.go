package core

import "context"

// LLMCallMeta describes who makes an LLM call. The router copies it into
// every call record (REQ-RTR-009).
type LLMCallMeta struct {
	// Source is one of turn, compaction, memory_capture, knowledge,
	// embedding, proxy, or test.
	Source    string
	Agent     string
	SessionID string
	TurnID    string
	// Requested is the combo or address the caller asked for. When empty,
	// StreamChain records the first target's address.
	Requested string
}

// LLMAttempt is one failed attempt of a chain, reported to the caller before
// the router retries or moves to the next target (REQ-RTR-005).
type LLMAttempt struct {
	Target LLMTarget
	// N is the 1-based attempt number on Target.
	N   int
	Err *ProviderError
}

// LLMRouter routes LLM calls through provider chains with retry, fallback,
// cooldown, and call records (ADR 0019). The agent uses it through core so
// that it does not import the router package.
type LLMRouter interface {
	LLMRegistry
	// Resolve turns a combo name, a "provider/model" address, or a legacy
	// "provider:model" value into a chain.
	Resolve(address string) ([]LLMTarget, error)
	// StreamChain tries the chain in order and returns the stream of the
	// first target that produced an event, with that target. onAttempt may
	// be nil.
	StreamChain(ctx context.Context, chain []LLMTarget, req ChatRequest, meta LLMCallMeta, onAttempt func(LLMAttempt)) (ChatStream, LLMTarget, error)
	// Embed embeds inputs with the model at address.
	Embed(ctx context.Context, address string, inputs []string, meta LLMCallMeta) ([][]float32, error)
}

// Provider error classes that are not written as literals elsewhere.
const (
	// ErrorClassNotFound is an HTTP 404 from a provider: the model or the
	// endpoint does not exist. The router moves to the next target.
	ErrorClassNotFound = "not_found"
)

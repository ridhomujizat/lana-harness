package router

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// maxRecordError is the longest error message kept in a call record.
const maxRecordError = 500

// CallRecord is one attempt of one LLM call (REQ-RTR-009, API-RTR-012).
type CallRecord struct {
	// ID is set by the store.
	ID        int64     `json:"id,omitempty"`
	At        time.Time `json:"at"`
	Source    string    `json:"source"`
	Agent     string    `json:"agent"`
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id,omitempty"`
	// Requested is the address or combo the caller asked for.
	Requested string `json:"requested"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	// Attempt is the 1-based attempt number on Provider/Model.
	Attempt int `json:"attempt"`
	// Outcome is "ok" or the error class.
	Outcome      string `json:"outcome"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	// CostUSD is nil when the model price is unknown.
	CostUSD *float64 `json:"cost_usd"`
	// TTFTMs is the time to the first stream event; 0 when there was none.
	TTFTMs     int64 `json:"ttft_ms"`
	DurationMs int64 `json:"duration_ms"`
	// Error is the redacted error message, at most 500 characters.
	Error string `json:"error"`
}

// Recorder receives call records. Record must not block: the router calls
// it on the stream path.
type Recorder interface {
	Record(CallRecord)
}

// Price is the USD price per one million tokens.
type Price = models.Price

// Cost returns the USD cost of usage at price, or nil when price is nil.
// Input tokens include the cached ones; cached tokens use the cached-input
// price, or the input price when the cached-input price is zero.
func Cost(price *Price, usage core.Usage) *float64 {
	if price == nil {
		return nil
	}
	cached := max(min(usage.CachedInputTokens, usage.InputTokens), 0)
	uncached := max(usage.InputTokens-cached, 0)
	cachedPrice := price.CachedInput
	if cachedPrice == 0 {
		cachedPrice = price.Input
	}
	cost := (float64(uncached)*price.Input + float64(cached)*cachedPrice + float64(max(usage.OutputTokens, 0))*price.Output) / 1e6
	return &cost
}

func (r *Router) baseRecord(requested string, target Target, n int, start time.Time, meta CallMeta) CallRecord {
	return CallRecord{
		At:        start.UTC(),
		Source:    meta.Source,
		Agent:     meta.Agent,
		SessionID: meta.SessionID,
		TurnID:    meta.TurnID,
		Requested: requested,
		Provider:  target.Provider,
		Model:     target.Model,
		Attempt:   n,
	}
}

func (r *Router) recordFailure(snap *snapshot, requested string, target Target, n int, start time.Time, meta CallMeta, pe *core.ProviderError) {
	rec := r.baseRecord(requested, target, n, start, meta)
	rec.Outcome = effectiveClass(pe)
	rec.DurationMs = r.now().Sub(start).Milliseconds()
	rec.Error = truncate(pe.Error(), maxRecordError)
	rec.CostUSD = Cost(r.priceFor(snap, target), core.Usage{})
	r.emit(rec)
}

// recordedStream replays the stream after its first event, measures the
// duration, captures usage, and records the attempt when the stream ends or
// the consumer stops.
func (r *Router) recordedStream(snap *snapshot, first core.ChatEvent, pump *streamPump, base CallRecord, start time.Time) core.ChatStream {
	return func(yield func(core.ChatEvent, error) bool) {
		defer pump.cancel()
		var usage core.Usage
		var streamErr error
		observe := func(event core.ChatEvent) {
			if event.Kind == core.ChatUsage {
				usage = event.Usage
			}
		}
		defer func() {
			rec := base
			rec.DurationMs = r.now().Sub(start).Milliseconds()
			rec.InputTokens = usage.InputTokens
			rec.OutputTokens = usage.OutputTokens
			rec.CachedTokens = usage.CachedInputTokens
			rec.CostUSD = Cost(r.priceFor(snap, Target{Provider: base.Provider, Model: base.Model}), usage)
			rec.Outcome = "ok"
			if streamErr != nil {
				pe := snap.redactError(r.opts.secrets, base.Provider, asProviderError(streamErr))
				rec.Outcome = effectiveClass(pe)
				rec.Error = truncate(pe.Error(), maxRecordError)
			}
			r.emit(rec)
		}()
		observe(first)
		if !yield(first, nil) {
			return
		}
		for result := range pump.ch {
			if result.err != nil {
				streamErr = result.err
			} else {
				observe(result.event)
			}
			if !yield(result.event, result.err) {
				return
			}
			if result.err != nil {
				return
			}
		}
	}
}

// RecordExternal hands a call record made outside the router to the attached
// recorder, such as the one call record of a Claude Agent turn (REQ-CCR-008).
// A zero At is set to now. It is a no-op without a recorder and never blocks.
func (r *Router) RecordExternal(rec CallRecord) {
	if rec.At.IsZero() {
		rec.At = r.now().UTC()
	}
	r.emit(rec)
}

func (r *Router) emit(rec CallRecord) {
	box := r.recorder.Load()
	if box == nil || box.rec == nil {
		return
	}
	defer func() { _ = recover() }()
	box.rec.Record(rec)
}

// priceFor returns the price of a target: the provider's own model list
// first (OpenRouter prices are live), then the catalog.
func (r *Router) priceFor(snap *snapshot, target Target) *Price {
	if p := r.models.cachedPrice(snap.providers[target.Provider], target.Model); p != nil {
		return p
	}
	if m, ok := models.Lookup(target.Model); ok && m.Price != nil {
		p := *m.Price
		return &p
	}
	return nil
}

// apiKey returns the API key of a provider, or "" when it has none.
func (s *snapshot) apiKey(secrets core.Secrets, provider string) string {
	entry, ok := s.providers[provider]
	if !ok || secrets == nil {
		return ""
	}
	env := EffectiveKeyEnv(entry.cfg, provider)
	if env == "" {
		return ""
	}
	secret, ok := secrets.Lookup(env)
	if !ok {
		return ""
	}
	return secret.Reveal()
}

// redactError returns pe with the provider's API key replaced by "***" in
// its message. pe itself is returned when the message has no key.
func (s *snapshot) redactError(secrets core.Secrets, provider string, pe *core.ProviderError) *core.ProviderError {
	key := s.apiKey(secrets, provider)
	redacted := Redact(pe.Message, key)
	if redacted == pe.Message {
		return pe
	}
	out := *pe
	out.Message = redacted
	return &out
}

// Redact replaces every secret in message with "***". Empty secrets are
// ignored.
func Redact(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		message = strings.ReplaceAll(message, secret, "***")
	}
	return message
}

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}

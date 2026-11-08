package openai

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/router"
)

// reportLimits pushes the rate limits of a response to the router
// (REQ-RTR-012). Responses without rate-limit headers push nothing.
func (a *Adapter) reportLimits(header http.Header) {
	if a.limits == nil {
		return
	}
	now := time.Now()
	rate := parseRateLimits(header, now)
	if rate == nil {
		return
	}
	a.limits(router.LimitUpdate{At: now, Rate: rate})
}

// parseRateLimits reads OpenAI-style x-ratelimit-{limit,remaining,reset}-
// {requests,tokens} headers. OpenRouter's plain x-ratelimit-limit,
// -remaining, and -reset headers count requests. It returns nil when no
// limit is reported.
func parseRateLimits(header http.Header, now time.Time) *router.RateLimits {
	rate := router.RateLimits{
		Requests: parseRateLimit(header, "-requests", now),
		Tokens:   parseRateLimit(header, "-tokens", now),
	}
	if rate.Requests == nil {
		rate.Requests = parseRateLimit(header, "", now)
	}
	if rate.Requests == nil && rate.Tokens == nil {
		return nil
	}
	return &rate
}

// parseRateLimit reads one limit; suffix is "-requests", "-tokens", or "".
// A limit without a parseable limit or remaining value is skipped.
func parseRateLimit(header http.Header, suffix string, now time.Time) *router.RateLimit {
	limit, okLimit := parseCount(header.Get("x-ratelimit-limit" + suffix))
	remaining, okRemaining := parseCount(header.Get("x-ratelimit-remaining" + suffix))
	if !okLimit && !okRemaining {
		return nil
	}
	out := &router.RateLimit{Limit: limit, Remaining: remaining}
	if reset, ok := parseReset(header.Get("x-ratelimit-reset"+suffix), now); ok {
		out.ResetAt = &reset
	}
	return out
}

// parseCount reads a non-negative integer header value; a float is
// truncated.
func parseCount(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return max(n, 0), true
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil && f >= 0 {
		return int64(f), true
	}
	return 0, false
}

// parseReset reads a reset header value: a Go-style duration ("6m0s", "1s",
// "20ms"), a number (Unix milliseconds, Unix seconds, or seconds from now by
// size), or an RFC 3339 time.
func parseReset(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if d, err := time.ParseDuration(value); err == nil && d >= 0 {
		return now.Add(d).UTC(), true
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil && f >= 0 {
		switch {
		case f >= 1e12:
			return time.UnixMilli(int64(f)).UTC(), true
		case f >= 1e9:
			return time.Unix(int64(f), 0).UTC(), true
		default:
			return now.Add(time.Duration(f * float64(time.Second))).UTC(), true
		}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

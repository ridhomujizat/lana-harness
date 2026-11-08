package agent

import (
	"math/rand"
	"time"
)

// The router owns retry, backoff, and fallback before the first stream event
// (REQ-RTR-005). These helpers serve the mid-stream step retry of the turn
// loop (REQ-LTC-032).

func retryable(class string) bool {
	return class == "rate_limit" || class == "server" || class == "network"
}

// randomJitter returns a factor in [0.8, 1.2].
func randomJitter() float64 {
	return 0.8 + rand.Float64()*0.4 //nolint:gosec // backoff jitter is not security sensitive
}

// jitteredDelay scales a delay by the jitter factor.
func jitteredDelay(delay time.Duration, jitter func() float64) time.Duration {
	factor := 1.0
	if jitter != nil {
		factor = jitter()
	}
	return time.Duration(float64(delay) * factor)
}

// Package secretlike detects text that looks like a credential.
//
// It is shared by spec 20 memory capture and spec 30 knowledge
// (REQ-KNW-012): both refuse content that looks like a secret.
package secretlike

import "regexp"

// patterns are the secret-like shapes both specs reject. The last one is
// case-insensitive.
var patterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`xox[bp]-`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN`),
	regexp.MustCompile(`(?i)(password|passwd|api[_-]?key|token)\s*[:=]\s*\S{6,}`),
}

// Match reports whether text holds a secret-like value.
func Match(text string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

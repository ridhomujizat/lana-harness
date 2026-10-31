// Package spill saves full tool outputs to private, session-scoped files.
package spill

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Store writes spilled text under a private root.
type Store struct {
	Root string
}

// Save writes text verbatim to a new file of the session and returns its path.
// callID is accepted for the caller but the file name is made unique by a
// random prefix, so the same call can be saved more than once.
func (s Store) Save(sessionID, tool, callID, text string) (string, error) {
	dir := SessionDir(s.Root, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// MkdirAll keeps an existing mode, so make the directory owner-only even
	// when it was created by an earlier run.
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, randomHex(4)+"-"+SanitizeTool(tool)+".txt")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// SessionDir returns the spill directory of one session under root.
func SessionDir(root, sessionID string) string {
	return filepath.Join(root, SessionHash(sessionID))
}

// SessionHash returns the directory name that groups one session's spills. The
// guard uses it to recognize the spill directory of the current session.
func SessionHash(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])[:16]
}

// SanitizeTool reduces a tool name to safe filename characters.
func SanitizeTool(tool string) string {
	var b strings.Builder
	for _, r := range tool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "output"
	}
	return b.String()
}

// randomHex returns n random bytes as hex. It never fails on supported systems.
func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	// signInCodeTTL is how long a sign-in link code stays valid (REQ-WLG-002).
	signInCodeTTL = 15 * time.Minute
	// resetCodeTTL is how long a Telegram reset code stays valid (REQ-WLG-004).
	resetCodeTTL = 10 * time.Minute
	// resetMaxTries is how many wrong reset codes void the code (REQ-WLG-004).
	resetMaxTries = 5
	// resetSendInterval is the shortest gap between two reset sends.
	resetSendInterval = time.Minute
	// loginWindow and loginMaxFails lock login after five wrong tries in five
	// minutes (REQ-WLG-003).
	loginWindow       = 5 * time.Minute
	loginMaxFails     = 5
	loginLockDuration = 5 * time.Minute
)

// signInCodeStore keeps the unexpired sign-in codes as SHA-256 hashes. The
// plain codes live only in the URL handed to the owner.
type signInCodeStore struct {
	now func() time.Time

	mu    sync.Mutex
	codes map[[sha256.Size]byte]time.Time
}

func newSignInCodeStore(now func() time.Time) *signInCodeStore {
	return &signInCodeStore{now: now, codes: map[[sha256.Size]byte]time.Time{}}
}

// create returns a new 32-byte base64url code and stores its hash for 15
// minutes.
func (s *signInCodeStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(code))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.codes[sum] = s.now().Add(signInCodeTTL)
	return code, nil
}

// consume validates code and removes it, so a code works once. An unknown or
// expired code returns false.
func (s *signInCodeStore) consume(code string) bool {
	if code == "" {
		return false
	}
	sum := sha256.Sum256([]byte(code))
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.codes[sum]
	if !ok {
		return false
	}
	delete(s.codes, sum)
	return s.now().Before(expiry)
}

func (s *signInCodeStore) pruneLocked() {
	now := s.now()
	for sum, expiry := range s.codes {
		if !now.Before(expiry) {
			delete(s.codes, sum)
		}
	}
}

// resetCodeStore keeps the current Telegram reset code: its hash, expiry,
// wrong-try count, and the last send time for the one-per-minute limit.
type resetCodeStore struct {
	now func() time.Time

	mu        sync.Mutex
	active    bool
	codeHash  [sha256.Size]byte
	expiresAt time.Time
	tries     int
	sentAt    time.Time
}

func newResetCodeStore(now func() time.Time) *resetCodeStore {
	return &resetCodeStore{now: now}
}

// start stores code when a minute has passed since the last send and reports
// whether it did (the one-per-minute limit of API-WLG-004).
func (s *resetCodeStore) start(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !s.sentAt.IsZero() && now.Before(s.sentAt.Add(resetSendInterval)) {
		return false
	}
	s.codeHash = sha256.Sum256([]byte(code))
	s.expiresAt = now.Add(resetCodeTTL)
	s.tries = 0
	s.sentAt = now
	s.active = true
	return true
}

// verify compares code in constant time. ok reports a match; expired reports
// that the code is gone (no code, past its time, or voided by five wrong
// tries). A match consumes the code.
func (s *resetCodeStore) verify(code string) (ok, expired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false, true
	}
	if !s.now().Before(s.expiresAt) {
		s.clearLocked()
		return false, true
	}
	sum := sha256.Sum256([]byte(code))
	if subtle.ConstantTimeCompare(sum[:], s.codeHash[:]) != 1 {
		s.tries++
		if s.tries >= resetMaxTries {
			s.clearLocked()
			return false, true
		}
		return false, false
	}
	s.clearLocked()
	return true, false
}

func (s *resetCodeStore) clearLocked() {
	s.active = false
	s.codeHash = [sha256.Size]byte{}
	s.expiresAt = time.Time{}
	s.tries = 0
}

// loginLock tracks wrong password tries and the lockout window.
type loginLock struct {
	now func() time.Time

	mu          sync.Mutex
	fails       []time.Time
	lockedUntil time.Time
}

func newLoginLock(now func() time.Time) *loginLock {
	return &loginLock{now: now}
}

// lockedUntilTime returns the time login stays locked, or the zero time.
func (l *loginLock) lockedUntilTime() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.now().Before(l.lockedUntil) {
		return time.Time{}
	}
	return l.lockedUntil
}

// fail records a wrong password and locks login after five in five minutes.
func (l *loginLock) fail() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.fails[:0]
	for _, at := range l.fails {
		if now.Sub(at) < loginWindow {
			kept = append(kept, at)
		}
	}
	l.fails = append(kept, now)
	if len(l.fails) >= loginMaxFails {
		l.lockedUntil = now.Add(loginLockDuration)
		l.fails = nil
	}
}

// success clears the failure window after a correct password.
func (l *loginLock) success() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails = nil
	l.lockedUntil = time.Time{}
}

// newResetCode returns a 6-digit numeric code.
func newResetCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

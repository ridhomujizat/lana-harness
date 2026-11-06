package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

const (
	// credentialFileName is the owner credential file under $LANA_HOME.
	credentialFileName = "web.json"
	// credentialFileMode is the mode of web.json.
	credentialFileMode = 0o600
	// credentialVersionV1 is the token-only file written before spec #18.
	credentialVersionV1 = 1
	// credentialVersionV2 adds the password fields of spec #18.
	credentialVersionV2 = 2
	// minPasswordChars is the shortest accepted dashboard password.
	minPasswordChars = 8
)

// argon2id parameters. They are stored with every hash so a future change
// still verifies old passwords.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 2
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// credentialFile is the on-disk form of $LANA_HOME/web.json. Version 1 files
// carry only token_sha256; version 2 adds the password fields.
type credentialFile struct {
	Version        int    `json:"version"`
	PasswordHash   string `json:"password_hash,omitempty"`
	PasswordSetAt  int64  `json:"password_set_at,omitempty"`
	ResetRequested bool   `json:"reset_requested,omitempty"`
	TokenSHA256    string `json:"token_sha256,omitempty"`
}

// credentialStore is the owner credential file: the argon2id password hash,
// the reset flag, and the legacy owner token hash kept until a password is set.
type credentialStore struct {
	path string

	mu             sync.Mutex
	passwordHash   string
	passwordSetAt  time.Time
	resetRequested bool
	tokenHash      [sha256.Size]byte
	hasToken       bool
}

// loadCredentials reads $LANA_HOME/web.json. A missing file yields an empty
// store (no password, no token); the file is not created here. An empty home
// means there is no credential store at all and login always fails.
func loadCredentials(home string) (*credentialStore, error) {
	if home == "" {
		return nil, nil
	}
	path, err := filepath.Abs(filepath.Join(home, credentialFileName))
	if err != nil {
		return nil, credentialFileError(filepath.Join(home, credentialFileName), err.Error())
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &credentialStore{path: path}, nil
	}
	if err != nil {
		return nil, credentialFileError(path, "cannot read the file")
	}
	return parseCredentialFile(path, data)
}

// parseCredentialFile validates the contents of an existing web.json.
func parseCredentialFile(path string, data []byte) (*credentialStore, error) {
	var stored credentialFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, credentialFileError(path, "not valid JSON")
	}
	if stored.Version != credentialVersionV1 && stored.Version != credentialVersionV2 {
		return nil, credentialFileError(path, fmt.Sprintf("unsupported version %d", stored.Version))
	}
	store := &credentialStore{path: path}
	if stored.TokenSHA256 != "" {
		if !isLowerHexSHA256(stored.TokenSHA256) {
			return nil, credentialFileError(path, "token_sha256 must be 64 lowercase hex characters")
		}
		hash, err := hex.DecodeString(stored.TokenSHA256)
		if err != nil {
			return nil, credentialFileError(path, "token_sha256 must be 64 lowercase hex characters")
		}
		copy(store.tokenHash[:], hash)
		store.hasToken = true
	}
	if stored.PasswordHash != "" {
		if !isArgon2idHash(stored.PasswordHash) {
			return nil, credentialFileError(path, "password_hash is not a valid argon2id hash")
		}
		store.passwordHash = stored.PasswordHash
		store.passwordSetAt = time.UnixMilli(stored.PasswordSetAt)
	}
	store.resetRequested = stored.ResetRequested
	return store, nil
}

func credentialFileError(path, reason string) error {
	return fmt.Errorf("web: credential file %s cannot be used: %s; delete it and restart the daemon to set a new password", path, reason)
}

// isArgon2idHash reports whether value looks like a PHC argon2id string.
func isArgon2idHash(value string) bool {
	parts := strings.Split(value, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}
	if _, err := decodeArgonParams(parts[3]); err != nil {
		return false
	}
	if _, err := base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return false
	}
	if _, err := base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return false
	}
	return true
}

// PasswordSet reports whether a password has been created.
func (c *credentialStore) PasswordSet() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.passwordHash != ""
}

// ResetRequested reports whether the owner asked for a reset (REQ-WLG-005).
func (c *credentialStore) ResetRequested() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resetRequested
}

// VerifyPassword compares password with the stored hash in constant time.
func (c *credentialStore) VerifyPassword(password string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	hash := c.passwordHash
	c.mu.Unlock()
	if hash == "" {
		return false
	}
	return verifyArgon2id(hash, password)
}

// VerifyToken compares the owner token hash in constant time. It reports false
// once a password is set, because the token stops working (REQ-WLG-008).
func (c *credentialStore) VerifyToken(token string) bool {
	if c == nil || token == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.passwordHash != "" || !c.hasToken {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(sum[:], c.tokenHash[:]) == 1
}

// SetPassword stores an argon2id hash of password. It ends the legacy token and
// clears reset_requested, because the reset is resolved.
func (c *credentialStore) SetPassword(password string, now time.Time) error {
	if c == nil {
		return errors.New("web: no credential store")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.snapshotLocked()
	c.passwordHash = hash
	c.passwordSetAt = now
	c.resetRequested = false
	c.hasToken = false
	c.tokenHash = [sha256.Size]byte{}
	if err := c.writeLocked(); err != nil {
		c.restoreLocked(previous)
		return err
	}
	return nil
}

// SetResetRequested records the reset flag of REQ-WLG-005.
func (c *credentialStore) SetResetRequested(requested bool) error {
	if c == nil {
		return errors.New("web: no credential store")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resetRequested == requested {
		return nil
	}
	previous := c.snapshotLocked()
	c.resetRequested = requested
	if err := c.writeLocked(); err != nil {
		c.restoreLocked(previous)
		return err
	}
	return nil
}

// credentialSnapshot restores in-memory state when a write fails.
type credentialSnapshot struct {
	passwordHash   string
	passwordSetAt  time.Time
	resetRequested bool
	tokenHash      [sha256.Size]byte
	hasToken       bool
}

func (c *credentialStore) snapshotLocked() credentialSnapshot {
	return credentialSnapshot{
		passwordHash:   c.passwordHash,
		passwordSetAt:  c.passwordSetAt,
		resetRequested: c.resetRequested,
		tokenHash:      c.tokenHash,
		hasToken:       c.hasToken,
	}
}

func (c *credentialStore) restoreLocked(s credentialSnapshot) {
	c.passwordHash = s.passwordHash
	c.passwordSetAt = s.passwordSetAt
	c.resetRequested = s.resetRequested
	c.tokenHash = s.tokenHash
	c.hasToken = s.hasToken
}

// writeLocked writes the v2 file atomically with mode 0600. The caller holds
// the lock.
func (c *credentialStore) writeLocked() error {
	stored := credentialFile{
		Version:        credentialVersionV2,
		PasswordHash:   c.passwordHash,
		ResetRequested: c.resetRequested,
	}
	if !c.passwordSetAt.IsZero() {
		stored.PasswordSetAt = c.passwordSetAt.UnixMilli()
	}
	if c.hasToken {
		stored.TokenSHA256 = hex.EncodeToString(c.tokenHash[:])
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return webfiles.AtomicWriteMode(c.path, append(data, '\n'), credentialFileMode)
}

// hashPassword returns a PHC argon2id string with the salt and parameters.
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// verifyArgon2id recomputes the hash of password with the stored parameters and
// compares in constant time.
func verifyArgon2id(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	params, err := decodeArgonParams(parts[3])
	if err != nil {
		return false
	}
	var version int
	if n, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || n != 1 || version != argon2.Version {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeArgonParams(value string) (argonParams, error) {
	var params argonParams
	var memory, iterations, threads int
	if _, err := fmt.Sscanf(value, "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return params, err
	}
	if memory <= 0 || iterations <= 0 || threads <= 0 || threads > 255 {
		return params, errors.New("invalid argon2id parameters")
	}
	return argonParams{memory: uint32(memory), time: uint32(iterations), threads: uint8(threads)}, nil
}

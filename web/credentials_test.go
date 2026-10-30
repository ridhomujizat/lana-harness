package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCredentialsV1Token(t *testing.T) {
	home := t.TempDir()
	token := "owner-token-value"
	sum := sha256.Sum256([]byte(token))
	data, err := json.Marshal(credentialFile{Version: credentialVersionV1, TokenSHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, credentialFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := loadCredentials(home)
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if store.PasswordSet() {
		t.Fatal("v1 token file reports a password")
	}
	if store.ResetRequested() {
		t.Fatal("v1 token file reports a reset request")
	}
	if !store.VerifyToken(token) {
		t.Fatal("the stored token does not verify")
	}
	if store.VerifyToken("wrong") {
		t.Fatal("a wrong token verifies")
	}
	if store.VerifyPassword("anything") {
		t.Fatal("a token-only store verifies a password")
	}
}

func TestLoadCredentialsMissingFile(t *testing.T) {
	store, err := loadCredentials(t.TempDir())
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if store.PasswordSet() || store.ResetRequested() {
		t.Fatal("a missing file is not an empty store")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.path), credentialFileName)); !os.IsNotExist(err) {
		t.Fatal("loadCredentials created the file")
	}
}

func TestSetPasswordWritesV2AndRetiresToken(t *testing.T) {
	home := t.TempDir()
	store, err := loadCredentials(home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	if err := store.SetPassword("hunter2hunter2", now); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if !store.PasswordSet() {
		t.Fatal("PasswordSet = false after SetPassword")
	}
	if !store.VerifyPassword("hunter2hunter2") {
		t.Fatal("the password does not verify")
	}
	if store.VerifyPassword("wrong password") {
		t.Fatal("a wrong password verifies")
	}

	path := filepath.Join(home, credentialFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != credentialFileMode {
		t.Fatalf("mode = %o, want %o", got, credentialFileMode)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored credentialFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("web.json %q: %v", raw, err)
	}
	if stored.Version != credentialVersionV2 {
		t.Fatalf("version = %d, want 2", stored.Version)
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("password_hash = %q", stored.PasswordHash)
	}
	if stored.PasswordSetAt != now.UnixMilli() {
		t.Fatalf("password_set_at = %d, want %d", stored.PasswordSetAt, now.UnixMilli())
	}
	if strings.Contains(string(raw), "hunter2hunter2") {
		t.Fatalf("web.json holds the plain password: %q", raw)
	}
}

func TestSetResetRequestedRoundTrip(t *testing.T) {
	home := t.TempDir()
	store, err := loadCredentials(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResetRequested(true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadCredentials(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.ResetRequested() {
		t.Fatal("reset_requested was not persisted")
	}
	// A password set clears the flag.
	if err := reloaded.SetPassword("a good password", time.Now()); err != nil {
		t.Fatal(err)
	}
	if reloaded.ResetRequested() {
		t.Fatal("SetPassword did not clear reset_requested")
	}
}

func TestArgon2VerifyTable(t *testing.T) {
	encoded, err := hashPassword("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$") {
		t.Fatalf("hash = %q", encoded)
	}
	cases := []struct {
		name     string
		encoded  string
		password string
		want     bool
	}{
		{name: "match", encoded: encoded, password: "s3cret-pass", want: true},
		{name: "wrong", encoded: encoded, password: "s3cret-pass2", want: false},
		{name: "garbage", encoded: "not-a-hash", password: "s3cret-pass", want: false},
		{name: "truncated", encoded: encoded[:len(encoded)-1], password: "s3cret-pass", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyArgon2id(tc.encoded, tc.password); got != tc.want {
				t.Fatalf("verifyArgon2id = %v, want %v", got, tc.want)
			}
		})
	}
	if isArgon2idHash("not-a-hash") {
		t.Fatal("isArgon2idHash accepted a non-hash")
	}
	if !isArgon2idHash(encoded) {
		t.Fatal("isArgon2idHash rejected a real hash")
	}
}

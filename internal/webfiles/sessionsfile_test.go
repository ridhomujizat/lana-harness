package webfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func testSessionHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sampleSessionRecords() SessionsFile {
	return SessionsFile{
		Version: SessionsFileVersion,
		Sessions: []SessionRecord{
			{
				IDHash:       testSessionHash("cookie-a"),
				CSRFHash:     testSessionHash("csrf-a"),
				CreatedAt:    1761825600000,
				LastUsedAt:   1761825660000,
				UserAgent:    "Browser A",
				Remote:       true,
				Login:        "ridho@github",
				Device:       "Ridho's iPhone",
				IP:           "100.64.0.1",
				ControlUntil: 1761825900000,
			},
			{
				IDHash:     testSessionHash("cookie-b"),
				CSRFHash:   testSessionHash("csrf-b"),
				CreatedAt:  1761825700000,
				LastUsedAt: 1761825700000,
				UserAgent:  "Browser B",
			},
		},
	}
}

func TestSessionsFileMissingIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), SessionsFileName)
	file, err := ReadSessionsFile(path)
	if err != nil {
		t.Fatalf("ReadSessionsFile: %v", err)
	}
	if file.Version != SessionsFileVersion {
		t.Fatalf("version = %d, want %d", file.Version, SessionsFileVersion)
	}
	if len(file.Sessions) != 0 {
		t.Fatalf("sessions = %d, want 0", len(file.Sessions))
	}
}

func TestSessionsFileRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SessionsFileName)
	want := sampleSessionRecords()
	if err := WriteSessionsFile(path, want); err != nil {
		t.Fatalf("WriteSessionsFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != SessionsFileMode {
		t.Fatalf("mode = %04o, want %04o", got, SessionsFileMode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds hashes only: no cookie, CSRF, or user-agent value leaks
	// through a plain preimage.
	for _, raw := range []string{"cookie-a", "cookie-b", "csrf-a", "csrf-b"} {
		if strings.Contains(string(data), raw) {
			t.Fatalf("%s contains the plain value %q: %s", SessionsFileName, raw, data)
		}
	}
	for _, record := range want.Sessions {
		if !strings.Contains(string(data), record.IDHash) {
			t.Fatalf("%s does not contain the id hash %q", SessionsFileName, record.IDHash)
		}
	}

	got, err := ReadSessionsFile(path)
	if err != nil {
		t.Fatalf("ReadSessionsFile: %v", err)
	}
	if got.Version != SessionsFileVersion || len(got.Sessions) != len(want.Sessions) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	for i := range want.Sessions {
		if got.Sessions[i] != want.Sessions[i] {
			t.Fatalf("sessions[%d] = %+v, want %+v", i, got.Sessions[i], want.Sessions[i])
		}
	}
	assertSessionsNoTempFiles(t, dir)
}

func assertSessionsNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestSessionsFileWriteForcesModeOnReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SessionsFileName)
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSessionsFile(path, sampleSessionRecords()); err != nil {
		t.Fatalf("WriteSessionsFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != SessionsFileMode {
		t.Fatalf("mode = %04o, want %04o", got, SessionsFileMode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old") {
		t.Fatalf("old content survived: %s", data)
	}
	assertSessionsNoTempFiles(t, dir)
}

func TestSessionsFileInvalid(t *testing.T) {
	goodID := testSessionHash("good")
	goodCSRF := testSessionHash("good-csrf")
	tests := []struct {
		name string
		body string
	}{
		{name: "not json", body: "not json"},
		{name: "wrong version", body: `{"version":2,"sessions":[]}`},
		{name: "short id hash", body: `{"version":1,"sessions":[{"id_hash":"abc","csrf_hash":"` + goodCSRF + `","created_at":1,"last_used_at":2,"user_agent":"x"}]}`},
		{name: "uppercase hash", body: `{"version":1,"sessions":[{"id_hash":"` + strings.ToUpper(goodID) + `","csrf_hash":"` + goodCSRF + `","created_at":1,"last_used_at":2,"user_agent":"x"}]}`},
		{name: "bad csrf hash", body: `{"version":1,"sessions":[{"id_hash":"` + goodID + `","csrf_hash":"zz","created_at":1,"last_used_at":2,"user_agent":"x"}]}`},
		{name: "duplicate id", body: `{"version":1,"sessions":[{"id_hash":"` + goodID + `","csrf_hash":"` + goodCSRF + `","created_at":1,"last_used_at":2,"user_agent":"x"},{"id_hash":"` + goodID + `","csrf_hash":"` + goodCSRF + `","created_at":3,"last_used_at":4,"user_agent":"y"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), SessionsFileName)
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadSessionsFile(path)
			if !errors.Is(err, ErrSessionFile) {
				t.Fatalf("error = %v, want ErrSessionFile", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %q, want the file path", err)
			}
		})
	}
}

func TestSessionsFileWriteRejectsBadHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), SessionsFileName)
	file := SessionsFile{Version: SessionsFileVersion, Sessions: []SessionRecord{{
		IDHash: "nope", CSRFHash: testSessionHash("csrf"), CreatedAt: 1, LastUsedAt: 1,
	}}}
	err := WriteSessionsFile(path, file)
	if !errors.Is(err, ErrSessionFile) {
		t.Fatalf("error = %v, want ErrSessionFile", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("file exists after a rejected write: %v", statErr)
	}
}

func TestSessionsFileTruncatesUserAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), SessionsFileName)
	long := strings.Repeat("a", 200)
	file := sampleSessionRecords()
	file.Sessions = file.Sessions[:1]
	file.Sessions[0].UserAgent = long
	if err := WriteSessionsFile(path, file); err != nil {
		t.Fatalf("WriteSessionsFile: %v", err)
	}
	got, err := ReadSessionsFile(path)
	if err != nil {
		t.Fatalf("ReadSessionsFile: %v", err)
	}
	if n := utf8.RuneCountInString(got.Sessions[0].UserAgent); n != MaxUserAgentChars {
		t.Fatalf("user_agent length = %d, want %d", n, MaxUserAgentChars)
	}
}

// TestSessionsFileLoadsOldRecord covers REQ-RA-020: a file written before the
// remote fields existed loads unchanged, with the fields empty.
func TestSessionsFileLoadsOldRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), SessionsFileName)
	body := `{"version":1,"sessions":[{"id_hash":"` + testSessionHash("old") +
		`","csrf_hash":"` + testSessionHash("old-csrf") +
		`","created_at":1,"last_used_at":2,"user_agent":"Old"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSessionsFile(path)
	if err != nil {
		t.Fatalf("ReadSessionsFile: %v", err)
	}
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got.Sessions))
	}
	record := got.Sessions[0]
	if record.Remote || record.Login != "" || record.Device != "" || record.IP != "" || record.ControlUntil != 0 {
		t.Fatalf("old record = %+v, want the remote fields empty", record)
	}
	// Writing it back keeps the old shape: a local record omits every remote
	// field, so the file stays byte-compatible.
	if err := WriteSessionsFile(path, got); err != nil {
		t.Fatalf("WriteSessionsFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"remote", "login", "device", `"ip"`, "control_until"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("%s holds a remote field %q for a local record: %s", SessionsFileName, key, data)
		}
	}
}

func TestSessionsFileTruncatesRemoteLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), SessionsFileName)
	long := strings.Repeat("b", 300)
	file := sampleSessionRecords()
	file.Sessions = file.Sessions[:1]
	file.Sessions[0].Remote = true
	file.Sessions[0].Login = long
	file.Sessions[0].Device = long
	file.Sessions[0].IP = long
	if err := WriteSessionsFile(path, file); err != nil {
		t.Fatalf("WriteSessionsFile: %v", err)
	}
	got, err := ReadSessionsFile(path)
	if err != nil {
		t.Fatalf("ReadSessionsFile: %v", err)
	}
	for name, value := range map[string]string{
		"login":  got.Sessions[0].Login,
		"device": got.Sessions[0].Device,
		"ip":     got.Sessions[0].IP,
	} {
		if n := utf8.RuneCountInString(value); n != MaxRemoteLabelChars {
			t.Fatalf("%s length = %d, want %d", name, n, MaxRemoteLabelChars)
		}
	}
}

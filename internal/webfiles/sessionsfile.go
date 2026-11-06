package webfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"
)

const (
	// SessionsFileName is the browser session record file under $LANA_HOME.
	SessionsFileName = "web-sessions.json"
	// SessionsFileMode is the only accepted mode for web-sessions.json.
	SessionsFileMode os.FileMode = 0o600
	// SessionsFileVersion is the on-disk schema version.
	SessionsFileVersion = 1
	// MaxUserAgentChars is the user-agent label limit of API-WEB-024.
	MaxUserAgentChars = 120
)

// ErrSessionFile is returned when web-sessions.json cannot be read, parsed, or
// written. The error names the file.
var ErrSessionFile = errors.New("session file cannot be used")

// SessionRecord is one browser session as stored on disk. It holds hashes
// only: never a cookie value or a CSRF value in plain text.
type SessionRecord struct {
	IDHash     string `json:"id_hash"`
	CSRFHash   string `json:"csrf_hash"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	UserAgent  string `json:"user_agent"`
}

// SessionsFile is the on-disk form of $LANA_HOME/web-sessions.json.
type SessionsFile struct {
	Version  int             `json:"version"`
	Sessions []SessionRecord `json:"sessions"`
}

// ReadSessionsFile parses path. A missing file is an empty version 1 file: the
// daemon starts with no browser sessions. Any other read or parse problem is
// an ErrSessionFile error that names the file.
func ReadSessionsFile(path string) (SessionsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionsFile{Version: SessionsFileVersion, Sessions: []SessionRecord{}}, nil
		}
		return SessionsFile{}, sessionFileError(path, "cannot read the file")
	}
	var file SessionsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return SessionsFile{}, sessionFileError(path, "not valid JSON")
	}
	if file.Version != SessionsFileVersion {
		return SessionsFile{}, sessionFileError(path, fmt.Sprintf("unsupported version %d", file.Version))
	}
	if file.Sessions == nil {
		file.Sessions = []SessionRecord{}
	}
	seen := make(map[string]struct{}, len(file.Sessions))
	for i := range file.Sessions {
		record := &file.Sessions[i]
		if !isHexSHA256(record.IDHash) {
			return SessionsFile{}, sessionFileError(path, fmt.Sprintf("sessions[%d].id_hash must be 64 lowercase hex characters", i))
		}
		if !isHexSHA256(record.CSRFHash) {
			return SessionsFile{}, sessionFileError(path, fmt.Sprintf("sessions[%d].csrf_hash must be 64 lowercase hex characters", i))
		}
		if _, ok := seen[record.IDHash]; ok {
			return SessionsFile{}, sessionFileError(path, fmt.Sprintf("sessions[%d].id_hash is a duplicate", i))
		}
		seen[record.IDHash] = struct{}{}
		record.UserAgent = truncateUserAgent(record.UserAgent)
	}
	return file, nil
}

// WriteSessionsFile writes path atomically with mode 0600. The caller passes
// hashes only; every record is validated before anything is written.
func WriteSessionsFile(path string, file SessionsFile) error {
	file.Version = SessionsFileVersion
	if file.Sessions == nil {
		file.Sessions = []SessionRecord{}
	}
	for i := range file.Sessions {
		record := &file.Sessions[i]
		if !isHexSHA256(record.IDHash) {
			return sessionFileError(path, fmt.Sprintf("sessions[%d].id_hash must be 64 lowercase hex characters", i))
		}
		if !isHexSHA256(record.CSRFHash) {
			return sessionFileError(path, fmt.Sprintf("sessions[%d].csrf_hash must be 64 lowercase hex characters", i))
		}
		record.UserAgent = truncateUserAgent(record.UserAgent)
	}
	data, err := json.Marshal(file)
	if err != nil {
		return sessionFileError(path, "cannot encode the records")
	}
	if err := AtomicWriteMode(path, data, SessionsFileMode); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrSessionFile, path, err)
	}
	return nil
}

func sessionFileError(path, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrSessionFile, path, reason)
}

// truncateUserAgent keeps at most MaxUserAgentChars characters (not bytes).
func truncateUserAgent(value string) string {
	if utf8.RuneCountInString(value) <= MaxUserAgentChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:MaxUserAgentChars])
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

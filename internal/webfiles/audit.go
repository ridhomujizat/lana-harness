package webfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// AuditDirName is the directory under $LANA_HOME that holds the audit file.
	AuditDirName = "logs"
	// AuditFileName is the audit file name under the logs directory.
	AuditFileName = "audit.jsonl"
	// AuditFileMode is the only accepted mode for audit.jsonl.
	AuditFileMode os.FileMode = 0o600
	// AuditDirMode is the mode of a logs directory the audit writer creates.
	AuditDirMode os.FileMode = 0o700
	// AuditDefaultLimit is the page size of API-WEB-030 when limit is absent.
	AuditDefaultLimit = 50
	// AuditMaxLimit is the largest accepted page size of API-WEB-030.
	AuditMaxLimit = 100
)

// AuditEntry is one line of $LANA_HOME/logs/audit.jsonl. It holds key paths
// and targets only: never a config value, a secret value, or file content.
type AuditEntry struct {
	Time        int64    `json:"time"`
	Kind        string   `json:"kind"`
	Target      string   `json:"target"`
	ChangedKeys []string `json:"changed_keys"`
}

// AuditPage is the GET /v1/audit response body: entries newest first and the
// cursor for the next page, or nil when no older entry exists.
type AuditPage struct {
	Entries    []AuditEntry `json:"entries"`
	NextBefore *int64       `json:"next_before"`
}

// AuditPath returns the audit file path under home.
func AuditPath(home string) string {
	return filepath.Join(home, AuditDirName, AuditFileName)
}

// AppendAudit appends one JSON line to the audit file. The logs directory is
// created with mode 0700 when missing and the file is mode 0600. A nil
// ChangedKeys is written as an empty array, never null.
func AppendAudit(path string, entry AuditEntry) error {
	if entry.ChangedKeys == nil {
		entry.ChangedKeys = []string{}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("webfiles: encode audit entry for %s: %w", path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, AuditDirMode); err != nil {
		return fmt.Errorf("webfiles: create directory for %s: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, AuditFileMode)
	if err != nil {
		return fmt.Errorf("webfiles: open %s: %w", path, err)
	}
	// O_CREATE keeps the mode of an existing file, so set it on every append.
	// The audit file must never be readable by any other user.
	if err := file.Chmod(AuditFileMode); err != nil {
		_ = file.Close()
		return fmt.Errorf("webfiles: set mode on %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("webfiles: append %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("webfiles: sync %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("webfiles: close %s: %w", path, err)
	}
	return nil
}

// ReadAudit returns audit entries newest first. A missing file is an empty
// page. before, when positive, keeps only entries older than that Unix ms
// time. limit is clamped to the API-WEB-030 range. A malformed line is
// skipped so a crash-truncated append does not hide every other entry.
func ReadAudit(path string, before int64, limit int) (AuditPage, error) {
	if limit <= 0 {
		limit = AuditDefaultLimit
	}
	if limit > AuditMaxLimit {
		limit = AuditMaxLimit
	}
	page := AuditPage{Entries: []AuditEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return page, nil
		}
		return page, fmt.Errorf("webfiles: read %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if before > 0 && entry.Time >= before {
			continue
		}
		if len(page.Entries) == limit {
			next := page.Entries[len(page.Entries)-1].Time
			page.NextBefore = &next
			break
		}
		if entry.ChangedKeys == nil {
			entry.ChangedKeys = []string{}
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

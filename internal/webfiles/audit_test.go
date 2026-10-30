package webfiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuditCoversACWEB059 covers AC-WEB-059 at the file layer: each save
// appends one JSON line with time, kind, target, and key paths, the file is
// mode 0600, and the logs directory is mode 0700. No value is written.
func TestAuditCoversACWEB059(t *testing.T) {
	home := t.TempDir()
	path := AuditPath(home)
	if path != filepath.Join(home, "logs", "audit.jsonl") {
		t.Fatalf("AuditPath = %q", path)
	}

	const first = int64(1_700_000_000_001)
	entries := []AuditEntry{
		{
			Time:        first,
			Kind:        "config.save",
			Target:      "config.toml",
			ChangedKeys: []string{"agents.coder.model", "agents.coder.permission"},
		},
		{
			Time:        first + 1,
			Kind:        "secret.set",
			Target:      "OPENAI_API_KEY",
			ChangedKeys: nil,
		},
		{
			Time:        first + 2,
			Kind:        "web.save",
			Target:      "config.toml",
			ChangedKeys: []string{"web.addr"},
		},
	}
	for _, entry := range entries {
		if err := AppendAudit(path, entry); err != nil {
			t.Fatalf("append %s: %v", entry.Kind, err)
		}
	}

	assertFileMode(t, path, AuditFileMode)
	dirInfo, err := os.Stat(filepath.Join(home, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != AuditDirMode {
		t.Fatalf("logs directory mode = %04o, want %04o", got, AuditDirMode)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != len(entries) {
		t.Fatalf("audit lines = %d, want %d:\n%s", len(lines), len(entries), data)
	}
	var line struct {
		Time        int64    `json:"time"`
		Kind        string   `json:"kind"`
		Target      string   `json:"target"`
		ChangedKeys []string `json:"changed_keys"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("line 0 %q: %v", lines[0], err)
	}
	if line.Time != first || line.Kind != "config.save" || line.Target != "config.toml" {
		t.Fatalf("line 0 = %+v", line)
	}
	if strings.Join(line.ChangedKeys, ",") != "agents.coder.model,agents.coder.permission" {
		t.Fatalf("line 0 changed_keys = %v", line.ChangedKeys)
	}
	if !strings.Contains(lines[1], `"changed_keys":[]`) {
		t.Fatalf("nil changed_keys is not an empty array: %s", lines[1])
	}
	for _, value := range []string{"coder-two", "sk-secret", "mock://remote"} {
		if strings.Contains(string(data), value) {
			t.Fatalf("audit file leaked a value %q:\n%s", value, data)
		}
	}
}

// TestAuditReadPaging covers the before and limit behaviour of API-WEB-030:
// newest first, an older-than cursor, and next_before only when a page has an
// older entry.
func TestAuditReadPaging(t *testing.T) {
	path := AuditPath(t.TempDir())
	const base = int64(1_700_000_000_000)
	for i := int64(0); i < 4; i++ {
		entry := AuditEntry{Time: base + i, Kind: "config.save", Target: "config.toml", ChangedKeys: []string{}}
		if err := AppendAudit(path, entry); err != nil {
			t.Fatal(err)
		}
	}

	page, err := ReadAudit(path, 0, AuditMaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 4 || page.NextBefore != nil {
		t.Fatalf("full page = %d entries, next %v", len(page.Entries), page.NextBefore)
	}
	for i, entry := range page.Entries {
		if want := base + int64(3-i); entry.Time != want {
			t.Fatalf("entry %d time = %d, want %d (newest first)", i, entry.Time, want)
		}
	}

	page, err = ReadAudit(path, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.NextBefore == nil || *page.NextBefore != base+2 {
		t.Fatalf("first page = %d entries, next %v", len(page.Entries), page.NextBefore)
	}

	page, err = ReadAudit(path, *page.NextBefore, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Time != base+1 || page.Entries[1].Time != base {
		t.Fatalf("older page = %+v", page.Entries)
	}
	if page.NextBefore != nil {
		t.Fatalf("last page has next_before %v", *page.NextBefore)
	}

	// before is exclusive and clamps to the time given.
	page, err = ReadAudit(path, base+2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Time != base+1 {
		t.Fatalf("before page = %+v", page.Entries)
	}
}

// TestAuditReadMissingFile covers a fresh home: the audit page is empty, not an
// error.
func TestAuditReadMissingFile(t *testing.T) {
	page, err := ReadAudit(AuditPath(t.TempDir()), 0, AuditDefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 || page.NextBefore != nil {
		t.Fatalf("missing file page = %+v", page)
	}
	if page.Entries == nil {
		t.Fatal("entries is nil, want an empty array")
	}
}

// TestAuditSkipsMalformedLine covers a crash-truncated last line: the valid
// lines still list.
func TestAuditSkipsMalformedLine(t *testing.T) {
	path := AuditPath(t.TempDir())
	if err := AppendAudit(path, AuditEntry{Time: 1, Kind: "config.save", Target: "config.toml"}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, AuditFileMode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"time":2,"kind":`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := ReadAudit(path, 0, AuditDefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Time != 1 {
		t.Fatalf("page = %+v", page.Entries)
	}
}

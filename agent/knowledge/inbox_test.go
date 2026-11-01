package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AC-KNU-003 (REQ-KNU-010, -011): an inbox PDF becomes a source and leaves the
// inbox; a duplicate is dropped; a .docx stays and is listed; a file modified 1 s
// ago waits.
func TestInboxScanIngestsSources(t *testing.T) {
	env := newVaultEnv(t, &fakeProvider{text: "[]"})
	now := env.clock.now()
	old := now.Add(-time.Minute)

	pdf := []byte("%PDF-1.4\nsource text\n")
	env.writeInboxFile("Rapat.pdf", pdf, old)
	env.writeInboxFile("notes.docx", []byte("docx bytes"), old)
	env.writeInboxFile("fresh.md", []byte("# fresh"), now.Add(-time.Second))

	ctx := context.Background()
	if err := env.svc.scanInbox(ctx); err != nil {
		t.Fatal(err)
	}

	if fileExists(filepath.Join(env.vaultDir, "inbox", "Rapat.pdf")) {
		t.Fatal("the ingested PDF still sits in inbox/")
	}
	sources := listFiles(t, filepath.Join(env.vaultDir, "sources"))
	if len(sources) != 1 || !strings.HasSuffix(sources[0], ".pdf") {
		t.Fatalf("sources = %v, want one PDF", sources)
	}
	if !fileExists(filepath.Join(env.vaultDir, "inbox", "notes.docx")) {
		t.Fatal("the unsupported .docx was removed")
	}
	if !fileExists(filepath.Join(env.vaultDir, "inbox", "fresh.md")) {
		t.Fatal("a file modified 1 s ago was ingested")
	}

	status, err := env.svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(status.InboxUnsupported, "inbox/notes.docx") {
		t.Fatalf("inbox_unsupported = %v, want inbox/notes.docx", status.InboxUnsupported)
	}
	if status.Counts.Sources != 1 {
		t.Fatalf("source count = %d, want 1", status.Counts.Sources)
	}

	// A duplicate is dropped and stored only once.
	env.writeInboxFile("Rapat copy.pdf", pdf, old)
	if err := env.svc.scanInbox(ctx); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(env.vaultDir, "inbox", "Rapat copy.pdf")) {
		t.Fatal("the duplicate inbox file was not removed")
	}
	if sources := listFiles(t, filepath.Join(env.vaultDir, "sources")); len(sources) != 1 {
		t.Fatalf("the duplicate created a source: %v", sources)
	}
}

// writeInboxFile writes one inbox file and sets its mtime.
func (e *vaultEnv) writeInboxFile(name string, data []byte, mtime time.Time) {
	e.t.Helper()
	path := filepath.Join(e.vaultDir, "inbox", name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		e.t.Fatal(err)
	}
}

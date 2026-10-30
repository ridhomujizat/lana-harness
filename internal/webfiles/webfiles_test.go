package webfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteKeepsModeAndReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("old content that is longer"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %04o, want 0640", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want %q", data, "new")
	}
	assertNoTempFiles(t, dir)
}

func TestAtomicWriteNewFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := AtomicWrite(path, []byte("created")); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want 0644", got)
	}
	assertNoTempFiles(t, dir)
}

func TestAtomicWriteFailureRemovesTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(target, []byte("data")); err == nil {
		t.Fatal("AtomicWrite over a directory succeeded")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("target directory disappeared: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target is no longer a directory")
	}
	assertNoTempFiles(t, dir)
}

func TestAtomicWriteMissingDirectoryLeavesNoTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing", "config.toml")
	if err := AtomicWrite(target, []byte("data")); err == nil {
		t.Fatal("AtomicWrite into a missing directory succeeded")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target exists after failure: %v", err)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
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

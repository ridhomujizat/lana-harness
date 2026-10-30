package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := checkWorkspace(dir); err != nil {
		t.Fatalf("existing directory: %v", err)
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkspace(file); err == nil || !strings.Contains(err.Error(), "workspace is not a directory:") {
		t.Fatalf("regular file error=%v", err)
	}

	missing := filepath.Join(dir, "missing")
	err := checkWorkspace(missing)
	if err == nil || !strings.Contains(err.Error(), "workspace does not exist:") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing path error=%v", err)
	}
}

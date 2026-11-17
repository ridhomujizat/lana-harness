//go:build darwin || linux

package workbench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFiles(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "lana-open-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// macOS temp dirs live under /private, so both sides are resolved.
	want := evalSymlinks(t, f.Name())
	for _, p := range openFiles(os.Getpid()) {
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved == want {
			return
		}
	}
	t.Fatalf("openFiles(%d) did not include %q", os.Getpid(), want)
}

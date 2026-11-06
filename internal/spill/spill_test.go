package spill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveWritesFullText(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	text := strings.Repeat("hello ", 1000)

	path, err := store.Save("session-a", "bash", "call-1", text)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := SessionDir(root, "session-a")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("path=%q want dir %q", path, wantDir)
	}
	if filepath.Ext(path) != ".txt" || !strings.HasSuffix(path, "-bash.txt") {
		t.Fatalf("path=%q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != text {
		t.Fatalf("file holds %d bytes want %d", len(data), len(text))
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode=%o want 600", got)
	}
	dirInfo, err := os.Stat(wantDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode=%o want 700", got)
	}
}

func TestSaveUniquePerCall(t *testing.T) {
	store := Store{Root: t.TempDir()}
	first, err := store.Save("s", "bash", "call-1", "one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Save("s", "bash", "call-1", "two")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two saves returned the same path: %q", first)
	}
}

func TestSessionHashSeparatesSessions(t *testing.T) {
	if SessionHash("a") == SessionHash("b") {
		t.Fatal("different sessions share a hash")
	}
	first, again := SessionHash("a"), SessionHash("a")
	if first != again {
		t.Fatal("the hash is not stable")
	}
	if len(SessionHash("a")) != 16 {
		t.Fatalf("hash length=%d want 16", len(SessionHash("a")))
	}
}

func TestSanitizeTool(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"bash", "bash"},
		{"mcp__fs__read", "mcp__fs__read"},
		{"web-fetch", "web-fetch"},
		{"a/b\\c d", "a_b_c_d"},
		{"", "output"},
	} {
		if got := SanitizeTool(tc.in); got != tc.want {
			t.Fatalf("SanitizeTool(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

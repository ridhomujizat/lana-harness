package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppBundle(t *testing.T) {
	cases := []struct {
		exe    string
		bundle string
		ok     bool
	}{
		{"/Applications/Lana.app/Contents/MacOS/lana", "/Applications/Lana.app", true},
		{"/Users/me/dist/Lana.app/Contents/MacOS/LanaMenu", "/Users/me/dist/Lana.app", true},
		{"/Users/me/.local/bin/lana", "", false},
		{"/Applications/Lana.app/Contents/Resources/lana", "", false},
		{"/Applications/Lana/Contents/MacOS/lana", "", false},
		{"/Applications/Lana.app/Contents/MacOS/bin/lana", "", false},
		{"/.app/Contents/MacOS/lana", "", false},
	}
	for _, tc := range cases {
		bundle, ok := AppBundle(tc.exe)
		if bundle != tc.bundle || ok != tc.ok {
			t.Errorf("AppBundle(%q) = %q, %v; want %q, %v", tc.exe, bundle, ok, tc.bundle, tc.ok)
		}
	}
}

// fakeReseal records the bundles codesign would sign.
func fakeReseal(t *testing.T, err error) *[]string {
	t.Helper()
	var sealed []string
	prev := resealCommand
	resealCommand = func(_ context.Context, bundle string) ([]byte, error) {
		sealed = append(sealed, bundle)
		return []byte("codesign output"), err
	}
	t.Cleanup(func() { resealCommand = prev })
	return &sealed
}

func writeBundleFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// TestReplaceExecutableResealsBundle: a binary inside Lana.app gets the
// bundle signed again; a plain binary does not.
func TestReplaceExecutableResealsBundle(t *testing.T) {
	sealed := fakeReseal(t, nil)
	dir := t.TempDir()
	exe := filepath.Join(dir, "Lana.app", "Contents", "MacOS", "lana")
	writeBundleFile(t, exe, "old", 0o755)
	writeBundleFile(t, filepath.Join(dir, "new"), "new", 0o644)
	if err := ReplaceExecutable(filepath.Join(dir, "new"), exe); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "bin", "lana")
	writeBundleFile(t, plain, "old", 0o755)
	writeBundleFile(t, filepath.Join(dir, "new2"), "new", 0o644)
	if err := ReplaceExecutable(filepath.Join(dir, "new2"), plain); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Fatalf("exe = %q", got)
	}
	want := []string{}
	if runtime.GOOS == "darwin" {
		want = []string{filepath.Join(dir, "Lana.app")}
	}
	if fmt.Sprint(*sealed) != fmt.Sprint(want) {
		t.Fatalf("sealed = %v, want %v", *sealed, want)
	}
}

// TestResealAppBundleError: a codesign failure is reported with its output,
// and ReplaceExecutable still succeeds (the reseal is best effort).
func TestResealAppBundleError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("reseal runs only on darwin")
	}
	fakeReseal(t, errors.New("exit status 1"))
	dir := t.TempDir()
	exe := filepath.Join(dir, "Lana.app", "Contents", "MacOS", "lana")
	writeBundleFile(t, exe, "old", 0o755)
	err := ResealAppBundle(exe)
	if err == nil || !strings.Contains(err.Error(), "codesign output") {
		t.Fatalf("err = %v", err)
	}
	writeBundleFile(t, filepath.Join(dir, "new"), "new", 0o644)
	if err := ReplaceExecutable(filepath.Join(dir, "new"), exe); err != nil {
		t.Fatalf("ReplaceExecutable: %v", err)
	}
}

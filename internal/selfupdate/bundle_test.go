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

// Real-looking `codesign -dvv` output: the bundle id and three certificate
// authorities, the first of which is the signing identity.
const signedDisplay = `Executable=/Applications/Lana.app/Contents/MacOS/lana
Identifier=dev.lana.menubar
Format=app bundle with Mach-O thin (arm64)
CodeDirectory v=20400 size=12345 flags=0x0(none) hashes=380+3 location=embedded
Signature size=4800
Authority=Apple Development: Name (7YK57ZPV98)
Authority=Apple Worldwide Developer Relations Certification Authority
Authority=Apple Root CA
TeamIdentifier=7YK57ZPV98
`

// fakeCodesign records the codesign invocations and answers them with fn.
func fakeCodesign(t *testing.T, fn func(args ...string) ([]byte, error)) *[][]string {
	t.Helper()
	var calls [][]string
	prev := codesignCommand
	codesignCommand = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return fn(args...)
	}
	t.Cleanup(func() { codesignCommand = prev })
	return &calls
}

// signs returns the recorded sign invocations, dropping the signature read.
func signs(calls [][]string) [][]string {
	var out [][]string
	for _, call := range calls {
		if len(call) > 0 && call[0] == "-dvv" {
			continue
		}
		out = append(out, call)
	}
	return out
}

// displayResponder answers the signature read with display and every sign run
// with "codesign output" and err.
func displayResponder(display string, err error) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-dvv" {
			return []byte(display), nil
		}
		return []byte("codesign output"), err
	}
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
	calls := fakeCodesign(t, displayResponder("Identifier=dev.lana.menubar\n", nil))
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
	var want [][]string
	if runtime.GOOS == "darwin" {
		want = [][]string{{"--force", "--sign", "-", filepath.Join(dir, "Lana.app")}}
	}
	if got := signs(*calls); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sign calls = %v, want %v", got, want)
	}
}

// TestResealBundleAdHoc: a bundle without an Authority line is signed once,
// ad-hoc.
func TestResealBundleAdHoc(t *testing.T) {
	calls := fakeCodesign(t, displayResponder("Identifier=dev.lana.menubar\n", nil))
	bundle := "/Applications/Lana.app"
	exe := filepath.Join(bundle, "Contents", "MacOS", "lana")
	if err := resealBundle(exe, bundle); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"--force", "--sign", "-", bundle}}
	if got := signs(*calls); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sign calls = %v, want %v", got, want)
	}
}

// TestResealBundleKeepsIdentity: a signed bundle keeps its identity on the
// replaced nested binary and the bundle.
func TestResealBundleKeepsIdentity(t *testing.T) {
	display := "Identifier=dev.lana.menubar\nAuthority=Apple Development: Name (TEAMID)\n"
	calls := fakeCodesign(t, displayResponder(display, nil))
	bundle := "/Applications/Lana.app"
	exe := filepath.Join(bundle, "Contents", "MacOS", "lana")
	if err := resealBundle(exe, bundle); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"--force", "--sign", "Apple Development: Name (TEAMID)", "--identifier", "dev.lana.menubar.daemon", exe},
		{"--force", "--sign", "Apple Development: Name (TEAMID)", bundle},
	}
	if got := signs(*calls); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sign calls = %v, want %v", got, want)
	}
}

// TestResealBundleFallsBackToAdHoc: a failed identity sign falls back to an
// ad-hoc signature of the nested binary and the bundle without an error.
func TestResealBundleFallsBackToAdHoc(t *testing.T) {
	display := "Identifier=dev.lana.menubar\nAuthority=Apple Development: Name (TEAMID)\n"
	calls := fakeCodesign(t, func(args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "-dvv" {
			return []byte(display), nil
		}
		// args[2] is the signing identity; "-" is the ad-hoc fallback, which
		// succeeds while the real identity is gone.
		if args[2] == "-" {
			return []byte("codesign output"), nil
		}
		return []byte("codesign output"), errors.New("exit status 1")
	})
	bundle := "/Applications/Lana.app"
	exe := filepath.Join(bundle, "Contents", "MacOS", "lana")
	if err := resealBundle(exe, bundle); err != nil {
		t.Fatalf("err = %v", err)
	}
	want := [][]string{
		{"--force", "--sign", "Apple Development: Name (TEAMID)", "--identifier", "dev.lana.menubar.daemon", exe},
		{"--force", "--sign", "-", exe},
		{"--force", "--sign", "-", bundle},
	}
	if got := signs(*calls); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sign calls = %v, want %v", got, want)
	}
}

// TestParseSignature: the bundle id and the first Authority line are picked
// out of real-looking codesign output.
func TestParseSignature(t *testing.T) {
	identifier, authority := parseSignature([]byte(signedDisplay))
	if identifier != "dev.lana.menubar" {
		t.Errorf("identifier = %q, want %q", identifier, "dev.lana.menubar")
	}
	if authority != "Apple Development: Name (7YK57ZPV98)" {
		t.Errorf("authority = %q, want %q", authority, "Apple Development: Name (7YK57ZPV98)")
	}
}

// TestResealAppBundleError: a codesign failure is reported with its output,
// and ReplaceExecutable still succeeds (the reseal is best effort).
func TestResealAppBundleError(t *testing.T) {
	fakeCodesign(t, displayResponder("Identifier=dev.lana.menubar\n", errors.New("exit status 1")))
	dir := t.TempDir()
	bundle := filepath.Join(dir, "Lana.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "lana")
	writeBundleFile(t, exe, "old", 0o755)
	err := resealBundle(exe, bundle)
	if err == nil || !strings.Contains(err.Error(), "codesign output") {
		t.Fatalf("err = %v", err)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	writeBundleFile(t, filepath.Join(dir, "new"), "new", 0o644)
	if err := ReplaceExecutable(filepath.Join(dir, "new"), exe); err != nil {
		t.Fatalf("ReplaceExecutable: %v", err)
	}
}

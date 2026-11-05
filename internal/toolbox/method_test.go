package toolbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// otherOS returns an OS filter that does not match this machine.
func otherOS() string {
	if runtime.GOOS == "linux" {
		return "darwin"
	}
	return "linux"
}

// TestMethodFits checks the os/arch aliases and the requires programs
// (REQ-TB-009).
func TestMethodFits(t *testing.T) {
	dir := t.TempDir()
	writePathExec(t, dir, "brew")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	osAlias := "linux"
	if runtime.GOOS == "darwin" {
		osAlias = "macos"
	}
	archAlias := "x86_64"
	if runtime.GOARCH == "arm64" {
		archAlias = "aarch64"
	}

	m := Manifest{Install: []InstallMethod{
		{URL: "a", OS: otherOS()},
		{URL: "b", OS: osAlias, Arch: archAlias, Requires: []string{"brew"}},
		{URL: "c", Requires: []string{"definitely-not-a-real-binary-xyz"}},
		{URL: "d"},
	}}
	if ok, reason := m.MethodFits(0); ok || reason == "" {
		t.Fatalf("method 0 fits=%v reason=%q", ok, reason)
	}
	if ok, reason := m.MethodFits(1); !ok {
		t.Fatalf("method 1 fits=%v reason=%q", ok, reason)
	}
	if ok, reason := m.MethodFits(2); ok || !strings.Contains(reason, "definitely-not-a-real-binary-xyz") {
		t.Fatalf("method 2 fits=%v reason=%q", ok, reason)
	}
	if ok, reason := m.MethodFits(3); !ok {
		t.Fatalf("method 3 fits=%v reason=%q", ok, reason)
	}
	if ok, _ := m.MethodFits(9); ok {
		t.Fatal("an out-of-range method must not fit")
	}

	if auto, ok := m.AutoMethod(); !ok || auto != 1 {
		t.Fatalf("AutoMethod=%d ok=%v want 1", auto, ok)
	}
}

// TestInstallAutoMethod installs the first method that fits when method is -1
// (REQ-TB-009).
func TestInstallAutoMethod(t *testing.T) {
	payload := []byte(fakeBinary)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	svc := testService(t)
	addManifest(t, svc, fmt.Sprintf(`
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"
version_args = ["--version"]

[[install]]
label = "other os"
url = "https://example.com/faketool"
os = %q

[[install]]
label = "download"
url = %q
`, otherOS(), srv.URL+"/faketool"))

	job, err := svc.Install(context.Background(), "faketool", -1)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if job.Method != 1 {
		t.Fatalf("method=%d want 1", job.Method)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	assertBinary(t, svc, payload)
}

// TestInstallNoMethodFits reports ErrMethodMismatch when nothing fits
// (REQ-TB-009).
func TestInstallNoMethodFits(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, fmt.Sprintf(`
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
label = "other os"
url = "https://example.com/faketool.tar.gz"
os = %q
`, otherOS()))

	_, err := svc.Install(context.Background(), "faketool", -1)
	if !errors.Is(err, ErrMethodMismatch) || !strings.Contains(err.Error(), "no install method fits this machine") {
		t.Fatalf("err=%v want ErrMethodMismatch with a clear message", err)
	}
}

// TestInstallExplicitMismatch explains why an explicit method does not fit
// (REQ-TB-009).
func TestInstallExplicitMismatch(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, fmt.Sprintf(`
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
label = "other os"
url = "https://example.com/faketool.tar.gz"
os = %q
`, otherOS()))

	_, err := svc.Install(context.Background(), "faketool", 0)
	if !errors.Is(err, ErrMethodMismatch) || !strings.Contains(err.Error(), otherOS()) {
		t.Fatalf("err=%v want ErrMethodMismatch naming %s", err, otherOS())
	}
}

// writePathExec writes an executable script into dir.
func writePathExec(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

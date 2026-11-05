package toolbox

import (
	"context"
	"testing"
)

// detectManifest builds a manifest with the given min version.
func detectManifest(t *testing.T, min string) Manifest {
	t.Helper()
	manifest, issues := ParseManifest(`
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"
version_args = ["--version"]
min_version = "` + min + `"

[[install]]
command = "true"
`)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	return manifest
}

// TestResolvePrefersBinDir finds the package bin before PATH.
func TestResolvePrefersBinDir(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := detectManifest(t, "")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := store.Resolve(manifest); ok {
		t.Fatal("resolved a binary that does not exist")
	}
	writeScript(t, store, "faketool", "faketool", fakeBinary)
	path, ok := store.Resolve(manifest)
	if !ok || path != store.BinDir("faketool")+"/faketool" {
		t.Fatalf("resolve=%q ok=%v", path, ok)
	}
}

// TestCheckInstalled reads the version from the first line of stdout.
func TestCheckInstalled(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := detectManifest(t, "0.2.0")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	writeScript(t, store, "faketool", "faketool", fakeBinary)

	status := store.Check(context.Background(), manifest)
	if status.State != StateInstalled || status.Version != "0.2.2" {
		t.Fatalf("status=%+v", status)
	}
	if status.BinaryPath == "" || status.CheckedAt == 0 {
		t.Fatalf("status=%+v", status)
	}
}

// TestCheckTooOld flags a binary below the minimum version.
func TestCheckTooOld(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := detectManifest(t, "0.3.0")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	writeScript(t, store, "faketool", "faketool", fakeBinary)
	if status := store.Check(context.Background(), manifest); status.State != StateTooOld {
		t.Fatalf("status=%+v want too_old", status)
	}
}

// TestCheckMissing reports a missing binary.
func TestCheckMissing(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := detectManifest(t, "")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	if status := store.Check(context.Background(), manifest); status.State != StateMissing {
		t.Fatalf("status=%+v want missing", status)
	}
}

// TestCheckBroken reports a failing version command.
func TestCheckBroken(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := detectManifest(t, "")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	writeScript(t, store, "faketool", "faketool", "#!/bin/sh\nexit 1\n")
	status := store.Check(context.Background(), manifest)
	if status.State != StateBroken || status.Error == "" {
		t.Fatalf("status=%+v want broken", status)
	}
}

// TestCheckUnparseableVersion is broken when min_version is set but the version
// line holds no number.
func TestCheckUnparseableVersion(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := detectManifest(t, "1.0.0")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	writeScript(t, store, "faketool", "faketool", "#!/bin/sh\necho no-version-here\n")
	if status := store.Check(context.Background(), manifest); status.State != StateBroken {
		t.Fatalf("status=%+v want broken", status)
	}
}

// TestCompareVersions checks the dotted numeric compare, ignoring non-numeric
// parts and missing segments.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.2", "0.2.2", 0},
		{"0.2.2", "0.2.0", 1},
		{"0.2.0", "0.2.2", -1},
		{"1.0", "1.0.0", 0},
		{"v0.2.2", "0.2.1", 1},
		{"1.2.3-rc1", "1.2.3", 0},
		{"2", "10", -1},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareVersions(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

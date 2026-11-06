package toolbox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// storeManifest parses a small, valid manifest.
func storeManifest(t *testing.T, id string) Manifest {
	t.Helper()
	manifest, issues := ParseManifest(`
id = "` + id + `"
name = "` + id + `"

[detect]
binary = "faketool"

[[install]]
command = "true"
`)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	return manifest
}

// TestStoreRoundTrip saves and reads a manifest and lists it.
func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	manifest := storeManifest(t, "faketool")

	if _, err := store.Get("faketool"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing err=%v want ErrNotFound", err)
	}
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Get("faketool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(manifest, got) {
		t.Fatalf("got=%+v want=%+v", got, manifest)
	}

	stored, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 1 || stored[0].ID != "faketool" || stored[0].Problem != "" {
		t.Fatalf("stored=%+v", stored)
	}
	if stored[0].UpdatedAt == 0 {
		t.Fatalf("UpdatedAt=%d want a nonzero mod time", stored[0].UpdatedAt)
	}
}

// TestStoreSaveCreatesDirs checks that Save creates bin/, data/ and logs/.
func TestStoreSaveCreatesDirs(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := store.Save(storeManifest(t, "faketool")); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, dir := range []string{store.BinDir("faketool"), store.DataDir("faketool"), store.LogDir("faketool")} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("dir %s: %v", dir, err)
		}
	}
	if store.Dir("faketool") != filepath.Join(root, "faketool") {
		t.Fatalf("dir=%s", store.Dir("faketool"))
	}
}

// TestStoreDelete removes the whole package home.
func TestStoreDelete(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(storeManifest(t, "faketool")); err != nil {
		t.Fatalf("save: %v", err)
	}
	writeScript(t, store, "faketool", "faketool", fakeBinary)
	if err := store.Delete("faketool"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(store.Dir("faketool")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dir still present: %v", err)
	}
	if err := store.Delete("faketool"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err=%v want ErrNotFound", err)
	}
}

// TestStoreBrokenFile returns a Problem entry instead of failing the list.
func TestStoreBrokenFile(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(storeManifest(t, "good")); err != nil {
		t.Fatalf("save: %v", err)
	}
	brokenDir := store.Dir("broken")
	if err := os.MkdirAll(brokenDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(store.ManifestPath("broken"), []byte("id = "), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	stored, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("len=%d want 2", len(stored))
	}
	byID := map[string]Stored{}
	for _, item := range stored {
		byID[item.ID] = item
	}
	if byID["good"].Problem != "" {
		t.Fatalf("good problem=%q", byID["good"].Problem)
	}
	if byID["broken"].Problem == "" {
		t.Fatalf("broken has no problem: %+v", byID["broken"])
	}
	if _, err := store.Get("broken"); !isInvalidManifest(err) {
		t.Fatalf("get broken err=%v want *InvalidManifestError", err)
	}
}

// TestStoreMissingManifest marks a directory without tool.toml.
func TestStoreMissingManifest(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.MkdirAll(store.Dir("empty"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stored, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 1 || stored[0].Problem == "" {
		t.Fatalf("stored=%+v", stored)
	}
	if _, err := store.Get("empty"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get err=%v want ErrNotFound", err)
	}
}

// isInvalidManifest reports whether err is an *InvalidManifestError.
func isInvalidManifest(err error) bool {
	var invalid *InvalidManifestError
	return errors.As(err, &invalid)
}

package agenthome

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeAgentUser(t *testing.T, lanaHome, agent, text string) string {
	t.Helper()
	path := filepath.Join(lanaHome, "agents", agent, "USER.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// AC-HOME-007: with several candidates and no shared file, the default agent's
// USER.md is copied and the other candidate is left alone.
func TestMigrateUserAC007CopiesDefaultCandidate(t *testing.T) {
	lanaHome := t.TempDir()
	helperData := []byte("Name: Ridho\n§\nWork: engineer")
	workerData := []byte("Name: Worker\n")
	helper := writeAgentUser(t, lanaHome, "helper", string(helperData))
	worker := writeAgentUser(t, lanaHome, "worker", string(workerData))

	got, err := MigrateUser(lanaHome, []string{"worker", "helper"}, "helper")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "helper" {
		t.Fatalf("source = %q, want helper", got.Source)
	}
	if len(got.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none", got.Skipped)
	}

	shared := SharedUser(lanaHome)
	if data := readFile(t, shared); !bytes.Equal(data, helperData) {
		t.Fatalf("shared file = %q, want %q", data, helperData)
	}
	if data := readFile(t, helper); !bytes.Equal(data, helperData) {
		t.Fatalf("helper file changed: %q", data)
	}
	if data := readFile(t, worker); !bytes.Equal(data, workerData) {
		t.Fatalf("worker file changed: %q", data)
	}
	info, err := os.Stat(shared)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("shared mode = %v, want 0600", info.Mode().Perm())
	}
}

// AC-HOME-008: with several candidates, none the default, and no shared file,
// nothing is copied and both candidates are reported for a warning.
func TestMigrateUserAC008ReportsSeveralCandidates(t *testing.T) {
	lanaHome := t.TempDir()
	helperData := []byte("Name: Helper\n")
	workerData := []byte("Name: Worker\n")
	helper := writeAgentUser(t, lanaHome, "helper", string(helperData))
	worker := writeAgentUser(t, lanaHome, "worker", string(workerData))

	got, err := MigrateUser(lanaHome, []string{"helper", "worker"}, "other")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "" {
		t.Fatalf("source = %q, want none", got.Source)
	}
	if want := []string{"helper", "worker"}; !slices.Equal(got.Skipped, want) {
		t.Fatalf("skipped = %v, want %v", got.Skipped, want)
	}
	warning := got.Warning(lanaHome)
	for _, name := range []string{"helper", "worker"} {
		if !strings.Contains(warning, name) {
			t.Fatalf("warning %q does not name %s", warning, name)
		}
	}
	if !strings.Contains(warning, SharedUser(lanaHome)) {
		t.Fatalf("warning %q does not name the shared file", warning)
	}

	if _, err := os.Stat(SharedUser(lanaHome)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared file exists or stat failed: %v", err)
	}
	if data := readFile(t, helper); !bytes.Equal(data, helperData) {
		t.Fatalf("helper file changed: %q", data)
	}
	if data := readFile(t, worker); !bytes.Equal(data, workerData) {
		t.Fatalf("worker file changed: %q", data)
	}
}

// AC-HOME-009: once the shared file exists, repeated runtime opens leave it
// unchanged.
func TestMigrateUserAC009KeepsExistingSharedFile(t *testing.T) {
	lanaHome := t.TempDir()
	sharedData := []byte("Name: Ridho\n§\nWork: engineer\n")
	shared := SharedUser(lanaHome)
	if err := os.WriteFile(shared, sharedData, 0o600); err != nil {
		t.Fatal(err)
	}
	helperData := []byte("Name: Older\n")
	helper := writeAgentUser(t, lanaHome, "helper", string(helperData))

	for i := 0; i < 2; i++ {
		got, err := MigrateUser(lanaHome, []string{"helper"}, "helper")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if got.Source != "" || len(got.Skipped) != 0 {
			t.Fatalf("run %d result = %#v, want no migration", i, got)
		}
		if data := readFile(t, shared); !bytes.Equal(data, sharedData) {
			t.Fatalf("run %d changed shared file: %q", i, data)
		}
	}
	if data := readFile(t, helper); !bytes.Equal(data, helperData) {
		t.Fatalf("helper file changed: %q", data)
	}
}

// A single candidate is copied even when there is no default agent or the
// default names another agent. Candidates without entries are ignored.
func TestMigrateUserSingleCandidate(t *testing.T) {
	lanaHome := t.TempDir()
	helperData := []byte("Name: Ridho\n")
	helper := writeAgentUser(t, lanaHome, "helper", string(helperData))
	writeAgentUser(t, lanaHome, "worker", "  \n§\n  \n")

	got, err := MigrateUser(lanaHome, []string{"worker", "helper"}, "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "helper" || len(got.Skipped) != 0 {
		t.Fatalf("result = %#v, want helper copied", got)
	}
	if data := readFile(t, helper); !bytes.Equal(data, helperData) {
		t.Fatalf("helper file changed: %q", data)
	}
	if data := readFile(t, SharedUser(lanaHome)); !bytes.Equal(data, helperData) {
		t.Fatalf("shared file = %q, want %q", data, helperData)
	}
}

func TestMigrateUserNoCandidates(t *testing.T) {
	lanaHome := t.TempDir()
	writeAgentUser(t, lanaHome, "helper", "Name: Ridho\n")

	got, err := MigrateUser(lanaHome, nil, "helper")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "" || len(got.Skipped) != 0 {
		t.Fatalf("result = %#v, want no migration", got)
	}
	if _, err := os.Stat(SharedUser(lanaHome)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared file exists or stat failed: %v", err)
	}
}

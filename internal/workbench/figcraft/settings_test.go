package figcraft

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeExecutable writes an executable file and returns its path.
func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSettingsStoreDefaultsOnMissingFile(t *testing.T) {
	store := NewSettingsStore(filepath.Join(t.TempDir(), "missing.json"))
	got := store.Get()
	if got.CLIPath != "" || !got.Live || got.LivePorts == nil || len(got.LivePorts) != 0 {
		t.Fatalf("defaults = %+v", got)
	}
}

func TestSettingsStoreDefaultsOnCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := NewSettingsStore(path).Get()
	want := DefaultSettings()
	if got.CLIPath != want.CLIPath || got.Live != want.Live || len(got.LivePorts) != len(want.LivePorts) {
		t.Fatalf("corrupt file = %+v, want %+v", got, want)
	}
}

func TestSettingsStoreMissingLiveKeyIsTrue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"cli_path":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewSettingsStore(path).Get(); !got.Live {
		t.Fatalf("live = %v, want true", got.Live)
	}

	// An explicit false is kept.
	if err := os.WriteFile(path, []byte(`{"live":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewSettingsStore(path).Get(); got.Live {
		t.Fatalf("live = %v, want false", got.Live)
	}
}

func TestSettingsStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cli := writeExecutable(t, filepath.Join(t.TempDir(), "figcraft-cli"))
	store := NewSettingsStore(path)

	stored, err := store.Put(Settings{CLIPath: cli, Live: false, LivePorts: []int{47810, 47811}})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if stored.CLIPath != cli || stored.Live || len(stored.LivePorts) != 2 {
		t.Fatalf("stored = %+v", stored)
	}

	// A fresh store reads the file back.
	got := NewSettingsStore(path).Get()
	if got.CLIPath != cli || got.Live || len(got.LivePorts) != 2 || got.LivePorts[0] != 47810 || got.LivePorts[1] != 47811 {
		t.Fatalf("round trip = %+v", got)
	}

	// The same store keeps the value in its cache.
	if cached := store.Get(); cached.CLIPath != cli || len(cached.LivePorts) != 2 {
		t.Fatalf("cached = %+v", cached)
	}
}

func TestSettingsStoreFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := NewSettingsStore(path).Put(Settings{Live: true, LivePorts: []int{}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

func TestSettingsStoreInvalidCLIPath(t *testing.T) {
	dir := t.TempDir()
	store := NewSettingsStore(filepath.Join(dir, "settings.json"))

	relative := "figcraft-cli"
	missing := filepath.Join(dir, "nope")
	directory := dir

	for name, cli := range map[string]string{
		"relative":  relative,
		"missing":   missing,
		"directory": directory,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Put(Settings{CLIPath: cli}); !errors.Is(err, ErrInvalidCLIPath) {
				t.Fatalf("err = %v, want ErrInvalidCLIPath", err)
			}
		})
	}
}

func TestSettingsStoreInvalidPorts(t *testing.T) {
	dir := t.TempDir()
	store := NewSettingsStore(filepath.Join(dir, "settings.json"))

	tooMany := make([]int, maxLivePorts+1)
	for i := range tooMany {
		tooMany[i] = 47810
	}

	for name, ports := range map[string][]int{
		"zero":     {0},
		"tooHigh":  {65536},
		"tooMany":  tooMany,
		"negative": {-1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Put(Settings{LivePorts: ports}); !errors.Is(err, ErrInvalidPorts) {
				t.Fatalf("err = %v, want ErrInvalidPorts", err)
			}
		})
	}

	// An empty list is valid and normalizes a nil one.
	stored, err := store.Put(Settings{Live: true})
	if err != nil {
		t.Fatalf("put empty: %v", err)
	}
	if stored.LivePorts == nil || len(stored.LivePorts) != 0 {
		t.Fatalf("live_ports = %+v, want an empty list", stored.LivePorts)
	}
}

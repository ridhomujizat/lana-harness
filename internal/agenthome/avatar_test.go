package agenthome

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// AC-PXA-005, REQ-PXA-012: the startup migration renames the spec 43 blob
// avatar avatar.svg to avatar.blob.svg once and leaves it alone on a second run.
func TestMigrateAvatarSVGAC005RenamesOnce(t *testing.T) {
	home := Paths(t.TempDir(), "coder")
	if err := os.MkdirAll(home.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := []byte(`<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="40"/></svg>`)
	if err := os.WriteFile(filepath.Join(home.Dir, "avatar.svg"), blob, 0o600); err != nil {
		t.Fatal(err)
	}

	renamed, err := MigrateAvatarSVG(home)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !renamed {
		t.Fatal("first run did not rename")
	}
	if got := readFile(t, home.AvatarBlob); string(got) != string(blob) {
		t.Fatalf("avatar.blob.svg = %q, want %q", got, blob)
	}
	if _, err := os.Stat(filepath.Join(home.Dir, "avatar.svg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("avatar.svg still exists: %v", err)
	}

	// A second run is a no-op: the backup exists already.
	renamed, err = MigrateAvatarSVG(home)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if renamed {
		t.Fatal("second run renamed a file")
	}
}

// REQ-PXA-012: a home without an avatar.svg and one that already has an
// avatar.blob.svg are both no-ops.
func TestMigrateAvatarSVGNoOp(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, home Home)
	}{
		{
			name:  "no avatar.svg",
			setup: func(t *testing.T, home Home) {},
		},
		{
			name: "backup already exists",
			setup: func(t *testing.T, home Home) {
				if err := os.WriteFile(filepath.Join(home.Dir, "avatar.svg"), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(home.AvatarBlob, []byte("backup"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := Paths(t.TempDir(), "coder")
			if err := os.MkdirAll(home.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, home)
			renamed, err := MigrateAvatarSVG(home)
			if err != nil {
				t.Fatal(err)
			}
			if renamed {
				t.Fatal("renamed a file")
			}
		})
	}
}

// REQ-PXA-012: the migration never reads or writes avatar.json.
func TestMigrateAvatarSVGKeepsSavedAvatar(t *testing.T) {
	home := Paths(t.TempDir(), "coder")
	if err := os.MkdirAll(home.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	saved := []byte(`{"kind":"parts"}`)
	if err := os.WriteFile(home.Avatar, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.Dir, "avatar.svg"), []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateAvatarSVG(home); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, home.Avatar); string(got) != string(saved) {
		t.Fatalf("avatar.json = %q, want %q", got, saved)
	}
}

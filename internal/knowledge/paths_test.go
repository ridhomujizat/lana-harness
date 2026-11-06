package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	tests := []struct {
		name string
		cfg  string
		want string
	}{
		{"empty", "", filepath.Join("/opt/lana", "knowledge")},
		{"absolute", "/srv/vault", "/srv/vault"},
		{"relative", "vault", filepath.Join("/opt/lana", "vault")},
		{"relative nested", "notes/vault", filepath.Join("/opt/lana", "notes", "vault")},
		{"home dir", "~", home},
		{"home child", "~/docs", filepath.Join(home, "docs")},
		{"cleaned", "/srv/a/../vault", "/srv/vault"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvePath("/opt/lana", tt.cfg); got != tt.want {
				t.Errorf("ResolvePath(%q) = %q, want %q", tt.cfg, got, tt.want)
			}
		})
	}
}

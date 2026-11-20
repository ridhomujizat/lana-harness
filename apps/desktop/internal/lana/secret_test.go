package lana

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSecret(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, SecretFileName)

	// A missing file is ErrNoSecret with a clear reason.
	_, err := ReadSecret(home)
	if !errors.Is(err, ErrNoSecret) {
		t.Fatalf("missing file error = %v, want ErrNoSecret", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("missing file error = %q, want the path", err)
	}

	// The daemon's own file parses.
	writeSecret(t, home, "127.0.0.1:18300", "s3cret")
	secret, err := ReadSecret(home)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if secret.Addr != "127.0.0.1:18300" || secret.Secret != "s3cret" {
		t.Fatalf("secret = %+v", secret)
	}

	// Bad JSON is ErrNoSecret too, and says so.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(home); !errors.Is(err, ErrNoSecret) || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("bad JSON error = %v, want ErrNoSecret with an invalid JSON reason", err)
	}

	// An empty field is unusable.
	if err := os.WriteFile(path, []byte(`{"addr":"","secret":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(home); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("empty fields error = %v, want ErrNoSecret", err)
	}
}

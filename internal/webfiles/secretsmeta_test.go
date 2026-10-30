package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestSecretsMetaCoversACWEB058 covers AC-WEB-058 at the file layer: a missing
// file is empty, set and remove write names and times only, and the file mode
// stays 0600.
func TestSecretsMetaCoversACWEB058(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SecretsMetaFileName)

	meta, err := ReadSecretsMeta(path)
	if err != nil {
		t.Fatalf("read missing file: %v", err)
	}
	if meta.Version != SecretsMetaVersion || len(meta.UpdatedMS) != 0 {
		t.Fatalf("missing file = %+v, want version 1 with no times", meta)
	}

	const first = int64(1_700_000_000_123)
	if err := SetSecretMeta(path, "OPENAI_API_KEY", first); err != nil {
		t.Fatalf("set: %v", err)
	}
	assertFileMode(t, path, SecretsMetaFileMode)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"updated_ms":{"OPENAI_API_KEY":1700000000123}}`
	if string(data) != want {
		t.Fatalf("content = %q, want %q", data, want)
	}

	if err := SetSecretMeta(path, "TELEGRAM_BOT_TOKEN", first+1); err != nil {
		t.Fatalf("set second: %v", err)
	}
	meta, err = ReadSecretsMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.UpdatedMS) != 2 || meta.UpdatedMS["OPENAI_API_KEY"] != first || meta.UpdatedMS["TELEGRAM_BOT_TOKEN"] != first+1 {
		t.Fatalf("times = %+v", meta.UpdatedMS)
	}

	if err := RemoveSecretMeta(path, "OPENAI_API_KEY"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	meta, err = ReadSecretsMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta.UpdatedMS["OPENAI_API_KEY"]; ok {
		t.Fatalf("OPENAI_API_KEY still has a time: %+v", meta.UpdatedMS)
	}
	if meta.UpdatedMS["TELEGRAM_BOT_TOKEN"] != first+1 {
		t.Fatalf("remove dropped another name: %+v", meta.UpdatedMS)
	}

	// Removing a name with no entry is not an error.
	if err := RemoveSecretMeta(path, "NOT_THERE"); err != nil {
		t.Fatalf("remove missing name: %v", err)
	}

	// Remove still writes the file when it did not exist.
	fresh := filepath.Join(t.TempDir(), SecretsMetaFileName)
	if err := RemoveSecretMeta(fresh, "OPENAI_API_KEY"); err != nil {
		t.Fatalf("remove from missing file: %v", err)
	}
	assertFileMode(t, fresh, SecretsMetaFileMode)
}

func TestSecretsMetaRejectsBadNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsMetaFileName)
	if err := SetSecretMeta(path, "1BAD", 1); err == nil {
		t.Fatal("set with an invalid name succeeded")
	}
	if err := RemoveSecretMeta(path, "A-B"); err == nil {
		t.Fatal("remove with an invalid name succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid name created the file: %v", err)
	}
}

func TestSecretsMetaBadFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{name: "invalid json", content: `{"version":1,"updated_ms":`},
		{name: "unsupported version", content: `{"version":2,"updated_ms":{}}`},
		{name: "wrong shape", content: `{"version":1,"updated_ms":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), SecretsMetaFileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadSecretsMeta(path); err == nil {
				t.Fatalf("read of %q succeeded", tc.content)
			}
			if err := SetSecretMeta(path, "OPENAI_API_KEY", 1); err == nil {
				t.Fatal("set over a bad file succeeded")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.content {
				t.Fatalf("bad file changed: %q", data)
			}
		})
	}
}

func TestSecretsMetaNormalizesMissingMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsMetaFileName)
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, err := ReadSecretsMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.UpdatedMS == nil {
		t.Fatal("updated_ms is nil, want an empty map")
	}
}

package webfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretEditorCoversACWEB026 covers AC-WEB-026: set, replace, remove,
// comments kept, quoting, invalid name, newline rejected, too long rejected,
// mode 0600, and creation with 0600 when the file is missing.
func TestSecretEditorCoversACWEB026(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	const original = "# keep me\n\nOTHER=one\n  # indented comment\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetSecret(path, "OPENAI_API_KEY", "sk-test"); err != nil {
		t.Fatalf("set: %v", err)
	}
	assertFileMode(t, path, SecretFileMode)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := original + "OPENAI_API_KEY=sk-test\n"
	if string(data) != want {
		t.Fatalf("after set:\n got %q\nwant %q", data, want)
	}

	if err := SetSecret(path, "OTHER", "two"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want = "# keep me\n\nOTHER=two\n  # indented comment\nOPENAI_API_KEY=sk-test\n"
	if string(data) != want {
		t.Fatalf("after replace:\n got %q\nwant %q", data, want)
	}

	if err := RemoveSecret(path, "OPENAI_API_KEY"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want = "# keep me\n\nOTHER=two\n  # indented comment\n"
	if string(data) != want {
		t.Fatalf("after remove:\n got %q\nwant %q", data, want)
	}
	if err := RemoveSecret(path, "OPENAI_API_KEY"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("remove missing error = %v, want ErrSecretNotFound", err)
	}
}

func TestSecretQuotingRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	values := []string{
		"plain",
		"has space",
		"hash#inside",
		"#leading",
		"'single'",
		" lead",
		"trail ",
		"a=b=c",
		`back\slash`,
	}
	for _, value := range values {
		if err := SetSecret(path, "KEY", value); err != nil {
			t.Fatalf("set %q: %v", value, err)
		}
		got, err := ReadSecrets(path)
		if err != nil {
			t.Fatal(err)
		}
		if got["KEY"] != value {
			t.Fatalf("value %q round-tripped as %q", value, got["KEY"])
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		needsQuotes := strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t'#")
		if needsQuotes && !strings.Contains(string(data), `KEY="`+value+`"`) {
			t.Fatalf("file %q does not quote %q", data, value)
		}
	}
}

func TestSecretValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{name: "valid key", key: "A_1", value: "v"},
		{name: "leading digit key", key: "1A", value: "v", wantErr: true},
		{name: "dash in key", key: "A-B", value: "v", wantErr: true},
		{name: "empty key", key: "", value: "v", wantErr: true},
		{name: "empty value", key: "A", value: "", wantErr: true},
		{name: "newline value", key: "A", value: "a\nb", wantErr: true},
		{name: "carriage return value", key: "A", value: "a\rb", wantErr: true},
		{name: "double quote value", key: "A", value: `a"b`, wantErr: true},
		{name: "too long", key: "A", value: strings.Repeat("x", MaxSecretValueBytes+1), wantErr: true},
		{name: "exactly max", key: "A", value: strings.Repeat("x", MaxSecretValueBytes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			err = SetSecret(path, tc.key, tc.value)
			if tc.wantErr {
				var validation *SecretValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("error = %v, want SecretValidationError", err)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal(readErr)
				}
				if string(after) != string(before) {
					t.Fatalf("invalid input changed the file: %q -> %q", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("set: %v", err)
			}
		})
	}
}

func TestSecretCreatesMissingFileWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secrets.env")
	if err := SetSecret(path, "OPENAI_API_KEY", "sk-test"); err != nil {
		t.Fatalf("set: %v", err)
	}
	assertFileMode(t, path, SecretFileMode)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "OPENAI_API_KEY=sk-test\n" {
		t.Fatalf("content = %q", data)
	}
}

func TestSecretVersionOfMissingFileIsEmptyHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	got, err := SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(nil)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("missing version = %q, want %q", got, want)
	}
	if err := SetSecret(path, "A", "v"); err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256([]byte("A=v\n"))
	if got, err = SecretsVersion(path); err != nil {
		t.Fatal(err)
	} else if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestSecretReplacePreservesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte("# c\r\nA=old\r\nB=keep\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetSecret(path, "A", "new"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# c\r\nA=new\r\nB=keep\r\n"; string(data) != want {
		t.Fatalf("content = %q, want %q", data, want)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %04o, want %04o", got, want)
	}
}

// TestSecretEditorTable is the table-driven form of the AC-WEB-026 cases:
// append, replace, remove, comments kept, quoting, and not-found removal.
func TestSecretEditorTable(t *testing.T) {
	cases := []struct {
		name    string
		initial string
		remove  bool
		key     string
		value   string
		want    string
		wantErr error
	}{
		{name: "create", key: "A", value: "v", want: "A=v\n"},
		{name: "replace keeps comments", initial: "# c\nA=old\nB=keep\n", key: "A", value: "new", want: "# c\nA=new\nB=keep\n"},
		{name: "remove keeps comments", initial: "# c\nA=1\nB=2\n", remove: true, key: "A", want: "# c\nB=2\n"},
		{name: "remove missing", initial: "A=1\n", remove: true, key: "B", want: "A=1\n", wantErr: ErrSecretNotFound},
		{name: "quote spaces", key: "A", value: "two words", want: "A=\"two words\"\n"},
		{name: "quote hash", key: "A", value: "a#b", want: "A=\"a#b\"\n"},
		{name: "quote single quotes", key: "A", value: "'x'", want: "A=\"'x'\"\n"},
		{name: "keep last line without newline", initial: "A=1", key: "A", value: "2", want: "A=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secrets.env")
			if tc.initial != "" {
				if err := os.WriteFile(path, []byte(tc.initial), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if tc.remove {
				err = RemoveSecret(path, tc.key)
			} else {
				err = SetSecret(path, tc.key, tc.value)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("editor: %v", err)
			}
			data, readErr := os.ReadFile(path)
			if tc.initial == "" && tc.wantErr != nil {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("file exists after failed create: %v", readErr)
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(data) != tc.want {
				t.Fatalf("content = %q, want %q", data, tc.want)
			}
			assertFileMode(t, path, SecretFileMode)
		})
	}
}

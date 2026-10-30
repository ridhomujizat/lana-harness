package webfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// SecretFileMode is the only accepted mode for secrets.env.
	SecretFileMode os.FileMode = 0o600
	// MaxSecretValueBytes is the largest value accepted by the line editor.
	MaxSecretValueBytes = 4096
)

var secretNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrSecretNotFound is returned when a name is removed but is not in the file.
var ErrSecretNotFound = errors.New("secret is not set")

// SecretValidationError is a rejected name or value. The field names the
// offending input so an HTTP handler can put it in the response details.
type SecretValidationError struct {
	Field   string
	Message string
}

func (e *SecretValidationError) Error() string { return e.Message }

// ValidateSecretName checks the env name rule: [A-Za-z_][A-Za-z0-9_]*.
func ValidateSecretName(name string) error {
	if !secretNameRE.MatchString(name) {
		return &SecretValidationError{Field: "name", Message: "name must match [A-Za-z_][A-Za-z0-9_]*"}
	}
	return nil
}

// ValidateSecretValue checks the value rule. Values cannot contain a newline
// and cannot contain a double quote: config/secrets.go has no escape syntax and
// strips one pair of outer quotes, so a value with a double quote cannot be
// represented without changing what the parser reads back (G-15).
func ValidateSecretValue(value string) error {
	if value == "" {
		return &SecretValidationError{Field: "value", Message: "value is required"}
	}
	if strings.ContainsAny(value, "\n\r") {
		return &SecretValidationError{Field: "value", Message: "value must not contain a newline"}
	}
	if strings.Contains(value, `"`) {
		return &SecretValidationError{Field: "value", Message: `value must not contain a double quote`}
	}
	if len(value) > MaxSecretValueBytes {
		return &SecretValidationError{Field: "value", Message: fmt.Sprintf("value is longer than %d bytes", MaxSecretValueBytes)}
	}
	return nil
}

// SecretsVersion returns the SHA-256 hex of the file bytes. A missing file has
// the same version as an empty file, the SHA-256 of zero bytes, so a caller can
// use the empty-file version to create the file with the expected-version check.
func SecretsVersion(path string) (string, error) {
	data, err := readSecretFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ReadSecrets parses path with the same grammar as config/secrets.go: skip
// blank and comment lines, split at the first '=', trim, and strip one pair of
// matching outer quotes. A missing file is an empty map.
func ReadSecrets(path string) (map[string]string, error) {
	data, err := readSecretFile(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(line[:eq])
		if name == "" {
			continue
		}
		value := strings.TrimSpace(line[eq+1:])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		values[name] = value
	}
	return values, nil
}

// SetSecret replaces every existing NAME line in place or appends a new line
// at the end. Comments, blank lines, and every other line are kept byte for
// byte. The file is created with mode 0600 and an existing file is chmodded to
// 0600.
func SetSecret(path, name, value string) error {
	if err := ValidateSecretName(name); err != nil {
		return err
	}
	if err := ValidateSecretValue(value); err != nil {
		return err
	}
	data, err := readSecretFile(path)
	if err != nil {
		return err
	}
	encoded := name + "=" + encodeSecretValue(value)
	var out strings.Builder
	replaced := false
	for _, line := range splitSecretLines(string(data)) {
		content, ending := splitLineEnding(line)
		if secretLineName(content) == name {
			out.WriteString(encoded)
			out.WriteString(ending)
			replaced = true
			continue
		}
		out.WriteString(line)
	}
	if !replaced {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		out.WriteString(encoded)
		out.WriteByte('\n')
	}
	return writeSecretFile(path, []byte(out.String()))
}

// RemoveSecret removes every NAME line. It returns ErrSecretNotFound when no
// line matches. Other lines are kept byte for byte.
func RemoveSecret(path, name string) error {
	if err := ValidateSecretName(name); err != nil {
		return err
	}
	data, err := readSecretFile(path)
	if err != nil {
		return err
	}
	var out strings.Builder
	removed := false
	for _, line := range splitSecretLines(string(data)) {
		content, _ := splitLineEnding(line)
		if secretLineName(content) == name {
			removed = true
			continue
		}
		out.WriteString(line)
	}
	if !removed {
		return ErrSecretNotFound
	}
	return writeSecretFile(path, []byte(out.String()))
}

// encodeSecretValue quotes a value with double quotes when it contains spaces,
// '#', or a single quote. A double quote is rejected before this point because
// the parser has no escape syntax. Quoting also protects leading or trailing
// whitespace that the parser would otherwise trim.
func encodeSecretValue(value string) string {
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t'#") {
		return `"` + value + `"`
	}
	return value
}

// secretLineName returns the trimmed name of a NAME=value line. Comments,
// blank lines, and lines without '=' have no name.
func secretLineName(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	eq := strings.Index(line, "=")
	if eq < 0 {
		return ""
	}
	return strings.TrimSpace(line[:eq])
}

func splitSecretLines(data string) []string {
	if data == "" {
		return nil
	}
	return strings.SplitAfter(data, "\n")
}

func splitLineEnding(line string) (content, ending string) {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return line[:len(line)-2], "\r\n"
	case strings.HasSuffix(line, "\n"):
		return line[:len(line)-1], "\n"
	default:
		return line, ""
	}
}

func readSecretFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("webfiles: read %s: %w", path, err)
	}
	return data, nil
}

func writeSecretFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("webfiles: create directory for %s: %w", path, err)
	}
	if err := AtomicWriteMode(path, data, SecretFileMode); err != nil {
		return err
	}
	// AtomicWriteMode already creates the file with 0600; chmod is a safety net
	// for a replaced file whose mode came from the old path.
	if err := os.Chmod(path, SecretFileMode); err != nil {
		return fmt.Errorf("webfiles: set mode on %s: %w", path, err)
	}
	return nil
}

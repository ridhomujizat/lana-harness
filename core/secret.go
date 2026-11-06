package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// Secret holds a secret value. All formats and encoders write "***".
type Secret struct {
	v string
}

func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the original value.
func (s Secret) Reveal() string { return s.v }

func (s Secret) String() string   { return "***" }
func (s Secret) GoString() string { return "***" }

func (s Secret) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, "***")
}

func (s Secret) LogValue() slog.Value { return slog.StringValue("***") }

func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal("***") }

func (s Secret) MarshalText() ([]byte, error) { return []byte("***"), nil }

// Secrets looks up secret values by env var name.
type Secrets interface {
	Lookup(name string) (Secret, bool)
}

// UnmarshalText accepts a value from the TOML decoder.
func (s *Secret) UnmarshalText(b []byte) error {
	s.v = string(b)
	return nil
}

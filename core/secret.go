package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// Secret menyimpan nilai rahasia. Semua format dan encoder menulis "***".
type Secret struct {
	v string
}

func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal mengembalikan nilai asli.
func (s Secret) Reveal() string { return s.v }

func (s Secret) String() string   { return "***" }
func (s Secret) GoString() string { return "***" }

func (s Secret) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, "***")
}

func (s Secret) LogValue() slog.Value { return slog.StringValue("***") }

func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal("***") }

func (s Secret) MarshalText() ([]byte, error) { return []byte("***"), nil }

// Secrets mengambil nilai secret berdasarkan nama env.
type Secrets interface {
	Lookup(name string) (Secret, bool)
}

// UnmarshalText menerima nilai dari decoder TOML.
func (s *Secret) UnmarshalText(b []byte) error {
	s.v = string(b)
	return nil
}

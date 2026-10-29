package core

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretFormatting(t *testing.T) {
	s := NewSecret("super-secret-value")
	for _, got := range []string{
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%+v", s),
		fmt.Sprintf("%#v", s),
		fmt.Sprintf("%s", s),
		s.String(),
		s.GoString(),
	} {
		if strings.Contains(got, "super-secret-value") {
			t.Fatalf("format membocorkan nilai: %q", got)
		}
		if got != "***" {
			t.Fatalf("format=%q want ***", got)
		}
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("secret", "value", s)
	if strings.Contains(buf.String(), "super-secret-value") {
		t.Fatalf("slog membocorkan nilai: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "***") {
		t.Fatalf("slog tidak meredaksi: %s", buf.String())
	}
	if s.Reveal() != "super-secret-value" {
		t.Fatalf("Reveal=%q", s.Reveal())
	}
}

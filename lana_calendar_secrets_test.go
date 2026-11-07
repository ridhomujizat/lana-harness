package lana

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/calendar"
)

// The Google link keeps its secrets in <home>/secrets.env under env names,
// read live so a new refresh token needs no restart (design "Data").
func TestCalendarSecretsFile(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	s := newCalendarSecrets(home)
	if _, ok, err := s.Get(ctx, calendar.SecretGoogleRefreshToken); ok || err != nil {
		t.Fatalf("empty get ok=%v err=%v", ok, err)
	}
	if err := s.Set(ctx, calendar.SecretGoogleRefreshToken, "1//tok-en"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "secrets.env"))
	if err != nil || !strings.Contains(string(data), "CALENDAR_GOOGLE_REFRESH_TOKEN=1//tok-en") {
		t.Fatalf("secrets.env %q %v", data, err)
	}
	if v, ok, err := s.Get(ctx, calendar.SecretGoogleRefreshToken); !ok || err != nil || v != "1//tok-en" {
		t.Fatalf("get %q %v %v", v, ok, err)
	}
	if err := s.Delete(ctx, calendar.SecretGoogleRefreshToken); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, calendar.SecretGoogleRefreshToken); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if _, ok, _ := s.Get(ctx, calendar.SecretGoogleRefreshToken); ok {
		t.Fatal("deleted secret still set")
	}

	mem := newCalendarSecrets("")
	if err := mem.Set(ctx, calendar.SecretGoogleClientID, "cid"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := mem.Get(ctx, calendar.SecretGoogleClientID); !ok || v != "cid" {
		t.Fatalf("memory get %q %v", v, ok)
	}
}

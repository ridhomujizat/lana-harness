package config

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestWebConfigDefaults(t *testing.T) {
	res, err := Load(context.Background(), Options{Home: t.TempDir(), Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Config.Web.Enabled {
		t.Fatal("web must be enabled when [web] is absent")
	}
	if got := res.Config.Web.Addr; got != core.DefaultWebAddr {
		t.Fatalf("addr = %q, want %q", got, core.DefaultWebAddr)
	}
}

func TestWebConfigExplicit(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[web]\nenabled = false\naddr = \"127.0.0.2:9000\"\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Web.Enabled {
		t.Fatal("web must be disabled")
	}
	if got := res.Config.Web.Addr; got != "127.0.0.2:9000" {
		t.Fatalf("addr = %q", got)
	}
}

func TestWebConfigEmptyTableDefaultsEnabled(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[web]\naddr = \"localhost:1234\"\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Config.Web.Enabled {
		t.Fatal("web must default to enabled")
	}
	if got := res.Config.Web.Addr; got != "localhost:1234" {
		t.Fatalf("addr = %q", got)
	}
}

func TestWebConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
		key  string
	}{
		{name: "unknown key", toml: "[web]\nport = 1\n", key: "web.port"},
		{name: "enabled type", toml: "[web]\nenabled = \"yes\"\n", key: "web.enabled"},
		{name: "addr type", toml: "[web]\naddr = 1\n", key: "web.addr"},
		{name: "table type", toml: "web = 1\n", key: "web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, filepath.Join(home, "config.toml"), tt.toml)
			_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
			var errs Errors
			if !errors.As(err, &errs) {
				t.Fatalf("error is not Errors: %v", err)
			}
			for _, item := range errs {
				if item.Key == tt.key {
					return
				}
			}
			t.Fatalf("no error for %s: %v", tt.key, errs)
		})
	}
}

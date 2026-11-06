package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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
		{name: "open_browser type", toml: "[web]\nopen_browser = \"yes\"\n", key: "web.open_browser"},
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

// TestWebOpenBrowserDefaultsTrue covers the default of REQ-WLG-001: when the
// key is absent open_browser is true.
func TestWebOpenBrowserDefaultsTrue(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[web]\naddr = \"127.0.0.1:18300\"\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Config.Web.OpenBrowser {
		t.Fatal("open_browser must default to true when the key is absent")
	}
}

// TestWebOpenBrowserFalse covers open_browser = false.
func TestWebOpenBrowserFalse(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[web]\nopen_browser = false\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Web.OpenBrowser {
		t.Fatal("open_browser = false loaded as true")
	}
}

// TestWebOpenBrowserTypeError covers the validation of a non-boolean value: the
// error names web.open_browser and the expected boolean type.
func TestWebOpenBrowserTypeError(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[web]\nopen_browser = \"yes\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, item := range errs {
		if item.Key != "web.open_browser" {
			continue
		}
		if !strings.Contains(item.Msg, "boolean") {
			t.Fatalf("message = %q, want the expected boolean type", item.Msg)
		}
		return
	}
	t.Fatalf("no error for web.open_browser: %v", errs)
}

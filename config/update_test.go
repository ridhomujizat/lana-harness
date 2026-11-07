package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateConfigDefaults covers the API-UPD-001 defaults: channel "stable"
// and check true when [update] is absent or empty.
func TestUpdateConfigDefaults(t *testing.T) {
	for _, text := range []string{"", "[update]\n"} {
		home := t.TempDir()
		if text != "" {
			writeFile(t, filepath.Join(home, "config.toml"), text)
		}
		res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
		if err != nil {
			t.Fatal(err)
		}
		if res.Config.Update.Channel != "stable" || !res.Config.Update.Check {
			t.Fatalf("update = %+v for %q, want stable/true", res.Config.Update, text)
		}
	}
}

func TestUpdateConfigExplicit(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[update]\nchannel = \"beta\"\ncheck = false\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Update.Channel != "beta" || res.Config.Update.Check {
		t.Fatalf("update = %+v, want beta/false", res.Config.Update)
	}
}

// TestUpdateConfigErrors covers the API-UPD-001 validation text.
func TestUpdateConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
		key  string
		msg  string
	}{
		{name: "unknown channel", toml: "[update]\nchannel = \"nightly\"\n", key: "update.channel", msg: `must be "stable" or "beta"`},
		{name: "channel type", toml: "[update]\nchannel = 1\n", key: "update.channel", msg: `must be "stable" or "beta"`},
		{name: "check type", toml: "[update]\ncheck = \"yes\"\n", key: "update.check"},
		{name: "unknown key", toml: "[update]\ninterval = 1\n", key: "update.interval"},
		{name: "table type", toml: "update = 1\n", key: "update"},
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
					if tt.msg != "" && item.Msg != tt.msg {
						t.Fatalf("msg = %q, want %q", item.Msg, tt.msg)
					}
					if tt.msg != "" && !strings.Contains(item.Error(), `update.channel: must be "stable" or "beta"`) {
						t.Fatalf("error text = %q", item.Error())
					}
					return
				}
			}
			t.Fatalf("no error for %s: %v", tt.key, errs)
		})
	}
}

package config

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestWebRemoteDefaults covers REQ-RA-010: when the [web.remote] table is
// absent the defaults are loaded and allowed_logins is non-nil.
func TestWebRemoteDefaults(t *testing.T) {
	for _, text := range []string{"", "[web]\naddr = \"127.0.0.1:18300\"\n", "[web.remote]\n"} {
		home := t.TempDir()
		if text != "" {
			writeFile(t, filepath.Join(home, "config.toml"), text)
		}
		res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
		if err != nil {
			t.Fatal(err)
		}
		want := core.DefaultWebRemoteConfig()
		if !reflect.DeepEqual(res.Config.Web.Remote, want) {
			t.Fatalf("remote = %+v for %q, want %+v", res.Config.Web.Remote, text, want)
		}
		if res.Config.Web.Remote.AllowedLogins == nil {
			t.Fatalf("allowed_logins must be non-nil for %q", text)
		}
	}
}

// TestWebRemoteExplicit covers REQ-RA-010: a full table loads, the host is
// lower-cased without a trailing dot, and the logins are trimmed and
// lower-cased.
func TestWebRemoteExplicit(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), `[web.remote]
enabled = true
host = "Ridho-MBP.Tail3f2a1.TS.NET."
require_identity = false
allowed_logins = ["Name@GitHub ", " Second@GitHub"]
terminal = "full"
confirm_with = "password"
control_minutes = 30
idle_minutes = 120
max_age_hours = 24
notify_sign_in = false
`)
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Config.Web.Remote
	want := core.WebRemoteConfig{
		Enabled:         true,
		Host:            "ridho-mbp.tail3f2a1.ts.net",
		RequireIdentity: false,
		AllowedLogins:   []string{"name@github", "second@github"},
		Terminal:        "full",
		ConfirmWith:     "password",
		ControlMinutes:  30,
		IdleMinutes:     120,
		MaxAgeHours:     24,
		NotifySignIn:    false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remote = %+v, want %+v", got, want)
	}
}

// TestWebRemoteErrors covers REQ-RA-010: an invalid value fails validation with
// the key name.
func TestWebRemoteErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
		key  string
	}{
		{name: "unknown key", toml: "[web.remote]\nfoo = 1\n", key: "web.remote.foo"},
		{name: "table type", toml: "[web]\nremote = 1\n", key: "web.remote"},
		{name: "enabled type", toml: "[web.remote]\nenabled = \"yes\"\n", key: "web.remote.enabled"},
		{name: "host type", toml: "[web.remote]\nhost = 1\n", key: "web.remote.host"},
		{name: "require_identity type", toml: "[web.remote]\nrequire_identity = \"yes\"\n", key: "web.remote.require_identity"},
		{name: "notify_sign_in type", toml: "[web.remote]\nnotify_sign_in = \"yes\"\n", key: "web.remote.notify_sign_in"},
		{name: "allowed_logins type", toml: "[web.remote]\nallowed_logins = \"name@github\"\n", key: "web.remote.allowed_logins"},
		{name: "allowed_logins empty entry", toml: "[web.remote]\nallowed_logins = [\"name@github\", \" \"]\n", key: "web.remote.allowed_logins.1"},
		{name: "terminal enum", toml: "[web.remote]\nterminal = \"lock\"\n", key: "web.remote.terminal"},
		{name: "terminal type", toml: "[web.remote]\nterminal = 1\n", key: "web.remote.terminal"},
		{name: "confirm_with enum", toml: "[web.remote]\nconfirm_with = \"code\"\n", key: "web.remote.confirm_with"},
		{name: "confirm_with type", toml: "[web.remote]\nconfirm_with = 1\n", key: "web.remote.confirm_with"},
		{name: "control_minutes low", toml: "[web.remote]\ncontrol_minutes = 0\n", key: "web.remote.control_minutes"},
		{name: "control_minutes high", toml: "[web.remote]\ncontrol_minutes = 241\n", key: "web.remote.control_minutes"},
		{name: "control_minutes type", toml: "[web.remote]\ncontrol_minutes = \"15\"\n", key: "web.remote.control_minutes"},
		{name: "idle_minutes low", toml: "[web.remote]\nidle_minutes = 4\n", key: "web.remote.idle_minutes"},
		{name: "idle_minutes high", toml: "[web.remote]\nidle_minutes = 1441\n", key: "web.remote.idle_minutes"},
		{name: "max_age_hours low", toml: "[web.remote]\nmax_age_hours = 0\n", key: "web.remote.max_age_hours"},
		{name: "max_age_hours high", toml: "[web.remote]\nmax_age_hours = 2161\n", key: "web.remote.max_age_hours"},
		{name: "enabled without host", toml: "[web.remote]\nenabled = true\n", key: "web.remote.host"},
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

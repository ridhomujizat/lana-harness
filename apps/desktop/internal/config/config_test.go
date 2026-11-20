package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLanaHome(t *testing.T) {
	const home = "/Users/me"
	cases := []struct {
		name     string
		env      map[string]string
		settings Settings
		devBuild bool
		userHome string
		want     string
	}{
		{
			name:     "the environment first",
			env:      map[string]string{"LANA_HOME": "/tmp/lana"},
			settings: Settings{LanaHome: "/tmp/setting"},
			userHome: home,
			want:     "/tmp/lana",
		},
		{
			name:     "the setting next",
			settings: Settings{LanaHome: "/tmp/setting"},
			userHome: home,
			want:     "/tmp/setting",
		},
		{
			name:     "an empty environment variable falls through",
			env:      map[string]string{"LANA_HOME": ""},
			settings: Settings{LanaHome: "/tmp/setting"},
			userHome: home,
			want:     "/tmp/setting",
		},
		{
			name:     "a home directory in the environment",
			env:      map[string]string{"LANA_HOME": "~/lana"},
			userHome: home,
			want:     home + "/lana",
		},
		{
			name:     "a home directory in the setting",
			settings: Settings{LanaHome: "~/lana"},
			userHome: home,
			want:     home + "/lana",
		},
		{
			name:     "a bare home directory",
			env:      map[string]string{"LANA_HOME": "~"},
			userHome: home,
			want:     home,
		},
		{
			name:     "the default",
			userHome: home,
			want:     home + "/.lana",
		},
		{
			name:     "the development default",
			devBuild: true,
			userHome: home,
			want:     home + "/.lana-dev",
		},
		{
			name:     "a chosen home beats the development default",
			env:      map[string]string{"LANA_HOME": "/tmp/lana"},
			devBuild: true,
			userHome: home,
			want:     "/tmp/lana",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := func(k string) string { return tc.env[k] }
			got := ResolveLanaHome(env, tc.settings, tc.devBuild, tc.userHome)
			if got != tc.want {
				t.Errorf("ResolveLanaHome(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	cases := []struct {
		name  string
		write bool
		file  string
		want  Settings
	}{
		{
			name: "a missing file",
			want: Defaults(),
		},
		{
			name:  "an empty file",
			write: true,
			want:  Defaults(),
		},
		{
			name:  "a file that is not JSON",
			write: true,
			file:  "not json",
			want:  Defaults(),
		},
		{
			name:  "a file without a text size",
			write: true,
			file:  `{"appearance":"dark"}`,
			want:  Settings{Appearance: "dark", TextSize: DefaultTextSize, PanelWidth: DefaultPanelWidth},
		},
		{
			name:  "a text size that was never set",
			write: true,
			file:  `{"textSize":0}`,
			want:  Defaults(),
		},
		{
			name:  "a panel width that was never set",
			write: true,
			file:  `{"panelHidden":true}`,
			want:  Settings{TextSize: DefaultTextSize, PanelWidth: DefaultPanelWidth, PanelHidden: true},
		},
		{
			name:  "every field",
			write: true,
			file:  `{"lanaHome":"/tmp/lana","appearance":"light","textSize":16,"notifications":true,"panelWidth":300,"panelHidden":true}`,
			want:  Settings{LanaHome: "/tmp/lana", Appearance: "light", TextSize: 16, PanelWidth: 300, PanelHidden: true, Notifications: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.write {
				if err := os.WriteFile(path(dir), []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := Load(dir); got != tc.want {
				t.Errorf("Load(...) = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSave(t *testing.T) {
	cases := []struct {
		name string
		save Settings
		want Settings
	}{
		{name: "the defaults", save: Defaults(), want: Defaults()},
		{name: "an unset text size", save: Settings{}, want: Defaults()},
		{
			name: "every field",
			save: Settings{LanaHome: "/tmp/lana", Appearance: "dark", TextSize: 15, Notifications: true},
			want: Settings{LanaHome: "/tmp/lana", Appearance: "dark", TextSize: 15, PanelWidth: DefaultPanelWidth, Notifications: true},
		},
		{
			name: "the panel",
			save: Settings{TextSize: DefaultTextSize, PanelWidth: 300, PanelHidden: true},
			want: Settings{TextSize: DefaultTextSize, PanelWidth: 300, PanelHidden: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := Save(dir, tc.save); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if got := Load(dir); got != tc.want {
				t.Errorf("Load after Save = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSaveReplaces checks that a second Save replaces the first, which is
// what the rename over the file does.
func TestSaveReplaces(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Settings{Appearance: "light"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := Settings{Appearance: "dark", TextSize: 14, PanelWidth: DefaultPanelWidth}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(dir); got != want {
		t.Errorf("Load after two Saves = %+v, want %+v", got, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Errorf("the directory holds %v, want %s alone", entries, FileName)
	}
}

// TestSaveMakesTheDirectory checks that Save works in the app's data
// directory before anything else made it.
func TestSaveMakesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Lana Workbench Dev")
	if err := Save(dir, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Load(dir); got != Defaults() {
		t.Errorf("Load(...) = %+v, want %+v", got, Defaults())
	}
}

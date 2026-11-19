package workbench

import (
	"os"
	"strings"
	"testing"
)

// lookupEnv returns the value env gives key, with a later entry winning over an
// earlier one as the OS reads the list, and whether the key appears at all.
func lookupEnv(env []string, key string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

// countEnv counts the entries of env that equal entry exactly.
func countEnv(env []string, entry string) int {
	n := 0
	for _, kv := range env {
		if kv == entry {
			n++
		}
	}
	return n
}

// clearEnv unsets each key and restores its previous value at cleanup.
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, ok := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
}

func TestSessionEnvLocale(t *testing.T) {
	tests := []struct {
		name string
		// set are the locale vars to put in the core env.
		set map[string]string
		// extra is the caller's env, appended last.
		extra []string
		// wantLANG is the effective LANG value in the result.
		wantLANG string
		// wantLANGCount is how many LANG entries the result holds.
		wantLANGCount int
		// wantCUTF8 is how many "LANG=C.UTF-8" entries the result holds.
		wantCUTF8 int
	}{
		{
			name:          "no locale",
			wantLANG:      "C.UTF-8",
			wantLANGCount: 1,
			wantCUTF8:     1,
		},
		{
			name:          "LANG set in core env",
			set:           map[string]string{"LANG": "en_US.UTF-8"},
			wantLANG:      "en_US.UTF-8",
			wantLANGCount: 1,
			wantCUTF8:     0,
		},
		{
			name:          "LC_ALL set",
			set:           map[string]string{"LC_ALL": "en_US.UTF-8"},
			wantLANG:      "",
			wantLANGCount: 0,
			wantCUTF8:     0,
		},
		{
			name:          "LC_CTYPE set",
			set:           map[string]string{"LC_CTYPE": "en_US.UTF-8"},
			wantLANG:      "",
			wantLANGCount: 0,
			wantCUTF8:     0,
		},
		{
			name:          "empty LANG counts as unset",
			set:           map[string]string{"LANG": ""},
			wantLANG:      "C.UTF-8",
			wantLANGCount: 2,
			wantCUTF8:     1,
		},
		{
			name:          "extra LANG kept",
			extra:         []string{"LANG=xx"},
			wantLANG:      "xx",
			wantLANGCount: 1,
			wantCUTF8:     0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t, "LANG", "LC_ALL", "LC_CTYPE")
			for k, v := range tt.set {
				t.Setenv(k, v)
			}
			env := sessionEnv("s1", tt.extra)
			if got, _ := lookupEnv(env, "LANG"); got != tt.wantLANG {
				t.Errorf("LANG = %q, want %q", got, tt.wantLANG)
			}
			if got := countEnv(env, "LANG=C.UTF-8"); got != tt.wantCUTF8 {
				t.Errorf("LANG=C.UTF-8 entries = %d, want %d", got, tt.wantCUTF8)
			}
			locks := 0
			for _, kv := range env {
				if k, _, ok := strings.Cut(kv, "="); ok && k == "LANG" {
					locks++
				}
			}
			if locks != tt.wantLANGCount {
				t.Errorf("LANG entries = %d, want %d", locks, tt.wantLANGCount)
			}
		})
	}
}

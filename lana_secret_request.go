package lana

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// secretExists reports whether a secret name already has a value, the way
// Settings > Secrets shows it as set: a non-empty value in secrets.env or in
// the process environment (spec 53, the replace warning).
func secretExists(home string) func(name string) bool {
	return func(name string) bool {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return true
		}
		if home == "" {
			return false
		}
		values, err := webfiles.ReadSecrets(filepath.Join(home, "secrets.env"))
		if err != nil {
			return false
		}
		return values[name] != ""
	}
}

// secretNames returns the sorted names in secrets.env. Values never leave this
// function.
func secretNames(home string) []string {
	if home == "" {
		return nil
	}
	values, err := webfiles.ReadSecrets(filepath.Join(home, "secrets.env"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

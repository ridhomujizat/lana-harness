package config

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// Check validates candidate as the home config.toml layer without writing any
// file. It runs the same parsing, layer merge, interpolation, and validation as
// Load; the candidate takes the place of $LANA_HOME/config.toml.
//
// Validation failures use the same shape as Load: the returned error is an
// Errors slice whose entries carry File, Key, and Msg (plus line and column
// when the parser supplied them). On success the returned Result is the
// effective config that the candidate would produce. The signature returns a
// Result value, rather than Load's *Result, so a caller cannot mistake the
// result for the daemon's loaded runtime config.
func Check(ctx context.Context, home string, candidate []byte) (Result, error) {
	result, err := load(ctx, Options{Home: home}, &candidate)
	if err != nil {
		return Result{}, err
	}
	return *result, nil
}

// LegacyUserWarnings returns the API-HOME-003 warnings for per-agent USER.md
// files that still have entries. After the migration these files are no longer
// read; their entries belong in the shared $LANA_HOME/USER.md.
func LegacyUserWarnings(home string, cfg core.Config) []string {
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		entries, err := agenthome.ReadEntries(filepath.Join(home, "agents", name, "USER.md"))
		if err != nil || len(entries) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("warning: agents/%s/USER.md is not read; move its entries to %s", name, agenthome.SharedUser(home)))
	}
	return out
}

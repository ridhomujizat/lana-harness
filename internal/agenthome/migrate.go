package agenthome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Migration describes the outcome of MigrateUser.
type Migration struct {
	// Source is the agent whose per-agent USER.md was copied to the shared
	// user file. It is empty when nothing was copied.
	Source string
	// Skipped holds the candidate agent names when nothing was copied because
	// several files exist and none belongs to the default agent. The caller
	// should warn that these files must be merged into SharedUser by hand.
	Skipped []string
}

// Warning returns the API-HOME-003 warning for a migration that copied nothing
// because several candidates exist. It returns "" when no warning is needed.
func (m Migration) Warning(lanaHome string) string {
	if m.Source != "" || len(m.Skipped) == 0 {
		return ""
	}
	return fmt.Sprintf("several agents have USER.md; merge them into %s by hand: %s",
		SharedUser(lanaHome), strings.Join(m.Skipped, ", "))
}

// MigrateUser implements the copy rule of API-HOME-003. When the shared user
// file (SharedUser(lanaHome)) does not exist, it copies one per-agent USER.md
// into it. Candidates are the given agents whose agents/<name>/USER.md has at
// least one entry. When exactly one agent qualifies, its file is copied. When
// several qualify and defaultAgent is one of them, the default agent's file is
// copied. Otherwise nothing is copied and the qualifying names are returned in
// Skipped.
//
// The copy is byte for byte, uses mode 0600, and leaves the source files
// unchanged. The migration is idempotent: once the shared file exists,
// MigrateUser does nothing. Read and write failures are returned; a missing
// candidate file is ignored. The caller logs the API-HOME-003 info line with
// Source and uses Warning for the no-copy warning.
func MigrateUser(lanaHome string, candidates []string, defaultAgent string) (Migration, error) {
	shared := SharedUser(lanaHome)
	if _, err := os.Stat(shared); err == nil {
		return Migration{}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Migration{}, err
	}

	type candidate struct {
		name string
		data []byte
	}
	found := make([]candidate, 0, len(candidates))
	for _, name := range candidates {
		data, err := os.ReadFile(agentUserFile(lanaHome, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Migration{}, err
		}
		if len(Parse(string(data))) == 0 {
			continue
		}
		found = append(found, candidate{name: name, data: data})
	}

	var chosen *candidate
	switch {
	case len(found) == 0:
		return Migration{}, nil
	case len(found) == 1:
		chosen = &found[0]
	default:
		for i := range found {
			if found[i].name == defaultAgent {
				chosen = &found[i]
				break
			}
		}
		if chosen == nil {
			skipped := make([]string, 0, len(found))
			for _, item := range found {
				skipped = append(skipped, item.name)
			}
			return Migration{Skipped: skipped}, nil
		}
	}

	if err := writeBytes(shared, chosen.data); err != nil {
		return Migration{}, err
	}
	return Migration{Source: chosen.name}, nil
}

// agentUserFile is the legacy per-agent user file read by the migration.
func agentUserFile(lanaHome, name string) string {
	return filepath.Join(lanaHome, "agents", name, "USER.md")
}

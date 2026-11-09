package knowledge

import (
	"path"
	"regexp"
	"strings"
)

// agentsDir is the vault folder that holds one subfolder per agent domain
// (REQ-DKN-001).
const agentsDir = "agents"

// ScopeShared is the scope of every vault file outside agents/ (design §1).
const ScopeShared = "shared"

// scopeNamePattern matches a legal agent scope name, the same rule as
// agenthome.ValidName (design §1).
var scopeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ScopeOf returns the scope of a vault-relative slash path: a path under
// agents/<name>/ belongs to <name>, everything else to shared (REQ-DKN-001).
func ScopeOf(rel string) string {
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	if len(parts) >= 2 && parts[0] == agentsDir && ValidScopeName(parts[1]) {
		return parts[1]
	}
	return ScopeShared
}

// WikiDir returns the vault-relative folder that holds the wiki pages of a
// scope: "wiki" for the shared scope, "agents/<scope>/wiki" for a domain
// (design §1).
func WikiDir(scope string) string {
	if scope == "" || scope == ScopeShared {
		return wikiDir
	}
	return path.Join(agentsDir, scope, wikiDir)
}

// JournalDir returns the vault-relative folder that holds the journal files of
// a scope: "journal" for the shared scope, "agents/<scope>/journal" for a
// domain (design §1).
func JournalDir(scope string) string {
	if scope == "" || scope == ScopeShared {
		return journalDir
	}
	return path.Join(agentsDir, scope, journalDir)
}

// ValidScopeName reports whether name is a legal agent scope name: it follows
// the agent name rule and is not the reserved word shared (design §1).
func ValidScopeName(name string) bool {
	return name != ScopeShared && scopeNamePattern.MatchString(name)
}

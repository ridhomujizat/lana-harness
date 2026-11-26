package web

import (
	"context"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/claudeshare"
)

// agyShareHomes holds the owner HOME of each server whose Antigravity account
// conversations are shared (spec 77 REQ-WRA-010). Like claudeDefaultDirs it is
// kept outside the Server struct.
var (
	agyShareHomesMu sync.Mutex
	agyShareHomes   = map[*Server]string{}
)

// SetAntigravityShareHome sets the owner HOME a saved Antigravity account links
// its conversations to (spec 77 REQ-WRA-010, experimental). Unset, an account
// save links nothing.
func (s *Server) SetAntigravityShareHome(home string) {
	agyShareHomesMu.Lock()
	defer agyShareHomesMu.Unlock()
	agyShareHomes[s] = home
}

// antigravityShareHome returns the HOME SetAntigravityShareHome set, or "".
func (s *Server) antigravityShareHome() string {
	agyShareHomesMu.Lock()
	defer agyShareHomesMu.Unlock()
	return agyShareHomes[s]
}

// shareAntigravityAccount links an added account HOME's conversations and
// settings to the owner's (spec 77 REQ-WRA-010, REQ-WRA-011). The built-in account (home "") is a no-op. A
// failure is only logged: it never fails the save.
func (s *Server) shareAntigravityAccount(name, home string) {
	owner := s.antigravityShareHome()
	if owner == "" || home == "" {
		return
	}
	report, err := claudeshare.LinkAntigravity(home, owner, s.nowTime())
	if err != nil {
		s.logger.Warn("web: share Antigravity conversations", "account", name, "home", home, "error", err)
	}
	if report.Changed() {
		s.logger.Info("web: shared Antigravity conversations", "account", name, "home", home)
	}
}

// agyAccountHomes maps every Antigravity account of the stored config to its
// HOME: the built-in "antigravity" to "", each kind=antigravity provider to
// its folder (spec 83). A config that cannot be read leaves only the built-in
// account.
func (s *Server) agyAccountHomes(ctx context.Context) map[string]string {
	out := map[string]string{core.AntigravityProvider: ""}
	if s.configStore == nil {
		return out
	}
	file, err := s.configStore.ConfigFile(ctx)
	if err != nil || !file.Exists {
		return out
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		return out
	}
	for name, table := range document.Providers {
		if name == core.AntigravityProvider || mapProviderString(table, "kind") != core.AntigravityKind {
			continue
		}
		out[name] = s.antigravityHome(name, providerRecordFromMap(table))
	}
	return out
}

// relinkAgyMCP puts a named Antigravity account's mcp_config.json back as a
// link to the owner's before a Workbench pane starts agy on it (spec 83
// REQ-WAG-004). The built-in account (home "") is a no-op; a failure is only
// logged and never blocks the launch.
func (s *Server) relinkAgyMCP(name, home string) {
	owner := s.antigravityShareHome()
	if owner == "" || home == "" {
		return
	}
	item, err := claudeshare.RelinkAgyMCPConfig(home, owner, s.nowTime())
	if err != nil {
		s.logger.Info("web: relink Antigravity mcp_config.json skipped", "account", name, "reason", err.Error())
		return
	}
	if item.Action == claudeshare.Adopted {
		s.logger.Info("web: relinked Antigravity mcp_config.json", "account", name, "backup", item.Backup)
	}
}

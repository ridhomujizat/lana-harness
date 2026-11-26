package web

import (
	"sync"

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

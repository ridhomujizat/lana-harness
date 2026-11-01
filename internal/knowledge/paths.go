package knowledge

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath returns the vault root for a configured knowledge path
// (REQ-KNW-001). An empty cfgPath gives <home>/knowledge, a leading ~ expands
// to the user home, and a relative path is joined with home. The result is
// cleaned.
func ResolvePath(home, cfgPath string) string {
	p := strings.TrimSpace(cfgPath)
	if p == "" {
		return filepath.Join(home, "knowledge")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if base, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = base
			} else {
				p = filepath.Join(base, p[2:])
			}
		}
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(home, p)
	}
	return filepath.Clean(p)
}

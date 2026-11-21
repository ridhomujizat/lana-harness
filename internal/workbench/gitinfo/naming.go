package gitinfo

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
)

// Palette is the worktree colour palette (REQ-WG-061). Its order is part of the
// contract: it is the seven SPACE_COLORS of the web UI
// (web/ui/src/views/workbench-model.ts), so the daemon and every client agree.
var Palette = []string{"#3E9C8F", "#5B7BE0", "#D9824B", "#7652C2", "#B21FB8", "#3F8F3F", "#8C8880"}

// Color is the stable colour of a worktree path (REQ-WG-061): FNV-1a of the
// cleaned path modulo the palette size. The same worktree always gets the same
// colour, whatever client asks.
func Color(path string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(path)))
	return Palette[h.Sum32()%uint32(len(Palette))]
}

// ShortBranch drops the first path segment of a branch that has one, so the
// type prefix of a common workflow is hidden (REQ-WG-030, REQ-WG-071):
// "feature/router" -> "router", "dev/integration" -> "integration", "main" ->
// "main".
func ShortBranch(branch string) string {
	if i := strings.IndexByte(branch, '/'); i >= 0 && i+1 < len(branch) {
		return branch[i+1:]
	}
	return branch
}

// WorktreeRoot is the folder new worktrees of a repository go in (REQ-WG-030): a
// sibling of the main worktree named after it, so
// "/code/lana-harness" -> "/code/lana-harness-worktree".
func WorktreeRoot(mainWorktree string) string {
	clean := filepath.Clean(mainWorktree)
	return filepath.Join(filepath.Dir(clean), filepath.Base(clean)+"-worktree")
}

// FolderName is the absolute folder of a new worktree for branch under root
// (REQ-WG-030). It uses the short branch name; when that path already exists it
// uses the whole branch with "/" replaced by "-"; when that exists too it
// appends "-2", "-3", ... until the path is free.
func FolderName(root, branch string) string {
	short := filepath.Join(root, ShortBranch(branch))
	if !exists(short) {
		return short
	}
	full := filepath.Join(root, strings.ReplaceAll(branch, "/", "-"))
	if !exists(full) {
		return full
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", full, n)
		if !exists(candidate) {
			return candidate
		}
	}
}

// exists reports whether path is on disk.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

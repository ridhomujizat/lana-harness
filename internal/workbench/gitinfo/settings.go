package gitinfo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// defaultBase is the base branch a repository falls back to when it has no
// development branch and no origin/HEAD (REQ-WG-050, D2).
const defaultBase = "main"

// Settings is the Workbench git settings of one repository (REQ-WG-050): the
// Base branch comparisons use, the WorktreeRoot new worktrees go in and
// WarnShared, which warns when two working agents share a worktree.
type Settings struct {
	Base         string `json:"base"`
	WorktreeRoot string `json:"worktree_root"`
	WarnShared   bool   `json:"warn_shared"`
}

// SettingsPatch is a partial update of a repository's Settings (REQ-WG-050): a
// nil field keeps its stored value.
type SettingsPatch struct {
	Base         *string `json:"base,omitempty"`
	WorktreeRoot *string `json:"worktree_root,omitempty"`
	WarnShared   *bool   `json:"warn_shared,omitempty"`
}

// SettingsStore keeps the Workbench git settings of every repository in one
// JSON file keyed by repo id (REQ-WG-050). It is safe for concurrent use and
// writes atomically, so a reader never sees a half-written file.
type SettingsStore struct {
	path string

	mu   sync.Mutex
	data map[string]SettingsPatch
}

// NewSettingsStore opens the settings file, reading an existing one and
// starting empty otherwise (REQ-WG-050). A missing or unreadable file is not an
// error: the store is simply empty.
func NewSettingsStore(path string) *SettingsStore {
	s := &SettingsStore{path: path, data: map[string]SettingsPatch{}}
	s.load()
	return s
}

// Get returns the settings of a repository: every field a Put has set, else the
// matching default. warn_shared is true when the repository has no entry at all
// (REQ-WG-050).
func (s *SettingsStore) Get(repo string, defaults Settings) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(repo, defaults)
}

// Put merges patch into a repository's entry and writes the file atomically
// with mode 0o600 (REQ-WG-050). Fields left nil keep their stored value. It
// returns the repository entry as the store's own defaults (warn_shared true)
// see it.
func (s *SettingsStore) Put(repo string, patch SettingsPatch) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string]SettingsPatch{}
	}
	cur := s.data[repo]
	if patch.Base != nil {
		cur.Base = patch.Base
	}
	if patch.WorktreeRoot != nil {
		cur.WorktreeRoot = patch.WorktreeRoot
	}
	if patch.WarnShared != nil {
		cur.WarnShared = patch.WarnShared
	}
	s.data[repo] = cur
	if err := s.save(); err != nil {
		return Settings{}, err
	}
	return s.get(repo, Settings{WarnShared: true}), nil
}

// get is Get without the lock: defaults overlaid with the stored patch, and
// warn_shared true for a repository with no entry.
func (s *SettingsStore) get(repo string, defaults Settings) Settings {
	out := defaults
	patch, ok := s.data[repo]
	if !ok {
		out.WarnShared = true
		return out
	}
	if patch.Base != nil {
		out.Base = *patch.Base
	}
	if patch.WorktreeRoot != nil {
		out.WorktreeRoot = *patch.WorktreeRoot
	}
	if patch.WarnShared != nil {
		out.WarnShared = *patch.WarnShared
	}
	return out
}

// load reads the file into the map; a missing or unreadable file leaves the
// store as it is (REQ-WG-050).
func (s *SettingsStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var m map[string]SettingsPatch
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	if m != nil {
		s.data = m
	}
}

// save writes the whole map to the file atomically (REQ-WG-050): a temp file in
// the same folder, mode 0o600, renamed into place, so the file is either the old
// or the new contents.
func (s *SettingsStore) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".workbench-git-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// DefaultSettings is the default base, worktree root and warn_shared of a
// repository (REQ-WG-050, D2, REQ-WG-030): base "development" when the
// repository has it locally or on origin, else the branch origin/HEAD points
// at, else "main"; worktree_root is the repository's worktree folder; and
// warn_shared is true.
func (r *Runner) DefaultSettings(ctx context.Context, repoDir string) Settings {
	_, mainPath, err := r.mainWorktree(ctx, repoDir)
	if err != nil {
		return Settings{Base: defaultBase, WarnShared: true}
	}
	base := defaultBase
	switch {
	case r.refExists(ctx, mainPath, "refs/heads/development"),
		r.refExists(ctx, mainPath, "refs/remotes/origin/development"):
		base = "development"
	default:
		if out, err := r.Git(ctx, mainPath, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
			if ref := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"); ref != "" {
				base = ref
			}
		}
	}
	return Settings{Base: base, WorktreeRoot: WorktreeRoot(mainPath), WarnShared: true}
}

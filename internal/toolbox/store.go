package toolbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store is the package home under $LANA_HOME/tools (REQ-TB-001). Every method
// is safe for concurrent use.
type Store struct {
	root string
}

// NewStore builds a Store over root, the tools directory.
func NewStore(root string) *Store {
	return &Store{root: root}
}

// Root returns the tools directory.
func (s *Store) Root() string { return s.root }

// Dir is the package home $LANA_HOME/tools/<id>.
func (s *Store) Dir(id string) string { return filepath.Join(s.root, id) }

// BinDir is the package bin directory; downloaded binaries live here.
func (s *Store) BinDir(id string) string { return filepath.Join(s.Dir(id), "bin") }

// DataDir is the package data directory; persistent data lives here.
func (s *Store) DataDir(id string) string { return filepath.Join(s.Dir(id), "data") }

// LogDir is the package log directory; install logs live here.
func (s *Store) LogDir(id string) string { return filepath.Join(s.Dir(id), "logs") }

// ManifestPath is the path of a package's tool.toml.
func (s *Store) ManifestPath(id string) string { return filepath.Join(s.Dir(id), "tool.toml") }

// Stored is one package directory read by List. Problem is set when the
// directory holds no readable tool.toml, so one broken package never hides the
// others (REQ-TB-001).
type Stored struct {
	ID        string
	Manifest  Manifest
	Problem   string
	UpdatedAt int64
}

// List reads every <id>/tool.toml. A directory without a valid manifest is
// returned with Problem set rather than failing the whole list.
func (s *Store) List() ([]Stored, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Stored, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		stored, err := s.entry(entry.Name())
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				out = append(out, Stored{ID: entry.Name(), Problem: "tool.toml is missing"})
				continue
			}
			out = append(out, Stored{ID: entry.Name(), Problem: err.Error()})
			continue
		}
		out = append(out, stored)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// entry reads one package directory. It returns ErrNotFound when the directory
// or its tool.toml does not exist and sets Problem for a manifest that does not
// parse or validate.
func (s *Store) entry(id string) (Stored, error) {
	if !idPattern.MatchString(id) {
		return Stored{}, ErrNotFound
	}
	data, err := os.ReadFile(s.ManifestPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Stored{}, ErrNotFound
		}
		return Stored{}, err
	}
	stored := Stored{ID: id}
	manifest, issues := ParseManifest(string(data))
	if len(issues) > 0 {
		stored.Problem = issuesText(issues)
		return stored, nil
	}
	stored.Manifest = manifest
	if info, err := os.Stat(s.ManifestPath(id)); err == nil {
		stored.UpdatedAt = info.ModTime().UnixMilli()
	}
	return stored, nil
}

// Get returns one manifest. It returns ErrNotFound for an unknown id and
// *InvalidManifestError for a package whose tool.toml is broken.
func (s *Store) Get(id string) (Manifest, error) {
	stored, err := s.entry(id)
	if err != nil {
		return Manifest{}, err
	}
	if stored.Problem != "" {
		return Manifest{}, &InvalidManifestError{Issues: []Issue{{Field: "tool.toml", Message: stored.Problem}}}
	}
	return stored.Manifest, nil
}

// ModTime returns the tool.toml modification time in Unix milliseconds, or 0.
func (s *Store) ModTime(id string) int64 {
	info, err := os.Stat(s.ManifestPath(id))
	if err != nil {
		return 0
	}
	return info.ModTime().UnixMilli()
}

// Save writes a manifest atomically and creates bin/, data/ and logs/ so a
// package is ready to install (REQ-TB-001).
func (s *Store) Save(m Manifest) error {
	if !idPattern.MatchString(m.ID) {
		return &InvalidManifestError{Issues: []Issue{{Field: "id", Message: fmt.Sprintf("id %q must match %s", m.ID, idPattern.String())}}}
	}
	for _, dir := range []string{s.Dir(m.ID), s.BinDir(m.ID), s.DataDir(m.ID), s.LogDir(m.ID)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	text, err := Encode(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.ManifestPath(m.ID), []byte(text), 0o600)
}

// Delete removes a package home, including its bin/, data/ and logs/. It
// returns ErrNotFound for an unknown id.
func (s *Store) Delete(id string) error {
	if !s.exists(id) {
		return ErrNotFound
	}
	return os.RemoveAll(s.Dir(id))
}

// exists reports whether a package directory exists.
func (s *Store) exists(id string) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	info, err := os.Stat(s.Dir(id))
	return err == nil && info.IsDir()
}

// writeFileAtomic writes data to path through a temp file in the same
// directory, then renames it into place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tool-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// issuesText joins issues for the Problem field of a broken package.
func issuesText(issues []Issue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Field == "" {
			parts = append(parts, issue.Message)
			continue
		}
		parts = append(parts, issue.Field+": "+issue.Message)
	}
	return strings.Join(parts, "; ")
}

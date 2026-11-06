package knowledge

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// maxSourceBytes is the largest source SaveSource accepts (REQ-KNW-020).
const maxSourceBytes = 30 << 20

// maxSourceTitle is the longest source title in runes.
const maxSourceTitle = 120

// sourceExts are the stored source types; HTML and URLs are converted to md by
// the caller before they reach SaveSource.
var sourceExts = map[string]bool{"md": true, "txt": true, "pdf": true}

// SaveSource stores one source under sources/<YYYY-MM-DD> <title>.<ext>. When
// the same bytes are already stored it returns the existing path with
// duplicate set and writes nothing (REQ-KNW-020).
func (v *Vault) SaveSource(title, ext string, data []byte) (string, bool, error) {
	ext = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(ext, ".")))
	if !sourceExts[ext] {
		return "", false, fmt.Errorf("unsupported source type %q", ext)
	}
	if len(data) > maxSourceBytes {
		return "", false, fmt.Errorf("source is larger than %d bytes", maxSourceBytes)
	}

	unlock, err := v.lock()
	if err != nil {
		return "", false, err
	}
	defer unlock()

	sum := sha256.Sum256(data)
	existing, err := v.findDuplicate(sum[:])
	if err != nil {
		return "", false, err
	}
	if existing != "" {
		return existing, true, nil
	}

	base := v.now().Format("2006-01-02") + " " + sanitizeSourceTitle(title)
	rel, err := v.freeSourceName(base, ext)
	if err != nil {
		return "", false, err
	}
	if err := writeBytes(filepath.Join(v.root, filepath.FromSlash(rel)), data); err != nil {
		return "", false, err
	}
	return rel, false, nil
}

// freeSourceName picks sources/<base>.<ext>, adding " (2)", " (3)", … on a
// clash.
func (v *Vault) freeSourceName(base, ext string) (string, error) {
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s (%d)", base, i)
		}
		rel := path.Join(sourcesDir, name+"."+ext)
		_, err := os.Stat(filepath.Join(v.root, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return rel, nil
		case err != nil:
			return "", err
		}
	}
}

// SetIndex attaches the derived index so SaveSource can compare a new source
// against indexed hashes before hashing the files itself (design §2). It is
// optional; SaveSource falls back to hashing files under sources/.
func (v *Vault) SetIndex(idx *Index) { v.index = idx }

// findDuplicate returns the vault-relative path of a source whose bytes hash to
// sum, or "" when none match. It consults the index first, then falls back to
// hashing the files under sources/ (design §2).
func (v *Vault) findDuplicate(sum []byte) (string, error) {
	if v.index != nil {
		rel, ok, err := v.index.sourceByHash(sum)
		if err != nil {
			return "", err
		}
		if ok {
			if _, err := os.Stat(filepath.Join(v.root, filepath.FromSlash(rel))); err == nil {
				return rel, nil
			}
		}
	}
	root := filepath.Join(v.root, sourcesDir)
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	sort.Strings(paths)
	for _, p := range paths {
		got, err := hashFile(p)
		if err != nil {
			return "", err
		}
		if bytes.Equal(got, sum) {
			return v.rel(p), nil
		}
	}
	return "", nil
}

// hashFile returns the SHA-256 of a file's bytes.
func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// sanitizeSourceTitle turns a title into a file name: forbidden characters
// become "-", runs of spaces collapse, the length is capped, and an empty
// result becomes "Source".
func sanitizeSourceTitle(title string) string {
	var b strings.Builder
	for _, r := range title {
		if strings.ContainsRune(forbiddenTitleChars, r) {
			b.WriteByte('-')
		} else {
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
	if runes := []rune(s); len(runes) > maxSourceTitle {
		s = strings.TrimSpace(string(runes[:maxSourceTitle]))
	}
	if s == "" {
		return "Source"
	}
	return s
}

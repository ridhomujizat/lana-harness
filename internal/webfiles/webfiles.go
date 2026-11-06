// Package webfiles writes files under $LANA_HOME atomically.
package webfiles

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultMode is used when the target does not exist, matching lana init.
const defaultMode os.FileMode = 0o644

// AtomicWrite replaces path with data through a temporary file in the same
// directory: write, fsync, close, rename over the target, then fsync the
// directory where the platform supports it. The target keeps its permission
// bits; a new file gets 0644. The temporary file is removed on failure.
func AtomicWrite(path string, data []byte) error {
	mode := defaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("webfiles: stat %s: %w", path, err)
	}
	return AtomicWriteMode(path, data, mode)
}

// AtomicWriteMode replaces path with data like AtomicWrite but sets mode on the
// temporary file, so a new file and a replaced one both end with mode. The
// caller passes the exact permission bits it needs (for example 0600 for
// secrets.env and agent files).
func AtomicWriteMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if mode == 0 {
		mode = defaultMode
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("webfiles: create temporary file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	remove := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(mode); err != nil {
		remove()
		return fmt.Errorf("webfiles: set mode on %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		remove()
		return fmt.Errorf("webfiles: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		remove()
		return fmt.Errorf("webfiles: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("webfiles: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("webfiles: rename %s to %s: %w", tmpName, path, err)
	}
	// Directory fsync is best-effort: not every platform allows syncing a
	// directory, and the rename is already durable on the platforms that do.
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

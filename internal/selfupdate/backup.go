package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/sqlitex"
)

const (
	// manifestName is the description file of one backup folder.
	manifestName = "manifest.json"
	// binaryName is the previous binary inside a backup folder.
	binaryName = "lana"
	// configName is the backed-up config file.
	configName = "config.toml"
	// KeepBackups is how many update backups stay (REQ-UPD-006).
	KeepBackups = 2
)

// Manifest is backups/<dir>/manifest.json (API-UPD-011).
type Manifest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	ExePath string `json:"exe_path"`
	// CreatedAt is unix milliseconds.
	CreatedAt int64    `json:"created_at"`
	Files     []string `json:"files"`
}

// BackupOptions describes one update backup.
type BackupOptions struct {
	Home    string
	ExePath string
	From    string
	To      string
	Now     time.Time
}

// Backup creates backups/<from>-to-<to>-<yyyymmddThhmmssZ>/ with VACUUM INTO
// copies of every *.db directly in Home, config.toml, the binary at ExePath,
// and manifest.json (REQ-UPD-003 step 5). A failure removes the partial
// folder.
func Backup(ctx context.Context, opts BackupOptions) (string, Manifest, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	paths := Paths{Home: opts.Home}
	if err := os.MkdirAll(paths.Backups(), 0o700); err != nil {
		return "", Manifest{}, err
	}
	base := safeName(opts.From) + "-to-" + safeName(opts.To) + "-" + now.UTC().Format("20060102T150405Z")
	dir := filepath.Join(paths.Backups(), base)
	for i := 2; ; i++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return "", Manifest{}, err
		}
		dir = filepath.Join(paths.Backups(), base+"-"+strconv.Itoa(i))
	}
	manifest := Manifest{From: opts.From, To: opts.To, ExePath: opts.ExePath, CreatedAt: now.UnixMilli()}
	fail := func(err error) (string, Manifest, error) {
		_ = os.RemoveAll(dir)
		return "", Manifest{}, err
	}
	dbs, err := CopyDatabases(ctx, opts.Home, dir)
	if err != nil {
		return fail(err)
	}
	manifest.Files = append(manifest.Files, dbs...)
	configPath := filepath.Join(opts.Home, configName)
	if _, err := os.Stat(configPath); err == nil {
		if err := CopyFile(configPath, filepath.Join(dir, configName), 0o600); err != nil {
			return fail(fmt.Errorf("back up %s: %w", configName, err))
		}
		manifest.Files = append(manifest.Files, configName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	if opts.ExePath != "" {
		if err := CopyFile(opts.ExePath, filepath.Join(dir, binaryName), 0o755); err != nil {
			return fail(fmt.Errorf("back up the binary: %w", err))
		}
		manifest.Files = append(manifest.Files, binaryName)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := atomicWrite(filepath.Join(dir, manifestName), append(data, '\n'), 0o600); err != nil {
		return fail(err)
	}
	return dir, manifest, nil
}

// ListDatabases returns the base names of the regular *.db files directly in
// home, sorted.
func ListDatabases(home string) ([]string, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out, nil
}

// CopyDatabases writes a VACUUM INTO copy of every *.db in home into dst and
// returns the copied base names. The source stays readable by other
// connections while it runs.
func CopyDatabases(ctx context.Context, home, dst string) ([]string, error) {
	names, err := ListDatabases(home)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if err := VacuumInto(ctx, filepath.Join(home, name), filepath.Join(dst, name)); err != nil {
			return nil, fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return names, nil
}

// VacuumInto writes a consistent copy of the SQLite database src to dst. dst
// must not exist.
func VacuumInto(ctx context.Context, src, dst string) error {
	db, err := sqlitex.Open("file:" + src + "?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		_ = os.Remove(dst)
		return err
	}
	_ = os.Chmod(dst, 0o600)
	return nil
}

// ReadManifest reads manifest.json of a backup folder.
func ReadManifest(dir string) (Manifest, error) {
	var manifest Manifest
	if err := readJSON(filepath.Join(dir, manifestName), &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// BackupInfo is one backup folder with its manifest.
type BackupInfo struct {
	Dir      string
	Manifest Manifest
}

// ListBackups returns the backup folders, newest first. A folder without a
// readable manifest is ordered by its modification time.
func ListBackups(p Paths) ([]BackupInfo, error) {
	entries, err := os.ReadDir(p.Backups())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BackupInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(p.Backups(), entry.Name())
		manifest, err := ReadManifest(dir)
		if err != nil {
			info, statErr := entry.Info()
			if statErr != nil {
				continue
			}
			manifest = Manifest{CreatedAt: info.ModTime().UnixMilli()}
		}
		out = append(out, BackupInfo{Dir: dir, Manifest: manifest})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Manifest.CreatedAt != out[j].Manifest.CreatedAt {
			return out[i].Manifest.CreatedAt > out[j].Manifest.CreatedAt
		}
		return out[i].Dir > out[j].Dir
	})
	return out, nil
}

// NewestBackup returns the newest backup with a readable manifest.
func NewestBackup(p Paths) (BackupInfo, bool, error) {
	backups, err := ListBackups(p)
	if err != nil {
		return BackupInfo{}, false, err
	}
	for _, backup := range backups {
		if _, err := ReadManifest(backup.Dir); err == nil {
			return backup, true, nil
		}
	}
	return BackupInfo{}, false, nil
}

// Prune keeps the newest keep backups and removes the rest (REQ-UPD-006).
func Prune(p Paths, keep int) error {
	backups, err := ListBackups(p)
	if err != nil {
		return err
	}
	var errs []error
	for i, backup := range backups {
		if i < keep {
			continue
		}
		if err := os.RemoveAll(backup.Dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Restore puts a backup back: each backed-up *.db over the live one (its -wal
// and -shm removed first), config.toml, and the previous binary over exePath
// through a temp file and rename (design.md, Rollback). exePath "" means the
// manifest's exe_path.
func Restore(dir, home, exePath string) error {
	manifest, err := ReadManifest(dir)
	if err != nil {
		return err
	}
	if exePath == "" {
		exePath = manifest.ExePath
	}
	for _, name := range manifest.Files {
		if name != filepath.Base(name) {
			return fmt.Errorf("backup %s lists an invalid file %q", dir, name)
		}
		src := filepath.Join(dir, name)
		switch {
		case strings.HasSuffix(name, ".db"):
			if err := restoreDatabase(src, filepath.Join(home, name)); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		case name == configName:
			if err := CopyFile(src, filepath.Join(home, configName), configMode(filepath.Join(home, configName))); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		case name == binaryName:
			if exePath == "" {
				return errors.New("restore the binary: the backup has no exe_path")
			}
			if err := CopyFile(src, exePath, 0o755); err != nil {
				return fmt.Errorf("restore the binary: %w", err)
			}
		}
	}
	return nil
}

func configMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o644
}

// restoreDatabase copies src next to dst, removes dst's -wal and -shm, and
// renames the copy over dst, so the old WAL never replays onto the backup.
func restoreDatabase(src, dst string) error {
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".restore-"+strconv.Itoa(os.Getpid()))
	if err := copyContents(src, tmp, 0o600); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dst + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// CopyFile copies src over dst through a temp file in dst's folder and a
// rename, so dst is never half written.
func CopyFile(src, dst string, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(dst), ".lana-copy-"+strconv.Itoa(os.Getpid())+"-"+filepath.Base(dst))
	if err := copyContents(src, tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func copyContents(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(mode)
	}
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// ReplaceExecutable moves the verified download src over exePath. A rename
// works when both are on one filesystem; otherwise src is copied to
// <exe_dir>/.lana-new-<pid> first and renamed from there (design.md,
// installing). The new file is 0755.
func ReplaceExecutable(src, exePath string) error {
	if err := os.Chmod(src, 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, exePath); err == nil {
		return nil
	}
	staged := filepath.Join(filepath.Dir(exePath), ".lana-new-"+strconv.Itoa(os.Getpid()))
	if err := copyContents(src, staged, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, exePath); err != nil {
		_ = os.Remove(staged)
		return err
	}
	_ = os.Remove(src)
	return nil
}

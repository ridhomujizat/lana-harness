// Package claudeshare links a Router Claude Code account folder to the default
// Claude Code folder (spec 77 REQ-WRA-001), so /resume, --continue, auto
// memory, prompt history, settings, the user CLAUDE.md, skills, subagents and
// commands are the same whichever account a Workbench pane runs on.
//
// It touches only the items it shares. Link never reads or writes .claude.json,
// .credentials.json or the Keychain; SeedClaudeJSON (REQ-WRA-012) only writes
// the .claude.json of an account that never signed in, without login keys.
// LinkAntigravity does the same for the conversations and settings of an
// Antigravity account HOME (spec 77 REQ-WRA-010, REQ-WRA-011).
package claudeshare

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Action is what Link did with one item of the account folder.
type Action string

const (
	// Linked: the account had nothing at the name; it now links to the default
	// item.
	Linked Action = "linked"
	// Adopted: the account had a real item; its content went into the default
	// item, the leftover was renamed *.before-share-<unix>, then it was linked.
	Adopted Action = "adopted"
	// Kept: the account already had a link (any target) or its own item, which
	// stays as it is.
	Kept Action = "kept"
	// Skipped: the default folder has no such item, so nothing is linked.
	Skipped Action = "skipped"
	// Copied: one value was copied from the default item into the account's
	// own file (no link), which was created when missing.
	Copied Action = "copied"
)

// Item is the outcome for one shared name.
type Item struct {
	Name   string
	Action Action
	// Backup is the renamed leftover of an adopted item ("" when nothing was
	// left behind).
	Backup string
}

// Report lists what Link did, one Item per shared name. A no-op run (the
// account folder is the default folder, or a folder is empty) has no items.
type Report struct {
	Items []Item
}

// Changed reports whether Link linked, adopted or copied anything.
func (r Report) Changed() bool {
	for _, item := range r.Items {
		if item.Action == Linked || item.Action == Adopted || item.Action == Copied {
			return true
		}
	}
	return false
}

// Shared item names.
const (
	projectsName = "projects"
	historyName  = "history.jsonl"
)

// optionalNames are linked only when the default folder has them and the
// account has nothing at the name.
var optionalNames = []string{"settings.json", "CLAUDE.md", "skills", "agents", "commands"}

// dirMode is the mode of a folder Link creates; fileMode of a file it creates.
const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// mu serialises Link runs, so the daemon-start pass and an account save never
// move the same files at once.
var mu sync.Mutex

// Link links the shared items of accountDir to defaultDir (REQ-WRA-001):
//
//   - projects/: the default folder is created (0700) when missing; a real
//     account folder is adopted (each entry moved into the shared folder, a
//     name the shared folder already has stays behind, the rest renamed
//     projects.before-share-<unix>), then linked.
//   - history.jsonl: the default file is created empty (0600) when missing; a
//     real account file is adopted (its lines appended to the shared file, then
//     renamed history.jsonl.before-share-<unix>), then linked.
//   - settings.json, CLAUDE.md, skills/, agents/, commands/: linked only when
//     the default item exists and the account has nothing at the name.
//
// An existing link is kept whatever it points at. An empty folder, or an
// account folder that is the default folder, is a no-op. Links use absolute
// targets. Each item is done on its own; the errors of all failed items are
// joined, and the report still lists the items that were done.
func Link(accountDir, defaultDir string, now time.Time) (Report, error) {
	if accountDir == "" || defaultDir == "" {
		return Report{}, nil
	}
	account, err := filepath.Abs(accountDir)
	if err != nil {
		return Report{}, err
	}
	shared, err := filepath.Abs(defaultDir)
	if err != nil {
		return Report{}, err
	}
	if sameDir(account, shared) {
		return Report{}, nil
	}
	// A folder that does not exist yet has nothing to link; the account save
	// creates it and links then.
	if info, err := os.Stat(account); errors.Is(err, os.ErrNotExist) {
		return Report{}, nil
	} else if err != nil {
		return Report{}, err
	} else if !info.IsDir() {
		return Report{}, fmt.Errorf("claudeshare: account folder %s is not a folder", account)
	}

	mu.Lock()
	defer mu.Unlock()

	stamp := strconv.FormatInt(now.Unix(), 10)
	var report Report
	var errs []error
	add := func(item Item, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("claudeshare: %s: %w", item.Name, err))
			return
		}
		report.Items = append(report.Items, item)
	}
	add(linkDir(account, shared, projectsName, stamp))
	add(linkAppendFile(account, shared, historyName, stamp))
	for _, name := range optionalNames {
		add(linkOptional(account, shared, name))
	}
	return report, errors.Join(errs...)
}

// sameDir reports whether a and b name the same folder, comparing the cleaned
// paths and, where they resolve, the paths with symlinks evaluated.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// isLink reports whether path is a symlink (dangling or not).
func isLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// exists reports whether something (a link included) is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// linkDir shares the folder name (projects/ for Claude Code): the shared
// folder is created when missing and a real account folder is merged into it.
func linkDir(account, shared, name, stamp string) (Item, error) {
	item := Item{Name: name}
	src := filepath.Join(account, name)
	dst := filepath.Join(shared, name)
	if isLink(src) {
		item.Action = Kept
		return item, nil
	}
	if err := os.MkdirAll(dst, dirMode); err != nil {
		return item, err
	}
	if info, err := os.Stat(dst); err != nil {
		return item, err
	} else if !info.IsDir() {
		return item, fmt.Errorf("%s is not a folder", dst)
	}
	info, err := os.Lstat(src)
	switch {
	case errors.Is(err, os.ErrNotExist):
		item.Action = Linked
	case err != nil:
		return item, err
	case info.IsDir():
		empty, err := mergeDir(src, dst)
		if err != nil {
			return item, err
		}
		if empty {
			if err := os.Remove(src); err != nil {
				return item, err
			}
		} else if item.Backup, err = moveAside(src, stamp); err != nil {
			return item, err
		}
		item.Action = Adopted
	default:
		// A file with the folder's name: keep it aside, then link.
		if item.Backup, err = moveAside(src, stamp); err != nil {
			return item, err
		}
		item.Action = Adopted
	}
	return item, os.Symlink(dst, src)
}

// mergeDir moves every entry of src into dst. A name dst does not have is
// renamed into it; a folder both have is merged the same way; any other name
// dst already has stays behind in src. Emptied sub-folders of src are removed.
// It reports whether src is empty afterwards. An entry that cannot be renamed
// (another volume) stays behind too.
func mergeDir(src, dst string) (bool, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return false, err
	}
	left := 0
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		toInfo, err := os.Lstat(to)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(from, to); err != nil {
				left++
			}
			continue
		}
		if err != nil {
			return false, err
		}
		fromInfo, err := os.Lstat(from)
		if err != nil {
			return false, err
		}
		if fromInfo.IsDir() && toInfo.IsDir() {
			empty, err := mergeDir(from, to)
			if err != nil {
				return false, err
			}
			if empty {
				if err := os.Remove(from); err != nil {
					return false, err
				}
				continue
			}
		}
		left++
	}
	return left == 0, nil
}

// moveAside renames path to <path>.before-share-<stamp>, adding -2, -3, … when
// that name is taken, and returns the new path.
func moveAside(path, stamp string) (string, error) {
	base := path + ".before-share-" + stamp
	backup := base
	for i := 2; exists(backup); i++ {
		backup = base + "-" + strconv.Itoa(i)
	}
	return backup, os.Rename(path, backup)
}

// linkAppendFile shares the JSON-lines file name (history.jsonl): the shared
// file is created empty when missing and a real account file is appended to
// it.
func linkAppendFile(account, shared, name, stamp string) (Item, error) {
	item := Item{Name: name}
	src := filepath.Join(account, name)
	dst := filepath.Join(shared, name)
	if isLink(src) {
		item.Action = Kept
		return item, nil
	}
	if err := ensureFile(dst); err != nil {
		return item, err
	}
	info, err := os.Lstat(src)
	switch {
	case errors.Is(err, os.ErrNotExist):
		item.Action = Linked
	case err != nil:
		return item, err
	case info.Mode().IsRegular():
		if err := appendLines(src, dst); err != nil {
			return item, err
		}
		if item.Backup, err = moveAside(src, stamp); err != nil {
			return item, err
		}
		item.Action = Adopted
	default:
		return item, fmt.Errorf("%s is not a file", src)
	}
	return item, os.Symlink(dst, src)
}

// ensureFile creates path empty (0600), and its folder (0700), when it is
// missing. Something at path that is not a file is an error.
func ensureFile(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a file", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// appendLines appends the bytes of src to dst, keeping a newline between the
// two and at the end, so every line stays one JSON line.
func appendLines(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	f, err := os.OpenFile(dst, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	needNewline, err := endsWithoutNewline(f)
	if err != nil {
		_ = f.Close()
		return err
	}
	var buf []byte
	if needNewline {
		buf = append(buf, '\n')
	}
	buf = append(buf, data...)
	if buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// endsWithoutNewline reports whether f is not empty and its last byte is not a
// newline.
func endsWithoutNewline(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return last[0] != '\n', nil
}

// linkOptional links name when the default item exists and the account has
// nothing at the name. name may hold a sub-folder (cache/x.json); the account's
// sub-folder is created (0700) when missing.
func linkOptional(account, shared, name string) (Item, error) {
	item := Item{Name: name}
	src := filepath.Join(account, name)
	dst := filepath.Join(shared, name)
	if exists(src) {
		item.Action = Kept
		return item, nil
	}
	if _, err := os.Stat(dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			item.Action = Skipped
			return item, nil
		}
		return item, err
	}
	if err := os.MkdirAll(filepath.Dir(src), dirMode); err != nil {
		return item, err
	}
	item.Action = Linked
	return item, os.Symlink(dst, src)
}

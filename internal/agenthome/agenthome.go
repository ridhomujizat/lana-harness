// Package agenthome manages the per-agent folder under $LANA_HOME/agents/<name>/:
// the persona file, the memory file, the shared user file under $LANA_HOME, and
// the entry format they share.
package agenthome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Size limits in Unicode code points.
const (
	UserLimit = 2000
	// MemoryLimit is the limit of MEMORY.md, in Unicode code points
	// (REQ-DKN-040).
	MemoryLimit = 8000
	// JobLimit is the limit of JOB.md, the agent job description
	// (REQ-ROOM-009, API-ROOM-007).
	JobLimit = 4000
)

// Separator is the line that separates entries.
const Separator = "§"

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Errors returned by Apply.
var (
	ErrOverLimit = errors.New("over limit")
	ErrNoMatch   = errors.New("no match")
	ErrAmbiguous = errors.New("ambiguous match")
	ErrInvalid   = errors.New("invalid input")
)

// ApplyError carries the details of a failed Apply.
type ApplyError struct {
	Kind    error
	Size    int
	Limit   int
	Matches int
	Field   string
}

func (e *ApplyError) Error() string {
	switch e.Kind {
	case ErrOverLimit:
		return fmt.Sprintf("would be %d/%d characters", e.Size, e.Limit)
	case ErrAmbiguous:
		return fmt.Sprintf("matches %d entries", e.Matches)
	case ErrInvalid:
		return e.Field
	}
	return e.Kind.Error()
}

func (e *ApplyError) Unwrap() error { return e.Kind }

// Home holds the paths of one agent home.
type Home struct {
	Dir    string
	Agent  string
	Memory string
	// Job is JOB.md, the agent job description (API-ROOM-007).
	Job string
	// Avatar is avatar.svg, the generated avatar (spec 43, REQ-AVT-006).
	Avatar string
}

// Paths returns the paths of agent name under lanaHome.
func Paths(lanaHome, name string) Home {
	dir := filepath.Join(lanaHome, "agents", name)
	return Home{
		Dir:    dir,
		Agent:  filepath.Join(dir, "AGENT.md"),
		Memory: filepath.Join(dir, "MEMORY.md"),
		Job:    filepath.Join(dir, "JOB.md"),
		Avatar: filepath.Join(dir, "avatar.svg"),
	}
}

// SharedUser returns the path of the install-wide user file under lanaHome.
// Every agent reads the same file.
func SharedUser(lanaHome string) string { return filepath.Join(lanaHome, "USER.md") }

// ValidName reports whether name is a valid agent name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Parse splits text into entries. Empty entries are dropped.
func Parse(text string) []string {
	var entries []string
	var current []string
	flush := func() {
		entry := strings.TrimSpace(strings.Join(current, "\n"))
		if entry != "" {
			entries = append(entries, entry)
		}
		current = current[:0]
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == Separator {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return entries
}

// Join renders entries as file text without a trailing newline.
func Join(entries []string) string { return strings.Join(entries, "\n"+Separator+"\n") }

// Size is the size of entries in code points.
func Size(entries []string) int { return utf8.RuneCountInString(Join(entries)) }

// ReadEntries reads the entries of path. A missing file has no entries.
func ReadEntries(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(data)), nil
}

// Apply applies one memory action to entries and checks the limit.
func Apply(entries []string, action, content, oldText string, limit int) ([]string, error) {
	content = strings.TrimSpace(content)
	if strings.Contains("\n"+content+"\n", "\n"+Separator+"\n") || len(Parse(content)) > 1 {
		return nil, &ApplyError{Kind: ErrInvalid, Field: "content must not contain a " + Separator + " line"}
	}
	oldSize := Size(entries)
	out := append([]string(nil), entries...)
	switch action {
	case "add":
		if content == "" {
			return nil, &ApplyError{Kind: ErrInvalid, Field: "content is required"}
		}
		out = append(out, content)
	case "replace", "remove":
		if action == "replace" && content == "" {
			return nil, &ApplyError{Kind: ErrInvalid, Field: "content is required"}
		}
		if strings.TrimSpace(oldText) == "" {
			return nil, &ApplyError{Kind: ErrInvalid, Field: "old_text is required"}
		}
		index, matches := -1, 0
		for i, entry := range out {
			if strings.Contains(entry, oldText) {
				index = i
				matches++
			}
		}
		if matches == 0 {
			return nil, &ApplyError{Kind: ErrNoMatch}
		}
		if matches > 1 {
			return nil, &ApplyError{Kind: ErrAmbiguous, Matches: matches}
		}
		if action == "replace" {
			out[index] = content
		} else {
			out = append(out[:index], out[index+1:]...)
		}
	default:
		return nil, &ApplyError{Kind: ErrInvalid, Field: "action must be add, replace, or remove"}
	}
	// REQ-DKN-040: a change that does not grow the file is accepted even when
	// it is over the limit, so an existing file is never cut.
	if size := Size(out); size > limit && size > oldSize {
		return nil, &ApplyError{Kind: ErrOverLimit, Size: size, Limit: limit}
	}
	return out, nil
}

// RemoveEntries removes the entries at the given 1-based ids from path, under
// the same file lock and atomic write as Update, and returns the removed texts
// in id order. An unknown id is an error and writes nothing.
func RemoveEntries(path string, ids []int) (removed []string, err error) {
	if len(ids) == 0 {
		return nil, nil
	}
	_, err = Update(path, func(entries []string) ([]string, error) {
		seen := make(map[int]bool, len(ids))
		for _, id := range ids {
			if id < 1 || id > len(entries) {
				return nil, fmt.Errorf("no entry m%d", id)
			}
			if seen[id] {
				return nil, fmt.Errorf("entry m%d is listed twice", id)
			}
			seen[id] = true
		}
		order := append([]int(nil), ids...)
		sort.Ints(order)
		removed = make([]string, 0, len(order))
		for i := len(order) - 1; i >= 0; i-- {
			id := order[i]
			removed = append(removed, entries[id-1])
			entries = append(entries[:id-1], entries[id:]...)
		}
		for i, j := 0, len(removed)-1; i < j; i, j = i+1, j-1 {
			removed[i], removed[j] = removed[j], removed[i]
		}
		return entries, nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// Update locks path, reads its entries, applies fn, and replaces the file atomically.
func Update(path string, fn func([]string) ([]string, error)) ([]string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	entries, err := ReadEntries(path)
	if err != nil {
		return nil, err
	}
	next, err := fn(entries)
	if err != nil {
		return nil, err
	}
	if err := WriteFile(path, Join(next)); err != nil {
		return nil, err
	}
	return next, nil
}

// WriteFile writes text to path atomically with mode 0600. A non-empty text
// without a trailing newline gets one.
func WriteFile(path, text string) error {
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeBytes(path, []byte(text))
}

// writeBytes writes data to path atomically with mode 0600.
func writeBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

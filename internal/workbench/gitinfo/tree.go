package gitinfo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// treeMaxEntries is how many entries one folder of the file tree returns before
// it is cut and Truncated is set (REQ-WG-007). It is a variable so a test can
// lower it instead of building two thousand files.
var treeMaxEntries = 2000

// The two kinds of a file tree entry (REQ-WG-007).
const (
	treeDirKind  = "dir"
	treeFileKind = "file"
)

// maxFileTextSize is the largest text file whose content is returned
// (REQ-WG-009): a bigger text file keeps only its kind and size.
const maxFileTextSize = 1 << 20

// maxSearchResults caps the file search answer (REQ-WG-008).
const maxSearchResults = 50

// TreeEntry is one row of the file tree (REQ-WG-007). Status is the git status
// letter of the uncommitted scope, Changed marks a folder with a changed path
// below it, Ignored marks a path git ignores, and Size is set for files only.
type TreeEntry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Status  string `json:"status,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Ignored bool   `json:"ignored"`
	Size    int64  `json:"size,omitempty"`
}

// TreeList is the answer of REQ-WG-007. Entries is never null.
type TreeList struct {
	Dir       string      `json:"dir"`
	Truncated bool        `json:"truncated"`
	Entries   []TreeEntry `json:"entries"`
}

// Tree lists one folder of a worktree (REQ-WG-007): folders first, then files,
// each group case-insensitively sorted, with the git status of every file, the
// changed mark of every folder and the ignored flag from git. A symlink is
// listed as a file and never followed, and ".git" is never listed. A folder
// with more than treeMaxEntries entries keeps the first treeMaxEntries and sets
// Truncated. dir is worktree-relative ("" is the root); a path that climbs out
// of the worktree is ErrForbiddenPath and a folder that does not exist or is not
// a folder is ErrNotFound.
func (r *Runner) Tree(ctx context.Context, worktree, dir string) (TreeList, error) {
	if err := r.CheckPath(worktree); err != nil {
		return TreeList{}, err
	}
	rel, ok := treeDir(dir)
	if !ok {
		return TreeList{}, ErrForbiddenPath
	}
	abs := filepath.Join(worktree, rel)
	if err := r.CheckPath(resolvedLeaf(abs)); err != nil {
		return TreeList{}, err
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return TreeList{}, ErrNotFound
	}

	statuses, err := r.uncommittedStatuses(ctx, worktree)
	if err != nil {
		return TreeList{}, err
	}
	ignored, err := r.ignoredEntries(ctx, worktree)
	if err != nil {
		return TreeList{}, err
	}
	ign := buildIgnored(ignored)

	dirs, files := []TreeEntry{}, []TreeEntry{}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		path := joinEntry(rel, name)
		if entry.IsDir() {
			dirs = append(dirs, TreeEntry{
				Name:    name,
				Kind:    treeDirKind,
				Changed: changedBelow(statuses, path),
				Ignored: ign.has(path),
			})
			continue
		}
		fe := TreeEntry{
			Name:    name,
			Kind:    treeFileKind,
			Status:  statuses[path],
			Ignored: ign.has(path),
		}
		// A symlink is listed as a file and its info is the link itself, not
		// its target (REQ-WG-007).
		if info, err := entry.Info(); err == nil {
			fe.Size = info.Size()
		}
		files = append(files, fe)
	}

	sortEntries(dirs)
	sortEntries(files)
	all := append(dirs, files...)
	if len(all) > treeMaxEntries {
		all = all[:treeMaxEntries]
		return TreeList{Dir: rel, Truncated: true, Entries: all}, nil
	}
	return TreeList{Dir: rel, Entries: all}, nil
}

// Search returns the best file matches of a query for the ⌘P filter
// (REQ-WG-008): a case-insensitive subsequence match over the whole relative
// path, with files whose base name holds the query as a substring first, then
// the shorter match span, the shorter path and finally the lexical path. At
// most maxSearchResults are returned and the result is never nil; an empty or
// whitespace-only query matches nothing.
func (r *Runner) Search(ctx context.Context, worktree, q string) ([]string, error) {
	if err := r.CheckPath(worktree); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(q)
	if query == "" {
		return []string{}, nil
	}
	files, err := r.trackedFiles(ctx, worktree)
	if err != nil {
		if isNotARepo(err) {
			return nil, ErrNotARepo
		}
		return nil, err
	}

	lower := strings.ToLower(query)
	matches := []searchMatch{}
	for _, file := range files {
		first, last, ok := subsequence(file, lower)
		if !ok {
			continue
		}
		matches = append(matches, searchMatch{
			path: file,
			base: strings.Contains(strings.ToLower(filepath.Base(file)), lower),
			span: last - first,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.base != b.base {
			return a.base
		}
		if a.span != b.span {
			return a.span < b.span
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	if len(matches) > maxSearchResults {
		matches = matches[:maxSearchResults]
	}
	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m.path
	}
	return result, nil
}

// searchMatch is one candidate of Search with the rank keys it is sorted on.
type searchMatch struct {
	path string
	base bool
	span int
}

// FileContent is the answer of REQ-WG-009. A text file up to 1 MB carries Text,
// Lines and Language; any other kind (image, pdf, html, markdown, binary, over
// 1 MB) keeps Kind and Size only.
type FileContent struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Lines    int    `json:"lines,omitempty"`
	Language string `json:"language,omitempty"`
	Text     string `json:"text,omitempty"`
}

// File returns the content of one file of a worktree (REQ-WG-009). A path that
// climbs out of the worktree or that the policy refuses is ErrForbiddenPath; a
// missing path or a folder is ErrNotFound.
func (r *Runner) File(ctx context.Context, worktree, path string) (FileContent, error) {
	if err := r.CheckPath(worktree); err != nil {
		return FileContent{}, err
	}
	rel, ok := relativePath(path)
	if !ok {
		return FileContent{}, ErrForbiddenPath
	}
	abs := filepath.Join(worktree, rel)
	if err := r.CheckPath(resolvedLeaf(abs)); err != nil {
		return FileContent{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return FileContent{}, ErrNotFound
	}
	kind := preview.KindOf(abs, info.Size())
	content := FileContent{Path: rel, Kind: string(kind), Size: info.Size()}
	if kind != preview.KindText || info.Size() > maxFileTextSize {
		return content, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return FileContent{}, ErrNotFound
		}
		return FileContent{}, err
	}
	content.Text = string(data)
	content.Lines = countLines(data)
	content.Language = languageOf(rel)
	return content, nil
}

// treeDir cleans a worktree-relative folder and refuses an absolute path or one
// that climbs out of the worktree (REQ-WG-007). An empty dir is the root.
func treeDir(dir string) (string, bool) {
	if dir == "" {
		return "", true
	}
	rel, ok := relativePath(dir)
	if !ok {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// joinEntry is the worktree-relative git path of an entry, which always uses a
// forward slash.
func joinEntry(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// sortEntries orders entries case-insensitively, with the exact name as the
// tie-break so the order is stable (REQ-WG-007).
func sortEntries(entries []TreeEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if a != b {
			return a < b
		}
		return entries[i].Name < entries[j].Name
	})
}

// uncommittedStatuses maps every changed path of a worktree to its status letter
// for the uncommitted scope, for the tree of REQ-WG-007.
func (r *Runner) uncommittedStatuses(ctx context.Context, worktree string) (map[string]string, error) {
	status, err := r.Status(ctx, worktree, ScopeUncommitted, "")
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]string, len(status.Files))
	for _, file := range status.Files {
		statuses[file.Path] = file.Status
	}
	return statuses, nil
}

// changedBelow reports whether any changed path sits below a folder, which marks
// the folder as changed (REQ-WG-007).
func changedBelow(statuses map[string]string, folder string) bool {
	prefix := folder + "/"
	for path := range statuses {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// ignoredEntries returns the ignored paths of a worktree from
// `git ls-files --others --ignored --exclude-standard --directory`, cached per
// worktree for lsFilesTTL (REQ-WG-007). `--directory` reports an ignored folder
// as one entry with a trailing slash instead of every file below it.
func (r *Runner) ignoredEntries(ctx context.Context, worktree string) ([]string, error) {
	key := filepath.Clean(worktree)
	if values, ok := r.caches.ignored.get(key, r.clock()); ok {
		return values, nil
	}
	out, err := r.Git(ctx, worktree, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	values := splitNUL(out)
	r.caches.ignored.put(key, values, r.clock())
	return values, nil
}

// ignoredSet is the ignored paths of a worktree split into exact files and
// folder prefixes, so a listing can test each entry at once (REQ-WG-007).
type ignoredSet struct {
	files []string
	dirs  []string
}

// buildIgnored splits the raw ignored paths into files and folders.
func buildIgnored(entries []string) ignoredSet {
	var set ignoredSet
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry, "/"); ok {
			set.dirs = append(set.dirs, name)
			continue
		}
		set.files = append(set.files, entry)
	}
	return set
}

// has reports whether a worktree-relative path is ignored: it is one of the
// ignored files, or it is an ignored folder or below one.
func (s ignoredSet) has(path string) bool {
	for _, file := range s.files {
		if path == file {
			return true
		}
	}
	for _, dir := range s.dirs {
		if path == dir || strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

// trackedFiles returns the tracked and untracked-not-ignored files of a worktree
// from `git ls-files -co --exclude-standard`, cached per worktree for
// lsFilesTTL (REQ-WG-008).
func (r *Runner) trackedFiles(ctx context.Context, worktree string) ([]string, error) {
	key := filepath.Clean(worktree)
	if values, ok := r.caches.files.get(key, r.clock()); ok {
		return values, nil
	}
	out, err := r.Git(ctx, worktree, "ls-files", "-co", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	values := splitNUL(out)
	r.caches.files.put(key, values, r.clock())
	return values, nil
}

// subsequence reports whether query is a subsequence of path, ignoring case (the
// query is already lowercased), and the byte index of its first and last matched
// character (REQ-WG-008).
func subsequence(path, query string) (first, last int, ok bool) {
	p := strings.ToLower(path)
	first, last = -1, -1
	i := 0
	for j := 0; j < len(p) && i < len(query); j++ {
		if p[j] == query[i] {
			if first < 0 {
				first = j
			}
			last = j
			i++
		}
	}
	if i < len(query) {
		return -1, -1, false
	}
	return first, last, true
}

// countLines is the line count of file content (REQ-WG-009): the number of
// newlines, plus one for a non-empty file with no trailing newline. An empty
// file has no lines.
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// languageOf names the language of a file from its extension for the viewer
// (REQ-WG-009); a file with any other extension is "plain".
func languageOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return "typescript"
	case ".py":
		return "python"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".css":
		return "css"
	case ".html", ".htm":
		return "html"
	case ".sql":
		return "sql"
	case ".rs":
		return "rust"
	case ".swift":
		return "swift"
	default:
		return "plain"
	}
}

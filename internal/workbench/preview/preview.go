// Package preview turns the links a Workbench terminal reports into local
// files to show in the browser preview panel (spec 61, REQ-WP-001..007). It is
// pure: it reads the file system to classify a file and to check the path
// policy, but it knows nothing of the web server or the Workbench core, so its
// rules are tested on their own.
package preview

import (
	"bytes"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxSize is the most a file may be and still be shown: anything larger that
// is not an image or a PDF is treated as binary (REQ-WP-002).
const MaxSize = 5 * 1024 * 1024

// headBytes is how much of an unknown file is read to tell text from binary
// (REQ-WP-002).
const headBytes = 8 * 1024

// ParseLink turns a link the terminal reported into a local path and the line
// and column its ":line" or ":line:col" suffix names (REQ-WP-001). A file://
// URL is taken as a path (its escapes undone); a ~ is expanded with home and a
// relative path is resolved against cwd. ok is false for an empty link and for
// any URL with a scheme other than file://, so http(s) links are refused.
func ParseLink(link, cwd, home string) (path string, line, col int, ok bool) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", 0, 0, false
	}
	switch {
	case strings.HasPrefix(link, "file://"):
		link = fileURLPath(link)
	case strings.Contains(link, "://"):
		return "", 0, 0, false
	}
	link, line, col = SplitLineCol(link)
	if link == "" {
		return "", 0, 0, false
	}
	link = ExpandHome(link, home)
	if !filepath.IsAbs(link) && cwd != "" {
		link = filepath.Join(cwd, link)
	}
	link = filepath.Clean(link)
	if link == "" || link == "." {
		return "", 0, 0, false
	}
	return link, line, col, true
}

// ExpandHome replaces a leading ~ or ~/ with home (REQ-WP-001). Anything else,
// including an empty home, is returned unchanged.
func ExpandHome(path, home string) string {
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	}
	return path
}

// IsLocalLink reports whether a link names a local file: it is not empty and
// carries no URL scheme other than file:// (REQ-WP-001). A link that is not
// local is refused with 400.
func IsLocalLink(link string) bool {
	link = strings.TrimSpace(link)
	if link == "" {
		return false
	}
	if strings.HasPrefix(link, "file://") {
		return true
	}
	return !strings.Contains(link, "://")
}

// IsRelative reports whether a link names a path relative to the folder the
// terminal is in (REQ-WP-001): a plain path with no scheme, no leading slash
// and no ~.
func IsRelative(link string) bool {
	_, ok := RelativePath(link)
	return ok
}

// RelativePath returns the path a relative link names, with no base folder
// joined to it (REQ-WP-001). ok is false for a link that is not a relative
// path: an empty link, a URL, a file:// URL, an absolute path or a ~ path. It
// lets the caller try the path against more than one folder.
func RelativePath(link string) (string, bool) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", false
	}
	switch {
	case strings.HasPrefix(link, "file://"):
		return "", false
	case strings.Contains(link, "://"):
		return "", false
	}
	link, _, _ = SplitLineCol(link)
	if link == "" || link == "~" || strings.HasPrefix(link, "~/") {
		return "", false
	}
	if filepath.IsAbs(link) {
		return "", false
	}
	return link, true
}

// fileURLPath is the path of a file:// URL: the scheme and any host are
// dropped and the escapes of the path are undone.
func fileURLPath(link string) string {
	rest := strings.TrimPrefix(link, "file://")
	if !strings.HasPrefix(rest, "/") {
		// A host, as in file://localhost/tmp/x: the path starts at the
		// first slash, or the URL names no path at all.
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[i:]
		} else {
			rest = "/"
		}
	}
	if u, err := url.PathUnescape(rest); err == nil {
		rest = u
	}
	return rest
}

// SplitLineCol takes a trailing ":line" or ":line:col" off a link, when both
// numbers are there. A colon that is not followed by digits is left in place,
// so a file name with one is not cut.
func SplitLineCol(link string) (string, int, int) {
	i := strings.LastIndexByte(link, ':')
	if i < 0 {
		return link, 0, 0
	}
	n, err := strconv.Atoi(link[i+1:])
	if err != nil || n <= 0 {
		return link, 0, 0
	}
	rest := link[:i]
	if j := strings.LastIndexByte(rest, ':'); j >= 0 {
		if m, err := strconv.Atoi(rest[j+1:]); err == nil && m > 0 {
			return rest[:j], m, n
		}
	}
	return rest, n, 0
}

// Resolve returns path with its symlinks resolved, and the cleaned path when
// it does not exist. The path policy runs on the resolved path (REQ-WP-003).
func Resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// EscapeName escapes a relative name for use in a preview URL: each path
// segment with url.PathEscape, the separators kept (REQ-WP-002).
func EscapeName(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// Kind is how the preview shows a file (REQ-WP-002).
type Kind string

const (
	// KindHTML is a page shown with its relative assets.
	KindHTML Kind = "html"
	// KindMarkdown is Markdown rendered as formatted HTML.
	KindMarkdown Kind = "markdown"
	// KindImage is a picture shown as it is.
	KindImage Kind = "image"
	// KindPDF is a PDF shown as it is.
	KindPDF Kind = "pdf"
	// KindText is any other text file, shown as code with line numbers.
	KindText Kind = "text"
	// KindBinary is anything else.
	KindBinary Kind = "binary"
)

// htmlExts, markdownExts and imageExts are the extensions that map to a kind
// on their own.
var (
	htmlExts     = map[string]bool{"html": true, "htm": true}
	markdownExts = map[string]bool{"md": true, "markdown": true}
	imageExts    = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "svg": true}
)

// IsImagePath reports whether a path's extension names an image.
func IsImagePath(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return imageExts[ext]
}

// textExts are the extensions of files shown as code (REQ-WP-002).
var textExts = map[string]bool{
	"go": true, "ts": true, "tsx": true, "js": true, "jsx": true, "mjs": true, "cjs": true,
	"json": true, "yaml": true, "yml": true, "toml": true, "txt": true, "log": true,
	"sh": true, "bash": true, "zsh": true, "fish": true, "py": true, "rs": true,
	"c": true, "h": true, "cc": true, "cpp": true, "hpp": true, "java": true, "kt": true,
	"swift": true, "rb": true, "sql": true, "css": true, "scss": true, "less": true,
	"xml": true, "csv": true, "tsv": true, "env": true, "ini": true, "conf": true,
	"mod": true, "sum": true, "lock": true, "mdx": true, "vue": true, "svelte": true,
	"lua": true, "pl": true, "r": true, "php": true, "scala": true, "dart": true,
	"groovy": true, "gradle": true, "proto": true, "graphql": true, "tf": true,
	"hcl": true, "nix": true, "diff": true, "patch": true, "properties": true,
	"gitignore": true, "editorconfig": true, "dockerignore": true,
}

// textBases are the base names of files with no extension shown as code
// (REQ-WP-002).
var textBases = map[string]bool{
	"makefile": true, "dockerfile": true, "license": true, "readme": true,
	"gemfile": true, "procfile": true, "justfile": true, "rakefile": true,
}

// KindOf classifies a file (REQ-WP-002). size is the file's size; a file
// larger than 5 MB that is not an image or a PDF is binary however it is
// named. A file with an unknown extension is read a little and is text when it
// is valid UTF-8 without NUL bytes, binary otherwise; a file that cannot be
// read is binary.
func KindOf(path string, size int64) Kind {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	base := strings.ToLower(filepath.Base(path))
	switch {
	case htmlExts[ext]:
		return capped(KindHTML, size)
	case markdownExts[ext]:
		return capped(KindMarkdown, size)
	case imageExts[ext]:
		return KindImage
	case ext == "pdf":
		return KindPDF
	case textExts[ext] || textBases[base]:
		return capped(KindText, size)
	case size > MaxSize:
		return KindBinary
	}
	if looksText(path) {
		return KindText
	}
	return KindBinary
}

// capped is kind, or binary when the file is too big to show (REQ-WP-002).
func capped(kind Kind, size int64) Kind {
	if size > MaxSize {
		return KindBinary
	}
	return kind
}

// looksText reports whether a file reads as text: valid UTF-8 without NUL
// bytes in its first headBytes.
func looksText(path string) bool {
	data, err := readHead(path)
	if err != nil {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	// The read may have split a multi-byte rune at its end; drop up to three
	// trailing bytes before deciding, so a cut rune is not read as binary.
	for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
		data = data[:len(data)-1]
	}
	return utf8.Valid(data)
}

// readHead reads up to headBytes from a file.
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, headBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// Policy is the allow/deny rule of REQ-WP-003: allowed under the home folder
// or a temp folder, always refused under the secret folders or for a secret
// base name. Its paths are assumed resolved.
type Policy struct {
	home    string
	temps   []string
	refused []string
}

// NewPolicy builds the path policy of REQ-WP-003 from the daemon user's home
// folder and the temp folders. The roots are resolved with their symlinks, so
// /var and /private/var compare equal on macOS. An empty home is ignored.
func NewPolicy(home string, temps []string) Policy {
	p := Policy{}
	if home != "" {
		p.home = Resolve(home)
		p.refused = []string{
			Resolve(filepath.Join(p.home, ".ssh")),
			Resolve(filepath.Join(p.home, ".gnupg")),
			Resolve(filepath.Join(p.home, ".aws")),
			Resolve(filepath.Join(p.home, ".config", "gcloud")),
		}
	}
	for _, t := range temps {
		if t != "" {
			p.temps = append(p.temps, Resolve(t))
		}
	}
	return p
}

// Home is the resolved home folder of the policy.
func (p Policy) Home() string { return p.home }

// Allowed reports whether a resolved path is inside an allowed folder: the
// home folder or a temp folder (REQ-WP-003).
func (p Policy) Allowed(path string) bool {
	if p.home != "" && within(p.home, path) {
		return true
	}
	for _, t := range p.temps {
		if t != "" && within(t, path) {
			return true
		}
	}
	return false
}

// Refused reports whether a resolved path is always refused (REQ-WP-003):
// anything under ~/.ssh, ~/.gnupg, ~/.aws or ~/.config/gcloud, any
// secrets.env, and any .env or .env.* file.
func (p Policy) Refused(path string) bool {
	for _, dir := range p.refused {
		if dir != "" && within(dir, path) {
			return true
		}
	}
	base := filepath.Base(path)
	if base == "secrets.env" {
		return true
	}
	return base == ".env" || strings.HasPrefix(base, ".env.")
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

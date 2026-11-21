package web

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// previewTTL is how long a preview token stays valid (REQ-WP-004).
const previewTTL = 30 * time.Minute

// previewPreBasePath is the prefix of the preview GET route (REQ-WP-004).
const previewBasePath = "/v1/workbench/preview/"

// previewTokens is the in-memory token store of the preview GET route
// (REQ-WP-004): every token maps to the resolved folder it may serve and the
// time it expires. It is the only credential of the GET route, so the route
// needs no cookie and a sandboxed iframe can load the page and its relative
// assets. Expired entries are pruned on each access; no goroutine is needed.
type previewTokens struct {
	mu     sync.Mutex
	tokens map[string]previewEntry
	now    func() time.Time
}

// previewEntry is one issued token: root is the resolved folder of the file,
// expires is when it stops working.
type previewEntry struct {
	root    string
	expires time.Time
}

// newPreviewTokens builds a token store on the clock now (nil means time.Now).
// The clock is injectable so tests can advance it past the TTL (REQ-WP-004).
func newPreviewTokens(now func() time.Time) *previewTokens {
	if now == nil {
		now = time.Now
	}
	return &previewTokens{tokens: map[string]previewEntry{}, now: now}
}

// issue stores a new 128-bit token bound to root for the TTL and returns it.
func (t *previewTokens) issue(root string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked()
	t.tokens[token] = previewEntry{root: root, expires: t.now().Add(previewTTL)}
	return token, nil
}

// lookup returns the resolved folder of a live token. An unknown or expired
// token is not found.
func (t *previewTokens) lookup(token string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked()
	entry, ok := t.tokens[token]
	if !ok || !t.now().Before(entry.expires) {
		return "", false
	}
	return entry.root, true
}

// pruneLocked drops the expired entries. The caller holds the mutex.
func (t *previewTokens) pruneLocked() {
	now := t.now()
	for token, entry := range t.tokens {
		if !now.Before(entry.expires) {
			delete(t.tokens, token)
		}
	}
}

// setPreviewHeaders applies the REQ-WP-006 headers to every preview response,
// errors included. It sets this route's sandbox CSP (not the shell policy of
// securityHeaders, which skips the preview paths), so the page runs in an
// opaque origin and this route alone may be framed by 'self'. There is no
// X-Frame-Options.
func setPreviewHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups; frame-ancestors 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
}

// isPreviewPath reports whether path is one of the Workbench file preview
// routes of spec 61: the authenticated POST and the token GET. Their responses
// carry the sandbox CSP of setPreviewHeaders (REQ-WP-006), so securityHeaders
// must not apply the shell policy with frame-ancestors 'none' to them.
func isPreviewPath(path string) bool {
	return path == "/v1/workbench/preview" || strings.HasPrefix(path, previewBasePath)
}

// workbenchPreviewResponse is the POST /v1/workbench/preview answer of
// REQ-WP-002.
type workbenchPreviewResponse struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	URL    string `json:"url"`
	RawURL string `json:"raw_url"`
}

// workbenchPreviewBody is the POST body of REQ-WP-001. dir is the space folder
// the client sends as the last relative-path candidate.
type workbenchPreviewBody struct {
	Session string `json:"session"`
	Link    string `json:"link"`
	Dir     string `json:"dir"`
}

// handleWorkbenchPreview serves POST /v1/workbench/preview (REQ-WP-001): it
// resolves the link against the session's working directory, the git root and
// the body's dir, checks the path policy, and answers with the file's kind and
// a fresh token for the GET route.
func (s *Server) handleWorkbenchPreview(w http.ResponseWriter, r *http.Request) {
	setPreviewHeaders(w)
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body workbenchPreviewBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	link := strings.TrimSpace(body.Link)
	if !preview.IsLocalLink(link) {
		writeAPIError(w, http.StatusBadRequest, "invalid_link", "the link must name a local file")
		return
	}
	// An empty session means the client has nothing to resolve against, so the
	// lookup is skipped: cwd stays empty and a relative link is tried against
	// the body's dir alone. A non-empty unknown session keeps its not-found
	// error from writeWorkbenchError.
	cwd := ""
	if session := strings.TrimSpace(body.Session); session != "" {
		sess, err := backend.Get(r.Context(), session)
		if err != nil {
			s.writeWorkbenchError(w, "workbench preview", err)
			return
		}
		cwd = sess.Cwd
		if cwd == "" {
			cwd = sess.Dir
		}
	}
	home := s.previewHomeDir()
	path, tried, line, col, ok := s.resolvePreviewLink(link, cwd, body.Dir, home)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "no such file: "+tried)
		return
	}
	resolved := preview.Resolve(path)
	policy := preview.NewPolicy(home, s.previewTempDirs())
	if !policy.Allowed(resolved) || policy.Refused(resolved) {
		writeAPIError(w, http.StatusForbidden, "forbidden_path", "the path is not allowed")
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		writeAPIError(w, http.StatusNotFound, "not_found", "no such file: "+tried)
		return
	}
	kind := preview.KindOf(resolved, info.Size())
	token, err := s.previewTokens.issue(filepath.Dir(resolved))
	if err != nil {
		s.logger.Error("web: workbench preview token", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	name := filepath.Base(resolved)
	rawURL := previewRawURL(token, name)
	url := rawURL
	if kind == preview.KindMarkdown || kind == preview.KindText || kind == preview.KindImage {
		url += "?view=1"
		if line > 0 {
			url += "#L" + strconv.Itoa(line)
		}
	}
	writeJSON(w, http.StatusOK, workbenchPreviewResponse{
		Path:   resolved,
		Name:   name,
		Dir:    filepath.Dir(resolved),
		Kind:   string(kind),
		Size:   info.Size(),
		Line:   line,
		Col:    col,
		URL:    url,
		RawURL: rawURL,
	})
}

// handleWorkbenchPreviewFile serves GET /v1/workbench/preview/{token}/{name...}
// (REQ-WP-004). The token is the only credential, so there is no cookie check;
// the route is registered behind the host check alone. Unknown or expired
// tokens, a path that resolves outside the token's folder, a refused path, a
// missing file and a directory are all 404; there are no directory listings.
func (s *Server) handleWorkbenchPreviewFile(w http.ResponseWriter, r *http.Request) {
	setPreviewHeaders(w)
	root, ok := s.previewTokens.lookup(r.PathValue("token"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	name := r.PathValue("name")
	full := filepath.Join(root, filepath.FromSlash(name))
	resolved := preview.Resolve(full)
	if !withinPreviewDir(root, resolved) {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	policy := preview.NewPolicy(s.previewHomeDir(), s.previewTempDirs())
	if !policy.Allowed(resolved) || policy.Refused(resolved) {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	kind := preview.KindOf(resolved, info.Size())
	if r.URL.Query().Get("view") == "1" {
		switch kind {
		case preview.KindMarkdown:
			s.servePreviewMarkdown(w, resolved)
			return
		case preview.KindText:
			s.servePreviewText(w, resolved)
			return
		case preview.KindImage:
			rel, err := filepath.Rel(root, resolved)
			if err != nil {
				writeAPIError(w, http.StatusNotFound, "not_found", "not found")
				return
			}
			page := preview.RenderImagePage(previewRawURL(r.PathValue("token"), filepath.ToSlash(rel)))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(page)
			return
		}
	}
	s.servePreviewRaw(w, r, resolved, info, kind)
}

// servePreviewMarkdown renders a Markdown file as its page (REQ-WP-005).
func (s *Server) servePreviewMarkdown(w http.ResponseWriter, path string) {
	source, err := os.ReadFile(path)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	page, err := preview.RenderMarkdownPage(source)
	if err != nil {
		s.logger.Error("web: workbench preview markdown", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}

// servePreviewText renders a text file as its page (REQ-WP-005).
func (s *Server) servePreviewText(w http.ResponseWriter, path string) {
	source, err := os.ReadFile(path)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(preview.RenderTextPage(source, 0))
}

// servePreviewRaw sends a file as it is, with its content type by extension and
// range support (REQ-WP-004).
func (s *Server) servePreviewRaw(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, kind preview.Kind) {
	f, err := os.Open(path)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", previewContentType(path, kind))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// previewContentType is the content type of a previewed file (REQ-WP-004):
// html, pdf and text have a fixed one, an image and a binary file go by
// extension.
func previewContentType(path string, kind preview.Kind) string {
	switch kind {
	case preview.KindHTML:
		return "text/html; charset=utf-8"
	case preview.KindPDF:
		return "application/pdf"
	case preview.KindText:
		return "text/plain; charset=utf-8"
	case preview.KindImage:
		return previewImageContentType(path)
	}
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// previewImageContentType is the content type of an image by its extension.
func previewImageContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	}
	return "application/octet-stream"
}

// previewRawURL is the GET URL of a file inside its token's folder
// (REQ-WP-002): the name is escaped per segment.
func previewRawURL(token, name string) string {
	return previewBasePath + token + "/" + preview.EscapeName(filepath.ToSlash(name))
}

// withinPreviewDir reports whether path is the token folder or below it. Both
// are resolved, so a symlink that leaves the folder fails this check.
func withinPreviewDir(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// resolvePreviewLink turns a link into the file it names (REQ-WP-001). A
// relative link is tried against the session cwd, the git root of that cwd and
// the body's dir, in order; the first regular file wins. An absolute, ~ or
// file:// link is tried alone. tried is the cleaned path that was attempted,
// for the 404 body.
func (s *Server) resolvePreviewLink(link, cwd, dir, home string) (path, tried string, line, col int, ok bool) {
	if rel, isRel := preview.RelativePath(link); isRel {
		_, line, col = preview.SplitLineCol(link)
		for _, base := range s.previewBases(cwd, dir, home) {
			candidate := filepath.Join(base, rel)
			if tried == "" {
				tried = candidate
			}
			if isRegularFile(candidate) {
				return candidate, candidate, line, col, true
			}
		}
		if tried == "" {
			tried = filepath.Clean(rel)
		}
		return "", tried, line, col, false
	}
	p, line, col, ok := preview.ParseLink(link, "", home)
	if !ok {
		return "", "", 0, 0, false
	}
	if isRegularFile(p) {
		return p, p, line, col, true
	}
	return "", p, line, col, false
}

// previewBases is the ordered list of folders a relative link is tried
// against (REQ-WP-001): the session cwd, the git root of that cwd, and the
// body's dir. An empty value is dropped, as is a git root equal to the cwd.
func (s *Server) previewBases(cwd, dir, home string) []string {
	var bases []string
	if cwd != "" {
		bases = append(bases, cwd)
	}
	if root := s.previewGitRootFor(cwd); root != "" && root != cwd {
		bases = append(bases, root)
	}
	if dir != "" {
		if expanded := preview.ExpandHome(dir, home); expanded != "" {
			bases = append(bases, expanded)
		}
	}
	return bases
}

// previewGitRootFor is the git-root lookup of the preview, injectable so tests
// do not depend on a real repository.
func (s *Server) previewGitRootFor(dir string) string {
	if s.previewGitRoot != nil {
		return s.previewGitRoot(dir)
	}
	return gitRoot(dir)
}

// gitRoot walks up from dir to the first folder that contains a .git entry
// (a directory or a worktree file), or "" when there is none (REQ-WP-001).
func gitRoot(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isRegularFile reports whether path is an existing regular file. The stat
// follows a symlink, so a link to a regular file counts.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// previewHomeDir is the home folder the preview policy uses (REQ-WP-003). It
// is injectable so tests do not depend on the real $HOME.
func (s *Server) previewHomeDir() string {
	if s.previewHome != "" {
		return s.previewHome
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// previewTempDirs are the temp folders the preview policy allows
// (REQ-WP-003). It is injectable for tests.
func (s *Server) previewTempDirs() []string {
	if len(s.previewTemps) > 0 {
		return s.previewTemps
	}
	dirs := []string{"/tmp", "/private/tmp"}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		dirs = append(dirs, tmp)
	}
	return dirs
}

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// gitResolveMaxDirs caps the dirs of one resolve call (api-contract).
const gitResolveMaxDirs = 64

// gitStatusCacheTTL is how long a marshalled status body is reused, so several
// polling clients share one git run (REQ-WG-021).
const gitStatusCacheTTL = 2 * time.Second

// workbenchExitedState is the session state of a finished session, which is not
// in use for a worktree removal (REQ-WG-033).
const workbenchExitedState = "exited"

// errUnknownScope marks a scope query value ParseScope refused; the error
// mapping turns it into 400 invalid_request (api-contract).
var errUnknownScope = errors.New("workbench: unknown git scope")

// registerWorkbenchGitRoutes registers the git routes of spec 66 (REQ-WG-020).
// They go through Handle, so the Host, Origin, cookie and CSRF checks apply, and
// each answers 503 workbench_unavailable when no Workbench core is installed.
func (s *Server) registerWorkbenchGitRoutes() {
	s.Handle("POST /v1/workbench/git/resolve", http.HandlerFunc(s.handleWorkbenchGitResolve))
	s.Handle("GET /v1/workbench/git/status", http.HandlerFunc(s.handleWorkbenchGitStatus))
	s.Handle("GET /v1/workbench/git/diff", http.HandlerFunc(s.handleWorkbenchGitDiff))
	s.Handle("GET /v1/workbench/git/tree", http.HandlerFunc(s.handleWorkbenchGitTree))
	s.Handle("GET /v1/workbench/git/search", http.HandlerFunc(s.handleWorkbenchGitSearch))
	s.Handle("GET /v1/workbench/git/file", http.HandlerFunc(s.handleWorkbenchGitFile))
	s.Handle("GET /v1/workbench/git/worktrees", http.HandlerFunc(s.handleWorkbenchGitWorktrees))
	s.Handle("GET /v1/workbench/git/worktrees/copy-candidates", http.HandlerFunc(s.handleWorkbenchGitCopyCandidates))
	s.Handle("GET /v1/workbench/git/worktrees/name", http.HandlerFunc(s.handleWorkbenchGitWorktreeName))
	s.Handle("POST /v1/workbench/git/worktrees", http.HandlerFunc(s.handleWorkbenchGitWorktreeCreate))
	s.Handle("POST /v1/workbench/git/worktrees/remove", http.HandlerFunc(s.handleWorkbenchGitWorktreeRemove))
	s.Handle("POST /v1/workbench/git/worktrees/cleanup", http.HandlerFunc(s.handleWorkbenchGitWorktreeCleanup))
	s.Handle("POST /v1/workbench/git/worktrees/move", http.HandlerFunc(s.handleWorkbenchGitWorktreeMove))
	s.Handle("POST /v1/workbench/git/worktrees/prune", http.HandlerFunc(s.handleWorkbenchGitWorktreePrune))
	s.Handle("POST /v1/workbench/git/worktrees/lock", http.HandlerFunc(s.handleWorkbenchGitWorktreeLock))
	s.Handle("GET /v1/workbench/git/settings", http.HandlerFunc(s.handleWorkbenchGitSettingsGet))
	s.Handle("PUT /v1/workbench/git/settings", http.HandlerFunc(s.handleWorkbenchGitSettingsPut))
}

// workbenchGit builds, on first use, the git runner and the settings store of
// the git routes (spec 66). The runner checks every path against the spec 61
// policy; the store keeps the per-repo settings of REQ-WG-050.
func (s *Server) workbenchGit() (*gitinfo.Runner, *gitinfo.SettingsStore) {
	s.workbenchGitOnce.Do(func() {
		policy := preview.NewPolicy(s.previewHomeDir(), s.previewTempDirs())
		s.workbenchGitRunner = gitinfo.NewRunner(policy)
		s.workbenchGitStore = gitinfo.NewSettingsStore(s.workbenchGitSettingsPath())
	})
	return s.workbenchGitRunner, s.workbenchGitStore
}

// workbenchGitSettingsPath is the settings file of REQ-WG-050:
// <LANA_HOME>/workbench-git.json, or a unique file under the temp folder when
// the daemon has no home.
func (s *Server) workbenchGitSettingsPath() string {
	if s.cfg.Home != "" {
		return filepath.Join(s.cfg.Home, "workbench-git.json")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("lana-workbench-git-%d.json", time.Now().UnixNano()))
}

// repoDir turns a repo id into a directory git accepts (api-contract): the web
// sends the absolute git common dir (".../.git"), so a ".git" base becomes its
// parent, and any other path is used as it is.
func repoDir(id string) string {
	if filepath.Base(id) == ".git" {
		return filepath.Dir(id)
	}
	return id
}

// gitSettingsFor resolves a directory and returns the settings of its repo:
// every value the settings store holds, else the repo's defaults (REQ-WG-050).
func (s *Server) gitSettingsFor(ctx context.Context, runner *gitinfo.Runner, store *gitinfo.SettingsStore, dir string) (gitinfo.Settings, gitinfo.Info, error) {
	info, err := runner.Resolve(ctx, dir)
	if err != nil {
		return gitinfo.Settings{}, gitinfo.Info{}, err
	}
	return store.Get(info.Repo, runner.DefaultSettings(ctx, dir)), info, nil
}

// gitScope parses a scope query value, defaulting an empty one to uncommitted;
// a bad value is answered as 400 invalid_request and ok is false.
func (s *Server) gitScope(w http.ResponseWriter, raw string) (gitinfo.Scope, bool) {
	if raw == "" {
		raw = string(gitinfo.ScopeUncommitted)
	}
	scope, err := gitinfo.ParseScope(raw)
	if err != nil {
		s.writeWorkbenchGitError(w, fmt.Errorf("%w: %q", errUnknownScope, raw))
		return "", false
	}
	return scope, true
}

// workbenchGitStatusCache caches a marshalled status body and its ETag per
// worktree and scope for gitStatusCacheTTL. Its zero value is usable and it is
// safe for concurrent use.
type workbenchGitStatusCache struct {
	mu      sync.Mutex
	entries map[string]gitStatusEntry
}

// gitStatusEntry is one cached status body.
type gitStatusEntry struct {
	body []byte
	etag string
	at   time.Time
}

// get returns a cached body and ETag while they are younger than the window.
func (c *workbenchGitStatusCache) get(key string, now time.Time) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, "", false
	}
	if now.Sub(entry.at) >= gitStatusCacheTTL {
		delete(c.entries, key)
		return nil, "", false
	}
	return entry.body, entry.etag, true
}

// put caches a body and ETag for key at now.
func (c *workbenchGitStatusCache) put(key string, body []byte, etag string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]gitStatusEntry)
	}
	c.entries[key] = gitStatusEntry{body: body, etag: etag, at: now}
}

// gitETag is the quoted hex SHA-256 of a response body (REQ-WG-003).
func gitETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// writeGitStatus writes a cached status body with its ETag. A request whose
// If-None-Match equals the ETag answers 304 with no body (REQ-WG-003).
func writeGitStatus(w http.ResponseWriter, r *http.Request, body []byte, etag string) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeWorkbenchGitError maps a gitinfo error to the daemon envelope of
// api-contract.md.
func (s *Server) writeWorkbenchGitError(w http.ResponseWriter, err error) {
	var (
		inUse   *gitinfo.InUseError
		hasWork *gitinfo.HasWorkError
		fetch   *gitinfo.FetchError
		gitErr  *gitinfo.GitError
	)
	switch {
	case errors.Is(err, errUnknownScope):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unknown scope")
	case errors.Is(err, gitinfo.ErrForbiddenPath):
		writeAPIError(w, http.StatusForbidden, "forbidden_path", "the path is not allowed")
	case errors.Is(err, gitinfo.ErrNotARepo):
		writeAPIError(w, http.StatusNotFound, "not_a_repo", "the path is not inside a git repository")
	case errors.Is(err, gitinfo.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, gitinfo.ErrBranchExists):
		writeAPIError(w, http.StatusConflict, "branch_exists", "the branch already exists")
	case errors.Is(err, gitinfo.ErrPathExists):
		writeAPIError(w, http.StatusConflict, "path_exists", "the folder already exists")
	case errors.Is(err, gitinfo.ErrMainWorktree):
		writeAPIError(w, http.StatusBadRequest, "main_worktree", "the main worktree cannot be removed")
	case errors.Is(err, gitinfo.ErrConfirmMismatch):
		writeAPIError(w, http.StatusBadRequest, "confirm_mismatch", "confirm must name the worktree folder")
	case errors.As(err, &inUse):
		writeAPIErrorDetails(w, http.StatusConflict, "in_use", "the worktree is in use",
			[]map[string]any{{"sessions": inUse.Sessions}})
	case errors.As(err, &hasWork):
		writeAPIErrorDetails(w, http.StatusConflict, "has_work", "the worktree has work that is not on a remote",
			[]map[string]any{{"dirty": hasWork.Dirty, "unpushed": hasWork.Unpushed}})
	case errors.As(err, &fetch):
		writeAPIError(w, http.StatusInternalServerError, "fetch_failed", gitMessageOr(fetch.Message, "fetch failed"))
	case errors.As(err, &gitErr):
		if gitErr.Code == "git_timeout" {
			writeAPIError(w, http.StatusGatewayTimeout, "git_timeout", "git timed out")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "git_failed", gitMessageOr(gitErr.Message, "git failed"))
	default:
		s.logger.Error("web: workbench git", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// gitMessageOr is message, or fallback when it is blank (api-contract).
func gitMessageOr(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return message
}

// sessionsInWorktree returns the ids of the live Workbench sessions inside a
// worktree (REQ-WG-033): a session whose working directory (cwd, else dir) is
// the worktree or below it, and which has not exited.
func sessionsInWorktree(sessions []workbench.Session, dir string) []string {
	clean := filepath.Clean(dir)
	var ids []string
	for _, sess := range sessions {
		if sess.State == workbenchExitedState {
			continue
		}
		cwd := sess.Cwd
		if cwd == "" {
			cwd = sess.Dir
		}
		if cwd == "" {
			continue
		}
		cwd = filepath.Clean(cwd)
		if cwd == clean || strings.HasPrefix(cwd, clean+string(filepath.Separator)) {
			ids = append(ids, sess.ID)
		}
	}
	return ids
}

// checkPlaceableFolder reports whether a folder a worktree would go in passes
// the path policy (REQ-WG-050): the folder itself when it exists, else its
// parent.
func checkPlaceableFolder(runner *gitinfo.Runner, path string) error {
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return runner.CheckPath(path)
	}
	return runner.CheckPath(filepath.Dir(path))
}

// workbenchGitResolveBody is the POST /v1/workbench/git/resolve body
// (api-contract).
type workbenchGitResolveBody struct {
	Dirs []string `json:"dirs"`
}

// handleWorkbenchGitResolve answers the resolve route (REQ-WG-002): for each
// dir its Info, {"repo": null} for a directory outside any repo, and
// {"error": "forbidden_path"} for one the path policy refuses.
func (s *Server) handleWorkbenchGitResolve(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var body workbenchGitResolveBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Dirs == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "dirs is required")
		return
	}
	if len(body.Dirs) > gitResolveMaxDirs {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "at most 64 dirs per call")
		return
	}
	runner, _ := s.workbenchGit()
	dirs := make(map[string]any, len(body.Dirs))
	for _, dir := range body.Dirs {
		info, err := runner.Resolve(r.Context(), dir)
		switch {
		case err == nil:
			dirs[dir] = info
		case errors.Is(err, gitinfo.ErrForbiddenPath):
			dirs[dir] = map[string]any{"error": "forbidden_path"}
		case errors.Is(err, gitinfo.ErrNotARepo):
			dirs[dir] = map[string]any{"repo": nil}
		default:
			s.writeWorkbenchGitError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Dirs map[string]any `json:"dirs"`
	}{Dirs: dirs})
}

// handleWorkbenchGitStatus answers the status route (REQ-WG-003, REQ-WG-004).
// The marshalled body is cached per worktree and scope for 2 s and carries an
// ETag; an If-None-Match with the same value answers 304.
func (s *Server) handleWorkbenchGitStatus(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	worktree := q.Get("worktree")
	if worktree == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree is required")
		return
	}
	scope, ok := s.gitScope(w, q.Get("scope"))
	if !ok {
		return
	}
	runner, store := s.workbenchGit()
	settings, _, err := s.gitSettingsFor(r.Context(), runner, store, worktree)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	key := worktree + "\x00" + string(scope)
	if body, etag, ok := s.workbenchGitStatus.get(key, s.nowTime()); ok {
		writeGitStatus(w, r, body, etag)
		return
	}
	status, err := runner.Status(r.Context(), worktree, scope, settings.Base)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	body, err := json.Marshal(status)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	etag := gitETag(body)
	s.workbenchGitStatus.put(key, body, etag, s.nowTime())
	writeGitStatus(w, r, body, etag)
}

// handleWorkbenchGitDiff answers the diff route (REQ-WG-006).
func (s *Server) handleWorkbenchGitDiff(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	worktree, path := q.Get("worktree"), q.Get("path")
	if worktree == "" || path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree and path are required")
		return
	}
	scope, ok := s.gitScope(w, q.Get("scope"))
	if !ok {
		return
	}
	runner, store := s.workbenchGit()
	settings, _, err := s.gitSettingsFor(r.Context(), runner, store, worktree)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	diff, err := runner.Diff(r.Context(), worktree, path, scope, settings.Base)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// handleWorkbenchGitTree answers the file tree route (REQ-WG-007).
func (s *Server) handleWorkbenchGitTree(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	worktree := r.URL.Query().Get("worktree")
	if worktree == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree is required")
		return
	}
	runner, _ := s.workbenchGit()
	list, err := runner.Tree(r.Context(), worktree, r.URL.Query().Get("dir"))
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleWorkbenchGitSearch answers the file search route (REQ-WG-008).
func (s *Server) handleWorkbenchGitSearch(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	worktree := q.Get("worktree")
	if worktree == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree is required")
		return
	}
	runner, _ := s.workbenchGit()
	files, err := runner.Search(r.Context(), worktree, q.Get("q"))
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Files []string `json:"files"`
	}{Files: files})
}

// handleWorkbenchGitFile answers the file route (REQ-WG-009): a text file is the
// FileContent JSON, any other kind is the spec 61 preview response for the file.
func (s *Server) handleWorkbenchGitFile(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	worktree, path := q.Get("worktree"), q.Get("path")
	if worktree == "" || path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree and path are required")
		return
	}
	runner, _ := s.workbenchGit()
	content, err := runner.File(r.Context(), worktree, path)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	if content.Kind == string(preview.KindText) {
		writeJSON(w, http.StatusOK, content)
		return
	}
	resolved := preview.Resolve(filepath.Join(worktree, content.Path))
	s.previewResponse(w, resolved, content.Size, preview.Kind(content.Kind), 0, 0)
}

// handleWorkbenchGitWorktrees answers the worktree list route (REQ-WG-010).
func (s *Server) handleWorkbenchGitWorktrees(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo is required")
		return
	}
	dir := repoDir(repo)
	runner, store := s.workbenchGit()
	settings, _, err := s.gitSettingsFor(r.Context(), runner, store, dir)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	list, err := runner.Worktrees(r.Context(), dir, settings.Base, settings.WorktreeRoot)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleWorkbenchGitCopyCandidates answers the copy-candidates route
// (REQ-WG-032).
func (s *Server) handleWorkbenchGitCopyCandidates(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo is required")
		return
	}
	runner, _ := s.workbenchGit()
	files, err := runner.CopyCandidates(r.Context(), repoDir(repo))
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Files []gitinfo.CopyCandidate `json:"files"`
	}{Files: files})
}

// handleWorkbenchGitWorktreeName answers the worktree-name route (REQ-WG-030).
func (s *Server) handleWorkbenchGitWorktreeName(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	repo, branch := q.Get("repo"), q.Get("branch")
	if repo == "" || branch == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo and branch are required")
		return
	}
	dir := repoDir(repo)
	runner, store := s.workbenchGit()
	settings, _, err := s.gitSettingsFor(r.Context(), runner, store, dir)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	path, err := runner.SuggestPath(r.Context(), dir, branch, settings.WorktreeRoot)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Path string `json:"path"`
	}{Path: path})
}

// workbenchGitCreateBody is the POST /v1/workbench/git/worktrees body
// (REQ-WG-031).
type workbenchGitCreateBody struct {
	Repo      string   `json:"repo"`
	Branch    string   `json:"branch"`
	NewBranch bool     `json:"new_branch"`
	Base      string   `json:"base"`
	Fetch     bool     `json:"fetch"`
	Path      string   `json:"path"`
	Copy      []string `json:"copy"`
}

// handleWorkbenchGitWorktreeCreate answers the create route (REQ-WG-031). An
// empty base uses the repo's settings base.
func (s *Server) handleWorkbenchGitWorktreeCreate(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var body workbenchGitCreateBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Repo == "" || body.Branch == "" || body.Path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo, branch and path are required")
		return
	}
	dir := repoDir(body.Repo)
	runner, store := s.workbenchGit()
	settings, _, err := s.gitSettingsFor(r.Context(), runner, store, dir)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	base := body.Base
	if base == "" {
		base = settings.Base
	}
	wt, err := runner.CreateWorktree(r.Context(), gitinfo.CreateInput{
		Repo:      dir,
		Branch:    body.Branch,
		NewBranch: body.NewBranch,
		Base:      base,
		Fetch:     body.Fetch,
		Path:      body.Path,
		Copy:      body.Copy,
	})
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	s.logger.Info("web: workbench git create", "repo", dir, "branch", body.Branch, "path", wt.Path)
	writeJSON(w, http.StatusCreated, wt)
}

// workbenchGitRemoveBody is the POST /v1/workbench/git/worktrees/remove body
// (REQ-WG-033).
type workbenchGitRemoveBody struct {
	Path         string `json:"path"`
	Force        bool   `json:"force"`
	Confirm      string `json:"confirm"`
	DeleteBranch bool   `json:"delete_branch"`
}

// handleWorkbenchGitWorktreeRemove answers the remove route (REQ-WG-033). The
// live sessions inside the worktree are computed from the core's session list.
func (s *Server) handleWorkbenchGitWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body workbenchGitRemoveBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "path is required")
		return
	}
	sessions, err := backend.List(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench git remove", err)
		return
	}
	runner, _ := s.workbenchGit()
	result, err := runner.RemoveWorktree(r.Context(), gitinfo.RemoveInput{
		Path:         body.Path,
		Force:        body.Force,
		Confirm:      body.Confirm,
		DeleteBranch: body.DeleteBranch,
		Sessions:     sessionsInWorktree(sessions, body.Path),
	})
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	s.logger.Info("web: workbench git remove", "path", body.Path, "removed", result.Removed, "branch_deleted", result.BranchDeleted)
	writeJSON(w, http.StatusOK, result)
}

// workbenchGitCleanupBody is the POST /v1/workbench/git/worktrees/cleanup body
// (REQ-WG-034).
type workbenchGitCleanupBody struct {
	Paths        []string `json:"paths"`
	DeleteBranch bool     `json:"delete_branch"`
	DeleteRemote bool     `json:"delete_remote"`
}

// handleWorkbenchGitWorktreeCleanup answers the clean up route (REQ-WG-034).
func (s *Server) handleWorkbenchGitWorktreeCleanup(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body workbenchGitCleanupBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Paths == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "paths is required")
		return
	}
	sessions, err := backend.List(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench git cleanup", err)
		return
	}
	runner, _ := s.workbenchGit()
	results := runner.Cleanup(r.Context(), gitinfo.CleanupInput{
		Paths:        body.Paths,
		DeleteBranch: body.DeleteBranch,
		DeleteRemote: body.DeleteRemote,
		InUse:        func(path string) []string { return sessionsInWorktree(sessions, path) },
	})
	for _, res := range results {
		s.logger.Info("web: workbench git cleanup", "path", res.Path, "result", res.Result, "reason", res.Reason)
	}
	writeJSON(w, http.StatusOK, struct {
		Results []gitinfo.ItemResult `json:"results"`
	}{Results: results})
}

// workbenchGitMoveBody is the POST /v1/workbench/git/worktrees/move body
// (REQ-WG-035).
type workbenchGitMoveBody struct {
	Moves []workbenchGitMoveItem `json:"moves"`
}

// workbenchGitMoveItem is one move of that body.
type workbenchGitMoveItem struct {
	Path string `json:"path"`
	To   string `json:"to"`
}

// handleWorkbenchGitWorktreeMove answers the move route (REQ-WG-035).
func (s *Server) handleWorkbenchGitWorktreeMove(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body workbenchGitMoveBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Moves == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "moves is required")
		return
	}
	sessions, err := backend.List(r.Context())
	if err != nil {
		s.writeWorkbenchError(w, "workbench git move", err)
		return
	}
	moves := make([]gitinfo.MoveItem, 0, len(body.Moves))
	for _, m := range body.Moves {
		moves = append(moves, gitinfo.MoveItem{Path: m.Path, To: m.To})
	}
	runner, _ := s.workbenchGit()
	results := runner.Move(r.Context(), moves, func(path string) []string { return sessionsInWorktree(sessions, path) })
	for _, res := range results {
		s.logger.Info("web: workbench git move", "path", res.Path, "result", res.Result, "reason", res.Reason)
	}
	writeJSON(w, http.StatusOK, struct {
		Results []gitinfo.ItemResult `json:"results"`
	}{Results: results})
}

// handleWorkbenchGitWorktreePrune answers the prune route (REQ-WG-036).
func (s *Server) handleWorkbenchGitWorktreePrune(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var body struct {
		Repo string `json:"repo"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Repo == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo is required")
		return
	}
	dir := repoDir(body.Repo)
	runner, _ := s.workbenchGit()
	pruned, err := runner.Prune(r.Context(), dir)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	s.logger.Info("web: workbench git prune", "repo", dir, "pruned", len(pruned))
	writeJSON(w, http.StatusOK, struct {
		Pruned []string `json:"pruned"`
	}{Pruned: pruned})
}

// handleWorkbenchGitWorktreeLock answers the lock route (REQ-WG-036).
func (s *Server) handleWorkbenchGitWorktreeLock(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var body struct {
		Path   string `json:"path"`
		Locked bool   `json:"locked"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "path is required")
		return
	}
	runner, _ := s.workbenchGit()
	wt, err := runner.Lock(r.Context(), body.Path, body.Locked)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	s.logger.Info("web: workbench git lock", "path", body.Path, "locked", body.Locked)
	writeJSON(w, http.StatusOK, wt)
}

// handleWorkbenchGitSettingsGet answers the settings GET (REQ-WG-050).
func (s *Server) handleWorkbenchGitSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo is required")
		return
	}
	runner, store := s.workbenchGit()
	settings, _, err := s.gitSettingsFor(r.Context(), runner, store, repoDir(repo))
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// handleWorkbenchGitSettingsPut answers the settings PUT (REQ-WG-050): omitted
// fields keep their value. An empty base or a worktree_root the path policy
// refuses is 400 invalid_request.
func (s *Server) handleWorkbenchGitSettingsPut(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "repo is required")
		return
	}
	var patch gitinfo.SettingsPatch
	if !decodeJSONObject(w, r, workbenchBodyLimit, &patch) {
		return
	}
	if patch.Base != nil && strings.TrimSpace(*patch.Base) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "base must not be empty")
		return
	}
	dir := repoDir(repo)
	runner, store := s.workbenchGit()
	info, err := runner.Resolve(r.Context(), dir)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	if patch.WorktreeRoot != nil {
		if err := checkPlaceableFolder(runner, *patch.WorktreeRoot); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree_root is not allowed")
			return
		}
	}
	if _, err := store.Put(info.Repo, patch); err != nil {
		s.logger.Error("web: workbench git settings", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	settings := store.Get(info.Repo, runner.DefaultSettings(r.Context(), dir))
	s.logger.Info("web: workbench git settings", "repo", info.Repo, "base", settings.Base)
	writeJSON(w, http.StatusOK, settings)
}

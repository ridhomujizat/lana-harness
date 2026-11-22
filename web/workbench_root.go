package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
)

// The spec 70 root routes: a root is a folder of several repos that is not a
// worktree itself (REQ-WM-001). The daemon keeps one shared watcher per root
// (REQ-WM-080), reads its cached summary (REQ-WM-003, REQ-WM-004) and pushes a
// git.root event when a subscribed root changes (REQ-WM-083).
const (
	// workbenchGitRootEventType is the SSE event of REQ-WM-083.
	workbenchGitRootEventType = "git.root"
	// workbenchActivityTTL is how long the root watcher's activity function
	// reuses one core session list, so the core is asked at most once per
	// window for every root together (REQ-WM-081).
	workbenchActivityTTL = 2 * time.Second
	// workbenchWorkingState is the session and agent state that counts as
	// working for the watcher schedule (REQ-WM-081).
	workbenchWorkingState = "working"
)

// workbenchGitRootEvent is the data of the git.root event (REQ-WM-083): the
// root, its summary ETag and the ETag of every repo with changes.
type workbenchGitRootEvent struct {
	Root  string            `json:"root"`
	Etag  string            `json:"etag"`
	Repos map[string]string `json:"repos"`
}

// workbenchRootInfo is the root field of a resolve answer (REQ-WM-002): the
// root path, its name and how many repos sit directly inside it.
type workbenchRootInfo struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Repos int    `json:"repos"`
}

// workbenchActivityCache is the session list the root watcher's activity
// function reuses for workbenchActivityTTL (REQ-WM-081).
type workbenchActivityCache struct {
	mu       sync.Mutex
	sessions []workbench.Session
	at       time.Time
	valid    bool
}

// workbenchRootSubscriptions remembers which roots have a live subscription
// (REQ-WM-080). The Files tree reads a cached summary only for one of them, so
// a folder with no open panel never starts a git run of its own (REQ-WM-004).
// It is safe for concurrent use.
type workbenchRootSubscriptions struct {
	mu      sync.Mutex
	clients map[string]map[string]struct{}
}

// add records client on root.
func (r *workbenchRootSubscriptions) add(root, client string) {
	key := filepath.Clean(root)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = map[string]map[string]struct{}{}
	}
	set := r.clients[key]
	if set == nil {
		set = map[string]struct{}{}
		r.clients[key] = set
	}
	set[client] = struct{}{}
}

// remove drops client from root; a root with no client left is forgotten.
func (r *workbenchRootSubscriptions) remove(root, client string) {
	key := filepath.Clean(root)
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.clients[key]
	if set == nil {
		return
	}
	delete(set, client)
	if len(set) == 0 {
		delete(r.clients, key)
	}
}

// has reports whether root has at least one live subscription.
func (r *workbenchRootSubscriptions) has(root string) bool {
	key := filepath.Clean(root)
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.clients[key]) > 0
}

// workbenchRootWatchers returns the shared root watcher, building it on first
// use with the server's git runner so a server that never sees a root does not
// start a ticker (REQ-WM-080). The manager's one-second loop calls Tick every
// second; Close stops it at shutdown.
func (s *Server) workbenchRootWatchers() *gitinfo.RootWatchers {
	s.workbenchRootWatchOnce.Do(func() {
		runner, _ := s.workbenchGit()
		watchers := gitinfo.NewRootWatchers(runner, s.workbenchRootActivity, s.publishGitRoot)
		s.workbenchRootWatchMu.Lock()
		s.workbenchRootWatch = watchers
		s.workbenchRootWatchMu.Unlock()
	})
	s.workbenchRootWatchMu.Lock()
	defer s.workbenchRootWatchMu.Unlock()
	return s.workbenchRootWatch
}

// closeWorkbenchRootWatchers stops the root watcher's ticker at shutdown
// (REQ-WM-080). A server that never built one is left alone.
func (s *Server) closeWorkbenchRootWatchers() {
	s.workbenchRootWatchMu.Lock()
	watchers := s.workbenchRootWatch
	s.workbenchRootWatchMu.Unlock()
	if watchers != nil {
		watchers.Close()
	}
}

// publishGitRoot sends the git.root event of a changed root on the workbench
// bus (REQ-WM-083).
func (s *Server) publishGitRoot(root, etag string, repos map[string]string) {
	s.PublishWorkbenchEvent(workbenchGitRootEventType, workbenchGitRootEvent{Root: root, Etag: etag, Repos: repos})
}

// workbenchRootActivity answers the watcher's Activity for a root (REQ-WM-081)
// from the daemon's Workbench sessions: whether any session under the root is
// working, the latest output time, the process directories of its panes and the
// spec 69 reported worktrees. A session belongs to the root when its process
// directory (cwd, else dir) or its reported work_dir is the root or below it.
func (s *Server) workbenchRootActivity(root string) gitinfo.Activity {
	var act gitinfo.Activity
	for _, sess := range s.workbenchRootSessions() {
		dir := sess.Cwd
		if dir == "" {
			dir = sess.Dir
		}
		reported := s.decorateWorkbenchSession(sess).WorkDir
		underDir := dir != "" && dirWithinRoot(root, dir)
		underReported := reported != "" && dirWithinRoot(root, reported)
		if !underDir && !underReported {
			continue
		}
		// An agent's screen state is the primary "working" signal; a plain
		// shell with fresh output reports it on State instead (spec 55).
		if sess.AgentState == workbenchWorkingState || sess.State == workbenchWorkingState {
			act.Working = true
		}
		if sess.LastOutput.After(act.LastOutput) {
			act.LastOutput = sess.LastOutput
		}
		if dir != "" {
			act.Dirs = append(act.Dirs, dir)
		}
		if reported != "" {
			act.Reported = append(act.Reported, reported)
		}
	}
	return act
}

// workbenchRootSessions returns the core session list the activity function
// reads, cached for workbenchActivityTTL so the core is asked at most once per
// window for all roots together (REQ-WM-081). A missing or unreachable core
// answers no sessions.
func (s *Server) workbenchRootSessions() []workbench.Session {
	cache := &s.workbenchActivity
	now := s.nowTime()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.valid && now.Sub(cache.at) < workbenchActivityTTL {
		return cache.sessions
	}
	var sessions []workbench.Session
	if backend := s.workbenchBackend(); backend != nil {
		if got, err := backend.List(context.Background()); err == nil {
			sessions = got
		}
	}
	cache.sessions, cache.at, cache.valid = sessions, now, true
	return sessions
}

// dirWithinRoot reports whether dir is root or sits below it, both cleaned.
func dirWithinRoot(root, dir string) bool {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	if dir == root {
		return true
	}
	return strings.HasPrefix(dir, root+string(filepath.Separator))
}

// handleWorkbenchGitRoot answers GET /v1/workbench/git/root (REQ-WM-003): the
// root watcher's summary with an ETag header and a 304 for a matching
// If-None-Match. An unknown root is 404 not_a_root and a refused folder is 403
// forbidden_path.
func (s *Server) handleWorkbenchGitRoot(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "dir is required")
		return
	}
	summary, err := s.workbenchRootWatchers().Summary(r.Context(), dir)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	body, err := json.Marshal(summary)
	if err != nil {
		s.logger.Error("web: workbench root summary", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("ETag", summary.ETag)
	w.Header().Set("Cache-Control", "no-store")
	if match := r.Header.Get("If-None-Match"); match != "" && match == summary.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// workbenchRootSubscribeBody is the PUT and DELETE
// /v1/workbench/git/root/subscribe body (REQ-WM-083).
type workbenchRootSubscribeBody struct {
	Root   string `json:"root"`
	Client string `json:"client"`
}

// handleWorkbenchRootSubscribe answers PUT and DELETE
// /v1/workbench/git/root/subscribe (REQ-WM-083): a PUT records or renews the
// client, a DELETE drops it, both 204. A bad root is 404 not_a_root or 403
// forbidden_path; a missing field is 400 invalid_request.
func (s *Server) handleWorkbenchRootSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var body workbenchRootSubscribeBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if body.Root == "" || body.Client == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "root and client are required")
		return
	}
	watchers := s.workbenchRootWatchers()
	if r.Method == http.MethodDelete {
		watchers.Unsubscribe(body.Root, body.Client)
		s.workbenchRootSubs.remove(body.Root, body.Client)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := watchers.Subscribe(r.Context(), body.Root, body.Client); err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	s.workbenchRootSubs.add(body.Root, body.Client)
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchFilesTree answers GET /v1/workbench/files/tree (REQ-WM-004):
// one folder outside a repo, folders first then files, with a repo mark on each
// child repository. The repo details come from the root watcher's cache, never
// from a git run here; a folder with no open panel gets the plain mark.
func (s *Server) handleWorkbenchFilesTree(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "dir is required")
		return
	}
	runner, _ := s.workbenchGit()
	var repos map[string]gitinfo.RepoSummary
	var loaded bool
	listing, err := runner.ListFolder(r.Context(), dir, func(child string) (gitinfo.RepoSummary, bool) {
		if !loaded {
			repos, loaded = s.workbenchRootChildSummaries(r.Context(), dir), true
		}
		repo, ok := repos[child]
		return repo, ok
	})
	if err != nil {
		s.writeWorkbenchFilesTreeError(w, r, runner, dir, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// workbenchRootChildSummaries returns the summaries of a root's child repos,
// keyed by folder name, from the root watcher's cache (REQ-WM-004). A root with
// no live subscription has none, so the caller marks a repo without details and
// no git run happens here.
func (s *Server) workbenchRootChildSummaries(ctx context.Context, root string) map[string]gitinfo.RepoSummary {
	if !s.workbenchRootSubs.has(root) {
		return nil
	}
	summary, err := s.workbenchRootWatchers().Summary(ctx, root)
	if err != nil {
		return nil
	}
	repos := make(map[string]gitinfo.RepoSummary, len(summary.Repos))
	for _, repo := range summary.Repos {
		repos[repo.Name] = repo
	}
	return repos
}

// writeWorkbenchFilesTreeError maps a ListFolder failure (REQ-WM-004): a folder
// inside a repo is 400 inside_repo with the worktree in the message, so the
// client uses the spec 66 tree route instead; anything else is the git error
// mapping of the spec 66 routes.
func (s *Server) writeWorkbenchFilesTreeError(w http.ResponseWriter, r *http.Request, runner *gitinfo.Runner, dir string, err error) {
	if errors.Is(err, gitinfo.ErrInsideRepo) {
		message := "the folder is inside a git repository"
		if info, rerr := runner.Resolve(r.Context(), dir); rerr == nil {
			message = "the folder is inside the git worktree " + info.Worktree
		}
		writeAPIError(w, http.StatusBadRequest, "inside_repo", message)
		return
	}
	s.writeWorkbenchGitError(w, err)
}

// handleWorkbenchFilesFile answers GET /v1/workbench/files/file (REQ-WM-005):
// the spec 66 file read of an absolute path, without the git fields. The path
// may be inside a repo or outside every one; the spec 61 policy applies.
func (s *Server) handleWorkbenchFilesFile(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "path is required")
		return
	}
	runner, _ := s.workbenchGit()
	content, err := runner.File(r.Context(), filepath.Dir(path), filepath.Base(path))
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	s.writeWorkbenchFileContent(w, content, path)
}

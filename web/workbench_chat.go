package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/claudechat"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
)

// Spec 71: the Claude Code chat view of a Workbench pane. The conversation is
// read from Claude Code's transcript file; the things that only live on the
// screen (the spinner, the permission mode, an open dialog) are read from a
// headless terminal mirror the daemon keeps of the pane. Everything the owner
// types is turned into terminal input written to the same pty.

// chatPollInterval is how often a hub re-reads the session, transcript and
// screen (REQ-WC-041). It is a package var so tests can shorten it.
var chatPollInterval = 300 * time.Millisecond

// chatIdleStop is how long a hub with no subscriber keeps running before it is
// dropped (REQ-WC-020).
var chatIdleStop = 30 * time.Second

// chatWriterIdle is how long a control attachment stays open after the last
// write (REQ-WC-030).
var chatWriterIdle = 60 * time.Second

// chatEnterDelay is the pause between the pasted say text and its Enter
// (REQ-WC-031).
var chatEnterDelay = 80 * time.Millisecond

// chatKeyGap is the pause between two named keys (REQ-WC-032).
var chatKeyGap = 30 * time.Millisecond

// chatLiveInterval is the shortest gap between two live deltas of one hub
// (REQ-WC-041).
var chatLiveInterval = 250 * time.Millisecond

// chatCmdGap is the pause between the /model and /effort commands (REQ-WC-034).
var chatCmdGap = 300 * time.Millisecond

// chatModeWait and chatModePoll bound one Shift+Tab attempt of the mode setter
// (REQ-WC-033).
var (
	chatModeWait = 400 * time.Millisecond
	chatModePoll = 50 * time.Millisecond
)

// chatMirrorWait is how long the mode setter waits for the hub's screen mirror
// to come up.
var chatMirrorWait = time.Second

// chatViewBackoff is how long a failed view attach waits before it is retried
// (REQ-WC-020).
const chatViewBackoff = 2 * time.Second

const (
	// chatSubBuffer is the per-subscriber event queue; a slower reader is
	// dropped (REQ-WC-041).
	chatSubBuffer = 32
	// chatSnapshotItems is the size of the first transcript window
	// (REQ-WC-015).
	chatSnapshotItems = 400
	// chatPageItems is the size of a ?before page (REQ-WC-015).
	chatPageItems = 200
)

// chatLocate finds the Claude Code conversation of a Workbench session. It is a
// package var so tests can stub it.
var chatLocate = claudechat.Locate

// chatHome returns the home directory the transcript lookup and the command
// scan read. It is a package var so tests can point it at a temp dir.
var chatHome = os.UserHomeDir

// ChatUpdate is the GET /chat body and the first SSE event of a stream
// (spec 71 api-contract). Reset is true for a full snapshot; a delta omits Meta
// and Live when they did not change.
type ChatUpdate struct {
	Available       bool              `json:"available"`
	Reason          string            `json:"reason,omitempty"`
	Agent           string            `json:"agent"`
	Conversation    *chatConversation `json:"conversation,omitempty"`
	Reset           bool              `json:"reset"`
	TruncatedBefore bool              `json:"truncated_before"`
	Items           []claudechat.Item `json:"items,omitempty"`
	Meta            *claudechat.Meta  `json:"meta,omitempty"`
	Live            *chatLive         `json:"live"`
}

// chatDelta is an SSE event that carries only what changed.
type chatDelta struct {
	Available       bool              `json:"available"`
	Reason          string            `json:"reason,omitempty"`
	Agent           string            `json:"agent"`
	Conversation    *chatConversation `json:"conversation,omitempty"`
	Reset           bool              `json:"reset"`
	TruncatedBefore bool              `json:"truncated_before,omitempty"`
	Items           []claudechat.Item `json:"items,omitempty"`
	Meta            *claudechat.Meta  `json:"meta,omitempty"`
	Live            *chatLive         `json:"live,omitempty"`
}

// chatConversation names the transcript the chat reads. Path has the home
// directory replaced by "~".
type chatConversation struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Started time.Time `json:"started"`
}

// chatLive is what only the screen knows (REQ-WC-021/022).
type chatLive struct {
	State      string             `json:"state"`
	Status     string             `json:"status,omitempty"`
	Mode       string             `json:"mode,omitempty"`
	InputEmpty bool               `json:"input_empty"`
	Dialog     *claudechat.Dialog `json:"dialog"`
}

// registerWorkbenchChatRoutes registers the chat routes of REQ-WC-040. They go
// through Handle, so the auth middleware applies like the other Workbench
// routes.
func (s *Server) registerWorkbenchChatRoutes() {
	s.Handle("GET /v1/workbench/sessions/{id}/chat", http.HandlerFunc(s.handleWorkbenchChat))
	s.Handle("GET /v1/workbench/sessions/{id}/chat/stream", http.HandlerFunc(s.handleWorkbenchChatStream))
	s.Handle("POST /v1/workbench/sessions/{id}/chat/say", http.HandlerFunc(s.handleWorkbenchChatSay))
	s.Handle("POST /v1/workbench/sessions/{id}/chat/keys", http.HandlerFunc(s.handleWorkbenchChatKeys))
	s.Handle("POST /v1/workbench/sessions/{id}/chat/mode", http.HandlerFunc(s.handleWorkbenchChatMode))
	s.Handle("POST /v1/workbench/sessions/{id}/chat/model", http.HandlerFunc(s.handleWorkbenchChatModel))
	s.Handle("GET /v1/workbench/sessions/{id}/chat/commands", http.HandlerFunc(s.handleWorkbenchChatCommands))
	s.Handle("GET /v1/workbench/sessions/{id}/chat/files", http.HandlerFunc(s.handleWorkbenchChatFiles))
	s.Handle("GET /v1/workbench/sessions/{id}/chat/image", http.HandlerFunc(s.handleWorkbenchChatImage))
	s.Handle("GET /v1/workbench/sessions/{id}/chat/agents/{agent_id}", http.HandlerFunc(s.handleWorkbenchChatAgent))
}

// chatSession loads the Workbench session of a chat route, writing the standard
// error and reporting false when it cannot.
func (s *Server) chatSession(w http.ResponseWriter, r *http.Request) (workbench.Session, bool) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return workbench.Session{}, false
	}
	sess, err := backend.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeWorkbenchError(w, "workbench chat session", err)
		return workbench.Session{}, false
	}
	return sess, true
}

// chatWritable is the shared guard of the chat POST routes (REQ-WC-030): the
// remote control lock, the session lookup and the Claude Code check.
func (s *Server) chatWritable(w http.ResponseWriter, r *http.Request) (workbench.Session, bool) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return workbench.Session{}, false
	}
	sess, ok := s.chatSession(w, r)
	if !ok {
		return workbench.Session{}, false
	}
	if sess.Agent != "claude" {
		writeAPIError(w, http.StatusConflict, "not_claude", "the workbench session does not run Claude Code")
		return workbench.Session{}, false
	}
	return sess, true
}

// handleWorkbenchChat serves the snapshot and the ?before paging of
// REQ-WC-015/040.
func (s *Server) handleWorkbenchChat(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.chatSession(w, r)
	if !ok {
		return
	}
	home := chatHomeDir()
	if before := r.URL.Query().Get("before"); before != "" {
		writeJSON(w, http.StatusOK, s.chatPage(sess, home, before))
		return
	}
	up := s.chatSnapshot(sess, home)
	if hub := s.lookupChatHub(r.PathValue("id")); hub != nil {
		up.Live = hub.currentLive()
	}
	writeJSON(w, http.StatusOK, up)
}

// chatSnapshot builds a full chat snapshot from the transcript (REQ-WC-003,
// REQ-WC-013, REQ-WC-015). live stays nil; the caller fills it from a running
// hub.
func (s *Server) chatSnapshot(sess workbench.Session, home string) ChatUpdate {
	up := ChatUpdate{Agent: sess.Agent, Reset: true}
	switch {
	case sess.Agent == "":
		up.Reason = "no_agent"
		return up
	case sess.Agent != "claude":
		up.Reason = "unsupported_agent"
		return up
	}
	up.Agent = "claude"
	conv, ok := chatLocate(sess.PID, home)
	if !ok || conv.Path == "" {
		up.Reason = "waiting"
		return up
	}
	tr := claudechat.Open(conv.Path, conv.Cwd)
	_, _, _ = tr.Poll()
	items, truncated := tr.Snapshot(chatSnapshotItems)
	meta := tr.Meta()
	up.Available = true
	up.Conversation = conversationUpdate(conv, home)
	up.Items = items
	up.TruncatedBefore = truncated
	up.Meta = &meta
	return up
}

// chatPage returns the items before one item id (REQ-WC-015).
func (s *Server) chatPage(sess workbench.Session, home, before string) ChatUpdate {
	up := ChatUpdate{Agent: sess.Agent}
	conv, ok := chatLocate(sess.PID, home)
	if !ok || conv.Path == "" {
		up.Reason = "waiting"
		return up
	}
	tr := claudechat.Open(conv.Path, conv.Cwd)
	_, _, _ = tr.Poll()
	items, more := tr.Before(before, chatPageItems)
	up.Available = true
	up.Conversation = conversationUpdate(conv, home)
	up.Items = items
	up.TruncatedBefore = more
	return up
}

// handleWorkbenchChatStream serves the SSE stream of REQ-WC-041. The first
// event is a full snapshot; later events are the hub's upserts.
func (s *Server) handleWorkbenchChatStream(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	if _, err := backend.Get(r.Context(), id); err != nil {
		s.writeWorkbenchError(w, "workbench chat stream", err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: workbench chat stream: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	hub := s.getChatHub(id)
	events := hub.subscribe()
	defer hub.unsubscribe(events)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(s.heartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping():
			_ = writeSSE(w, flusher, "shutdown", "", struct{}{})
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case payload, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, "chat", "", json.RawMessage(payload)); err != nil {
				return
			}
		}
	}
}

// handleWorkbenchChatSay answers POST …/chat/say (REQ-WC-031).
func (s *Server) handleWorkbenchChatSay(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.chatWritable(w, r); !ok {
		return
	}
	var body struct {
		Text  string   `json:"text"`
		Paths []string `json:"paths"`
		Force bool     `json:"force"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" && countChatPaths(body.Paths) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "text or paths is required")
		return
	}
	if len(body.Text) > claudechat.MaxSayBytes {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "text is too long")
		return
	}
	hub := s.getChatHub(r.PathValue("id"))
	if !body.Force && hub.hasDialog() {
		writeAPIError(w, http.StatusConflict, "dialog_open", "a dialog is open; send force to answer it")
		return
	}
	ctrlU, _ := claudechat.KeyBytes("ctrl+u")
	if err := hub.write(ctrlU); err != nil {
		s.writeWorkbenchError(w, "workbench chat say", err)
		return
	}
	if err := hub.write(claudechat.PasteBytes(body.Text, body.Paths)); err != nil {
		s.writeWorkbenchError(w, "workbench chat say", err)
		return
	}
	time.Sleep(chatEnterDelay)
	if err := hub.write([]byte("\r")); err != nil {
		s.writeWorkbenchError(w, "workbench chat say", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchChatKeys answers POST …/chat/keys (REQ-WC-032).
func (s *Server) handleWorkbenchChatKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.chatWritable(w, r); !ok {
		return
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	seqs := make([][]byte, 0, len(body.Keys))
	for _, name := range body.Keys {
		b, err := claudechat.KeyBytes(name)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_key", "unknown key: "+name)
			return
		}
		seqs = append(seqs, b)
	}
	hub := s.getChatHub(r.PathValue("id"))
	for i, b := range seqs {
		if i > 0 {
			time.Sleep(chatKeyGap)
		}
		if err := hub.write(b); err != nil {
			s.writeWorkbenchError(w, "workbench chat keys", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchChatMode answers POST …/chat/mode (REQ-WC-033): it presses
// Shift+Tab until the mirror reports the target mode.
func (s *Server) handleWorkbenchChatMode(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.chatWritable(w, r); !ok {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if !claudechat.ValidMode(body.Mode) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unknown permission mode")
		return
	}
	hub := s.getChatHub(r.PathValue("id"))
	hub.addView()
	defer hub.removeView()

	mirror := hub.waitMirror(chatMirrorWait)
	if mirror == nil {
		writeAPIError(w, http.StatusConflict, "mode_unavailable", "no screen to read the permission mode from")
		return
	}
	shiftTab, _ := claudechat.KeyBytes("shift+tab")
	seen := map[string]bool{}
	record := func(mode string) {
		if mode == "" {
			mode = "(unknown)"
		}
		seen[mode] = true
	}
	for i := 0; i < 6; i++ {
		current := claudechat.ParseScreen(mirror.Lines()).Mode
		record(current)
		if current == body.Mode {
			writeJSON(w, http.StatusOK, struct {
				Mode string `json:"mode"`
			}{Mode: body.Mode})
			return
		}
		if err := hub.write(shiftTab); err != nil {
			s.writeWorkbenchError(w, "workbench chat mode", err)
			return
		}
		waitModeChange(mirror, current, chatModeWait, chatModePoll)
	}
	final := claudechat.ParseScreen(mirror.Lines()).Mode
	if final == body.Mode {
		writeJSON(w, http.StatusOK, struct {
			Mode string `json:"mode"`
		}{Mode: body.Mode})
		return
	}
	record(final)
	writeAPIError(w, http.StatusConflict, "mode_unavailable",
		"could not switch to mode "+body.Mode+"; modes seen: "+strings.Join(sortedKeys(seen), ", "))
}

// handleWorkbenchChatModel answers POST …/chat/model (REQ-WC-034).
func (s *Server) handleWorkbenchChatModel(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.chatWritable(w, r)
	if !ok {
		return
	}
	if sess.AgentState == "working" {
		writeAPIError(w, http.StatusConflict, "busy", "Claude is working; try again when it is idle")
		return
	}
	var body struct {
		Model  string `json:"model"`
		Effort string `json:"effort"`
	}
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	model := ""
	if body.Model != "" {
		m, ok := claudechat.ModelArg(body.Model)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "unknown model")
			return
		}
		model = m
	}
	effort := ""
	if body.Effort != "" {
		if !claudechat.EffortArg(body.Effort) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "unknown effort")
			return
		}
		effort = body.Effort
	}
	if model == "" && effort == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "model or effort is required")
		return
	}
	hub := s.getChatHub(r.PathValue("id"))
	if model != "" {
		if err := hub.sayCommand("/model " + model); err != nil {
			s.writeWorkbenchError(w, "workbench chat model", err)
			return
		}
	}
	if effort != "" {
		if model != "" {
			time.Sleep(chatCmdGap)
		}
		if err := hub.sayCommand("/effort " + effort); err != nil {
			s.writeWorkbenchError(w, "workbench chat model", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkbenchChatCommands answers GET …/chat/commands (REQ-WC-035).
func (s *Server) handleWorkbenchChatCommands(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.chatSession(w, r)
	if !ok {
		return
	}
	home := chatHomeDir()
	cwd := sess.Cwd
	if cwd == "" {
		cwd = sess.Dir
	}
	if conv, found := chatLocate(sess.PID, home); found && conv.Cwd != "" {
		cwd = conv.Cwd
	}
	commands := claudechat.Commands(cwd, home)
	if commands == nil {
		commands = []claudechat.Command{}
	}
	writeJSON(w, http.StatusOK, struct {
		Commands []claudechat.Command `json:"commands"`
	}{Commands: commands})
}

// chatFilesTTL is how long the walked file list of a root folder is reused
// (REQ-WC-055). It is a package var so tests can shorten it.
var chatFilesTTL = 10 * time.Second

const (
	// chatFilesLimit caps the files a query returns (REQ-WC-055).
	chatFilesLimit = 50
	// chatFilesMaxDepth is how deep a non-git root is walked (REQ-WC-055).
	chatFilesMaxDepth = 6
	// chatFilesMaxEntries caps how many entries one walk visits (REQ-WC-055).
	chatFilesMaxEntries = 20000
)

// chatFilesCacheMu guards chatFilesCache. chatFilesCache keeps the walked file
// list of each root folder for chatFilesTTL, so the `@` menu does not walk the
// tree on every keystroke (REQ-WC-055).
var (
	chatFilesCacheMu sync.Mutex
	chatFilesCache   = map[string]chatFilesEntry{}
)

// chatFilesEntry is one cached walk: the root's list and when it was stored.
type chatFilesEntry struct {
	files []string
	at    time.Time
}

// chatFilesBody is the GET …/chat/files answer (REQ-WC-055). Files is never
// null; Root is the folder the list was taken from.
type chatFilesBody struct {
	Files []string `json:"files"`
	Root  string   `json:"root"`
}

// handleWorkbenchChatFiles answers GET …/chat/files (REQ-WC-055): the files of
// the session's folder for the `@` menu, whether that folder is a git
// repository or not.
func (s *Server) handleWorkbenchChatFiles(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.chatSession(w, r)
	if !ok {
		return
	}
	root := chatSessionRoot(sess, chatHomeDir())
	if root == "" {
		writeChatFiles(w, []string{}, root)
		return
	}
	// The git runner's resolve checks the spec 61 path policy and tells us
	// whether the folder sits in a repository (REQ-WC-055).
	runner, _ := s.workbenchGit()
	info, err := runner.Resolve(r.Context(), root)
	switch {
	case err == nil:
		// Inside a repository: reuse the spec 66 search over its worktree.
		// The search matches nothing without a query, so the empty `@` menu
		// lists the first files of the worktree walk instead, the same shape
		// the non-git folder returns.
		if strings.TrimSpace(r.URL.Query().Get("q")) == "" {
			writeChatFiles(w, matchChatFiles(s.chatFilesWalk(info.Worktree), ""), root)
			return
		}
		files, serr := runner.Search(r.Context(), info.Worktree, r.URL.Query().Get("q"))
		if serr != nil {
			s.writeWorkbenchGitError(w, serr)
			return
		}
		writeChatFiles(w, files, root)
	case errors.Is(err, gitinfo.ErrForbiddenPath):
		writeAPIError(w, http.StatusForbidden, "forbidden_path", "the path is not allowed")
	default:
		// Outside any repository, or the check could not run: walk the folder
		// itself and match the query against what it holds.
		writeChatFiles(w, matchChatFiles(s.chatFilesWalk(root), r.URL.Query().Get("q")), root)
	}
}

// writeChatFiles writes the …/chat/files answer, keeping Files an empty array
// rather than null.
func writeChatFiles(w http.ResponseWriter, files []string, root string) {
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, chatFilesBody{Files: files, Root: root})
}

// chatSessionRoot is the folder the `@` menu lists (REQ-WC-055): the located
// conversation cwd, else the session's cwd, else its dir.
func chatSessionRoot(sess workbench.Session, home string) string {
	root := sess.Cwd
	if root == "" {
		root = sess.Dir
	}
	if conv, found := chatLocate(sess.PID, home); found && conv.Cwd != "" {
		root = conv.Cwd
	}
	return root
}

// chatFilesWalk returns the cached walk of a root folder, or walks it and caches
// the result for chatFilesTTL (REQ-WC-055).
func (s *Server) chatFilesWalk(root string) []string {
	key := filepath.Clean(root)
	now := s.nowTime()
	chatFilesCacheMu.Lock()
	entry, ok := chatFilesCache[key]
	chatFilesCacheMu.Unlock()
	if ok && now.Sub(entry.at) < chatFilesTTL {
		return entry.files
	}
	files := walkChatFiles(root)
	chatFilesCacheMu.Lock()
	chatFilesCache[key] = chatFilesEntry{files: files, at: now}
	chatFilesCacheMu.Unlock()
	return files
}

// walkChatFiles lists a non-git root folder (REQ-WC-055): paths use "/"
// separators, folders carry a trailing "/", the hidden and vendor folders are
// left out, and the walk stops after chatFilesMaxDepth levels or
// chatFilesMaxEntries entries.
func walkChatFiles(root string) []string {
	files := []string{}
	visited := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if visited >= chatFilesMaxEntries {
			return filepath.SkipAll
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/") + 1
		if d.IsDir() {
			if depth > chatFilesMaxDepth || chatFilesSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			visited++
			files = append(files, rel+"/")
			return nil
		}
		if depth > chatFilesMaxDepth {
			return nil
		}
		visited++
		files = append(files, rel)
		return nil
	})
	return files
}

// chatFilesSkipDir reports whether a folder is left out of the walk
// (REQ-WC-055): a hidden folder, or one of the vendor and build folders.
func chatFilesSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "dist", "build", "vendor", "target", "__pycache__":
		return true
	}
	return false
}

// matchChatFiles ranks a walked list for a query (REQ-WC-055). An empty query
// returns the first chatFilesLimit paths in order; any other is a
// case-insensitive subsequence match, with a base-name substring hit first and
// then the shorter path.
func matchChatFiles(entries []string, query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		out := append([]string(nil), entries...)
		sort.Strings(out)
		if len(out) > chatFilesLimit {
			out = out[:chatFilesLimit]
		}
		return out
	}
	lower := strings.ToLower(query)
	type hit struct {
		path string
		base bool
	}
	hits := []hit{}
	for _, entry := range entries {
		if !chatSubsequence(entry, lower) {
			continue
		}
		hits = append(hits, hit{
			path: entry,
			base: strings.Contains(strings.ToLower(filepath.Base(entry)), lower),
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.base != b.base {
			return a.base
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	if len(hits) > chatFilesLimit {
		hits = hits[:chatFilesLimit]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.path
	}
	return out
}

// chatSubsequence reports whether the lowercased query is a subsequence of the
// path, ignoring case (REQ-WC-055).
func chatSubsequence(path, query string) bool {
	p := strings.ToLower(path)
	i := 0
	for j := 0; j < len(p) && i < len(query); j++ {
		if p[j] == query[i] {
			i++
		}
	}
	return i == len(query)
}

// handleWorkbenchChatImage answers GET …/chat/image (REQ-WC-036).
func (s *Server) handleWorkbenchChatImage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.chatSession(w, r)
	if !ok {
		return
	}
	item := r.URL.Query().Get("item")
	index := r.URL.Query().Get("i")
	if item == "" || index == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "item and i are required")
		return
	}
	tr := s.chatTranscript(r.PathValue("id"), sess, chatHomeDir())
	if tr == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "the image was not found")
		return
	}
	mediaType, data, found := tr.ImageBytes(item + ":" + index)
	if !found {
		writeAPIError(w, http.StatusNotFound, "not_found", "the image was not found")
		return
	}
	h := w.Header()
	h.Set("Content-Type", mediaType)
	h.Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// chatAgentBody is the GET …/chat/agents/{agent_id} body (REQ-WC-017).
type chatAgentBody struct {
	AgentID     string            `json:"agent_id"`
	AgentType   string            `json:"agent_type"`
	Description string            `json:"description"`
	Model       string            `json:"model"`
	Finished    bool              `json:"finished"`
	Steps       int               `json:"steps"`
	Items       []claudechat.Item `json:"items"`
}

// handleWorkbenchChatAgent answers GET …/chat/agents/{agent_id}: a subagent's own
// items, its meta and step count (REQ-WC-017).
func (s *Server) handleWorkbenchChatAgent(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.chatSession(w, r)
	if !ok {
		return
	}
	agentID := r.PathValue("agent_id")
	if !claudechat.ValidAgentID(agentID) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid agent id")
		return
	}
	conv, found := chatLocate(sess.PID, chatHomeDir())
	if !found || conv.Path == "" {
		writeAPIError(w, http.StatusNotFound, "not_found", "the agent was not found")
		return
	}
	jsonl, metaPath := claudechat.SubagentPaths(conv.Path, agentID)
	if _, err := os.Stat(jsonl); err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "the agent was not found")
		return
	}
	tr := claudechat.OpenSubagent(jsonl, conv.Cwd)
	if _, _, err := tr.Poll(); err != nil {
		s.writeWorkbenchError(w, "workbench chat agent", err)
		return
	}
	items, _ := tr.Snapshot(chatSnapshotItems)
	meta, _ := claudechat.ReadSubagentMeta(metaPath)
	body := chatAgentBody{
		AgentID:     agentID,
		AgentType:   meta.AgentType,
		Description: meta.Description,
		Model:       meta.Model,
		Finished:    meta.Finished,
		Steps:       chatAgentSteps(items),
		Items:       items,
	}
	writeJSON(w, http.StatusOK, body)
}

// chatAgentSteps counts a subagent's tool items (REQ-WC-017).
func chatAgentSteps(items []claudechat.Item) int {
	n := 0
	for _, it := range items {
		if it.Kind == "tool" {
			n++
		}
	}
	return n
}

// chatTranscript returns the transcript a hub already holds for the session, or
// reads one fresh, so an image can be served (REQ-WC-036).
func (s *Server) chatTranscript(id string, sess workbench.Session, home string) *claudechat.Transcript {
	if hub := s.lookupChatHub(id); hub != nil {
		hub.mu.Lock()
		tr := hub.transcript
		hub.mu.Unlock()
		if tr != nil {
			return tr
		}
	}
	conv, ok := chatLocate(sess.PID, home)
	if !ok || conv.Path == "" {
		return nil
	}
	tr := claudechat.Open(conv.Path, conv.Cwd)
	if _, _, err := tr.Poll(); err != nil {
		return nil
	}
	return tr
}

// chatHomeDir reads the home directory, or "" when it cannot.
func chatHomeDir() string {
	home, err := chatHome()
	if err != nil {
		return ""
	}
	return home
}

// conversationUpdate turns a located conversation into its chat form, with the
// home directory replaced by "~" in the path.
func conversationUpdate(conv claudechat.Conversation, home string) *chatConversation {
	return &chatConversation{
		ID:      conv.SessionID,
		Path:    chatPath(conv.Path, home),
		Started: conv.Started,
	}
}

// chatPath replaces a leading home directory with "~".
func chatPath(path, home string) string {
	if home == "" || path == "" || !strings.HasPrefix(path, home) {
		return path
	}
	rest := strings.TrimPrefix(path, home)
	if rest == "" {
		return "~"
	}
	if strings.HasPrefix(rest, string(os.PathSeparator)) {
		return "~" + rest
	}
	return path
}

// countChatPaths counts the non-empty attachment paths.
func countChatPaths(paths []string) int {
	n := 0
	for _, p := range paths {
		if p != "" {
			n++
		}
	}
	return n
}

// waitModeChange polls the mirror until its mode leaves previous, or the wait
// elapses (REQ-WC-033).
func waitModeChange(m *claudechat.Mirror, previous string, wait, poll time.Duration) string {
	deadline := time.Now().Add(wait)
	for {
		mode := claudechat.ParseScreen(m.Lines()).Mode
		if mode != previous {
			return mode
		}
		if !time.Now().Before(deadline) {
			return mode
		}
		time.Sleep(poll)
	}
}

// sortedKeys returns a map's keys in a stable order, for the mode_unavailable
// message.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// chatDialogKind reports whether a parsed dialog kind is one the chat shows
// (REQ-WC-022).
func chatDialogKind(kind string) bool {
	switch kind {
	case "permission", "plan", "question":
		return true
	}
	return false
}

// chatSize is the mirror size for a session, defaulting like the core.
func chatSize(sess workbench.Session) (cols, rows int) {
	cols, rows = sess.Cols, sess.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return cols, rows
}

// getChatHub returns the session's hub, building and starting it on first use.
// Building a hub resolves the conversation and polls the transcript once so a
// subscriber's first event already carries the items (REQ-WC-041).
func (s *Server) getChatHub(id string) *chatHub {
	s.chatMu.Lock()
	if hub, ok := s.chatHubs[id]; ok {
		s.chatMu.Unlock()
		return hub
	}
	if s.chatHubs == nil {
		s.chatHubs = map[string]*chatHub{}
	}
	hub := newChatHub(s, id)
	s.chatHubs[id] = hub
	s.chatMu.Unlock()

	hub.refresh(context.Background())
	go hub.run()
	return hub
}

// lookupChatHub returns the running hub of a session, or nil.
func (s *Server) lookupChatHub(id string) *chatHub {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.chatHubs[id]
}

// removeChatHub drops a hub if it is still the one in the map.
func (s *Server) removeChatHub(id string, hub *chatHub) {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	if s.chatHubs[id] == hub {
		delete(s.chatHubs, id)
	}
}

// stopChatHubs stops every hub, for server shutdown and test teardown, and
// waits for each poll loop to end so no goroutine outlives the server.
func (s *Server) stopChatHubs() {
	s.chatMu.Lock()
	hubs := make([]*chatHub, 0, len(s.chatHubs))
	for _, hub := range s.chatHubs {
		hubs = append(hubs, hub)
	}
	s.chatMu.Unlock()
	for _, hub := range hubs {
		hub.stop()
	}
	for _, hub := range hubs {
		select {
		case <-hub.loopDone:
		case <-time.After(time.Second):
		}
	}
}

// chatHub keeps the transcript reader, the screen mirror and the subscribers of
// one Workbench session (REQ-WC-020, REQ-WC-041).
type chatHub struct {
	srv *Server
	id  string

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	loopDone chan struct{}
	once     sync.Once

	mu      sync.Mutex
	subs    map[chan []byte]struct{}
	emptyAt time.Time

	available  bool
	reason     string
	agent      string
	conv       *chatConversation
	transcript *claudechat.Transcript
	convPath   string
	lastKey    string

	mirror           *claudechat.Mirror
	viewStream       io.ReadWriteCloser
	viewCols         int
	viewRows         int
	viewRefs         int
	viewBackoffUntil time.Time

	live         *chatLive
	liveJSON     string
	liveSentJSON string
	liveSentAt   time.Time
	screenEffort string

	writeMu      sync.Mutex
	control      io.ReadWriteCloser
	controlTimer *time.Timer
	controlGen   int
}

// newChatHub builds an idle hub; refresh and run are started by getChatHub.
func newChatHub(s *Server, id string) *chatHub {
	ctx, cancel := context.WithCancel(context.Background())
	return &chatHub{
		srv:      s,
		id:       id,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		loopDone: make(chan struct{}),
		subs:     map[chan []byte]struct{}{},
		emptyAt:  time.Now(),
	}
}

// run is the hub's poll loop (REQ-WC-041).
func (h *chatHub) run() {
	defer close(h.loopDone)
	ticker := time.NewTicker(chatPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.srv.stopping():
			h.stop()
			return
		case <-ticker.C:
			if h.idleExpired() {
				h.stop()
				return
			}
			h.refresh(h.ctx)
			if h.isStopped() {
				return
			}
		}
	}
}

// refresh reads the session, the conversation's transcript and the screen, and
// emits a full snapshot or the deltas the change made.
func (h *chatHub) refresh(ctx context.Context) {
	backend := h.srv.workbenchBackend()
	if backend == nil {
		return
	}
	sess, err := backend.Get(ctx, h.id)

	var (
		available bool
		reason    string
		agent     string
		conv      *chatConversation
		convPath  string
		tr        *claudechat.Transcript
	)
	var items []claudechat.Item
	metaChanged := false

	switch {
	case err != nil:
		// The session is gone: tell the chat and stop (REQ-WC-003).
		available, reason, agent = false, "no_agent", ""
	case sess.Agent != "claude":
		available, agent = false, sess.Agent
		reason = "unsupported_agent"
		if sess.Agent == "" {
			reason = "no_agent"
		}
	default:
		home := chatHomeDir()
		located, found := chatLocate(sess.PID, home)
		if !found || located.Path == "" {
			available, reason, agent = false, "waiting", "claude"
			break
		}
		available, agent = true, "claude"
		conv = conversationUpdate(located, home)
		// The comparison below uses the absolute path; conv.Path is the
		// display form with "~".
		convPath = located.Path
		h.mu.Lock()
		same := h.transcript != nil && h.convPath == located.Path
		h.mu.Unlock()
		if same {
			h.mu.Lock()
			tr = h.transcript
			h.mu.Unlock()
			changed, meta, perr := tr.Poll()
			if perr == nil {
				items, metaChanged = changed, meta
			}
		} else {
			tr = claudechat.Open(located.Path, located.Cwd)
			_, _, _ = tr.Poll()
		}
	}

	h.mu.Lock()
	key := "unavailable:" + reason
	if available {
		key = "available:" + conv.Path
	}
	stateChanged := key != h.lastKey
	h.lastKey = key
	h.available, h.reason, h.agent = available, reason, agent
	if available {
		h.conv = conv
		h.transcript = tr
		h.convPath = convPath
	}
	h.mu.Unlock()

	if err == nil {
		h.ensureView(ctx, sess)
		h.updateLive(sess)
	}

	if err != nil {
		h.emitSnapshot()
		h.stop()
		return
	}
	h.emitUpdate(stateChanged, items, metaChanged)
}

// emitUpdate sends the full snapshot when the connection changed, else the item
// and meta upserts and a live delta (REQ-WC-041).
func (h *chatHub) emitUpdate(stateChanged bool, items []claudechat.Item, metaChanged bool) {
	if stateChanged {
		h.emitSnapshot()
		return
	}
	if len(items) > 0 || metaChanged {
		h.mu.Lock()
		delta := chatDelta{Available: true, Agent: "claude", Items: items}
		if metaChanged {
			delta.Meta = h.metaLocked()
		}
		h.mu.Unlock()
		if b, err := json.Marshal(delta); err == nil {
			h.emit(b)
		}
	}
	if b, ok := h.liveDelta(time.Now()); ok {
		h.emit(b)
	}
}

// emitSnapshot builds and sends a full snapshot with reset true.
func (h *chatHub) emitSnapshot() {
	h.mu.Lock()
	up := h.snapshotLocked()
	h.liveSentJSON = h.liveJSON
	h.liveSentAt = time.Now()
	h.mu.Unlock()
	if b, err := json.Marshal(up); err == nil {
		h.emit(b)
	}
}

// snapshotLocked is the current full snapshot. The caller holds h.mu.
func (h *chatHub) snapshotLocked() ChatUpdate {
	up := ChatUpdate{
		Available:    h.available,
		Reason:       h.reason,
		Agent:        h.agent,
		Conversation: h.conv,
		Reset:        true,
		Live:         h.live,
	}
	if h.available && h.transcript != nil {
		items, truncated := h.transcript.Snapshot(chatSnapshotItems)
		up.Items = items
		up.TruncatedBefore = truncated
		up.Meta = h.metaLocked()
	}
	return up
}

// metaLocked is the transcript meta with the screen's effort filled in when the
// transcript has none (REQ-WC-013). The caller holds h.mu.
func (h *chatHub) metaLocked() *claudechat.Meta {
	if h.transcript == nil {
		return nil
	}
	meta := h.transcript.Meta()
	if meta.Effort == "" && h.screenEffort != "" {
		meta.Effort = h.screenEffort
	}
	return &meta
}

// liveDelta returns a live delta when the live state changed and the rate limit
// allows it (REQ-WC-041).
func (h *chatHub) liveDelta(now time.Time) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.live == nil || h.liveJSON == h.liveSentJSON {
		return nil, false
	}
	if !h.liveSentAt.IsZero() && now.Sub(h.liveSentAt) < chatLiveInterval {
		return nil, false
	}
	h.liveSentJSON = h.liveJSON
	h.liveSentAt = now
	delta := chatDelta{Available: h.available, Reason: h.reason, Agent: h.agent, Live: h.live}
	b, err := json.Marshal(delta)
	if err != nil {
		return nil, false
	}
	return b, true
}

// updateLive recomputes the screen state of the hub (REQ-WC-021/022). A dialog
// is reported while the state is blocked or when the parsed kind is one the
// chat shows.
func (h *chatHub) updateLive(sess workbench.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var lines []string
	if h.mirror != nil {
		lines = h.mirror.Lines()
	}
	screen := claudechat.ParseScreen(lines)
	state := sess.AgentState
	if state == "" {
		state = "idle"
	}
	var dialog *claudechat.Dialog
	if screen.Dialog != nil && (state == "blocked" || chatDialogKind(screen.Dialog.Kind)) {
		dialog = screen.Dialog
	}
	live := &chatLive{
		State:      state,
		Status:     screen.Status,
		Mode:       screen.Mode,
		InputEmpty: screen.InputEmpty,
		Dialog:     dialog,
	}
	h.live = live
	if b, err := json.Marshal(live); err == nil {
		h.liveJSON = string(b)
	}
	h.screenEffort = screen.Effort
}

// ensureView opens and sizes the mirror's view attachment when the hub has a
// viewer (REQ-WC-020).
func (h *chatHub) ensureView(ctx context.Context, sess workbench.Session) {
	cols, rows := chatSize(sess)
	h.mu.Lock()
	if h.viewRefs <= 0 {
		h.mu.Unlock()
		return
	}
	if stream := h.viewStream; stream != nil {
		if h.viewCols != cols || h.viewRows != rows {
			h.viewCols, h.viewRows = cols, rows
			mirror := h.mirror
			h.mu.Unlock()
			if mirror != nil {
				mirror.Resize(cols, rows)
			}
			return
		}
		h.mu.Unlock()
		return
	}
	if time.Now().Before(h.viewBackoffUntil) {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()

	stream, err := h.srv.workbenchBackend().Attach(ctx, h.id, workbenchModeView)
	if err != nil {
		h.mu.Lock()
		h.viewBackoffUntil = time.Now().Add(chatViewBackoff)
		h.mu.Unlock()
		return
	}
	mirror := claudechat.NewMirror(cols, rows)
	h.mu.Lock()
	if h.viewStream != nil {
		h.mu.Unlock()
		mirror.Close()
		_ = stream.Close()
		return
	}
	h.viewStream = stream
	h.mirror = mirror
	h.viewCols, h.viewRows = cols, rows
	h.mu.Unlock()
	go h.pumpView(stream, mirror)
}

// pumpView feeds the pane's output into the mirror until the attachment ends.
func (h *chatHub) pumpView(stream io.ReadWriteCloser, mirror *claudechat.Mirror) {
	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			_, _ = mirror.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// addView and removeView track the viewers that need the mirror: the stream
// subscribers and the mode setter (REQ-WC-020).
func (h *chatHub) addView() {
	h.mu.Lock()
	h.viewRefs++
	h.mu.Unlock()
}

func (h *chatHub) removeView() {
	h.mu.Lock()
	if h.viewRefs > 0 {
		h.viewRefs--
	}
	h.mu.Unlock()
}

// waitMirror waits for the hub's mirror to come up, or returns nil on timeout.
func (h *chatHub) waitMirror(wait time.Duration) *claudechat.Mirror {
	deadline := time.Now().Add(wait)
	for {
		h.mu.Lock()
		mirror := h.mirror
		h.mu.Unlock()
		if mirror != nil {
			return mirror
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(chatModePoll)
	}
}

// hasDialog reports whether the hub's live state holds an open dialog, the same
// state the stream publishes (REQ-WC-031). Reading h.live keeps `say` in step
// with the updateLive gating, which hides a parsed dialog the chat does not
// show.
func (h *chatHub) hasDialog() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live != nil && h.live.Dialog != nil
}

// currentLive returns the hub's last live state, or nil.
func (h *chatHub) currentLive() *chatLive {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live
}

// subscribe registers a buffered event channel and queues the current full
// snapshot as its first event (REQ-WC-041).
func (h *chatHub) subscribe() chan []byte {
	ch := make(chan []byte, chatSubBuffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.viewRefs++
	h.emptyAt = time.Time{}
	snapshot := h.snapshotLocked()
	h.liveSentJSON = h.liveJSON
	h.liveSentAt = time.Now()
	h.mu.Unlock()
	if b, err := json.Marshal(snapshot); err == nil {
		ch <- b
	}
	return ch
}

// unsubscribe forgets a subscriber channel, closing it.
func (h *chatHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
		h.viewRefs--
	}
	if len(h.subs) == 0 {
		h.emptyAt = time.Now()
	}
	h.mu.Unlock()
}

// emit fans a payload out to the subscribers; a full queue drops that reader
// (REQ-WC-041).
func (h *chatHub) emit(payload []byte) {
	h.mu.Lock()
	subs := make([]chan []byte, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- payload:
		default:
			h.unsubscribe(ch)
		}
	}
}

// idleExpired reports whether the hub has had no subscriber for chatIdleStop.
func (h *chatHub) idleExpired() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) > 0 || h.emptyAt.IsZero() {
		return false
	}
	return time.Since(h.emptyAt) >= chatIdleStop
}

// isStopped reports whether the hub has been stopped.
func (h *chatHub) isStopped() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// stop closes the hub's attachments and subscribers and drops it from the
// server.
func (h *chatHub) stop() {
	h.once.Do(func() {
		close(h.done)
		h.cancel()
		h.srv.removeChatHub(h.id, h)

		h.mu.Lock()
		if h.viewStream != nil {
			_ = h.viewStream.Close()
			h.viewStream = nil
		}
		if h.mirror != nil {
			h.mirror.Close()
			h.mirror = nil
		}
		for ch := range h.subs {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()

		h.writeMu.Lock()
		h.controlGen++
		if h.controlTimer != nil {
			h.controlTimer.Stop()
			h.controlTimer = nil
		}
		if h.control != nil {
			_ = h.control.Close()
			h.control = nil
		}
		h.writeMu.Unlock()
	})
}

// write sends bytes to the pane through the hub's control attachment, opening
// it on first use and arming its idle close (REQ-WC-030).
func (h *chatHub) write(b []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.control == nil {
		stream, err := h.srv.workbenchBackend().Attach(h.ctx, h.id, workbenchModeControl)
		if err != nil {
			return err
		}
		h.control = stream
	}
	if _, err := h.control.Write(b); err != nil {
		return err
	}
	h.controlGen++
	gen := h.controlGen
	stream := h.control
	if h.controlTimer != nil {
		h.controlTimer.Stop()
	}
	h.controlTimer = time.AfterFunc(chatWriterIdle, func() { h.expireWriter(gen, stream) })
	return nil
}

// expireWriter closes the control attachment after its idle timeout, unless a
// newer write took it over.
func (h *chatHub) expireWriter(gen int, stream io.ReadWriteCloser) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.controlGen != gen || h.control != stream {
		return
	}
	_ = stream.Close()
	h.control = nil
	h.controlTimer = nil
}

// sayCommand types one slash command into the pane the way say does: clear the
// draft, paste the text, wait, then Enter (REQ-WC-034).
func (h *chatHub) sayCommand(text string) error {
	ctrlU, _ := claudechat.KeyBytes("ctrl+u")
	if err := h.write(ctrlU); err != nil {
		return err
	}
	if err := h.write(claudechat.PasteBytes(text, nil)); err != nil {
		return err
	}
	time.Sleep(chatEnterDelay)
	return h.write([]byte("\r"))
}

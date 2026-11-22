package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/figcraft"
	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// figcraftBackend is the subset of *figcraft.Service the FigCraft viewer
// handlers use (REQ-FV-020). It is defined in web so the package is not tied to
// the concrete service; a test installs a fake with s.workbenchFigcraft.
type figcraftBackend interface {
	Info(ctx context.Context, abs string) (figcraft.Info, error)
	Layers(ctx context.Context, abs string, node int64) (figcraft.Children, error)
	RenderPage(ctx context.Context, abs string, page int64, scale float64) (figcraft.Render, error)
	RenderNode(ctx context.Context, abs string, node int64, scale float64) (figcraft.Render, error)
}

// registerWorkbenchFigcraftRoutes registers the FigCraft viewer routes of spec
// 67 (REQ-FV-020). They go through Handle, so the Host, Origin, cookie and CSRF
// checks apply, and each answers 503 workbench_unavailable when no Workbench
// core is installed.
func (s *Server) registerWorkbenchFigcraftRoutes() {
	s.Handle("GET /v1/workbench/figcraft/info", http.HandlerFunc(s.handleWorkbenchFigcraftInfo))
	s.Handle("GET /v1/workbench/figcraft/layers", http.HandlerFunc(s.handleWorkbenchFigcraftLayers))
	s.Handle("GET /v1/workbench/figcraft/render", http.HandlerFunc(s.handleWorkbenchFigcraftRender))
	s.Handle("POST /v1/workbench/figcraft/open", http.HandlerFunc(s.handleWorkbenchFigcraftOpen))
	s.Handle("GET /v1/workbench/figcraft/settings", http.HandlerFunc(s.handleWorkbenchFigcraftSettingsGet))
	s.Handle("PUT /v1/workbench/figcraft/settings", http.HandlerFunc(s.handleWorkbenchFigcraftSettingsPut))
}

// workbenchFigcraftService builds, on first use, the FigCraft viewer service and
// its settings store (REQ-FV-050). A test may set s.workbenchFigcraft or
// s.workbenchFigcraftStore before the first use, and the lazy init keeps them.
func (s *Server) workbenchFigcraftService() (figcraftBackend, *figcraft.SettingsStore) {
	s.workbenchFigcraftOnce.Do(func() {
		if s.workbenchFigcraftStore == nil {
			s.workbenchFigcraftStore = figcraft.NewSettingsStore(s.workbenchFigcraftSettingsPath())
		}
		if s.workbenchFigcraft == nil {
			store := s.workbenchFigcraftStore
			s.workbenchFigcraft = figcraft.New(figcraft.Options{
				Settings: store.Get,
				HomeDir:  s.previewHomeDir(),
				Logger:   s.logger,
			})
		}
	})
	return s.workbenchFigcraft, s.workbenchFigcraftStore
}

// workbenchFigcraftSettingsPath is the settings file of REQ-FV-050:
// <LANA_HOME>/workbench-figcraft.json, or a unique file under the temp folder
// when the daemon has no home.
func (s *Server) workbenchFigcraftSettingsPath() string {
	if s.cfg.Home != "" {
		return filepath.Join(s.cfg.Home, "workbench-figcraft.json")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("lana-workbench-figcraft-%d.json", time.Now().UnixNano()))
}

// figcraftResolve turns a worktree and a relative path into the absolute file
// the viewer reads (REQ-FV-020): it must stay inside the worktree, end in
// .figcraft, pass the spec 61 path policy and exist as a regular file. It writes
// the mapped error and returns ok false on the first failure.
func (s *Server) figcraftResolve(w http.ResponseWriter, worktree, path string) (string, bool) {
	if worktree == "" || path == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "worktree and path are required")
		return "", false
	}
	abs := filepath.Join(worktree, filepath.Clean(path))
	rel, err := filepath.Rel(worktree, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		writeAPIError(w, http.StatusForbidden, "forbidden_path", "the path is outside the worktree")
		return "", false
	}
	if !strings.EqualFold(filepath.Ext(abs), ".figcraft") {
		writeAPIError(w, http.StatusBadRequest, "not_figcraft", "the file does not end in .figcraft")
		return "", false
	}
	// The policy runs on the symlink-resolved path, like the preview and git
	// routes, so a link pointing out of an allowed folder is refused.
	resolved := preview.Resolve(abs)
	policy := preview.NewPolicy(s.previewHomeDir(), s.previewTempDirs())
	if !policy.Allowed(resolved) || policy.Refused(resolved) {
		writeAPIError(w, http.StatusForbidden, "forbidden_path", "the path is not allowed")
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
		return "", false
	}
	return resolved, true
}

// writeWorkbenchFigcraftError maps a figcraft error to the daemon envelope of
// api-contract.md: a missing CLI is 424, an unknown file or node is 404, a
// failed or timed-out run is 500 or 504 by its code, and anything else is 500.
func (s *Server) writeWorkbenchFigcraftError(w http.ResponseWriter, err error) {
	var (
		missing *figcraft.CLIMissingError
		failed  *figcraft.FailedError
	)
	switch {
	case errors.As(err, &missing):
		writeAPIError(w, http.StatusFailedDependency, "figcraft_cli_missing", "no figcraft-cli found (tried "+strings.Join(missing.Tried, ", ")+")")
	case errors.Is(err, figcraft.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
	case errors.As(err, &failed):
		if failed.Code == "figcraft_timeout" {
			writeAPIError(w, http.StatusGatewayTimeout, "figcraft_timeout", figcraftMessageOr(failed.Message, "figcraft timed out"))
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "figcraft_failed", figcraftMessageOr(failed.Message, "figcraft failed"))
	default:
		s.logger.Error("web: workbench figcraft", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// figcraftMessageOr is message, or fallback when it is blank (api-contract).
func figcraftMessageOr(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return message
}

// handleWorkbenchFigcraftInfo answers the info route (REQ-FV-021): the Info
// JSON with the fingerprint as its ETag, and 304 when If-None-Match matches.
func (s *Server) handleWorkbenchFigcraftInfo(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	abs, ok := s.figcraftResolve(w, q.Get("worktree"), q.Get("path"))
	if !ok {
		return
	}
	service, _ := s.workbenchFigcraftService()
	info, err := service.Info(r.Context(), abs)
	if err != nil {
		s.writeWorkbenchFigcraftError(w, err)
		return
	}
	etag := `"` + info.Fingerprint + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleWorkbenchFigcraftLayers answers the layers route (REQ-FV-005): the
// children of the node named by the node query value.
func (s *Server) handleWorkbenchFigcraftLayers(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	abs, ok := s.figcraftResolve(w, q.Get("worktree"), q.Get("path"))
	if !ok {
		return
	}
	node, err := strconv.ParseInt(q.Get("node"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "node is required")
		return
	}
	service, _ := s.workbenchFigcraftService()
	children, err := service.Layers(r.Context(), abs, node)
	if err != nil {
		s.writeWorkbenchFigcraftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, children)
}

// handleWorkbenchFigcraftRender answers the render route (REQ-FV-022): a PNG
// with the origin, scale, fingerprint, stale and duration headers, and the
// immutable cache when the client's fingerprint matches a fresh render.
func (s *Server) handleWorkbenchFigcraftRender(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	q := r.URL.Query()
	abs, ok := s.figcraftResolve(w, q.Get("worktree"), q.Get("path"))
	if !ok {
		return
	}
	pageRaw, nodeRaw := q.Get("page"), q.Get("node")
	if (pageRaw == "") == (nodeRaw == "") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "exactly one of page or node is required")
		return
	}
	scale := 1.0
	if raw := q.Get("scale"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "scale must be a number")
			return
		}
		scale = parsed
	}
	service, _ := s.workbenchFigcraftService()
	var (
		render figcraft.Render
		err    error
	)
	if pageRaw != "" {
		page, perr := strconv.ParseInt(pageRaw, 10, 64)
		if perr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "page must be an id")
			return
		}
		render, err = service.RenderPage(r.Context(), abs, page, scale)
	} else {
		node, nerr := strconv.ParseInt(nodeRaw, 10, 64)
		if nerr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "node must be an id")
			return
		}
		render, err = service.RenderNode(r.Context(), abs, node, scale)
	}
	if err != nil {
		s.writeWorkbenchFigcraftError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Figcraft-Origin-X", strconv.FormatFloat(render.OriginX, 'f', -1, 64))
	h.Set("X-Figcraft-Origin-Y", strconv.FormatFloat(render.OriginY, 'f', -1, 64))
	h.Set("X-Figcraft-Scale", strconv.FormatFloat(render.Scale, 'f', -1, 64))
	h.Set("X-Figcraft-Fingerprint", render.Fingerprint)
	h.Set("X-Figcraft-Render-Ms", strconv.FormatInt(render.Took.Milliseconds(), 10))
	if render.Stale {
		h.Set("X-Figcraft-Stale", "1")
	}
	if fp := q.Get("fp"); fp != "" && fp == render.Fingerprint && !render.Stale {
		h.Set("Cache-Control", "private, max-age=86400, immutable")
	} else {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(render.PNG)
}

// workbenchFigcraftOpenBody is the POST /v1/workbench/figcraft/open body
// (api-contract).
type workbenchFigcraftOpenBody struct {
	Worktree string `json:"worktree"`
	Path     string `json:"path"`
}

// handleWorkbenchFigcraftOpen answers the open route (REQ-FV-023): it runs
// `open -a FigCraft <file>` on the daemon's Mac and is 501 elsewhere. The call
// is logged at info level.
func (s *Server) handleWorkbenchFigcraftOpen(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var body workbenchFigcraftOpenBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	if runtime.GOOS != "darwin" {
		writeAPIError(w, http.StatusNotImplemented, "unsupported", "opening FigCraft is only supported on macOS")
		return
	}
	abs, ok := s.figcraftResolve(w, body.Worktree, body.Path)
	if !ok {
		return
	}
	if err := s.figcraftOpenPath(r.Context(), abs); err != nil {
		s.logger.Warn("web: workbench figcraft open", "path", abs, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.logger.Info("web: workbench figcraft open", "path", abs)
	w.WriteHeader(http.StatusNoContent)
}

// figcraftOpenPath opens a file in the FigCraft app: the test hook when set,
// else `open -a FigCraft` without a shell (REQ-FV-023).
func (s *Server) figcraftOpenPath(ctx context.Context, abs string) error {
	if s.figcraftOpen != nil {
		return s.figcraftOpen(ctx, abs)
	}
	return exec.CommandContext(ctx, "open", "-a", "FigCraft", abs).Run()
}

// handleWorkbenchFigcraftSettingsGet answers the settings GET (REQ-FV-050).
func (s *Server) handleWorkbenchFigcraftSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	_, store := s.workbenchFigcraftService()
	writeJSON(w, http.StatusOK, store.Get())
}

// handleWorkbenchFigcraftSettingsPut answers the settings PUT (REQ-FV-050): it
// replaces the whole object and returns it. A bad cli_path is 400
// invalid_cli_path and a bad live_ports list is 400 invalid_request.
func (s *Server) handleWorkbenchFigcraftSettingsPut(w http.ResponseWriter, r *http.Request) {
	if s.workbenchOrError(w) == nil {
		return
	}
	var settings figcraft.Settings
	if !decodeJSONObject(w, r, workbenchBodyLimit, &settings) {
		return
	}
	_, store := s.workbenchFigcraftService()
	stored, err := store.Put(settings)
	switch {
	case errors.Is(err, figcraft.ErrInvalidCLIPath):
		writeAPIError(w, http.StatusBadRequest, "invalid_cli_path", "cli_path must be an absolute path to an executable file")
		return
	case errors.Is(err, figcraft.ErrInvalidPorts):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "live_ports must be between 1 and 65535, at most 16 entries")
		return
	case err != nil:
		s.logger.Error("web: workbench figcraft settings", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

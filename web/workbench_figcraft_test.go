package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/figcraft"
)

// fakeFigcraft is a figcraftBackend that answers canned values, so the routes
// are tested without a real figcraft-cli (REQ-FV-101).
type fakeFigcraft struct {
	info     figcraft.Info
	infoErr  error
	children figcraft.Children
	layersEr error
	render   figcraft.Render
	renderEr error

	lastNode  int64
	lastPage  int64
	lastScale float64
}

func (f *fakeFigcraft) Info(context.Context, string) (figcraft.Info, error) {
	return f.info, f.infoErr
}

func (f *fakeFigcraft) Layers(_ context.Context, _ string, node int64) (figcraft.Children, error) {
	f.lastNode = node
	return f.children, f.layersEr
}

func (f *fakeFigcraft) RenderPage(_ context.Context, _ string, page int64, scale float64) (figcraft.Render, error) {
	f.lastPage, f.lastScale = page, scale
	return f.render, f.renderEr
}

func (f *fakeFigcraft) RenderNode(_ context.Context, _ string, node int64, scale float64) (figcraft.Render, error) {
	f.lastNode, f.lastScale = node, scale
	return f.render, f.renderEr
}

// newFigcraftEnv returns a logged-in server whose preview policy allows a
// resolved temp tree and whose FigCraft service is backend. The settings store
// is a fresh file in that tree. root is the tree.
func newFigcraftEnv(t *testing.T, backend figcraftBackend) (*Server, *http.Cookie, string, string) {
	t.Helper()
	s, cookie, csrf := newWorkbenchServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s.previewHome = home
	s.previewTemps = []string{root}
	s.workbenchFigcraftStore = figcraft.NewSettingsStore(filepath.Join(root, "figcraft-settings.json"))
	if backend != nil {
		s.SetWorkbench(newFakeWorkbench())
		s.workbenchFigcraft = backend
	}
	return s, cookie, csrf, root
}

// figcraftFile writes a file under root and returns its absolute path.
func figcraftFile(t *testing.T, root, rel string) string {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// figcraftTarget builds a route target with its query values.
func figcraftTarget(path string, params map[string]string) string {
	q := url.Values{}
	for key, value := range params {
		q.Set(key, value)
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// figcraftGet sends an authenticated GET to a FigCraft route.
func figcraftGet(s *Server, cookie *http.Cookie, target string) *httptest.ResponseRecorder {
	return workbenchRequest(s, http.MethodGet, target, "", cookie, "")
}

func TestWorkbenchFigcraftUnavailable(t *testing.T) {
	s, cookie, _, root := newFigcraftEnv(t, nil)
	figcraftFile(t, root, "design/x.figcraft")
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "design/x.figcraft"})
	assertAuthError(t, figcraftGet(s, cookie, target), http.StatusServiceUnavailable, "workbench_unavailable")
}

func TestWorkbenchFigcraftInfo(t *testing.T) {
	fake := &fakeFigcraft{info: figcraft.Info{
		Path:        "design/x.figcraft",
		Source:      "file",
		Fingerprint: "f-3f9a1c0e",
		Pages:       []figcraft.Page{{ID: 1, Name: "Page 1"}},
	}}
	s, cookie, _, root := newFigcraftEnv(t, fake)
	figcraftFile(t, root, "design/x.figcraft")
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "design/x.figcraft"})

	rr := figcraftGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("ETag"); got != `"f-3f9a1c0e"` {
		t.Fatalf("ETag = %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var info figcraft.Info
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Fingerprint != "f-3f9a1c0e" || len(info.Pages) != 1 {
		t.Fatalf("info = %+v", info)
	}

	// If-None-Match with the same value is a 304 with no body (REQ-FV-021).
	req := authRequest(http.MethodGet, target, "")
	req.AddCookie(cookie)
	req.Header.Set("If-None-Match", `"f-3f9a1c0e"`)
	rr304 := serveAuth(s, req)
	if rr304.Code != http.StatusNotModified {
		t.Fatalf("304 status = %d, body = %q", rr304.Code, rr304.Body.String())
	}
	if rr304.Body.Len() != 0 {
		t.Fatalf("304 body = %q", rr304.Body.String())
	}
}

func TestWorkbenchFigcraftForbiddenOutsideWorktree(t *testing.T) {
	s, cookie, _, root := newFigcraftEnv(t, &fakeFigcraft{})
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "../x.figcraft"})
	assertAuthError(t, figcraftGet(s, cookie, target), http.StatusForbidden, "forbidden_path")
}

func TestWorkbenchFigcraftForbiddenPolicy(t *testing.T) {
	s, cookie, _, root := newFigcraftEnv(t, &fakeFigcraft{})
	// A file under the policy's home ~/.ssh is refused even though it exists.
	figcraftFile(t, root, "home/.ssh/key.figcraft")
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "home/.ssh/key.figcraft"})
	assertAuthError(t, figcraftGet(s, cookie, target), http.StatusForbidden, "forbidden_path")
}

func TestWorkbenchFigcraftNotFigcraft(t *testing.T) {
	s, cookie, _, root := newFigcraftEnv(t, &fakeFigcraft{})
	figcraftFile(t, root, "design/notes.txt")
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "design/notes.txt"})
	assertAuthError(t, figcraftGet(s, cookie, target), http.StatusBadRequest, "not_figcraft")
}

func TestWorkbenchFigcraftNotFound(t *testing.T) {
	s, cookie, _, root := newFigcraftEnv(t, &fakeFigcraft{})
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "design/missing.figcraft"})
	assertAuthError(t, figcraftGet(s, cookie, target), http.StatusNotFound, "not_found")
}

func TestWorkbenchFigcraftCLIMissing(t *testing.T) {
	fake := &fakeFigcraft{infoErr: &figcraft.CLIMissingError{Tried: []string{"/opt/figcraft-cli", "root/.local/bin/figcraft-cli"}}}
	s, cookie, _, root := newFigcraftEnv(t, fake)
	figcraftFile(t, root, "design/x.figcraft")
	target := figcraftTarget("/v1/workbench/figcraft/info", map[string]string{"worktree": root, "path": "design/x.figcraft"})

	rr := figcraftGet(s, cookie, target)
	assertAuthError(t, rr, http.StatusFailedDependency, "figcraft_cli_missing")
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.Error.Message, "/opt/figcraft-cli") {
		t.Fatalf("message = %q, want the tried paths", env.Error.Message)
	}
}

func TestWorkbenchFigcraftLayers(t *testing.T) {
	fake := &fakeFigcraft{children: figcraft.Children{
		Node:        53933,
		Fingerprint: "f-1",
		Children:    []figcraft.Layer{{ID: 54066, Name: "Sidebar", Type: "FRAME", Visible: true}},
	}}
	s, cookie, _, root := newFigcraftEnv(t, fake)
	figcraftFile(t, root, "design/x.figcraft")
	target := figcraftTarget("/v1/workbench/figcraft/layers", map[string]string{"worktree": root, "path": "design/x.figcraft", "node": "53933"})

	rr := figcraftGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var children figcraft.Children
	if err := json.Unmarshal(rr.Body.Bytes(), &children); err != nil {
		t.Fatal(err)
	}
	if children.Node != 53933 || len(children.Children) != 1 {
		t.Fatalf("children = %+v", children)
	}
	if fake.lastNode != 53933 {
		t.Fatalf("node passed = %d", fake.lastNode)
	}

	// A missing node is 400.
	bad := figcraftTarget("/v1/workbench/figcraft/layers", map[string]string{"worktree": root, "path": "design/x.figcraft"})
	assertAuthError(t, figcraftGet(s, cookie, bad), http.StatusBadRequest, "invalid_request")
}

func TestWorkbenchFigcraftRender(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	fake := &fakeFigcraft{render: figcraft.Render{
		PNG:         png,
		OriginX:     12,
		OriginY:     -3.5,
		Scale:       2,
		Fingerprint: "f-abc",
		Took:        1500 * time.Millisecond,
	}}
	s, cookie, _, root := newFigcraftEnv(t, fake)
	figcraftFile(t, root, "design/x.figcraft")

	target := figcraftTarget("/v1/workbench/figcraft/render", map[string]string{
		"worktree": root, "path": "design/x.figcraft", "page": "5", "scale": "2", "fp": "f-abc",
	})
	rr := figcraftGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	h := rr.Header()
	for header, want := range map[string]string{
		"Content-Type":           "image/png",
		"X-Content-Type-Options": "nosniff",
		"X-Figcraft-Origin-X":    "12",
		"X-Figcraft-Origin-Y":    "-3.5",
		"X-Figcraft-Scale":       "2",
		"X-Figcraft-Fingerprint": "f-abc",
		"X-Figcraft-Render-Ms":   "1500",
	} {
		if got := h.Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	if h.Get("X-Figcraft-Stale") != "" {
		t.Fatalf("X-Figcraft-Stale = %q, want empty", h.Get("X-Figcraft-Stale"))
	}
	if got := h.Get("Cache-Control"); got != "private, max-age=86400, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if rr.Body.String() != string(png) {
		t.Fatalf("body = %v, want the PNG", rr.Body.Bytes())
	}
	if fake.lastPage != 5 || fake.lastScale != 2 {
		t.Fatalf("page = %d, scale = %v", fake.lastPage, fake.lastScale)
	}

	// A stale render is never immutable and carries the stale header.
	fake.render.Stale = true
	rr = figcraftGet(s, cookie, target)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("stale Cache-Control = %q", got)
	}
	if got := rr.Header().Get("X-Figcraft-Stale"); got != "1" {
		t.Fatalf("X-Figcraft-Stale = %q", got)
	}

	// A fingerprint that does not match is not cached.
	fake.render.Stale = false
	mismatch := figcraftTarget("/v1/workbench/figcraft/render", map[string]string{
		"worktree": root, "path": "design/x.figcraft", "page": "5", "fp": "f-other",
	})
	if got := figcraftGet(s, cookie, mismatch).Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("mismatch Cache-Control = %q", got)
	}
}

func TestWorkbenchFigcraftRenderRequiresPageOrNode(t *testing.T) {
	s, cookie, _, root := newFigcraftEnv(t, &fakeFigcraft{render: figcraft.Render{PNG: []byte{1}, Scale: 1}})
	figcraftFile(t, root, "design/x.figcraft")

	both := figcraftTarget("/v1/workbench/figcraft/render", map[string]string{
		"worktree": root, "path": "design/x.figcraft", "page": "1", "node": "2",
	})
	assertAuthError(t, figcraftGet(s, cookie, both), http.StatusBadRequest, "invalid_request")

	neither := figcraftTarget("/v1/workbench/figcraft/render", map[string]string{
		"worktree": root, "path": "design/x.figcraft",
	})
	assertAuthError(t, figcraftGet(s, cookie, neither), http.StatusBadRequest, "invalid_request")

	// A node render goes through RenderNode with the default scale.
	node := figcraftTarget("/v1/workbench/figcraft/render", map[string]string{
		"worktree": root, "path": "design/x.figcraft", "node": "7",
	})
	if rr := figcraftGet(s, cookie, node); rr.Code != http.StatusOK {
		t.Fatalf("node render = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchFigcraftSettings(t *testing.T) {
	s, cookie, csrf, _ := newFigcraftEnv(t, &fakeFigcraft{})
	target := "/v1/workbench/figcraft/settings"

	// The defaults: live on, no path, no ports.
	rr := figcraftGet(s, cookie, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("get = %d, body = %q", rr.Code, rr.Body.String())
	}
	var defaults figcraft.Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.CLIPath != "" || !defaults.Live || len(defaults.LivePorts) != 0 {
		t.Fatalf("defaults = %+v", defaults)
	}

	// A relative cli_path is 400 invalid_cli_path.
	rr = workbenchRequest(s, http.MethodPut, target, `{"cli_path":"figcraft-cli","live":true,"live_ports":[]}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_cli_path")

	// A valid put is stored and returned.
	rr = workbenchRequest(s, http.MethodPut, target, `{"cli_path":"","live":false,"live_ports":[47810]}`, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("put = %d, body = %q", rr.Code, rr.Body.String())
	}
	var put figcraft.Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	if put.Live || len(put.LivePorts) != 1 || put.LivePorts[0] != 47810 {
		t.Fatalf("put = %+v", put)
	}

	// The next get reads it back.
	rr = figcraftGet(s, cookie, target)
	var got figcraft.Settings
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Live || len(got.LivePorts) != 1 || got.LivePorts[0] != 47810 {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestWorkbenchFigcraftOpen(t *testing.T) {
	s, cookie, csrf, root := newFigcraftEnv(t, &fakeFigcraft{})
	abs := figcraftFile(t, root, "design/x.figcraft")
	var opened string
	s.figcraftOpen = func(_ context.Context, path string) error {
		opened = path
		return nil
	}
	body := `{"worktree":` + strconvQuote(root) + `,"path":"design/x.figcraft"}`
	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/figcraft/open", body, cookie, csrf)
	if runtime.GOOS != "darwin" {
		assertAuthError(t, rr, http.StatusNotImplemented, "unsupported")
		return
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("open = %d, body = %q", rr.Code, rr.Body.String())
	}
	if opened != abs {
		t.Fatalf("opened = %q, want %q", opened, abs)
	}
}

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// newPreviewServer returns a logged-in server whose preview home and temp
// folders live inside a resolved temp tree, so the path policy allows the
// test files without touching the real $HOME (REQ-WP-003). root is that tree.
func newPreviewServer(t *testing.T) (*Server, *http.Cookie, string, string) {
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
	return s, cookie, csrf, root
}

// setPreviewSession installs a fake backend with one session at cwd.
func setPreviewSession(s *Server, id, cwd string) *fakeWorkbench {
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: id, Cwd: cwd}}
	s.SetWorkbench(fake)
	return fake
}

func writePreviewFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// previewPOST sends the REQ-WP-001 request through the authenticated route.
func previewPOST(s *Server, cookie *http.Cookie, csrf, body string) *httptest.ResponseRecorder {
	return workbenchRequest(s, http.MethodPost, "/v1/workbench/preview", body, cookie, csrf)
}

// previewGET sends a cookie-less GET to the preview route (REQ-WP-004).
func previewGET(s *Server, target string) *httptest.ResponseRecorder {
	return serveAuth(s, authRequest(http.MethodGet, target, ""))
}

// previewTokenFrom pulls the token out of a raw_url.
func previewTokenFrom(t *testing.T, rawURL string) string {
	t.Helper()
	rest := strings.TrimPrefix(rawURL, previewBasePath)
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		t.Fatalf("raw_url %q has no name", rawURL)
	}
	return rest[:i]
}

// previewPOSTToken is a POST that must succeed, returning the token and body.
func previewPOSTToken(t *testing.T, s *Server, cookie *http.Cookie, csrf, body string) (workbenchPreviewResponse, string) {
	t.Helper()
	rr := previewPOST(s, cookie, csrf, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp workbenchPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp, previewTokenFrom(t, resp.RawURL)
}

func assertPreviewHeaders(t *testing.T, h http.Header) {
	t.Helper()
	if got := h.Get("Content-Security-Policy"); got != "sandbox allow-scripts allow-forms allow-popups; frame-ancestors 'self'" {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := h.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
	if got := h.Get("X-Frame-Options"); got != "" {
		t.Fatalf("X-Frame-Options should be unset, got %q", got)
	}
}

func TestWorkbenchPreviewRelativeLink(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	file := filepath.Join(cwd, "main.go")
	writePreviewFile(t, file, "package main\n")
	setPreviewSession(s, "s1", cwd)

	rr := previewPOST(s, cookie, csrf, `{"session":"s1","link":"main.go"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	var resp workbenchPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Path != preview.Resolve(file) || resp.Name != "main.go" || resp.Dir != preview.Resolve(cwd) {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Kind != "text" || resp.Size != int64(len("package main\n")) {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Line != 0 || resp.Col != 0 {
		t.Fatalf("line/col = %d/%d", resp.Line, resp.Col)
	}
	if resp.RawURL != previewRawURL(previewTokenFrom(t, resp.RawURL), "main.go") {
		t.Fatalf("raw_url = %q", resp.RawURL)
	}
	if resp.URL != resp.RawURL+"?view=1" {
		t.Fatalf("url = %q, raw_url = %q", resp.URL, resp.RawURL)
	}
}

func TestWorkbenchPreviewHomeLink(t *testing.T) {
	s, cookie, csrf, _ := newPreviewServer(t)
	file := filepath.Join(s.previewHome, "notes.md")
	writePreviewFile(t, file, "# hi\n")
	setPreviewSession(s, "s1", s.previewHome)

	resp, _ := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"~/notes.md"}`)
	if resp.Path != preview.Resolve(file) || resp.Kind != "markdown" {
		t.Fatalf("resp = %+v", resp)
	}
	if !strings.HasSuffix(resp.URL, "?view=1") {
		t.Fatalf("url = %q", resp.URL)
	}
}

func TestWorkbenchPreviewAbsoluteLink(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	file := filepath.Join(root, "abs.txt")
	writePreviewFile(t, file, "hello\n")
	setPreviewSession(s, "s1", root)

	body := `{"session":"s1","link":` + strconvQuote(file) + `}`
	resp, _ := previewPOSTToken(t, s, cookie, csrf, body)
	if resp.Path != preview.Resolve(file) || resp.Name != "abs.txt" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestWorkbenchPreviewLineLink(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "main.go"), "a\nb\nc\n")
	setPreviewSession(s, "s1", cwd)

	resp, _ := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"main.go:3"}`)
	if resp.Line != 3 {
		t.Fatalf("line = %d", resp.Line)
	}
	if !strings.HasSuffix(resp.URL, "?view=1#L3") {
		t.Fatalf("url = %q", resp.URL)
	}
}

func TestWorkbenchPreviewLineColLink(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "main.go"), "a\nb\nc\n")
	setPreviewSession(s, "s1", cwd)

	resp, _ := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"main.go:2:5"}`)
	if resp.Line != 2 || resp.Col != 5 {
		t.Fatalf("line/col = %d/%d", resp.Line, resp.Col)
	}
}

func TestWorkbenchPreviewGitRootFallback(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, "root.txt")
	writePreviewFile(t, file, "root\n")
	setPreviewSession(s, "s1", sub)

	resp, _ := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"root.txt"}`)
	if resp.Path != preview.Resolve(file) {
		t.Fatalf("path = %q, want %q", resp.Path, preview.Resolve(file))
	}
}

func TestWorkbenchPreviewDirFallback(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	space := filepath.Join(root, "space")
	file := filepath.Join(space, "a.go")
	writePreviewFile(t, file, "package space\n")
	setPreviewSession(s, "s1", cwd)

	body := `{"session":"s1","link":"a.go","dir":` + strconvQuote(space) + `}`
	resp, _ := previewPOSTToken(t, s, cookie, csrf, body)
	if resp.Path != preview.Resolve(file) {
		t.Fatalf("path = %q, want %q", resp.Path, preview.Resolve(file))
	}
}

func TestWorkbenchPreviewEmptySessionNoDir(t *testing.T) {
	s, cookie, csrf, _ := newPreviewServer(t)
	// An empty session must not look up a session, so an empty table is fine.
	fake := setPreviewSession(s, "s1", s.previewHome)
	fake.sessions = nil

	rr := previewPOST(s, cookie, csrf, `{"session":"","link":"sub/nope.go"}`)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
	assertPreviewHeaders(t, rr.Header())
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if want := "no such file: " + filepath.Clean("sub/nope.go"); env.Error.Message != want {
		t.Fatalf("message = %q, want %q", env.Error.Message, want)
	}
}

func TestWorkbenchPreviewEmptySessionDir(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	space := filepath.Join(root, "space")
	file := filepath.Join(space, "a.go")
	writePreviewFile(t, file, "package space\n")
	// No session lookup: a relative link is tried against dir alone.
	fake := setPreviewSession(s, "s1", filepath.Join(root, "proj"))
	fake.sessions = nil

	body := `{"session":"","link":"a.go","dir":` + strconvQuote(space) + `}`
	resp, _ := previewPOSTToken(t, s, cookie, csrf, body)
	if resp.Path != preview.Resolve(file) {
		t.Fatalf("path = %q, want %q", resp.Path, preview.Resolve(file))
	}
}

func TestWorkbenchPreviewEmptySessionAbsolute(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	// Absolute and ~ links resolve without any session, so an empty table must
	// not stop them.
	fake := setPreviewSession(s, "s1", root)
	fake.sessions = nil

	file := filepath.Join(root, "abs.txt")
	writePreviewFile(t, file, "hello\n")
	body := `{"session":"","link":` + strconvQuote(file) + `}`
	resp, _ := previewPOSTToken(t, s, cookie, csrf, body)
	if resp.Path != preview.Resolve(file) || resp.Name != "abs.txt" {
		t.Fatalf("resp = %+v", resp)
	}

	homeFile := filepath.Join(s.previewHome, "notes.md")
	writePreviewFile(t, homeFile, "# hi\n")
	resp, _ = previewPOSTToken(t, s, cookie, csrf, `{"session":"","link":"~/notes.md"}`)
	if resp.Path != preview.Resolve(homeFile) {
		t.Fatalf("home path = %q, want %q", resp.Path, preview.Resolve(homeFile))
	}
}

func TestWorkbenchPreviewUnknownSession(t *testing.T) {
	s, cookie, csrf, _ := newPreviewServer(t)
	setPreviewSession(s, "s1", s.previewHome)
	rr := previewPOST(s, cookie, csrf, `{"session":"nope","link":"a.go"}`)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestWorkbenchPreviewBadLink(t *testing.T) {
	s, cookie, csrf, _ := newPreviewServer(t)
	setPreviewSession(s, "s1", s.previewHome)
	for _, link := range []string{"", "http://example.com/x", "https://example.com/x"} {
		body := `{"session":"s1","link":` + strconvQuote(link) + `}`
		rr := previewPOST(s, cookie, csrf, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("link %q: status = %d, body = %q", link, rr.Code, rr.Body.String())
		}
		assertPreviewHeaders(t, rr.Header())
		assertAuthError(t, rr, http.StatusBadRequest, "invalid_link")
	}
}

func TestWorkbenchPreviewMissingFile(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	setPreviewSession(s, "s1", cwd)

	rr := previewPOST(s, cookie, csrf, `{"session":"s1","link":"nope.go"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	want := "no such file: " + filepath.Join(cwd, "nope.go")
	if env.Error.Message != want {
		t.Fatalf("error message = %q, want %q", env.Error.Message, want)
	}
}

// TestWorkbenchPreviewDirectoryAbsolute checks an absolute folder link answers
// 200 kind "dir" with the folder's path and a git root, no token and no URLs.
func TestWorkbenchPreviewDirectoryAbsolute(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	dir := filepath.Join(root, "folder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	setPreviewSession(s, "s1", root)
	gitRoot := filepath.Join(root, "gitroot")
	s.previewGitRoot = func(string) string { return gitRoot }

	body := `{"session":"s1","link":` + strconvQuote(dir) + `}`
	rr := previewPOST(s, cookie, csrf, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	var resp workbenchPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := preview.Resolve(dir)
	if resp.Path != want || resp.Name != "folder" || resp.Dir != want {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Kind != "dir" || resp.Size != 0 || resp.Line != 0 || resp.Col != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.URL != "" || resp.RawURL != "" {
		t.Fatalf("url = %q, raw_url = %q", resp.URL, resp.RawURL)
	}
	if resp.Root != gitRoot {
		t.Fatalf("root = %q, want %q", resp.Root, gitRoot)
	}
	if len(s.previewTokens.tokens) != 0 {
		t.Fatalf("tokens = %d, want none for a folder", len(s.previewTokens.tokens))
	}
}

// TestWorkbenchPreviewDirectoryRelativeLink checks a relative folder link
// resolves against the session cwd and carries the default git root lookup.
func TestWorkbenchPreviewDirectoryRelativeLink(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	setPreviewSession(s, "s1", repo)

	rr := previewPOST(s, cookie, csrf, `{"session":"s1","link":"sub"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var resp workbenchPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := preview.Resolve(sub)
	if resp.Path != want || resp.Name != "sub" || resp.Dir != want || resp.Kind != "dir" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Root != preview.Resolve(repo) {
		t.Fatalf("root = %q, want %q", resp.Root, preview.Resolve(repo))
	}
}

// TestWorkbenchPreviewDirectoryForbidden checks the policy still refuses a
// folder outside the allowed roots, exactly as it does for a file.
func TestWorkbenchPreviewDirectoryForbidden(t *testing.T) {
	s, cookie, csrf, _ := newPreviewServer(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setPreviewSession(s, "s1", s.previewHome)

	body := `{"session":"s1","link":` + strconvQuote(outside) + `}`
	rr := previewPOST(s, cookie, csrf, body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	assertAuthError(t, rr, http.StatusForbidden, "forbidden_path")
}

// TestWorkbenchPreviewFileOmitsRoot checks a regular file keeps its file
// response and carries no "root" key.
func TestWorkbenchPreviewFileOmitsRoot(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "main.go"), "package main\n")
	setPreviewSession(s, "s1", cwd)

	rr := previewPOST(s, cookie, csrf, `{"session":"s1","link":"main.go"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"root"`) {
		t.Fatalf("body has a root key: %s", rr.Body.String())
	}
	var resp workbenchPreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Kind != "text" || resp.URL == "" || resp.RawURL == "" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Root != "" {
		t.Fatalf("root = %q, want empty", resp.Root)
	}
}

func TestWorkbenchPreviewRefusedPaths(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, ".env"), "SECRET=1\n")
	writePreviewFile(t, filepath.Join(cwd, "secrets.env"), "SECRET=1\n")
	secret := filepath.Join(s.previewHome, ".ssh", "id_rsa")
	writePreviewFile(t, secret, "key\n")
	setPreviewSession(s, "s1", cwd)

	for _, link := range []string{".env", "secrets.env", "~/.ssh/id_rsa", secret} {
		body := `{"session":"s1","link":` + strconvQuote(link) + `}`
		rr := previewPOST(s, cookie, csrf, body)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("link %q: status = %d, body = %q", link, rr.Code, rr.Body.String())
		}
		assertPreviewHeaders(t, rr.Header())
		assertAuthError(t, rr, http.StatusForbidden, "forbidden_path")
	}
}

func TestWorkbenchPreviewRefusedThroughSymlink(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	secret := filepath.Join(s.previewHome, ".ssh", "id_rsa")
	writePreviewFile(t, secret, "key\n")
	link := filepath.Join(root, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	setPreviewSession(s, "s1", root)

	body := `{"session":"s1","link":` + strconvQuote(link) + `}`
	rr := previewPOST(s, cookie, csrf, body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchPreviewTokenScope(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	folderA := filepath.Join(root, "a")
	folderB := filepath.Join(root, "b")
	writePreviewFile(t, filepath.Join(folderA, "real.txt"), "real\n")
	writePreviewFile(t, filepath.Join(folderB, "secret.txt"), "secret\n")
	setPreviewSession(s, "s1", folderA)

	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"real.txt"}`)

	// The sibling is reached with an encoded .. so the mux does not redirect;
	// the handler still refuses it because it leaves the token folder.
	rr := previewGET(s, previewBasePath+token+"/%2e%2e/b/secret.txt")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
}

func TestWorkbenchPreviewTokenExpiry(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "main.go"), "package main\n")
	setPreviewSession(s, "s1", cwd)

	resp, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"main.go"}`)
	target := previewBasePath + token + "/main.go"
	if rr := previewGET(s, target); rr.Code != http.StatusOK {
		t.Fatalf("before expiry status = %d, body = %q", rr.Code, rr.Body.String())
	}
	_ = resp
	now = now.Add(31 * time.Minute)
	if rr := previewGET(s, target); rr.Code != http.StatusNotFound {
		t.Fatalf("after expiry status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchPreviewUnknownToken(t *testing.T) {
	s, _, _, _ := newPreviewServer(t)
	rr := previewGET(s, previewBasePath+"deadbeefdeadbeef/secret.go")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
}

func TestWorkbenchPreviewGETNoCookiePOSTNeedsCookie(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "main.go"), "package main\n")
	setPreviewSession(s, "s1", cwd)

	// A cookie-less GET works: the token is the only credential (REQ-WP-004).
	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"main.go"}`)
	if rr := previewGET(s, previewBasePath+token+"/main.go"); rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// The POST without a cookie is refused.
	rr := previewPOST(s, nil, "", `{"session":"s1","link":"main.go"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("POST without cookie status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchPreviewGETRawAndHeaders(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "main.txt"), "hello\n")
	setPreviewSession(s, "s1", cwd)

	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"main.txt"}`)
	rr := previewGET(s, previewBasePath+token+"/main.txt")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	if got := rr.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if rr.Body.String() != "hello\n" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestWorkbenchPreviewGETView(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "readme.md"), "# Title\n")
	setPreviewSession(s, "s1", cwd)

	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"readme.md"}`)
	rr := previewGET(s, previewBasePath+token+"/readme.md?view=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	body := rr.Body.String()
	if !strings.Contains(body, "#1D1C1B") || !strings.Contains(body, "<h1") {
		t.Fatalf("view page = %q", body)
	}

	// The raw bytes are served without view=1.
	rr = previewGET(s, previewBasePath+token+"/readme.md")
	if rr.Code != http.StatusOK || rr.Body.String() != "# Title\n" {
		t.Fatalf("raw status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestWorkbenchPreviewGETViewTheme checks the view page honours &theme=dark
// (REQ-WP-005): the html tag carries data-theme="dark".
func TestWorkbenchPreviewGETViewTheme(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "readme.md"), "# Title\n")
	setPreviewSession(s, "s1", cwd)

	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"readme.md"}`)
	rr := previewGET(s, previewBasePath+token+"/readme.md?view=1&theme=dark")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())
	if body := rr.Body.String(); !strings.Contains(body, `<html data-theme="dark">`) {
		t.Fatalf("view page missing data-theme:\n%s", body)
	}
}

func TestWorkbenchPreviewGETSymlinkLeavesFolder(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	folderA := filepath.Join(root, "a")
	folderB := filepath.Join(root, "b")
	writePreviewFile(t, filepath.Join(folderA, "real.txt"), "real\n")
	target := filepath.Join(folderB, "secret.txt")
	writePreviewFile(t, target, "secret\n")
	if err := os.Symlink(target, filepath.Join(folderA, "out.txt")); err != nil {
		t.Fatal(err)
	}
	setPreviewSession(s, "s1", folderA)

	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"real.txt"}`)
	rr := previewGET(s, previewBasePath+token+"/out.txt")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchPreviewGETNotFoundMessage(t *testing.T) {
	s, _, _, _ := newPreviewServer(t)
	rr := previewGET(s, previewBasePath+"deadbeefdeadbeef/nope.txt")
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
	assertPreviewHeaders(t, rr.Header())
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Message != "not found" {
		t.Fatalf("message = %q, want %q", env.Error.Message, "not found")
	}
}

// TestWorkbenchPreviewHeaderPolicy checks REQ-WP-006 through the full handler
// chain (remoteGate and securityHeaders included): the token GET of an HTML
// file carries the sandbox policy and no X-Frame-Options, while every other
// route keeps the shell policy with frame-ancestors 'none'.
func TestWorkbenchPreviewHeaderPolicy(t *testing.T) {
	s, cookie, csrf, root := newPreviewServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "index.html"), "<h1>hi</h1>")
	setPreviewSession(s, "s1", cwd)

	_, token := previewPOSTToken(t, s, cookie, csrf, `{"session":"s1","link":"index.html"}`)

	rr := previewGET(s, previewBasePath+token+"/index.html")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	assertPreviewHeaders(t, rr.Header())

	state := previewGET(s, "/v1/auth/state")
	if state.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %q", state.Code, state.Body.String())
	}
	want := "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; connect-src 'self' ws://" + s.Addr() + " wss://" + s.Addr() + "; frame-ancestors 'none'"
	if got := state.Header().Get("Content-Security-Policy"); got != want {
		t.Fatalf("state Content-Security-Policy = %q, want %q", got, want)
	}
}

// strconvQuote renders s as a JSON string.
func strconvQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dropPart is one multipart file part of a Workbench drop body.
type dropPart struct {
	field   string
	name    string
	content string
}

// dropMultipart builds a multipart/form-data body holding the given parts and
// returns it with its content type.
func dropMultipart(t *testing.T, parts ...dropPart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		w, err := mw.CreateFormFile(p.field, p.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(p.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// workbenchDropRequest serves an authenticated POST /v1/workbench/drops with
// the given parts, mirroring workbenchRequest's cookie, CSRF, and Origin setup.
func workbenchDropRequest(t *testing.T, s *Server, cookie *http.Cookie, csrf string, parts ...dropPart) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := dropMultipart(t, parts...)
	req := httptest.NewRequest(http.MethodPost, "/v1/workbench/drops", body)
	req.Host = authTestAddr
	req.Header.Set("Content-Type", contentType)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	req.Header.Set("Origin", authTestOrigin)
	return serveAuth(s, req)
}

// dropPaths decodes the paths of a drop response.
func dropPaths(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Paths
}

func TestWorkbenchDropSavesFiles(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)

	rr := workbenchDropRequest(t, s, cookie, csrf,
		dropPart{field: "files", name: "one.txt", content: "hello"},
		dropPart{field: "files", name: "two.bin", content: "world!"},
	)
	paths := dropPaths(t, rr)
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want 2", paths)
	}
	base := filepath.Join(s.auditHome(), "workbench", "drops")
	for i, want := range []string{"hello", "world!"} {
		p := paths[i]
		if !filepath.IsAbs(p) {
			t.Fatalf("path %q is not absolute", p)
		}
		if !strings.HasPrefix(p, base+string(filepath.Separator)) {
			t.Fatalf("path %q is not under %q", p, base)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("file %q = %q, want %q", p, data, want)
		}
	}
	if filepath.Dir(paths[0]) != filepath.Dir(paths[1]) {
		t.Fatalf("files landed in different directories: %v", paths)
	}
	if filepath.Base(paths[0]) != "one.txt" || filepath.Base(paths[1]) != "two.bin" {
		t.Fatalf("names = %q, %q", filepath.Base(paths[0]), filepath.Base(paths[1]))
	}
}

func TestWorkbenchDropSanitizesName(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)

	rr := workbenchDropRequest(t, s, cookie, csrf,
		dropPart{field: "files", name: "../../evil.txt", content: "x"},
	)
	paths := dropPaths(t, rr)
	if len(paths) != 1 {
		t.Fatalf("paths = %v, want 1", paths)
	}
	base := filepath.Join(s.auditHome(), "workbench", "drops")
	p := paths[0]
	if filepath.Base(p) != "evil.txt" {
		t.Fatalf("name = %q, want evil.txt", filepath.Base(p))
	}
	if !strings.HasPrefix(p, base+string(filepath.Separator)) {
		t.Fatalf("path %q is not under %q", p, base)
	}
}

func TestWorkbenchDropRejectsWrongPartName(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)

	rr := workbenchDropRequest(t, s, cookie, csrf,
		dropPart{field: "attachment", name: "a.txt", content: "x"},
	)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")
}

func TestWorkbenchDropRefusedWhenControlLocked(t *testing.T) {
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	cookie, csrf := remoteTokenLogin(t, s, token)

	body, contentType := dropMultipart(t, dropPart{field: "files", name: "a.txt", content: "x"})
	req := httptest.NewRequest(http.MethodPost, "/v1/workbench/drops", body)
	req.RemoteAddr = remoteTestPeer
	req.Host = remoteTestHost
	req.Header.Set(remoteForwardedForHeader, remoteTestIP)
	req.Header.Set(remoteUserLoginHeader, remoteTestLogin)
	req.Header.Set("Origin", "https://"+remoteTestHost)
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(cookie)
	req.Header.Set(csrfHeaderName, csrf)

	rr := serveAuth(s, req)
	assertAuthError(t, rr, http.StatusForbidden, "control_locked")
	if !strings.Contains(rr.Body.String(), controlLockedMessage) {
		t.Fatalf("body = %q, want %q", rr.Body.String(), controlLockedMessage)
	}

	if _, err := os.Stat(filepath.Join(s.auditHome(), "workbench", "drops")); !os.IsNotExist(err) {
		t.Fatalf("drops directory exists after a locked request: %v", err)
	}
}

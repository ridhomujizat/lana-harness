package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// testToolCatalogBackend adds the tool catalog port to the in-memory backend.
type testToolCatalogBackend struct {
	*fakeWebBackend
	catalog func(agent string) ([]ToolGroup, error)
}

func (b *testToolCatalogBackend) ToolCatalog(agent string) ([]ToolGroup, error) {
	return b.catalog(agent)
}

// newToolCatalogTestServer returns an authenticated server with a backend.
// New mounts the catalog route (registerToolRoutes).
func newToolCatalogTestServer(t *testing.T, backend Backend) (*Server, *http.Cookie) {
	t.Helper()
	home := t.TempDir()
	token := authTokenFor(t, home)
	s := newAuthServer(t, home, io.Discard)
	if backend != nil {
		s.SetBackend(backend)
	}
	cookie, _ := loginAndSession(t, s, token)
	return s, cookie
}

// TestToolCatalog covers API-WEB-026: the response shape, group order, the
// asks_first and enabled flags, and the two 404 cases.
func TestToolCatalog(t *testing.T) {
	groups := []ToolGroup{
		{ID: "files", Label: "Files", Tools: []ToolEntry{
			{Name: "read_file", Risk: "read", AsksFirst: false, Enabled: true},
			{Name: "write_file", Risk: "write", AsksFirst: true, Enabled: true},
		}},
		{ID: "shell", Label: "Shell", Tools: []ToolEntry{
			{Name: "bash", Risk: "exec", AsksFirst: true, Enabled: false},
		}},
		{ID: "web", Label: "Web", Tools: []ToolEntry{
			{Name: "web_fetch", Risk: "network", AsksFirst: true, Enabled: true},
		}},
		{ID: "memory", Label: "Memory", Tools: []ToolEntry{
			{Name: "memory_read", Risk: "read", AsksFirst: false, Enabled: true},
		}},
		{ID: "skills", Label: "Skills", Tools: []ToolEntry{}},
		{ID: "chat", Label: "Chat", Tools: []ToolEntry{
			{Name: "task", Risk: "exec", AsksFirst: true, Enabled: true},
		}},
		{ID: "mcp:github", Label: "github", Tools: []ToolEntry{
			{Name: "github/create_issue", Risk: "exec", AsksFirst: true, Enabled: false},
		}},
	}
	backend := &testToolCatalogBackend{
		fakeWebBackend: newFakeWebBackend(),
		catalog: func(agent string) ([]ToolGroup, error) {
			if agent != "coder" {
				return nil, ErrUnknownAgent
			}
			return groups, nil
		},
	}
	s, cookie := newToolCatalogTestServer(t, backend)

	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/tools?agent=coder", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/tools status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got toolCatalogResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Agent != "coder" {
		t.Fatalf("agent = %q, want coder", got.Agent)
	}
	wantOrder := []string{"files", "shell", "web", "memory", "skills", "chat", "mcp:github"}
	if len(got.Groups) != len(wantOrder) {
		t.Fatalf("groups = %+v, want %d groups", got.Groups, len(wantOrder))
	}
	for i, id := range wantOrder {
		if got.Groups[i].ID != id {
			t.Fatalf("group %d = %q, want %q", i, got.Groups[i].ID, id)
		}
	}
	if got.Groups[0].Label != "Files" || got.Groups[1].Label != "Shell" || got.Groups[2].Label != "Web" {
		t.Fatalf("labels = %q, %q, %q", got.Groups[0].Label, got.Groups[1].Label, got.Groups[2].Label)
	}
	if got.Groups[4].Tools == nil {
		t.Fatal("empty group tools is nil, want []")
	}
	bash := got.Groups[1].Tools[0]
	if bash.Name != "bash" || bash.Risk != "exec" || !bash.AsksFirst || bash.Enabled {
		t.Fatalf("bash entry = %+v", bash)
	}
	read := got.Groups[0].Tools[0]
	if read.AsksFirst || !read.Enabled {
		t.Fatalf("read_file entry = %+v", read)
	}
	issue := got.Groups[6].Tools[0]
	if issue.Name != "github/create_issue" || issue.Enabled {
		t.Fatalf("MCP entry = %+v", issue)
	}

	// A missing agent query is the same 404 as an unknown agent.
	for _, target := range []string{"/v1/tools", "/v1/tools?agent=", "/v1/tools?agent=missing"} {
		rr = serveAuth(s, settingsRequest(http.MethodGet, target, "", cookie, ""))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, body = %q", target, rr.Code, rr.Body.String())
		}
		envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()})
		if envelope.Error.Code != "not_found" {
			t.Fatalf("%s error = %+v", target, envelope.Error)
		}
	}
}

// TestToolCatalogErrors checks the two failure modes behind the catalog route:
// a backend error is 500 and a server without the port is 500.
func TestToolCatalogErrors(t *testing.T) {
	backend := &testToolCatalogBackend{
		fakeWebBackend: newFakeWebBackend(),
		catalog: func(string) ([]ToolGroup, error) {
			return nil, errors.New("boom")
		},
	}
	s, cookie := newToolCatalogTestServer(t, backend)
	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/tools?agent=coder", "", cookie, ""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("backend error status = %d, body = %q", rr.Code, rr.Body.String())
	}

	s, cookie = newToolCatalogTestServer(t, nil)
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/tools?agent=coder", "", cookie, ""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("missing port status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

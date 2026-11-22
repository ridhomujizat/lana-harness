package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agenthook"
)

// agentHookAnswer is the GET and PUT answer of the hook setting.
type agentHookAnswer struct {
	Enabled bool `json:"enabled"`
	Folders []struct {
		Dir       string `json:"dir"`
		Account   string `json:"account"`
		Installed bool   `json:"installed"`
		Error     string `json:"error"`
	} `json:"folders"`
}

// writeAgentHookFlag writes the switch file so SetAgentHook's background
// install is quiet while a test drives the routes itself.
func writeAgentHookFlag(t *testing.T, s *Server, enabled bool) {
	t.Helper()
	path := s.agentHookEnabledPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]bool{"enabled": enabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeAgentHook(t *testing.T, rr *httptest.ResponseRecorder) agentHookAnswer {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body agentHookAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	return body
}

// folderView returns the answer row of a folder.
func folderView(t *testing.T, body agentHookAnswer, dir string) struct {
	Dir       string `json:"dir"`
	Account   string `json:"account"`
	Installed bool   `json:"installed"`
	Error     string `json:"error"`
} {
	t.Helper()
	for _, folder := range body.Folders {
		if folder.Dir == dir {
			return folder
		}
	}
	t.Fatalf("folder %q is missing from %+v", dir, body.Folders)
	return body.Folders[0]
}

// settingsInstalled reports whether Lana's hook runs bin in the folder.
func settingsInstalled(t *testing.T, folder, bin string) bool {
	t.Helper()
	installed, err := agenthook.Installed(filepath.Join(folder, "settings.json"), bin)
	if err != nil {
		t.Fatalf("Installed: %v", err)
	}
	return installed
}

func TestWorkbenchAgentHookGetInstalls(t *testing.T) {
	s, cookie, _ := newWorkbenchServer(t)
	writeAgentHookFlag(t, s, false)
	folder := t.TempDir()
	const bin = "/opt/lana/lana"
	s.SetAgentHook(AgentHookConfig{
		Binary:  bin,
		Folders: func() []AgentHookFolder { return []AgentHookFolder{{Dir: folder, Account: "claude-code"}} },
		Allowed: true,
	})
	// The switch is on, and the folder has no hook yet.
	writeAgentHookFlag(t, s, true)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	body := decodeAgentHook(t, rr)
	if !body.Enabled {
		t.Fatal("enabled = false")
	}
	view := folderView(t, body, folder)
	if view.Account != "claude-code" || !view.Installed || view.Error != "" {
		t.Fatalf("folder = %+v", view)
	}
	if !settingsInstalled(t, folder, bin) {
		t.Fatal("GET did not install the hook")
	}
}

func TestWorkbenchAgentHookPut(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	writeAgentHookFlag(t, s, false)
	folder := t.TempDir()
	const bin = "/opt/lana/lana"
	s.SetAgentHook(AgentHookConfig{
		Binary:  bin,
		Folders: func() []AgentHookFolder { return []AgentHookFolder{{Dir: folder, Account: "claude-code"}} },
		Allowed: true,
	})

	rr := workbenchRequest(s, http.MethodPut, "/v1/workbench/agent-hook", `{"enabled":true}`, cookie, csrf)
	body := decodeAgentHook(t, rr)
	if !body.Enabled || !folderView(t, body, folder).Installed {
		t.Fatalf("PUT true answer = %+v", body)
	}
	if !settingsInstalled(t, folder, bin) {
		t.Fatal("PUT true did not install the hook")
	}

	rr = workbenchRequest(s, http.MethodPut, "/v1/workbench/agent-hook", `{"enabled":false}`, cookie, csrf)
	body = decodeAgentHook(t, rr)
	if body.Enabled || folderView(t, body, folder).Installed {
		t.Fatalf("PUT false answer = %+v", body)
	}
	if settingsInstalled(t, folder, bin) {
		t.Fatal("PUT false did not remove the hook")
	}
}

func TestWorkbenchAgentHookInvalidJSONFolder(t *testing.T) {
	s, cookie, _ := newWorkbenchServer(t)
	writeAgentHookFlag(t, s, false)
	good := t.TempDir()
	bad := t.TempDir()
	const bin = "/opt/lana/lana"
	if err := os.WriteFile(filepath.Join(bad, "settings.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetAgentHook(AgentHookConfig{
		Binary: bin,
		Folders: func() []AgentHookFolder {
			return []AgentHookFolder{{Dir: good, Account: "home"}, {Dir: bad, Account: "work"}}
		},
		Allowed: true,
	})
	writeAgentHookFlag(t, s, true)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	body := decodeAgentHook(t, rr)
	if !body.Enabled {
		t.Fatal("enabled = false")
	}
	if view := folderView(t, body, bad); view.Installed || view.Error != "settings.json is not valid JSON" {
		t.Fatalf("bad folder = %+v", view)
	}
	if view := folderView(t, body, good); !view.Installed || view.Error != "" {
		t.Fatalf("good folder = %+v", view)
	}
	// The bad file is left alone, the good one is installed.
	data, err := os.ReadFile(filepath.Join(bad, "settings.json"))
	if err != nil || string(data) != "{ not json" {
		t.Fatalf("bad settings.json changed: %q, %v", data, err)
	}
	if !settingsInstalled(t, good, bin) {
		t.Fatal("the good folder was not installed")
	}
}

func TestWorkbenchAgentHookNotAllowed(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	folder := t.TempDir()
	s.SetAgentHook(AgentHookConfig{
		Binary:  "/opt/lana/lana",
		Folders: func() []AgentHookFolder { return []AgentHookFolder{{Dir: folder, Account: "claude-code"}} },
		Allowed: false,
	})

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	body := decodeAgentHook(t, rr)
	if body.Enabled {
		t.Fatal("GET enabled = true for a daemon that may not manage the hook")
	}
	if view := folderView(t, body, folder); view.Installed || view.Error != "only the main Lana daemon manages the hook" {
		t.Fatalf("folder = %+v", view)
	}

	rr = workbenchRequest(s, http.MethodPut, "/v1/workbench/agent-hook", `{"enabled":true}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "not_allowed")

	if _, err := os.Stat(filepath.Join(folder, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a daemon that may not manage the hook wrote settings.json: %v", err)
	}
}

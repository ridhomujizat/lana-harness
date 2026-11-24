package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agenthook"
)

// agentHookAnswer is the GET and PUT answer of the hook setting.
type agentHookAnswer struct {
	Enabled      bool   `json:"enabled"`
	State        string `json:"state"`
	NeedsInstall bool   `json:"needs_install"`
	Folders      []struct {
		Dir       string `json:"dir"`
		Account   string `json:"account"`
		Installed bool   `json:"installed"`
		Error     string `json:"error"`
	} `json:"folders"`
}

// writeAgentHookFlag writes the switch file so SetAgentHook's background
// install is quiet while a test drives the routes itself: false is off, so no
// install runs.
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

// hookFolderList is a folder list a test can extend between calls, so a GET can
// pick up an account added after the first one.
type hookFolderList struct {
	mu      sync.Mutex
	folders []AgentHookFolder
}

func (l *hookFolderList) set(folders ...AgentHookFolder) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.folders = folders
}

func (l *hookFolderList) get() []AgentHookFolder {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AgentHookFolder(nil), l.folders...)
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
	if !body.Enabled || body.State != "on" || body.NeedsInstall {
		t.Fatalf("answer = %+v", body)
	}
	view := folderView(t, body, folder)
	if view.Account != "claude-code" || !view.Installed || view.Error != "" {
		t.Fatalf("folder = %+v", view)
	}
	if !settingsInstalled(t, folder, bin) {
		t.Fatal("GET did not install the hook")
	}
}

func TestWorkbenchAgentHookGetUnset(t *testing.T) {
	s, cookie, _ := newWorkbenchServer(t)
	folder := t.TempDir()
	const bin = "/opt/lana/lana"
	settings := filepath.Join(folder, "settings.json")
	before := []byte("{\n  \"permissions\": {}\n}\n")
	if err := os.WriteFile(settings, before, 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetAgentHook(AgentHookConfig{
		Binary:  bin,
		Folders: func() []AgentHookFolder { return []AgentHookFolder{{Dir: folder, Account: "claude-code"}} },
		Allowed: true,
	})

	// No switch file: the owner was never asked.
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	body := decodeAgentHook(t, rr)
	if body.Enabled || body.State != "unset" || !body.NeedsInstall {
		t.Fatalf("answer = %+v", body)
	}
	if view := folderView(t, body, folder); view.Installed || view.Error != "" {
		t.Fatalf("folder = %+v", view)
	}
	after, err := os.ReadFile(settings)
	if err != nil || string(after) != string(before) {
		t.Fatalf("GET changed settings.json: %q, %v", after, err)
	}
	if _, err := os.Stat(s.agentHookEnabledPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("GET created agent-hook.json: %v", err)
	}
}

func TestWorkbenchAgentHookUnsetDoesNotInstall(t *testing.T) {
	s, _, _ := newWorkbenchServer(t)
	folder := t.TempDir()
	cfg := AgentHookConfig{
		Binary:  "/opt/lana/lana",
		Folders: func() []AgentHookFolder { return []AgentHookFolder{{Dir: folder, Account: "claude-code"}} },
		Allowed: true,
	}
	s.SetAgentHook(cfg)
	// Run the daemon-start body once. Unset asks nothing, so no settings.json
	// appears.
	s.installAgentHook(cfg)
	if _, err := os.Stat(filepath.Join(folder, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unset installed the hook: %v", err)
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
	if !body.Enabled || body.State != "on" || body.NeedsInstall || !folderView(t, body, folder).Installed {
		t.Fatalf("PUT true answer = %+v", body)
	}
	if !settingsInstalled(t, folder, bin) {
		t.Fatal("PUT true did not install the hook")
	}

	rr = workbenchRequest(s, http.MethodPut, "/v1/workbench/agent-hook", `{"enabled":false}`, cookie, csrf)
	body = decodeAgentHook(t, rr)
	if body.Enabled || body.State != "off" || !body.NeedsInstall || folderView(t, body, folder).Installed {
		t.Fatalf("PUT false answer = %+v", body)
	}
	if settingsInstalled(t, folder, bin) {
		t.Fatal("PUT false did not remove the hook")
	}
}

func TestWorkbenchAgentHookPutOffFromUnset(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)
	folder := t.TempDir()
	s.SetAgentHook(AgentHookConfig{
		Binary:  "/opt/lana/lana",
		Folders: func() []AgentHookFolder { return []AgentHookFolder{{Dir: folder, Account: "claude-code"}} },
		Allowed: true,
	})

	rr := workbenchRequest(s, http.MethodPut, "/v1/workbench/agent-hook", `{"enabled":false}`, cookie, csrf)
	body := decodeAgentHook(t, rr)
	if body.Enabled || body.State != "off" || !body.NeedsInstall {
		t.Fatalf("answer = %+v", body)
	}
	if view := folderView(t, body, folder); view.Installed || view.Error != "" {
		t.Fatalf("folder = %+v", view)
	}
	// Remove is a no-op from unset, so no settings.json appears.
	if _, err := os.Stat(filepath.Join(folder, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PUT false wrote settings.json: %v", err)
	}
	// The stored off sticks: a later GET still reports off.
	data, err := os.ReadFile(s.agentHookEnabledPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Enabled {
		t.Fatalf("switch file = %q, %v", data, err)
	}
	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	if state := decodeAgentHook(t, rr).State; state != "off" {
		t.Fatalf("state after PUT false = %q, want off", state)
	}
}

func TestWorkbenchAgentHookGetInstallsNewFolder(t *testing.T) {
	s, cookie, _ := newWorkbenchServer(t)
	writeAgentHookFlag(t, s, true)
	first := t.TempDir()
	second := t.TempDir()
	const bin = "/opt/lana/lana"
	list := &hookFolderList{}
	list.set(AgentHookFolder{Dir: first, Account: "home"})
	s.SetAgentHook(AgentHookConfig{
		Binary:  bin,
		Folders: list.get,
		Allowed: true,
	})

	workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	if !settingsInstalled(t, first, bin) {
		t.Fatal("GET did not install the first folder")
	}

	// A new account shows up: the next GET installs into it too.
	list.set(AgentHookFolder{Dir: first, Account: "home"}, AgentHookFolder{Dir: second, Account: "work"})
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/agent-hook", "", cookie, "")
	body := decodeAgentHook(t, rr)
	if body.State != "on" || body.NeedsInstall {
		t.Fatalf("answer = %+v", body)
	}
	if !settingsInstalled(t, second, bin) {
		t.Fatal("GET did not install the new folder")
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
	if !body.Enabled || body.State != "on" {
		t.Fatalf("answer = %+v", body)
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
	if body.Enabled || body.State != "off" || body.NeedsInstall {
		t.Fatalf("GET answer = %+v", body)
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

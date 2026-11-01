package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// TestAgentFileLimitCoversACWEB027 covers AC-WEB-027: USER.md over its limit
// is refused with 422 over_limit and the file is unchanged.
func TestAgentFileLimitCoversACWEB027(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	path := agenthome.SharedUser(home)
	content := strings.Repeat("u", agenthome.UserLimit)
	payload, err := json.Marshal(map[string]string{"content": content, "expected_version": fileVersion(nil)})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/user", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	over := strings.Repeat("u", agenthome.UserLimit+1)
	payload, err = json.Marshal(map[string]string{"content": over, "expected_version": fileVersion(before)})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/user", string(payload), cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-limit status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string           `json:"code"`
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "over_limit" {
		t.Fatalf("code = %q, body = %q", envelope.Error.Code, rr.Body.String())
	}
	if len(envelope.Error.Details) != 1 || envelope.Error.Details[0]["limit"] != float64(agenthome.UserLimit) {
		t.Fatalf("details = %+v", envelope.Error.Details)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("over-limit save changed USER.md")
	}

	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents/coder/files/user", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got agentFileResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Characters != agenthome.UserLimit || got.Limit == nil || *got.Limit != agenthome.UserLimit {
		t.Fatalf("GET response = %+v", got)
	}
}

// TestAgentFileEditIsUsedNextTurn is the handler half of AC-WEB-027; the
// cmd/lana TestWebNextTurn test proves the next prompt contains the text.
func TestAgentFileEditIsUsedNextTurn(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	content := "You are the new persona."
	payload, err := json.Marshal(map[string]string{"content": content, "expected_version": fileVersion(nil)})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/agent", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	path := agenthome.Paths(home, "coder").Agent
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %04o, want 0600", mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("file = %q", data)
	}

	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents/coder/files/agent", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got agentFileResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != content || got.Version != fileVersion([]byte(content)) || got.Characters != utf8.RuneCountInString(content) || got.Limit != nil {
		t.Fatalf("GET response = %+v", got)
	}

	// The same content is a no-op with the same version.
	payload, err = json.Marshal(map[string]string{"content": content, "expected_version": got.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/agent", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-op PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	// A stale version is refused.
	payload, err = json.Marshal(map[string]string{"content": "other", "expected_version": "stale"})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/agent", string(payload), cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestAgentFileUnknownAgentAndFile(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	for _, target := range []string{
		"/v1/agents/missing/files/agent",
		"/v1/agents/coder/files/notes",
		"/v1/agents/Bad_Name/files/agent",
	} {
		rr := serveAuth(s, settingsRequest(http.MethodGet, target, "", cookie, ""))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, body = %q", target, rr.Code, rr.Body.String())
		}
	}
	payload := `{"content":"x","expected_version":"` + fileVersion(nil) + `"}`
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/missing/files/agent", payload, cookie, csrf))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown agent status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "missing", "AGENT.md")); !os.IsNotExist(err) {
		t.Fatalf("unknown agent file exists: %v", err)
	}
}

func TestAgentMemoryLimit(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	over := strings.Repeat("m", agenthome.MemoryLimit+1)
	payload, err := json.Marshal(map[string]string{"content": over, "expected_version": fileVersion(nil)})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/memory", string(payload), cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	path := agenthome.Paths(home, "coder").Memory
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("over-limit memory file exists: %v", err)
	}
}

// TestAgentJobFile covers REQ-ROOM-009 and API-ROOM-007: the job file reads and
// writes $LANA_HOME/agents/<name>/JOB.md with limit 4000 and the agent file
// shape, a missing file reads as empty, and an over-limit save is refused.
func TestAgentJobFile(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	path := agenthome.Paths(home, "coder").Job

	// A missing file reads as empty content with the 4000 limit.
	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents/coder/files/job", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got agentFileResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != "" || got.Characters != 0 || got.Limit == nil || *got.Limit != agenthome.JobLimit {
		t.Fatalf("missing GET response = %+v", got)
	}

	content := "• Catat setiap transaksi"
	payload, err := json.Marshal(map[string]string{"content": content, "expected_version": got.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/job", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("JOB.md = %q, want %q", data, content)
	}

	// GET reads it back with the limit.
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents/coder/files/job", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != content || got.Characters != utf8.RuneCountInString(content) || got.Version != fileVersion([]byte(content)) {
		t.Fatalf("GET response = %+v", got)
	}
	if got.Limit == nil || *got.Limit != agenthome.JobLimit {
		t.Fatalf("GET limit = %v, want %d", got.Limit, agenthome.JobLimit)
	}

	// Over the limit is 422 and leaves the file unchanged.
	over := strings.Repeat("j", agenthome.JobLimit+1)
	payload, err = json.Marshal(map[string]string{"content": over, "expected_version": fileVersion([]byte(content))})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder/files/job", string(payload), cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-limit status = %d, body = %q", rr.Code, rr.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != content {
		t.Fatal("over-limit save changed JOB.md")
	}
}

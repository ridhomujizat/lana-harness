package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

const validGitSkill = `---
name: git-workflow
description: Commit and review changes safely
---
Run the checks before committing.
`

// TestSkillsCreateCoversACWEB028 covers AC-WEB-028: a valid skill is created
// for an agent scope and listed, and a SKILL.md without description is 422.
func TestSkillsCreateCoversACWEB028(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)

	payload, err := json.Marshal(map[string]string{"content": validGitSkill, "expected_version": ""})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var created putSkillResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(agenthome.AgentSkillsDir(home, "coder"), "git-workflow", "SKILL.md")
	if created.Path != wantPath || created.Version != fileVersion([]byte(validGitSkill)) {
		t.Fatalf("create response = %+v", created)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %04o, want 0600", mode)
	}

	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills?scope=agent:coder", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list listSkillsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 1 || list.Skills[0].Name != "git-workflow" || list.Skills[0].Scope != "agent:coder" || !list.Skills[0].Valid {
		t.Fatalf("agent scope list = %+v", list)
	}
	if list.Skills[0].Description != "Commit and review changes safely" {
		t.Fatalf("description = %q", list.Skills[0].Description)
	}

	// The all-scopes list includes the agent skill.
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("all list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	list = listSkillsResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 1 || list.Skills[0].Scope != "agent:coder" {
		t.Fatalf("all list = %+v", list)
	}

	// Missing description is a validation failure.
	bad := "---\nname: no-description\n---\nBody\n"
	payload, err = json.Marshal(map[string]string{"content": bad, "expected_version": ""})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/no-description", string(payload), cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing description status = %d, body = %q", rr.Code, rr.Body.String())
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
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) != 1 || envelope.Error.Details[0]["field"] != "description" {
		t.Fatalf("validation response = %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(agenthome.AgentSkillsDir(home, "coder"), "no-description")); !os.IsNotExist(err) {
		t.Fatalf("invalid skill left a directory: %v", err)
	}
}

func TestSkillsReadCreateConflictAndEdit(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)

	payload, err := json.Marshal(map[string]string{"content": validGitSkill, "expected_version": ""})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// Read it back.
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills/agent:coder/git-workflow", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got skillContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != validGitSkill || got.Version != fileVersion([]byte(validGitSkill)) {
		t.Fatalf("GET response = %+v", got)
	}

	// Creating an existing name is a conflict.
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// Same content with the current version is a no-op.
	payload, err = json.Marshal(map[string]string{"content": validGitSkill, "expected_version": got.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-op status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// A stale version is refused.
	changed := strings.Replace(validGitSkill, "Commit and review changes safely", "Updated description", 1)
	payload, err = json.Marshal(map[string]string{"content": changed, "expected_version": "stale"})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale edit status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// A valid edit with the current version succeeds.
	payload, err = json.Marshal(map[string]string{"content": changed, "expected_version": got.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("edit status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var edited putSkillResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	if edited.Version != fileVersion([]byte(changed)) {
		t.Fatalf("edited version = %q", edited.Version)
	}
}

func TestSkillsGlobalScopeAndErrors(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	global := strings.Replace(validGitSkill, "name: git-workflow", "name: global-skill", 1)
	payload, err := json.Marshal(map[string]string{"content": global, "expected_version": ""})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/global/global-skill", string(payload), cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("global create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills?scope=global", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("global list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list listSkillsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 1 || list.Skills[0].Scope != "global" || list.Skills[0].Name != "global-skill" {
		t.Fatalf("global list = %+v", list)
	}

	for _, target := range []string{
		"/v1/skills/agent:missing/global-skill",
		"/v1/skills/agent:missing",
	} {
		rr = serveAuth(s, settingsRequest(http.MethodGet, target, "", cookie, ""))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, body = %q", target, rr.Code, rr.Body.String())
		}
	}
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills?scope=team:coder", "", cookie, ""))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad scope status = %d, body = %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills/global/not-there", "", cookie, ""))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing skill status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestSkillsInvalidFrontmatter(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	cases := []struct {
		name    string
		content string
	}{
		{name: "no frontmatter", content: "just a body\n"},
		{name: "name mismatch", content: "---\nname: other\ndescription: d\n---\nBody\n"},
		{name: "missing name", content: "---\ndescription: d\n---\nBody\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]string{"content": tc.content, "expected_version": ""})
			if err != nil {
				t.Fatal(err)
			}
			rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/agent:coder/git-workflow", string(payload), cookie, csrf))
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "validation_failed") {
				t.Fatalf("body = %q", rr.Body.String())
			}
		})
	}
}

func skillFixture(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
}

func writeSkillFixture(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeSkillError(t *testing.T, rr *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return envelope
}

// TestSkillsListCountsAndDeleteCoversACWEB051 covers AC-WEB-051: ten valid
// skills and one invalid skill are returned with their counts, size, update
// time, and error; deleting a skill folder makes the runtime skip its listed
// name without an error.
func TestSkillsListCountsAndDeleteCoversACWEB051(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)

	global := agenthome.GlobalSkillsDir(home)
	firstText := ""
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		text := skillFixture(name, "Description of "+name, "Body of "+name)
		if i == 0 {
			firstText = text
		}
		writeSkillFixture(t, filepath.Join(global, name), text)
	}
	invalidText := "---\nname: broken\n---\nBody\n"
	writeSkillFixture(t, filepath.Join(global, "broken"), invalidText)

	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/skills", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list listSkillsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 10 || len(list.Invalid) != 1 {
		t.Fatalf("counts = %d valid, %d invalid", len(list.Skills), len(list.Invalid))
	}
	bad := list.Invalid[0]
	if bad.Name != "broken" || bad.Scope != "global" || bad.Valid || bad.Error != "missing description" {
		t.Fatalf("invalid entry = %+v", bad)
	}
	if bad.Size != utf8.RuneCountInString(invalidText) || bad.UpdatedAt <= 0 {
		t.Fatalf("invalid size/updated_at = %d/%d", bad.Size, bad.UpdatedAt)
	}
	found := false
	for _, item := range list.Skills {
		if item.Valid != true || item.Size <= 0 || item.UpdatedAt <= 0 {
			t.Fatalf("valid entry = %+v", item)
		}
		if item.Name == "skill-00" {
			found = true
			if item.Size != utf8.RuneCountInString(firstText) {
				t.Fatalf("size = %d, want %d", item.Size, utf8.RuneCountInString(firstText))
			}
			if item.Description != "Description of skill-00" {
				t.Fatalf("description = %q", item.Description)
			}
		}
	}
	if !found {
		t.Fatal("skill-00 is missing from the list")
	}

	// Delete the skill and check that its folder is gone.
	payload, err := json.Marshal(map[string]string{"expected_version": fileVersion([]byte(firstText))})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/skills/global/skill-00", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var deleted deleteSkillResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Name != "skill-00" || deleted.Scope != "global" || !deleted.Deleted {
		t.Fatalf("delete response = %+v", deleted)
	}
	if _, err := os.Stat(filepath.Join(global, "skill-00")); !os.IsNotExist(err) {
		t.Fatalf("skill folder still exists: %v", err)
	}

	// An agent that still lists the deleted name loads without an error (Q-026).
	visible, invalid := agenthome.VisibleSkills(home, "coder", []string{"skill-00"})
	if len(visible) != 0 {
		t.Fatalf("visible = %#v, want no visible skill", visible)
	}
	for _, item := range invalid {
		if filepath.Base(item.Path) == "skill-00" {
			t.Fatalf("deleted skill reported invalid: %#v", item)
		}
	}
}

// TestSkillsDeleteStaleVersionAndCSRF covers the DELETE version and CSRF rules:
// a stale expected_version and a missing CSRF header write nothing.
func TestSkillsDeleteStaleVersionAndCSRF(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)

	text := skillFixture("git-workflow", "Commit safely", "Run the checks")
	payload, err := json.Marshal(map[string]string{"content": text, "expected_version": ""})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/global/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	path := filepath.Join(agenthome.GlobalSkillsDir(home), "git-workflow", "SKILL.md")

	// A stale version is a 409 and the file stays as it was.
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/skills/global/git-workflow", `{"expected_version":"stale"}`, cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale delete status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeSkillError(t, rr); envelope.Error.Code != "stale_version" {
		t.Fatalf("stale delete error = %+v", envelope.Error)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != text {
		t.Fatalf("file after stale delete = %q, %v", data, err)
	}

	// A missing CSRF header is refused the same way for DELETE and PUT.
	good, err := json.Marshal(map[string]string{"expected_version": fileVersion([]byte(text))})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/skills/global/git-workflow", string(good), cookie, ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("delete without CSRF status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeSkillError(t, rr); envelope.Error.Code != "csrf_invalid" {
		t.Fatalf("delete without CSRF error = %+v", envelope.Error)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/global/git-workflow", string(payload), cookie, ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("put without CSRF status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeSkillError(t, rr); envelope.Error.Code != "csrf_invalid" {
		t.Fatalf("put without CSRF error = %+v", envelope.Error)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file after refused deletes: %v", err)
	}

	// Unknown scope agent and unknown skill are 404.
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/skills/global/missing", string(good), cookie, csrf))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown skill status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeSkillError(t, rr); envelope.Error.Code != "not_found" {
		t.Fatalf("unknown skill error = %+v", envelope.Error)
	}
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/skills/agent:missing/git-workflow", string(good), cookie, csrf))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown agent status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeSkillError(t, rr); envelope.Error.Code != "not_found" {
		t.Fatalf("unknown agent error = %+v", envelope.Error)
	}

	// The current version deletes it.
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/skills/global/git-workflow", string(good), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(agenthome.GlobalSkillsDir(home), "git-workflow")); !os.IsNotExist(err) {
		t.Fatalf("skill folder still exists: %v", err)
	}
}

// TestSkillsLimitsAndAgentAddCoversACWEB052 covers AC-WEB-052: a 301 character
// description and a 20,001 character body are 422 validation failures naming
// the field and write nothing, and adding the skill to coder's skills list puts
// the name in that list.
func TestSkillsLimitsAndAgentAddCoversACWEB052(t *testing.T) {
	home := t.TempDir()
	store := &testSettingsStore{home: home, agents: []string{"coder"}}
	s, cookie, csrf := newSettingsTestServer(t, store)

	cases := []struct {
		name        string
		description string
		body        string
		field       string
	}{
		{name: "long-description", description: strings.Repeat("x", 301), body: "Body", field: "description"},
		{name: "long-body", description: "Fine", body: strings.Repeat("x", 20001), field: "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := skillFixture(tc.name, tc.description, tc.body)
			payload, err := json.Marshal(map[string]string{"content": text, "expected_version": ""})
			if err != nil {
				t.Fatal(err)
			}
			rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/global/"+tc.name, string(payload), cookie, csrf))
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
			}
			envelope := decodeSkillError(t, rr)
			if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) != 1 || envelope.Error.Details[0]["field"] != tc.field {
				t.Fatalf("validation response = %s", rr.Body.String())
			}
			if _, err := os.Stat(filepath.Join(agenthome.GlobalSkillsDir(home), tc.name)); !os.IsNotExist(err) {
				t.Fatalf("invalid skill left a directory: %v", err)
			}
		})
	}

	// Adding the global skill to coder's skills list uses the existing config
	// save path that owns agents.<name>.skills (API-WEB-025).
	raw := strings.Replace(configMaskRaw, `model = "coder"`, "model = \"coder\"\nskills = []", 1)
	configStore := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s.SetConfigStore(configStore)

	text := skillFixture("git-workflow", "Commit safely", "Run the checks")
	payload, err := json.Marshal(map[string]string{"content": text, "expected_version": ""})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/skills/global/git-workflow", string(payload), cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}

	updated := strings.Replace(raw, "skills = []", `skills = ["git-workflow"]`, 1)
	payload, err = json.Marshal(map[string]string{"raw": updated, "expected_version": configStore.loaded})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/config", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("config save status = %d, body = %q", rr.Code, rr.Body.String())
	}
	saved, err := os.ReadFile(configStore.path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.Check(t.Context(), configStore.home, saved)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(result.Config.Agents["coder"].Skills, "git-workflow") {
		t.Fatalf("coder skills = %v", result.Config.Agents["coder"].Skills)
	}
}

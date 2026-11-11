package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

// teamTestConfig has comments before and after the table so a replace can show
// that only the table lines change (AC-TEAM-039).
const teamTestConfig = `version = 1

# agents
[agents.lead]
[agents.researcher]
[agents.writer]

# team block
[teams.research-squad]
lead = "lead"
members = ["researcher"]
description = "Old description"

# after the team
[web]
enabled = true
`

// teamCreateConfig has no teams, so a create appends the first table.
const teamCreateConfig = `version = 1

[agents.lead]
[agents.coder]
`

// teamRouteConfig has a route that names the team, so a delete fails
// config.Check (AC-TEAM-042).
const teamRouteConfig = `version = 1

[agents.lead]
[agents.researcher]

[teams.research-squad]
lead = "lead"
members = ["researcher"]

[[routes]]
channel = "telegram"
agent = "research-squad"
`

// teamDeleteConfig has a blank separator directly before the table, the case
// where a delete removes the table and that blank line (API-TEAM-010).
const teamDeleteConfig = `version = 1

[agents.lead]
[agents.researcher]

[teams.research-squad]
lead = "lead"
members = ["researcher"]

[web]
enabled = true
`

func newTeamTestServer(t *testing.T, raw string) (*Server, *testConfigStore, *http.Cookie, string) {
	t.Helper()
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)
	return s, store, cookie, csrf
}

func serveTeam(t *testing.T, s *Server, method, target, body string, cookie *http.Cookie, csrf string) *httptestResponse {
	t.Helper()
	rr := serveAuth(s, settingsRequest(method, target, body, cookie, csrf))
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

func teamVersion(t *testing.T, s *Server, name string, cookie *http.Cookie) teamJSON {
	t.Helper()
	response := serveTeam(t, s, http.MethodGet, "/v1/teams/"+name, "", cookie, "")
	if response.code != http.StatusOK {
		t.Fatalf("GET /v1/teams/%s status = %d, body = %q", name, response.code, response.body)
	}
	var team teamJSON
	response.json(t, &team)
	return team
}

func teamJSONBody(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readTeamFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestTeamPutKeepsCommentsCoversACTEAM039 covers AC-TEAM-039: a replace
// changes only the table lines, keeps the comments around it, and reports
// restart_required.
func TestTeamPutKeepsCommentsCoversACTEAM039(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamTestConfig)

	before := teamVersion(t, s, "research-squad", cookie)
	if before.Name != "research-squad" || before.Lead != "lead" || before.Description != "Old description" {
		t.Fatalf("GET team = %+v", before)
	}
	if !reflect.DeepEqual(before.Members, []string{"researcher"}) {
		t.Fatalf("members = %v", before.Members)
	}
	if before.Version != store.loaded {
		t.Fatalf("version = %q, want the file hash %q", before.Version, store.loaded)
	}
	if before.RestartRequired {
		t.Fatal("restart_required is true before any save")
	}

	body := teamJSONBody(t, map[string]any{
		"lead":             "lead",
		"members":          []string{"researcher", "writer"},
		"description":      "New description",
		"expected_version": before.Version,
	})
	response := serveTeam(t, s, http.MethodPut, "/v1/teams/research-squad", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", response.code, response.body)
	}
	var updated teamJSON
	response.json(t, &updated)
	if updated.Name != "research-squad" || updated.Lead != "lead" || updated.Description != "New description" {
		t.Fatalf("PUT response = %+v", updated)
	}
	if !reflect.DeepEqual(updated.Members, []string{"researcher", "writer"}) {
		t.Fatalf("PUT members = %v", updated.Members)
	}
	if !updated.RestartRequired {
		t.Fatal("PUT restart_required = false, want true")
	}
	if updated.Version == before.Version {
		t.Fatal("PUT version did not change")
	}

	after := readTeamFile(t, store.path)
	want := strings.Replace(teamTestConfig,
		"[teams.research-squad]\nlead = \"lead\"\nmembers = [\"researcher\"]\ndescription = \"Old description\"\n",
		"[teams.research-squad]\nlead = 'lead'\nmembers = ['researcher', 'writer']\ndescription = 'New description'\n",
		1)
	if string(after) != want {
		t.Fatalf("config after replace:\n got %q\nwant %q", after, want)
	}
	for _, comment := range []string{"# agents", "# team block", "# after the team"} {
		if !strings.Contains(string(after), comment+"\n") {
			t.Fatalf("comment %q was lost: %q", comment, after)
		}
	}
	if got, wantVersion := updated.Version, shaHex(after); got != wantVersion {
		t.Fatalf("PUT version = %q, want %q", got, wantVersion)
	}

	reloaded := teamVersion(t, s, "research-squad", cookie)
	if reloaded.Version != updated.Version || !reloaded.RestartRequired {
		t.Fatalf("GET after PUT = %+v", reloaded)
	}
}

// TestTeamPutStaleVersionCoversACTEAM040 covers AC-TEAM-040: a stale
// expected_version is 409 stale_version and writes nothing.
func TestTeamPutStaleVersionCoversACTEAM040(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamTestConfig)
	original := readTeamFile(t, store.path)

	body := teamJSONBody(t, map[string]any{
		"lead":             "lead",
		"members":          []string{"researcher", "writer"},
		"description":      "New description",
		"expected_version": strings.Repeat("0", 64),
	})
	response := serveTeam(t, s, http.MethodPut, "/v1/teams/research-squad", body, cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("stale PUT status = %d, body = %q", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "stale_version" {
		t.Fatalf("stale PUT error = %+v", envelope.Error)
	}
	if after := readTeamFile(t, store.path); !bytes.Equal(after, original) {
		t.Fatalf("stale PUT changed the file:\n got %q\nwant %q", after, original)
	}
}

// TestTeamPutValidationFailsCoversACTEAM041 covers AC-TEAM-041: a member that
// is not an agent fails config.Check with 422 validation_failed and writes
// nothing.
func TestTeamPutValidationFailsCoversACTEAM041(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamTestConfig)
	original := readTeamFile(t, store.path)
	team := teamVersion(t, s, "research-squad", cookie)

	body := teamJSONBody(t, map[string]any{
		"lead":             "lead",
		"members":          []string{"ghost"},
		"expected_version": team.Version,
	})
	response := serveTeam(t, s, http.MethodPut, "/v1/teams/research-squad", body, cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PUT status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("invalid PUT error = %+v", envelope.Error)
	}
	found := false
	for _, detail := range envelope.Error.Details {
		key, _ := detail["key"].(string)
		message, _ := detail["message"].(string)
		if key == "teams.research-squad.members.0" && strings.Contains(message, "ghost") && strings.Contains(message, "not registered") {
			found = true
		}
	}
	if !found {
		t.Fatalf("details do not name the bad member: %+v", envelope.Error.Details)
	}
	if after := readTeamFile(t, store.path); !bytes.Equal(after, original) {
		t.Fatalf("invalid PUT changed the file:\n got %q\nwant %q", after, original)
	}
}

// TestTeamDeleteRouteCoversACTEAM042 covers AC-TEAM-042: deleting a team that
// a route names fails config.Check with 422 naming the route and writes
// nothing.
func TestTeamDeleteRouteCoversACTEAM042(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamRouteConfig)
	original := readTeamFile(t, store.path)
	team := teamVersion(t, s, "research-squad", cookie)

	target := fmt.Sprintf("/v1/teams/research-squad?expected_version=%s", team.Version)
	response := serveTeam(t, s, http.MethodDelete, target, "", cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("route DELETE status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("route DELETE error = %+v", envelope.Error)
	}
	found := false
	for _, detail := range envelope.Error.Details {
		key, _ := detail["key"].(string)
		message, _ := detail["message"].(string)
		if key == "routes.0.agent" && strings.Contains(message, "research-squad") {
			found = true
		}
	}
	if !found {
		t.Fatalf("details do not name the route: %+v", envelope.Error.Details)
	}
	if after := readTeamFile(t, store.path); !bytes.Equal(after, original) {
		t.Fatalf("route DELETE changed the file:\n got %q\nwant %q", after, original)
	}
}

// TestTeamCreateAndNameTaken covers the create endpoint: 201 with kind team,
// and 409 name_taken for a team name and an agent name.
func TestTeamCreateAndNameTaken(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamCreateConfig)

	body := teamJSONBody(t, map[string]any{
		"name":             "research-squad",
		"lead":             "lead",
		"members":          []string{"coder"},
		"description":      "A team",
		"expected_version": shaHex([]byte(teamCreateConfig)),
	})
	response := serveTeam(t, s, http.MethodPost, "/v1/teams", body, cookie, csrf)
	if response.code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", response.code, response.body)
	}
	var created teamCreatedResponse
	response.json(t, &created)
	if created.Name != "research-squad" || created.Kind != "team" || !created.RestartRequired {
		t.Fatalf("create response = %+v", created)
	}
	after := readTeamFile(t, store.path)
	if got := created.Version; got != shaHex(after) {
		t.Fatalf("create version = %q, want %q", got, shaHex(after))
	}
	want := teamCreateConfig + "\n[teams.research-squad]\nlead = 'lead'\nmembers = ['coder']\ndescription = 'A team'\n"
	if string(after) != want {
		t.Fatalf("config after create:\n got %q\nwant %q", after, want)
	}

	version := created.Version
	for _, name := range []string{"research-squad", "lead"} {
		body := teamJSONBody(t, map[string]any{
			"name":             name,
			"lead":             "lead",
			"members":          []string{"coder"},
			"expected_version": version,
		})
		response := serveTeam(t, s, http.MethodPost, "/v1/teams", body, cookie, csrf)
		if response.code != http.StatusConflict {
			t.Fatalf("create %q status = %d, body = %q", name, response.code, response.body)
		}
		if envelope := decodeError(t, response); envelope.Error.Code != "name_taken" {
			t.Fatalf("create %q error = %+v", name, envelope.Error)
		}
	}
	if now := readTeamFile(t, store.path); !bytes.Equal(now, after) {
		t.Fatalf("name_taken create changed the file:\n got %q\nwant %q", now, after)
	}

	body = teamJSONBody(t, map[string]any{
		"name":             "Bad Name",
		"lead":             "lead",
		"members":          []string{"coder"},
		"expected_version": version,
	})
	response = serveTeam(t, s, http.MethodPost, "/v1/teams", body, cookie, csrf)
	if response.code != http.StatusBadRequest {
		t.Fatalf("bad name status = %d, body = %q", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "invalid_request" {
		t.Fatalf("bad name error = %+v", envelope.Error)
	}
}

// TestTeamGetNotFound covers the read endpoint: a missing team and an agent
// name both return 404 not_found.
func TestTeamGetNotFound(t *testing.T) {
	s, _, cookie, _ := newTeamTestServer(t, teamCreateConfig)
	for _, name := range []string{"missing", "lead"} {
		response := serveTeam(t, s, http.MethodGet, "/v1/teams/"+name, "", cookie, "")
		if response.code != http.StatusNotFound {
			t.Fatalf("GET %q status = %d, body = %q", name, response.code, response.body)
		}
		if envelope := decodeError(t, response); envelope.Error.Code != "not_found" {
			t.Fatalf("GET %q error = %+v", name, envelope.Error)
		}
	}
}

// TestTeamDeleteRequiresExpectedVersion covers the query parameter: a DELETE
// without expected_version is 400 invalid_request and writes nothing.
func TestTeamDeleteRequiresExpectedVersion(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamTestConfig)
	original := readTeamFile(t, store.path)

	response := serveTeam(t, s, http.MethodDelete, "/v1/teams/research-squad", "", cookie, csrf)
	if response.code != http.StatusBadRequest {
		t.Fatalf("DELETE without version status = %d, body = %q", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "invalid_request" {
		t.Fatalf("DELETE without version error = %+v", envelope.Error)
	}
	if after := readTeamFile(t, store.path); !bytes.Equal(after, original) {
		t.Fatalf("DELETE without version changed the file:\n got %q\nwant %q", after, original)
	}
}

// TestTeamDeleteRemovesTableAndBlankLine covers a DELETE of an unused team:
// the table and the blank line before it are removed and every other byte is
// kept.
func TestTeamDeleteRemovesTableAndBlankLine(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamDeleteConfig)
	team := teamVersion(t, s, "research-squad", cookie)

	target := fmt.Sprintf("/v1/teams/research-squad?expected_version=%s", team.Version)
	response := serveTeam(t, s, http.MethodDelete, target, "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %q", response.code, response.body)
	}
	var deleted teamDeletedResponse
	response.json(t, &deleted)
	if deleted.Name != "research-squad" || !deleted.Deleted || !deleted.RestartRequired {
		t.Fatalf("DELETE response = %+v", deleted)
	}

	want := `version = 1

[agents.lead]
[agents.researcher]

[web]
enabled = true
`
	after := readTeamFile(t, store.path)
	if string(after) != want {
		t.Fatalf("config after delete:\n got %q\nwant %q", after, want)
	}
	if got := deleted.Version; got != shaHex(after) {
		t.Fatalf("DELETE version = %q, want %q", got, shaHex(after))
	}

	response = serveTeam(t, s, http.MethodGet, "/v1/teams/research-squad", "", cookie, "")
	if response.code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, body = %q", response.code, response.body)
	}
}

// TestTeamDiscussionSettings covers spec 47 API-RDS-010: GET fills the
// defaults, PUT writes only non-default keys and keeps omitted ones.
func TestTeamDiscussionSettings(t *testing.T) {
	s, store, cookie, csrf := newTeamTestServer(t, teamTestConfig)
	before := teamVersion(t, s, "research-squad", cookie)
	if before.Discuss != "auto" || before.Rounds != 3 || before.ExtraRounds != "once" || !before.RoomMemory || !before.ShareSummary || !before.Learn || !before.TelegramAll {
		t.Fatalf("defaults = %+v", before.teamDiscussionJSON)
	}
	body := teamJSONBody(t, map[string]any{
		"lead": "lead", "members": []string{"researcher"}, "description": "Old description",
		"expected_version": before.Version, "discuss": "ask", "rounds": 2, "telegram_all": false,
		"display_name": "Research squad",
	})
	response := serveTeam(t, s, http.MethodPut, "/v1/teams/research-squad", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", response.code, response.body)
	}
	file := string(readTeamFile(t, store.path))
	for _, want := range []string{"discuss = 'ask'", "rounds = 2", "telegram_all = false", "display_name = 'Research squad'"} {
		if !strings.Contains(file, want) {
			t.Fatalf("config lacks %q:\n%s", want, file)
		}
	}
	if strings.Contains(file, "extra_rounds") || strings.Contains(file, "room_memory") {
		t.Fatalf("default keys written:\n%s", file)
	}
	// A second PUT without the settings keeps them.
	mid := teamVersion(t, s, "research-squad", cookie)
	body = teamJSONBody(t, map[string]any{"lead": "lead", "members": []string{"researcher", "writer"}, "description": "x", "expected_version": mid.Version})
	response = serveTeam(t, s, http.MethodPut, "/v1/teams/research-squad", body, cookie, csrf)
	var after teamJSON
	response.json(t, &after)
	if response.code != http.StatusOK || after.Discuss != "ask" || after.Rounds != 2 || after.TelegramAll {
		t.Fatalf("second PUT = %d %+v", response.code, after)
	}
	// A bad value fails config.Check.
	last := teamVersion(t, s, "research-squad", cookie)
	body = teamJSONBody(t, map[string]any{"lead": "lead", "members": []string{"researcher"}, "description": "x", "expected_version": last.Version, "rounds": 9})
	if response = serveTeam(t, s, http.MethodPut, "/v1/teams/research-squad", body, cookie, csrf); response.code != http.StatusUnprocessableEntity {
		t.Fatalf("bad rounds status = %d, body = %q", response.code, response.body)
	}
}

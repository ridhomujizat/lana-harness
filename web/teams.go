package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// teamBodyLimit is the largest accepted POST/PUT team body.
const teamBodyLimit = 64 << 10

// teamNameRE is the name rule of API-TEAM-001. A team shares the
// [agents.<name>] namespace, so a create checks both maps.
var teamNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// teamCreateRequest is the POST /v1/teams body. Unknown keys and wrong field
// types are rejected by decodeJSONObject with 400 invalid_request.
type teamCreateRequest struct {
	Name            string   `json:"name"`
	Lead            string   `json:"lead"`
	Members         []string `json:"members"`
	Description     string   `json:"description"`
	ExpectedVersion string   `json:"expected_version"`
	teamDiscussionInput
}

// teamDiscussionInput is the discussion settings part of a team write
// (spec 47 API-RDS-010). A nil field keeps the current value on PUT and the
// default on POST.
type teamDiscussionInput struct {
	DisplayName  *string `json:"display_name,omitempty"`
	Discuss      *string `json:"discuss,omitempty"`
	Rounds       *int    `json:"rounds,omitempty"`
	ExtraRounds  *string `json:"extra_rounds,omitempty"`
	RoomMemory   *bool   `json:"room_memory,omitempty"`
	ShareSummary *bool   `json:"share_summary,omitempty"`
	Learn        *bool   `json:"learn,omitempty"`
	TelegramAll  *bool   `json:"telegram_all,omitempty"`
}

// apply copies the set fields onto record.
func (in teamDiscussionInput) apply(record *teamRecord) {
	if in.DisplayName != nil {
		record.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.Discuss != nil {
		record.Discuss = *in.Discuss
	}
	if in.Rounds != nil {
		record.Rounds = *in.Rounds
	}
	if in.ExtraRounds != nil {
		record.ExtraRounds = *in.ExtraRounds
	}
	if in.RoomMemory != nil {
		record.RoomMemory = in.RoomMemory
	}
	if in.ShareSummary != nil {
		record.ShareSummary = in.ShareSummary
	}
	if in.Learn != nil {
		record.Learn = in.Learn
	}
	if in.TelegramAll != nil {
		record.TelegramAll = in.TelegramAll
	}
}

// teamSaveRequest is the PUT /v1/teams/{name} body. The name is the path and
// cannot change; rename is a delete and a create.
type teamSaveRequest struct {
	Lead            string   `json:"lead"`
	Members         []string `json:"members"`
	Description     string   `json:"description"`
	ExpectedVersion string   `json:"expected_version"`
	teamDiscussionInput
}

// teamJSON is the GET and PUT response body of API-TEAM-008 and API-TEAM-009.
type teamJSON struct {
	Name            string   `json:"name"`
	Lead            string   `json:"lead"`
	Members         []string `json:"members"`
	Description     string   `json:"description"`
	Version         string   `json:"version"`
	RestartRequired bool     `json:"restart_required"`
	teamDiscussionJSON
}

// teamDiscussionJSON is every discussion setting with its default filled in
// (API-RDS-010).
type teamDiscussionJSON struct {
	DisplayName  string `json:"display_name"`
	Discuss      string `json:"discuss"`
	Rounds       int    `json:"rounds"`
	ExtraRounds  string `json:"extra_rounds"`
	RoomMemory   bool   `json:"room_memory"`
	ShareSummary bool   `json:"share_summary"`
	Learn        bool   `json:"learn"`
	TelegramAll  bool   `json:"telegram_all"`
}

// discussionJSON fills the defaults of record's discussion settings.
func (record teamRecord) discussionJSON() teamDiscussionJSON {
	t := core.TeamConfig{Discuss: record.Discuss, Rounds: record.Rounds, ExtraRounds: record.ExtraRounds,
		RoomMemory: record.RoomMemory, ShareSummary: record.ShareSummary, Learn: record.Learn, TelegramAll: record.TelegramAll}
	return teamDiscussionJSON{
		DisplayName: record.DisplayName,
		Discuss:     t.DiscussMode(), Rounds: t.RoundLimit(), ExtraRounds: t.ExtraMode(),
		RoomMemory: t.KeepsRoomMemory(), ShareSummary: t.SharesSummary(), Learn: t.Learns(), TelegramAll: t.TelegramAllMessages(),
	}
}

// teamCreatedResponse is the POST /v1/teams response body of API-TEAM-007.
type teamCreatedResponse struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

// teamDeletedResponse is the DELETE /v1/teams/{name} response body of
// API-TEAM-010.
type teamDeletedResponse struct {
	Name            string `json:"name"`
	Deleted         bool   `json:"deleted"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

// teamRecord is the [teams.<name>] table as the endpoints read and write it.
// A nil Members means the key is absent; an empty, non-nil slice writes an
// empty array so config.Check can report the length.
type teamRecord struct {
	Lead        string
	Members     []string
	Description string
	// Spec 47: the room name people see, and the discussion settings; zero
	// or nil is the default and is not written.
	DisplayName  string
	Discuss      string
	Rounds       int
	ExtraRounds  string
	RoomMemory   *bool
	ShareSummary *bool
	Learn        *bool
	TelegramAll  *bool
}

// teamDocument is the [teams.*] and [agents.*] view used for existence and
// name-taken checks. config.Check stays the authority for the table contents.
type teamDocument struct {
	Teams  map[string]map[string]any
	Agents map[string]map[string]any
}

// registerTeamRoutes installs the team config endpoints. Handle applies the
// owner cookie and CSRF checks.
func (s *Server) registerTeamRoutes() {
	s.Handle("POST /v1/teams", http.HandlerFunc(s.handleCreateTeam))
	s.Handle("GET /v1/teams/{name}", http.HandlerFunc(s.handleGetTeam))
	s.Handle("PUT /v1/teams/{name}", http.HandlerFunc(s.handlePutTeam))
	s.Handle("DELETE /v1/teams/{name}", http.HandlerFunc(s.handleDeleteTeam))
}

// handleCreateTeam appends a [teams.<name>] table (API-TEAM-007).
func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body teamCreateRequest
	if !decodeJSONObject(w, r, teamBodyLimit, &body) {
		return
	}
	if !teamNameRE.MatchString(body.Name) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "name must match [a-z][a-z0-9_-]{0,31}")
		return
	}
	if body.ExpectedVersion == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before team create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseTeamDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before team create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := document.Teams[body.Name]; exists {
		writeTeamNameTaken(w)
		return
	}
	if _, exists := document.Agents[body.Name]; exists {
		writeTeamNameTaken(w)
		return
	}

	record := teamRecord{
		Lead:        body.Lead,
		Members:     body.Members,
		Description: body.Description,
	}
	body.apply(&record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), file.Raw, teamHeader(body.Name), renderTeamTable(body.Name, record))
	if err != nil {
		s.writeTeamSaveError(w, "create team", err)
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for team create", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version := webfiles.ConfigVersion(candidate)
	applied := s.applyLiveConfig(r.Context(), store, candidate)
	s.auditSave("team.create", agentConfigPath(file, s.agentHome()), body.Name, changed)
	writeJSON(w, http.StatusCreated, teamCreatedResponse{
		Name:            body.Name,
		Kind:            "team",
		Version:         version,
		RestartRequired: !applied,
	})
}

// handleGetTeam reads one [teams.<name>] table (API-TEAM-008). Agents are not
// teams, so an agent name is 404.
func (s *Server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !teamNameRE.MatchString(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for team", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}
	document, err := parseTeamDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for team", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	table, exists := document.Teams[name]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}
	record := teamRecordFromMap(table)
	writeJSON(w, http.StatusOK, teamJSON{
		Name:        name,
		Lead:        record.Lead,
		Members:     nonNilStrings(record.Members),
		Description: record.Description,
		Version:     file.Version,
		// Compared at the whole-file level, the way GET /v1/config and the
		// provider list report restart_required. The loaded config is only
		// exposed as its startup version, so a team-level comparison would
		// need a second effective-config port.
		RestartRequired:    file.Version != store.ConfigLoadedVersion(),
		teamDiscussionJSON: record.discussionJSON(),
	})
}

// handlePutTeam replaces the lines of an existing team table (API-TEAM-009).
func (s *Server) handlePutTeam(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !teamNameRE.MatchString(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}
	var body teamSaveRequest
	if !decodeJSONObject(w, r, teamBodyLimit, &body) {
		return
	}
	if body.ExpectedVersion == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before team save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseTeamDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before team save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := document.Teams[name]; !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}

	record := teamRecordFromMap(document.Teams[name])
	record.Lead, record.Members, record.Description = body.Lead, body.Members, body.Description
	body.apply(&record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), file.Raw, teamHeader(name), renderTeamTable(name, record))
	if err != nil {
		s.writeTeamSaveError(w, "save team", err)
		return
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for team save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("team.save", agentConfigPath(file, s.agentHome()), name, changed)
	}
	applied := s.applyLiveConfig(r.Context(), store, candidate)
	writeJSON(w, http.StatusOK, teamJSON{
		Name:               name,
		Lead:               record.Lead,
		Members:            nonNilStrings(record.Members),
		Description:        record.Description,
		Version:            webfiles.ConfigVersion(candidate),
		RestartRequired:    !applied,
		teamDiscussionJSON: record.discussionJSON(),
	})
}

// handleDeleteTeam removes a team table (API-TEAM-010). expected_version is a
// query parameter because this API gives DELETE no body.
func (s *Server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !teamNameRE.MatchString(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}
	expected := r.URL.Query().Get("expected_version")
	if expected == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before team delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseTeamDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before team delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := document.Teams[name]; !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "team is not configured")
		return
	}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), file.Raw, teamHeader(name), "")
	if err != nil {
		s.writeTeamSaveError(w, "delete team", err)
		return
	}
	if len(candidate) < len(file.Raw) {
		candidate = dropBlankLineBeforeRemovedTable(file.Raw, candidate)
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for team delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	version := webfiles.ConfigVersion(candidate)
	applied := s.applyLiveConfig(r.Context(), store, candidate)
	s.auditSave("team.delete", agentConfigPath(file, s.agentHome()), name, changed)
	writeJSON(w, http.StatusOK, teamDeletedResponse{
		Name:            name,
		Deleted:         true,
		Version:         version,
		RestartRequired: !applied,
	})
}

// parseTeamDocument reads the team and agent maps used for existence and
// name-taken checks. Malformed tables are left to config.Check; this parse
// only needs the names that exist.
func parseTeamDocument(raw []byte) (teamDocument, error) {
	document := teamDocument{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		return teamDocument{}, err
	}
	document.Teams = tableMap(root["teams"])
	document.Agents = tableMap(root["agents"])
	return document, nil
}

// tableMap keeps only the values that are tables, so one malformed entry does
// not hide the other names.
func tableMap(value any) map[string]map[string]any {
	tables := map[string]map[string]any{}
	entries, ok := value.(map[string]any)
	if !ok {
		return tables
	}
	for name, entry := range entries {
		if table, ok := entry.(map[string]any); ok {
			tables[name] = table
		}
	}
	return tables
}

func teamRecordFromMap(table map[string]any) teamRecord {
	return teamRecord{
		Lead:         mapAgentString(table, "lead"),
		Members:      mapAgentStrings(table, "members"),
		Description:  mapAgentString(table, "description"),
		DisplayName:  mapAgentString(table, "display_name"),
		Discuss:      mapAgentString(table, "discuss"),
		Rounds:       mapAgentInt(table, "rounds"),
		ExtraRounds:  mapAgentString(table, "extra_rounds"),
		RoomMemory:   mapAgentBoolPtr(table, "room_memory"),
		ShareSummary: mapAgentBoolPtr(table, "share_summary"),
		Learn:        mapAgentBoolPtr(table, "learn"),
		TelegramAll:  mapAgentBoolPtr(table, "telegram_all"),
	}
}

// renderTeamTable renders one [teams.<name>] table. An empty lead or
// description drops its key so config.Check reports "is required"; a nil
// members list drops the key too, while an empty list stays as [].
func renderTeamTable(name string, record teamRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[teams.%s]\n", name)
	if record.Lead != "" {
		fmt.Fprintf(&b, "lead = %s\n", agentTOMLString(record.Lead))
	}
	if record.Members != nil {
		fmt.Fprintf(&b, "members = %s\n", agentStringArray(record.Members))
	}
	if record.Description != "" {
		fmt.Fprintf(&b, "description = %s\n", agentTOMLString(record.Description))
	}
	if record.DisplayName != "" {
		fmt.Fprintf(&b, "display_name = %s\n", agentTOMLString(record.DisplayName))
	}
	// API-RDS-010: a setting equal to its default is not written.
	if record.Discuss != "" && record.Discuss != core.TeamDiscussAuto {
		fmt.Fprintf(&b, "discuss = %s\n", agentTOMLString(record.Discuss))
	}
	if record.Rounds != 0 && record.Rounds != core.TeamRoundsDefault {
		fmt.Fprintf(&b, "rounds = %d\n", record.Rounds)
	}
	if record.ExtraRounds != "" && record.ExtraRounds != core.TeamExtraOnce {
		fmt.Fprintf(&b, "extra_rounds = %s\n", agentTOMLString(record.ExtraRounds))
	}
	for _, kv := range []struct {
		key string
		val *bool
	}{{"room_memory", record.RoomMemory}, {"share_summary", record.ShareSummary}, {"learn", record.Learn}, {"telegram_all", record.TelegramAll}} {
		if kv.val != nil && !*kv.val {
			fmt.Fprintf(&b, "%s = false\n", kv.key)
		}
	}
	return b.String()
}

// teamHeader is the section header the config writer rewrites.
func teamHeader(name string) string {
	return "[teams." + name + "]"
}

// dropBlankLineBeforeRemovedTable removes the blank separator line before a
// deleted table. RewriteTable starts its span at the header, so the separator
// survives the delete; API-TEAM-010 removes it with the table. Removing one
// blank line cannot change the parsed TOML, so the checked candidate stays
// valid.
func dropBlankLineBeforeRemovedTable(raw, candidate []byte) []byte {
	prefix := 0
	for prefix < len(raw) && prefix < len(candidate) && raw[prefix] == candidate[prefix] {
		prefix++
	}
	// The line before the table must be blank: its newline is raw[prefix-1]
	// and the previous line must have ended with a newline as well.
	if prefix == 0 || raw[prefix-1] != '\n' {
		return candidate
	}
	if prefix > 1 && raw[prefix-2] != '\n' {
		return candidate
	}
	out := make([]byte, 0, len(candidate)-1)
	out = append(out, candidate[:prefix-1]...)
	out = append(out, candidate[prefix:]...)
	return out
}

// writeTeamSaveError maps the section writer's validation error to 422 and
// logs any other failure without exposing it. The checker is agentChecker, so
// the issue list arrives as agentValidationError.
func (s *Server) writeTeamSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *agentValidationError
	if errors.As(err, &validation) {
		writeValidationFailed(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// writeTeamNameTaken reports a name used by an existing team or agent.
func writeTeamNameTaken(w http.ResponseWriter) {
	writeAPIError(w, http.StatusConflict, "name_taken", "name is already used by a team or an agent")
}

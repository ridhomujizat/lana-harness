package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// channelBodyLimit is the largest accepted Telegram form or route body.
const channelBodyLimit = 64 << 10

const (
	defaultTelegramTokenEnv = "TELEGRAM_BOT_TOKEN"
	defaultTelegramAPIBase  = "https://api.telegram.org"
)

// channelEnvRE is the token_env rule of API-WEB-028.
var channelEnvRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// TelegramCheckResult is the outcome of POST /v1/channels/telegram/check. When
// OK is false, Message names the problem and never contains the token value.
type TelegramCheckResult struct {
	OK          bool
	BotUsername string
	Message     string
}

// TelegramChecker is the daemon port for API-WEB-028. The adapter in cmd/lana
// implements it with channels/telegram Client.GetMe. It stays separate from
// Backend so the existing session fakes do not grow Telegram methods.
type TelegramChecker interface {
	TelegramCheck(ctx context.Context) (TelegramCheckResult, error)
}

// registerChannelRoutes installs the channels routes. The backend and config
// store are read at call time, so SetBackend installs them together with the
// other ports.
func (s *Server) registerChannelRoutes() {
	s.Handle("GET /v1/channels", http.HandlerFunc(s.handleListChannels))
	s.Handle("PUT /v1/channels/telegram", http.HandlerFunc(s.handlePutTelegram))
	s.Handle("POST /v1/channels/telegram/check", http.HandlerFunc(s.handleCheckTelegram))
	s.Handle("GET /v1/channels/telegram/waiting", http.HandlerFunc(s.handleTelegramWaiting))
	s.Handle("PUT /v1/channels/telegram/routes/{chat_id}", http.HandlerFunc(s.handlePutTelegramRoute))
	s.Handle("DELETE /v1/channels/telegram/routes/{chat_id}", http.HandlerFunc(s.handleDeleteTelegramRoute))
}

type channelsResponse struct {
	Channels []any `json:"channels"`
}

type telegramCardJSON struct {
	Name          string             `json:"name"`
	Status        string             `json:"status"`
	State         string             `json:"state"`
	Enabled       bool               `json:"enabled"`
	TokenEnv      string             `json:"token_env"`
	TokenPresent  bool               `json:"token_present"`
	BotUsername   string             `json:"bot_username"`
	Chats         []channelChatJSON  `json:"chats"`
	Owners        []channelOwnerJSON `json:"owners"`
	ChatCount     int                `json:"chat_count"`
	OwnerCount    int                `json:"owner_count"`
	PollingSince  *int64             `json:"polling_since"`
	LastMessageAt *int64             `json:"last_message_at"`
}

type channelCardJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Addr   string `json:"addr,omitempty"`
}

type channelChatJSON struct {
	ChatID int64  `json:"chat_id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Agent  string `json:"agent"`
}

type channelOwnerJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type telegramSaveResponse struct {
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

type telegramCheckOKResponse struct {
	OK          bool   `json:"ok"`
	BotUsername string `json:"bot_username"`
}

type telegramCheckFailResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type waitingChatsResponse struct {
	Chats []waitingChatJSON `json:"chats"`
}

type waitingChatJSON struct {
	ChatID        int64  `json:"chat_id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	LastMessageAt int64  `json:"last_message_at"`
}

type telegramRouteResponse struct {
	ChatID          int64  `json:"chat_id"`
	Agent           string `json:"agent"`
	RestartRequired bool   `json:"restart_required"`
}

type telegramRouteDeleteResponse struct {
	ChatID          int64 `json:"chat_id"`
	RestartRequired bool  `json:"restart_required"`
}

// channelDocument is the part of config.toml the channel handlers read.
type channelDocument struct {
	Version  int                       `toml:"version"`
	Channels map[string]map[string]any `toml:"channels"`
	Routes   []core.RouteConfig        `toml:"routes"`
}

// telegramTable is [channels.telegram] as a form edits it. The token value is
// not part of the table; only the variable name is.
type telegramTable struct {
	Enabled  bool
	TokenEnv string
	OwnerIDs []int64
	APIBase  string
}

// telegramRoute is one [[routes]] entry with channel = "telegram".
type telegramRoute struct {
	ChatID int64
	Agent  string
	Name   string
}

// channelValidationError carries config.Check issues through the section
// writer, the same way agentValidationError does.
type channelValidationError struct {
	issues []ConfigIssue
}

func (e *channelValidationError) Error() string { return "config.toml is not valid" }

func defaultTelegramTable() telegramTable {
	return telegramTable{
		Enabled:  true,
		TokenEnv: defaultTelegramTokenEnv,
		OwnerIDs: []int64{},
	}
}

func parseChannelDocument(raw []byte) (channelDocument, error) {
	document := channelDocument{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	if err := toml.Unmarshal(raw, &document); err != nil {
		return channelDocument{}, err
	}
	return document, nil
}

// telegram returns the table with the config defaults applied. The second
// result reports whether [channels.telegram] is present.
func (d channelDocument) telegram() (telegramTable, bool) {
	raw, configured := d.Channels["telegram"]
	if !configured {
		return defaultTelegramTable(), false
	}
	table := defaultTelegramTable()
	if value, ok := raw["enabled"].(bool); ok {
		table.Enabled = value
	}
	if value, ok := raw["token_env"].(string); ok && value != "" {
		table.TokenEnv = value
	}
	if value, ok := raw["api_base"].(string); ok {
		table.APIBase = value
	}
	table.OwnerIDs = channelOwnerIDs(raw["owner_ids"])
	return table, true
}

// channelOwnerIDs converts the decoded TOML list of owner IDs.
func channelOwnerIDs(value any) []int64 {
	ids := []int64{}
	switch list := value.(type) {
	case []any:
		for _, item := range list {
			if id, ok := channelChatID(item); ok {
				ids = append(ids, id)
			}
		}
	case []int64:
		ids = append(ids, list...)
	}
	return ids
}

// telegramRoutes returns the [[routes]] entries for the Telegram channel.
func (d channelDocument) telegramRoutes() []telegramRoute {
	routes := []telegramRoute{}
	for _, route := range d.Routes {
		if route.Channel != "telegram" {
			continue
		}
		id, ok := channelChatID(route.Match["chat_id"])
		if !ok {
			continue
		}
		name, _ := route.Match["name"].(string)
		routes = append(routes, telegramRoute{ChatID: id, Agent: route.Agent, Name: name})
	}
	return routes
}

func channelChatID(value any) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case int:
		return int64(number), true
	case float64:
		if number == float64(int64(number)) {
			return int64(number), true
		}
	}
	return 0, false
}

// channelHome returns the LANA_HOME used for secrets.env.
func (s *Server) channelHome() string {
	if store, ok := s.configStore.(interface{ SettingsHome() string }); ok {
		if home := store.SettingsHome(); home != "" {
			return home
		}
	}
	return s.cfg.Home
}

// telegramTokenPresent reports whether the token variable has a value in the
// process environment or secrets.env. The value itself is never returned.
func (s *Server) telegramTokenPresent(tokenEnv string) bool {
	if tokenEnv == "" {
		return false
	}
	if value, ok := os.LookupEnv(tokenEnv); ok && strings.TrimSpace(value) != "" {
		return true
	}
	home := s.channelHome()
	if home == "" {
		return false
	}
	values, err := webfiles.ReadSecrets(secretsPath(home))
	if err != nil {
		return false
	}
	return strings.TrimSpace(values[tokenEnv]) != ""
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for channel list", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	document := channelDocument{}
	if file.Exists {
		document, err = parseChannelDocument(file.Raw)
		if err != nil {
			s.logger.Error("web: parse config for channel list", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
	}
	table, configured := document.telegram()
	status := s.TelegramStatus()
	present := s.telegramTokenPresent(table.TokenEnv)
	state := status.State
	switch {
	case !configured:
		state = "not_configured"
	case !table.Enabled:
		// Disconnect saves the flag immediately; the running daemon keeps
		// polling until the next restart, so the card follows the file.
		state = "off"
	case !present:
		state = "token_missing"
	case state == "":
		state = "connected"
	}

	routes := document.telegramRoutes()
	seen := pendingDisplayNames(status.PendingChats)
	chats := make([]channelChatJSON, 0, len(routes))
	for _, route := range routes {
		name := route.Name
		if name == "" {
			name = seen.users[route.ChatID]
		}
		chatType := seen.types[route.ChatID]
		if chatType == "" {
			// Routed chats the bot has not seen this run still have the
			// Telegram convention: groups have negative chat IDs.
			if route.ChatID < 0 {
				chatType = "group"
			} else {
				chatType = "private"
			}
		}
		chats = append(chats, channelChatJSON{
			ChatID: route.ChatID,
			Name:   name,
			Type:   chatType,
			Agent:  route.Agent,
		})
	}
	owners := make([]channelOwnerJSON, 0, len(table.OwnerIDs))
	for _, id := range table.OwnerIDs {
		owners = append(owners, channelOwnerJSON{ID: id, Name: seen.users[id]})
	}

	chatCount := status.ChatCount
	ownerCount := status.OwnerCount
	if !configured {
		chatCount = 0
		ownerCount = 0
	}
	// The snapshot is authoritative, but a fresh save before the daemon
	// restarts would otherwise show zero for the rows just written.
	if chatCount == 0 && len(routes) > 0 {
		chatCount = len(routes)
	}
	if ownerCount == 0 && len(table.OwnerIDs) > 0 {
		ownerCount = len(table.OwnerIDs)
	}

	channels := make([]any, 0, 8)
	channels = append(channels, telegramCardJSON{
		Name:          "telegram",
		Status:        state,
		State:         state,
		Enabled:       table.Enabled,
		TokenEnv:      table.TokenEnv,
		TokenPresent:  present,
		BotUsername:   status.BotUsername,
		Chats:         chats,
		Owners:        owners,
		ChatCount:     chatCount,
		OwnerCount:    ownerCount,
		PollingSince:  status.PollingSince,
		LastMessageAt: status.LastMessageAt,
	})
	channels = append(channels, channelCardJSON{Name: "web", Status: "on", Addr: s.cfg.Addr})
	channels = append(channels, channelCardJSON{Name: "cli", Status: "built_in"})
	for _, name := range []string{"discord", "slack", "whatsapp"} {
		channels = append(channels, channelCardJSON{Name: name, Status: "coming_soon"})
	}
	writeJSON(w, http.StatusOK, channelsResponse{Channels: channels})
}

// pendingNames indexes the waiting chats so the Telegram detail can show the
// chat type and the owner display name the bot has seen since start.
type pendingNames struct {
	users map[int64]string
	types map[int64]string
}

func pendingDisplayNames(pending []TelegramPendingChat) pendingNames {
	names := pendingNames{
		users: map[int64]string{},
		types: map[int64]string{},
	}
	for _, chat := range pending {
		display := chat.Name
		if display == "" {
			display = chat.Username
		}
		if display != "" {
			names.users[chat.ChatID] = display
			if chat.UserID != 0 {
				names.users[chat.UserID] = display
			}
		}
		if chat.Type != "" {
			names.types[chat.ChatID] = chat.Type
		}
	}
	return names
}

func (s *Server) handleCheckTelegram(w http.ResponseWriter, r *http.Request) {
	checker := s.telegramCheckerOrError(w)
	if checker == nil {
		return
	}
	result, err := checker.TelegramCheck(r.Context())
	if err != nil {
		// The adapter scrubs the token from Bot API errors. Keep the log line
		// free of it as well: the message is the only field that may carry it.
		s.logger.Error("web: telegram check", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if result.OK {
		writeJSON(w, http.StatusOK, telegramCheckOKResponse{OK: true, BotUsername: result.BotUsername})
		return
	}
	message := result.Message
	if message == "" {
		message = "Token missing"
	}
	writeJSON(w, http.StatusOK, telegramCheckFailResponse{OK: false, Message: message})
}

func (s *Server) telegramCheckerOrError(w http.ResponseWriter) TelegramChecker {
	if s.backend != nil {
		if checker, ok := s.backend.(TelegramChecker); ok {
			return checker
		}
	}
	s.logger.Error("web: telegram checker is not configured")
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	return nil
}

func (s *Server) handleTelegramWaiting(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for waiting chats", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	document := channelDocument{}
	if file.Exists {
		document, err = parseChannelDocument(file.Raw)
		if err != nil {
			s.logger.Error("web: parse config for waiting chats", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
	}
	table, configured := document.telegram()
	status := s.TelegramStatus()
	chats := []waitingChatJSON{}
	// The waiting list is memory only and is empty while the channel is off
	// (API-WEB-028, Q-027).
	if configured && table.Enabled && status.State != "off" {
		routed := map[int64]bool{}
		for _, route := range document.telegramRoutes() {
			routed[route.ChatID] = true
		}
		for _, pending := range status.PendingChats {
			if routed[pending.ChatID] {
				continue
			}
			chats = append(chats, waitingChatJSON{
				ChatID:        pending.ChatID,
				Name:          pending.Name,
				Type:          pending.Type,
				LastMessageAt: pending.LastSeen,
			})
		}
	}
	writeJSON(w, http.StatusOK, waitingChatsResponse{Chats: chats})
}

func (s *Server) handlePutTelegram(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, channelBodyLimit, &body) {
		return
	}
	allowed := map[string]bool{
		"enabled":          true,
		"token_env":        true,
		"owner_ids":        true,
		"api_base":         true,
		"expected_version": true,
	}
	if issues := unknownChannelKeys(body, allowed); len(issues) > 0 {
		writeChannelIssues(w, issues)
		return
	}
	expected, present, err := channelStringField(body, "expected_version")
	if err != nil || !present || expected == "" {
		writeChannelIssue(w, "expected_version", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before telegram save", "error", err)
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
	document, err := parseChannelDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before telegram save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	table, _ := document.telegram()
	if raw, ok := body["enabled"]; ok {
		if err := json.Unmarshal(raw, &table.Enabled); err != nil {
			writeChannelIssue(w, "channels.telegram.enabled", "enabled must be a boolean")
			return
		}
	}
	if raw, ok := body["token_env"]; ok {
		if err := json.Unmarshal(raw, &table.TokenEnv); err != nil {
			writeChannelIssue(w, "channels.telegram.token_env", "token_env must be a string")
			return
		}
	}
	if strings.TrimSpace(table.TokenEnv) == "" || !channelEnvRE.MatchString(table.TokenEnv) {
		writeChannelIssue(w, "channels.telegram.token_env", "token_env must match [A-Za-z_][A-Za-z0-9_]*")
		return
	}
	if raw, ok := body["owner_ids"]; ok {
		var ids []int64
		if err := json.Unmarshal(raw, &ids); err != nil {
			writeChannelIssue(w, "channels.telegram.owner_ids", "owner_ids must be an array of integers")
			return
		}
		table.OwnerIDs = uniqueChannelIDs(ids)
	}
	if raw, ok := body["api_base"]; ok {
		if err := json.Unmarshal(raw, &table.APIBase); err != nil {
			writeChannelIssue(w, "channels.telegram.api_base", "api_base must be a string")
			return
		}
	}
	if err := validateTelegramAPIBase(table.APIBase); err != nil {
		writeChannelIssue(w, "channels.telegram.api_base", err.Error())
		return
	}
	// G-21: an enabled Telegram channel needs at least one chat route. A
	// disabled channel may keep zero routes.
	if table.Enabled && len(document.telegramRoutes()) == 0 {
		writeChannelIssue(w, "channels.telegram.enabled", "telegram needs at least one chat route before it can be enabled; add a chat first")
		return
	}

	candidate, changed, err := webfiles.RewriteTable(r.Context(), channelChecker(store), file.Raw, "[channels.telegram]", renderTelegramTable(table))
	if err != nil {
		s.writeChannelSaveError(w, "save telegram channel", err)
		return
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for telegram save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("channel.telegram.save", providerConfigPath(file, s.channelHome()), "telegram", changed)
	}
	writeJSON(w, http.StatusOK, telegramSaveResponse{
		Version:         webfiles.ConfigVersion(candidate),
		RestartRequired: true,
	})
}

func (s *Server) handlePutTelegramRoute(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	chatID, ok := parseChatID(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSONObject(w, r, channelBodyLimit, &body) {
		return
	}
	allowed := map[string]bool{
		"agent":            true,
		"name":             true,
		"expected_version": true,
	}
	if issues := unknownChannelKeys(body, allowed); len(issues) > 0 {
		writeChannelIssues(w, issues)
		return
	}
	agent, present, err := channelStringField(body, "agent")
	if err != nil || !present || strings.TrimSpace(agent) == "" {
		writeChannelIssue(w, "channels.telegram.routes.agent", "agent is required")
		return
	}
	name, namePresent, err := channelStringField(body, "name")
	if err != nil {
		writeChannelIssue(w, "channels.telegram.routes.name", "name must be a string")
		return
	}
	expected, present, err := channelStringField(body, "expected_version")
	if err != nil || !present || expected == "" {
		writeChannelIssue(w, "expected_version", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before route save", "error", err)
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
	document, err := parseChannelDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before route save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !namePresent {
		for _, route := range document.telegramRoutes() {
			if route.ChatID == chatID {
				name = route.Name
				break
			}
		}
	}
	candidate, changed, err := webfiles.RewriteRoute(r.Context(), channelChecker(store), file.Raw, chatID, renderTelegramRoute(chatID, agent, name))
	if err != nil {
		s.writeChannelSaveError(w, "save telegram route", err)
		return
	}
	if !bytes.Equal(candidate, file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for route save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("route.save", providerConfigPath(file, s.channelHome()), strconv.FormatInt(chatID, 10), changed)
	}
	writeJSON(w, http.StatusOK, telegramRouteResponse{
		ChatID:          chatID,
		Agent:           agent,
		RestartRequired: true,
	})
}

func (s *Server) handleDeleteTelegramRoute(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	chatID, ok := parseChatID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion string `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, channelBodyLimit, &body) {
		return
	}
	if body.ExpectedVersion == "" {
		writeChannelIssue(w, "expected_version", "expected_version is required")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before route delete", "error", err)
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
	document, err := parseChannelDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before route delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	routes := document.telegramRoutes()
	found := false
	for _, route := range routes {
		if route.ChatID == chatID {
			found = true
			break
		}
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "not_found", "telegram route is not configured")
		return
	}
	table, _ := document.telegram()
	if table.Enabled && len(routes) == 1 {
		writeChannelIssue(w, "channels.telegram.enabled", "telegram is enabled and needs at least one chat route; disconnect before removing the last chat")
		return
	}
	candidate, changed, err := webfiles.RewriteRoute(r.Context(), channelChecker(store), file.Raw, chatID, "")
	if err != nil {
		s.writeChannelSaveError(w, "delete telegram route", err)
		return
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for route delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSave("route.delete", providerConfigPath(file, s.channelHome()), strconv.FormatInt(chatID, 10), changed)
	writeJSON(w, http.StatusOK, telegramRouteDeleteResponse{
		ChatID:          chatID,
		RestartRequired: true,
	})
}

func parseChatID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	chatID, err := strconv.ParseInt(r.PathValue("chat_id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "chat_id must be an integer")
		return 0, false
	}
	return chatID, true
}

func renderTelegramTable(table telegramTable) string {
	var b strings.Builder
	b.WriteString("[channels.telegram]\n")
	fmt.Fprintf(&b, "enabled = %t\n", table.Enabled)
	fmt.Fprintf(&b, "token_env = %s\n", channelTOMLString(table.TokenEnv))
	ids := make([]string, 0, len(table.OwnerIDs))
	for _, id := range table.OwnerIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	fmt.Fprintf(&b, "owner_ids = [%s]\n", strings.Join(ids, ", "))
	if table.APIBase != "" {
		fmt.Fprintf(&b, "api_base = %s\n", channelTOMLString(table.APIBase))
	}
	return b.String()
}

func renderTelegramRoute(chatID int64, agent, name string) string {
	var b strings.Builder
	b.WriteString("[[routes]]\n")
	b.WriteString("channel = \"telegram\"\n")
	fmt.Fprintf(&b, "agent = %s\n", channelTOMLString(agent))
	match := fmt.Sprintf("chat_id = %d", chatID)
	if name != "" {
		match += fmt.Sprintf(", name = %s", channelTOMLString(name))
	}
	fmt.Fprintf(&b, "match = { %s }\n", match)
	return b.String()
}

func channelTOMLString(value string) string {
	data, err := toml.Marshal(value)
	if err != nil {
		return `""`
	}
	return strings.TrimSuffix(string(data), "\n")
}

func validateTelegramAPIBase(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.HasSuffix(value, "/") {
		return errors.New("api_base must be an http or https URL without trailing /")
	}
	return nil
}

func uniqueChannelIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func channelStringField(body map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := body[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, err
	}
	return value, true, nil
}

func unknownChannelKeys(body map[string]json.RawMessage, allowed map[string]bool) []ConfigIssue {
	keys := make([]string, 0, len(body))
	for key := range body {
		if !allowed[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	issues := make([]ConfigIssue, 0, len(keys))
	for _, key := range keys {
		issues = append(issues, ConfigIssue{Key: key, Message: "unknown key"})
	}
	return issues
}

func writeChannelIssue(w http.ResponseWriter, key, message string) {
	writeChannelIssues(w, []ConfigIssue{{Key: key, Message: message}})
}

func writeChannelIssues(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		detail := map[string]any{
			"key":     issue.Key,
			"field":   issue.Key,
			"message": issue.Message,
		}
		if issue.File != "" {
			detail["file"] = issue.File
		}
		details = append(details, detail)
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "channel is not valid", details)
}

func channelChecker(store ConfigStore) func(context.Context, []byte) error {
	return func(ctx context.Context, candidate []byte) error {
		issues, err := store.ConfigCheck(ctx, candidate)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return &channelValidationError{issues: issues}
		}
		return nil
	}
}

func (s *Server) writeChannelSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *channelValidationError
	if errors.As(err, &validation) {
		writeChannelIssues(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

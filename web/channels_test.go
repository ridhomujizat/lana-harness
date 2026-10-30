package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const channelsBaseConfig = `version = 1

[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
`

// fakeBotAPIBackend implements TelegramChecker the way the cmd/lana adapter
// does: it reads no value from the request and calls Bot API getMe with the
// configured token.
type fakeBotAPIBackend struct {
	*fakeWebBackend
	apiBase string
	token   string
	calls   int
}

func (b *fakeBotAPIBackend) TelegramCheck(ctx context.Context) (TelegramCheckResult, error) {
	if b.token == "" {
		return TelegramCheckResult{OK: false, Message: "Token missing"}, nil
	}
	b.calls++
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+"/bot"+b.token+"/getMe", strings.NewReader("{}"))
	if err != nil {
		return TelegramCheckResult{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return TelegramCheckResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return TelegramCheckResult{OK: false, Message: "invalid bot token"}, nil
	}
	var decoded struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return TelegramCheckResult{}, err
	}
	if !decoded.OK {
		return TelegramCheckResult{OK: false, Message: decoded.Description}, nil
	}
	return TelegramCheckResult{OK: true, BotUsername: decoded.Result.Username}, nil
}

// newFakeBotAPI is the fake Bot API getMe endpoint of AC-WEB-055.
func newFakeBotAPI(t *testing.T, token, username string, calls *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/bot"+token+"/getMe" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		*calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"id":         42,
				"is_bot":     true,
				"first_name": "Lana",
				"username":   username,
			},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func newChannelsServer(t *testing.T, raw string) (*Server, *testConfigStore, *http.Cookie, string) {
	t.Helper()
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)
	return s, store, cookie, csrf
}

func serveChannels(t *testing.T, s *Server, method, target, body string, cookie *http.Cookie, csrf string) *httptestResponse {
	t.Helper()
	req := authRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	rr := serveAuth(s, req)
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

func channelConfigVersion(t *testing.T, store *testConfigStore) string {
	t.Helper()
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	return shaHex(data)
}

func telegramTestConfig(extra string) string {
	return channelsBaseConfig + "\n[channels.telegram]\nenabled = true\ntoken_env = \"TELEGRAM_BOT_TOKEN\"\nowner_ids = [111]\n" + extra +
		"\n[[routes]]\nchannel = \"telegram\"\nagent = \"coder\"\nmatch = { chat_id = 333 }\n"
}

// TestChannelsTelegramCheckFakeBotAPI covers AC-WEB-055: the check calls the
// fake Bot API getMe and reports the identity, and a missing token returns
// ok:false without leaking a value.
func TestChannelsTelegramCheckFakeBotAPI(t *testing.T) {
	calls := 0
	api := newFakeBotAPI(t, "tok-good", "lana_bot", &calls)
	raw := telegramTestConfig("api_base = \"" + api.URL + "\"\n")
	s, _, cookie, csrf := newChannelsServer(t, raw)
	backend := &fakeBotAPIBackend{fakeWebBackend: newFakeWebBackend(), apiBase: api.URL, token: "tok-good"}
	s.SetBackend(backend)

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/check", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("check status = %d, body = %q", response.code, response.body)
	}
	var result map[string]any
	response.json(t, &result)
	if result["ok"] != true || result["bot_username"] != "lana_bot" {
		t.Fatalf("check result = %v, want ok true and bot_username lana_bot", result)
	}
	if calls != 1 {
		t.Fatalf("fake Bot API getMe calls = %d, want 1", calls)
	}
	if strings.Contains(string(response.body), "tok-good") {
		t.Fatalf("check response leaked the token: %q", response.body)
	}

	backend.token = ""
	missing := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/check", "", cookie, csrf)
	if missing.code != http.StatusOK {
		t.Fatalf("missing token status = %d, body = %q", missing.code, missing.body)
	}
	var missingResult map[string]any
	missing.json(t, &missingResult)
	if missingResult["ok"] != false || missingResult["message"] != "Token missing" {
		t.Fatalf("missing token result = %v, want ok false and Token missing", missingResult)
	}
	if calls != 1 {
		t.Fatalf("missing token still called getMe: calls = %d", calls)
	}
}

// TestChannelsTelegramInvalidToken covers the disconnect case of AC-WEB-055:
// a token the Bot API rejects returns ok:false, and the token never appears.
func TestChannelsTelegramInvalidToken(t *testing.T) {
	calls := 0
	api := newFakeBotAPI(t, "tok-good", "lana_bot", &calls)
	s, _, cookie, csrf := newChannelsServer(t, telegramTestConfig("api_base = \""+api.URL+"\"\n"))
	s.SetBackend(&fakeBotAPIBackend{fakeWebBackend: newFakeWebBackend(), apiBase: api.URL, token: "tok-bad"})

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/check", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var result map[string]any
	response.json(t, &result)
	if result["ok"] != false || !strings.Contains(result["message"].(string), "invalid bot token") {
		t.Fatalf("result = %v, want ok false with the Bot API error", result)
	}
	if strings.Contains(string(response.body), "tok-bad") {
		t.Fatalf("response leaked the token: %q", response.body)
	}
}

// TestChannelsTelegramDisconnect covers AC-WEB-055: enabled=false is saved,
// the card shows Off even before the daemon restarts, restart_required is true,
// and the routes stay in config.toml.
func TestChannelsTelegramDisconnect(t *testing.T) {
	raw := telegramTestConfig("")
	s, store, cookie, csrf := newChannelsServer(t, raw)
	s.SetTelegramStatus(func() TelegramStatus {
		return TelegramStatus{State: "connected", BotUsername: "lana_bot", ChatCount: 1, OwnerCount: 1}
	})

	body := fmt.Sprintf(`{"enabled":false,"expected_version":%q}`, channelConfigVersion(t, store))
	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("disconnect status = %d, body = %q", response.code, response.body)
	}
	var saved map[string]any
	response.json(t, &saved)
	if saved["restart_required"] != true {
		t.Fatalf("restart_required = %v, want true", saved["restart_required"])
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "enabled = false") {
		t.Fatalf("config does not save enabled = false:\n%s", data)
	}
	if !strings.Contains(string(data), "chat_id = 333") || !strings.Contains(string(data), "channel = \"telegram\"") {
		t.Fatalf("disconnect dropped the routes:\n%s", data)
	}

	card := serveChannels(t, s, http.MethodGet, "/v1/channels", "", cookie, csrf)
	if card.code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", card.code, card.body)
	}
	var list channelsResponse
	card.json(t, &list)
	telegram := telegramCardFrom(t, list)
	if telegram["status"] != "off" || telegram["enabled"] != false {
		t.Fatalf("telegram card = %v, want off and enabled false", telegram)
	}
	chats, _ := telegram["chats"].([]any)
	if len(chats) != 1 {
		t.Fatalf("telegram chats = %v, want the existing route", telegram["chats"])
	}

	waiting := serveChannels(t, s, http.MethodGet, "/v1/channels/telegram/waiting", "", cookie, csrf)
	if waiting.code != http.StatusOK {
		t.Fatalf("waiting status = %d, body = %q", waiting.code, waiting.body)
	}
	var waitingBody waitingChatsResponse
	waiting.json(t, &waitingBody)
	if len(waitingBody.Chats) != 0 {
		t.Fatalf("waiting chats while off = %+v, want empty", waitingBody.Chats)
	}
}

// TestChannelsListTokenMissing covers REQ-DMN-006 from the web side: the web
// view works and reports token_present=false, and the value is never returned.
func TestChannelsListTokenMissing(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	s, store, cookie, csrf := newChannelsServer(t, telegramTestConfig(""))

	response := serveChannels(t, s, http.MethodGet, "/v1/channels", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var list channelsResponse
	response.json(t, &list)
	telegram := telegramCardFrom(t, list)
	if telegram["status"] != "token_missing" || telegram["token_present"] != false {
		t.Fatalf("telegram card = %v, want token_missing and token_present false", telegram)
	}
	if strings.Contains(string(response.body), "TELEGRAM_BOT_TOKEN=") {
		t.Fatalf("response contains a token assignment: %q", response.body)
	}

	// A value in secrets.env makes the variable present; it is still not
	// returned.
	if err := os.WriteFile(secretsPath(store.home), []byte("TELEGRAM_BOT_TOKEN=secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	response = serveChannels(t, s, http.MethodGet, "/v1/channels", "", cookie, csrf)
	var withSecret channelsResponse
	response.json(t, &withSecret)
	telegram = telegramCardFrom(t, withSecret)
	if telegram["token_present"] != true {
		t.Fatalf("token_present = %v, want true after secrets.env", telegram["token_present"])
	}
	if strings.Contains(string(response.body), "secret-value") {
		t.Fatalf("response leaked the token value: %q", response.body)
	}
}

// TestChannelsListNotConfigured covers the empty state when config.toml has no
// [channels.telegram] table.
func TestChannelsListNotConfigured(t *testing.T) {
	s, _, cookie, csrf := newChannelsServer(t, "version = 1\n")
	response := serveChannels(t, s, http.MethodGet, "/v1/channels", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var list channelsResponse
	response.json(t, &list)
	telegram := telegramCardFrom(t, list)
	if telegram["status"] != "not_configured" {
		t.Fatalf("telegram status = %v, want not_configured", telegram["status"])
	}
}

// TestChannelsTelegramSave covers PUT /v1/channels/telegram: the form fields
// replace the table under the write lock and the route stays.
func TestChannelsTelegramSave(t *testing.T) {
	raw := telegramTestConfig("api_base = \"https://old.example.com\"\n")
	s, store, cookie, csrf := newChannelsServer(t, raw)

	body := fmt.Sprintf(`{"enabled":true,"token_env":"MY_TELEGRAM_TOKEN","owner_ids":[7,8,7],"api_base":"https://proxy.example.com/base","expected_version":%q}`, channelConfigVersion(t, store))
	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", response.code, response.body)
	}
	var saved map[string]any
	response.json(t, &saved)
	if saved["restart_required"] != true {
		t.Fatalf("restart_required = %v, want true", saved["restart_required"])
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"MY_TELEGRAM_TOKEN",
		"owner_ids = [7, 8]",
		"https://proxy.example.com/base",
		"chat_id = 333",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "https://old.example.com") {
		t.Fatalf("old api_base still present:\n%s", text)
	}

	bad := fmt.Sprintf(`{"api_base":"ftp://example.com","expected_version":%q}`, channelConfigVersion(t, store))
	if got := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram", bad, cookie, csrf); got.code != http.StatusUnprocessableEntity {
		t.Fatalf("bad api_base status = %d, body = %q", got.code, got.body)
	}
}

// TestChannelsTelegramRejectsTokenValue covers the write-only token rule: the
// form may name the variable but never carries the token value.
func TestChannelsTelegramRejectsTokenValue(t *testing.T) {
	s, store, cookie, csrf := newChannelsServer(t, telegramTestConfig(""))
	body := fmt.Sprintf(`{"token":"super-secret","expected_version":%q}`, channelConfigVersion(t, store))
	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram", body, cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("code = %q, want validation_failed", envelope.Error.Code)
	}
	if strings.Contains(string(response.body), "super-secret") {
		t.Fatalf("response echoed the token value: %q", response.body)
	}
}

// TestChannelsEnableNeedsRoute covers G-21: enabling the channel with zero
// routes is a validation error; disabling it with zero routes is allowed.
func TestChannelsEnableNeedsRoute(t *testing.T) {
	raw := "version = 1\n\n[channels.telegram]\nenabled = false\ntoken_env = \"TELEGRAM_BOT_TOKEN\"\n"
	s, store, cookie, csrf := newChannelsServer(t, raw)

	enable := fmt.Sprintf(`{"enabled":true,"expected_version":%q}`, channelConfigVersion(t, store))
	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram", enable, cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("enable with zero routes status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("code = %q, want validation_failed", envelope.Error.Code)
	}
	found := false
	for _, detail := range envelope.Error.Details {
		if detail["key"] == "channels.telegram.enabled" {
			found = true
		}
	}
	if !found {
		t.Fatalf("details do not name channels.telegram.enabled: %+v", envelope.Error.Details)
	}

	disable := fmt.Sprintf(`{"enabled":false,"expected_version":%q}`, channelConfigVersion(t, store))
	if got := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram", disable, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("disable with zero routes status = %d, body = %q", got.code, got.body)
	}
}

// TestChannelsTelegramRoutes covers the upsert and delete of one [[routes]]
// entry, the not-found case, and the G-21 last-route rule.
func TestChannelsTelegramRoutes(t *testing.T) {
	s, store, cookie, csrf := newChannelsServer(t, telegramTestConfig(""))

	body := fmt.Sprintf(`{"agent":"coder","name":"Budi","expected_version":%q}`, channelConfigVersion(t, store))
	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram/routes/555", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("route save status = %d, body = %q", response.code, response.body)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "chat_id = 555") != 1 {
		t.Fatalf("route 555 not written once:\n%s", text)
	}
	if !strings.Contains(text, "Budi") || !strings.Contains(text, "agent = ") {
		t.Fatalf("route entry missing fields:\n%s", text)
	}

	// The card lists the saved route immediately.
	card := serveChannels(t, s, http.MethodGet, "/v1/channels", "", cookie, csrf)
	var list channelsResponse
	card.json(t, &list)
	telegram := telegramCardFrom(t, list)
	chats, _ := telegram["chats"].([]any)
	found := false
	for _, item := range chats {
		chat, _ := item.(map[string]any)
		if chat["chat_id"] == float64(555) {
			found = true
			if chat["name"] != "Budi" || chat["agent"] != "coder" {
				t.Fatalf("saved chat = %v, want name Budi and agent coder", chat)
			}
		}
	}
	if !found {
		t.Fatalf("saved route not listed: %v", chats)
	}

	// A second save for the same chat replaces the entry.
	body = fmt.Sprintf(`{"agent":"coder","name":"Budi 2","expected_version":%q}`, channelConfigVersion(t, store))
	if got := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram/routes/555", body, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("route replace status = %d, body = %q", got.code, got.body)
	}
	data, _ = os.ReadFile(store.path)
	if strings.Count(string(data), "chat_id = 555") != 1 {
		t.Fatalf("route replace duplicated the entry:\n%s", data)
	}

	// Delete one of the two routes.
	deleteBody := fmt.Sprintf(`{"expected_version":%q}`, channelConfigVersion(t, store))
	if got := serveChannels(t, s, http.MethodDelete, "/v1/channels/telegram/routes/555", deleteBody, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("route delete status = %d, body = %q", got.code, got.body)
	}
	data, _ = os.ReadFile(store.path)
	if strings.Contains(string(data), "chat_id = 555") {
		t.Fatalf("route 555 still present:\n%s", data)
	}

	// A missing route is 404 and changes nothing.
	deleteBody = fmt.Sprintf(`{"expected_version":%q}`, channelConfigVersion(t, store))
	if got := serveChannels(t, s, http.MethodDelete, "/v1/channels/telegram/routes/999", deleteBody, cookie, csrf); got.code != http.StatusNotFound {
		t.Fatalf("missing route status = %d, body = %q", got.code, got.body)
	}

	// The last route of an enabled channel cannot be removed.
	deleteBody = fmt.Sprintf(`{"expected_version":%q}`, channelConfigVersion(t, store))
	if got := serveChannels(t, s, http.MethodDelete, "/v1/channels/telegram/routes/333", deleteBody, cookie, csrf); got.code != http.StatusUnprocessableEntity {
		t.Fatalf("last route delete status = %d, body = %q", got.code, got.body)
	}
	data, _ = os.ReadFile(store.path)
	if !strings.Contains(string(data), "chat_id = 333") {
		t.Fatalf("last route was removed:\n%s", data)
	}
}

// TestChannelsWaiting covers REQ-WEB-053: the waiting list is memory only,
// routed chats are hidden, and the list is empty while the channel is off.
func TestChannelsWaiting(t *testing.T) {
	s, _, cookie, csrf := newChannelsServer(t, telegramTestConfig(""))
	s.SetTelegramStatus(func() TelegramStatus {
		return TelegramStatus{
			State: "connected",
			PendingChats: []TelegramPendingChat{
				{ChatID: 444, Type: "group", Name: "Ops", LastSeen: 2000},
				{ChatID: 333, Type: "private", Name: "Routed", LastSeen: 1500},
				{ChatID: 555, Type: "private", Name: "Sari", LastSeen: 1000},
			},
		}
	})

	response := serveChannels(t, s, http.MethodGet, "/v1/channels/telegram/waiting", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body waitingChatsResponse
	response.json(t, &body)
	if len(body.Chats) != 2 {
		t.Fatalf("waiting chats = %+v, want the two unrouted chats", body.Chats)
	}
	if body.Chats[0].ChatID != 444 || body.Chats[0].Name != "Ops" || body.Chats[0].Type != "group" || body.Chats[0].LastMessageAt != 2000 {
		t.Fatalf("first waiting chat = %+v", body.Chats[0])
	}
	if body.Chats[1].ChatID != 555 || body.Chats[1].Name != "Sari" {
		t.Fatalf("second waiting chat = %+v", body.Chats[1])
	}

	s.SetTelegramStatus(func() TelegramStatus { return TelegramStatus{State: "off"} })
	response = serveChannels(t, s, http.MethodGet, "/v1/channels/telegram/waiting", "", cookie, csrf)
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Chats) != 0 {
		t.Fatalf("waiting chats while off = %+v, want empty", body.Chats)
	}
}

// telegramCardFrom returns the telegram entry of a GET /v1/channels response.
func telegramCardFrom(t *testing.T, response channelsResponse) map[string]any {
	t.Helper()
	for _, item := range response.Channels {
		card, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if card["name"] == "telegram" {
			return card
		}
	}
	t.Fatalf("no telegram card in %+v", response.Channels)
	return nil
}

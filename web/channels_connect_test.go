package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// validTelegramToken has the shape API-TGC-001 accepts.
const validTelegramToken = "1234567890:abcdefghijklmnopqrstuvwxyzABCDE"

// TestTelegramBotURL covers the Bot link rule: a known username becomes the
// t.me link, and an unknown username stays empty so the page renders no link.
func TestTelegramBotURL(t *testing.T) {
	if got := telegramBotURL("lana_bot"); got != "https://t.me/lana_bot" {
		t.Fatalf("telegramBotURL(lana_bot) = %q, want https://t.me/lana_bot", got)
	}
	if got := telegramBotURL(""); got != "" {
		t.Fatalf("telegramBotURL(\"\") = %q, want the empty string", got)
	}
}

// connectSecretStore gives the token handler a secrets home without touching
// the config store's behavior.
type connectSecretStore struct {
	home string
}

func (s connectSecretStore) SettingsHome() string { return s.home }
func (connectSecretStore) ConfigSecretNames(context.Context) ([]string, error) {
	return nil, nil
}

// fakeTelegramSetup is the TelegramSetup port used by the handler tests. The
// embedded backend keeps SetBackend's other type assertions harmless.
type fakeTelegramSetup struct {
	*fakeWebBackend
	checkResult   TelegramCheckResult
	checkErr      error
	checkCalls    int
	seenToken     string
	discoverChats []TelegramSeenChat
	discoverErr   error
	discoverCalls int
	botUsername   string
	botErr        error
	botCalls      int
}

var _ TelegramSetup = (*fakeTelegramSetup)(nil)
var _ TelegramBotNamer = (*fakeTelegramSetup)(nil)

func (f *fakeTelegramSetup) TelegramTokenCheck(_ context.Context, token string) (TelegramCheckResult, error) {
	f.checkCalls++
	f.seenToken = token
	return f.checkResult, f.checkErr
}

func (f *fakeTelegramSetup) TelegramDiscover(context.Context) ([]TelegramSeenChat, error) {
	f.discoverCalls++
	return f.discoverChats, f.discoverErr
}

func (f *fakeTelegramSetup) TelegramBotUsername(context.Context) (string, error) {
	f.botCalls++
	return f.botUsername, f.botErr
}

func newConnectServer(t *testing.T, raw string) (*Server, *testConfigStore, *http.Cookie, string) {
	t.Helper()
	s, store, cookie, csrf := newChannelsServer(t, raw)
	s.SetSecretStore(connectSecretStore{home: store.home})
	return s, store, cookie, csrf
}

func telegramTokenBody(token string) string {
	data, _ := json.Marshal(map[string]string{"token": token})
	return string(data)
}

func connectBody(t *testing.T, version string, chatID, senderID int64, agent string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"version": version, "chat_id": chatID, "sender_id": senderID, "agent": agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestTelegramTokenSaveAC001 covers AC-TGC-001: a token getMe accepts is
// written to secrets.env, the response has the bot identity, and neither the
// response nor the log buffer contains the token.
func TestTelegramTokenSaveAC001(t *testing.T) {
	s, store, cookie, csrf := newConnectServer(t, channelsBaseConfig)
	fake := &fakeTelegramSetup{
		fakeWebBackend: newFakeWebBackend(),
		checkResult:    TelegramCheckResult{OK: true, BotUsername: "lana_bot"},
	}
	s.SetBackend(fake)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram/token", telegramTokenBody(validTelegramToken), cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved telegramTokenResponse
	response.json(t, &saved)
	if saved.BotUsername != "lana_bot" {
		t.Fatalf("bot_username = %q, want lana_bot", saved.BotUsername)
	}
	if saved.BotURL != "https://t.me/lana_bot" {
		t.Fatalf("bot_url = %q, want https://t.me/lana_bot", saved.BotURL)
	}
	if saved.TokenEnv != defaultTelegramTokenEnv {
		t.Fatalf("token_env = %q, want %q", saved.TokenEnv, defaultTelegramTokenEnv)
	}
	if !saved.RestartRequired {
		t.Fatal("restart_required = false, want true")
	}
	if strings.Contains(string(response.body), validTelegramToken) {
		t.Fatalf("response leaked the token: %q", response.body)
	}
	if strings.Contains(logs.String(), validTelegramToken) {
		t.Fatalf("log buffer leaked the token: %q", logs.String())
	}
	if fake.checkCalls != 1 {
		t.Fatalf("TelegramTokenCheck calls = %d, want 1", fake.checkCalls)
	}
	if fake.seenToken != validTelegramToken {
		t.Fatalf("TelegramTokenCheck token = %q, want the request token", fake.seenToken)
	}

	values, err := webfiles.ReadSecrets(filepath.Join(store.home, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if values[defaultTelegramTokenEnv] != validTelegramToken {
		t.Fatalf("secrets.env value = %q, want the saved token", values[defaultTelegramTokenEnv])
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != shaHex(data) {
		t.Fatalf("version = %q, want the config sha256 after the write", saved.Version)
	}
	text := string(data)
	for _, want := range []string{"[channels.telegram]", "token_env = ", defaultTelegramTokenEnv, "enabled = false"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, validTelegramToken) {
		t.Fatalf("config leaked the token:\n%s", text)
	}
}

// TestTelegramTokenRejectedAC002 covers AC-TGC-002: a getMe rejection is a 422
// and nothing is written.
func TestTelegramTokenRejectedAC002(t *testing.T) {
	s, store, cookie, csrf := newConnectServer(t, channelsBaseConfig)
	s.SetBackend(&fakeTelegramSetup{fakeWebBackend: newFakeWebBackend(), checkResult: TelegramCheckResult{OK: false}})
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}

	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram/token", telegramTokenBody(validTelegramToken), cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "telegram_token_invalid" {
		t.Fatalf("code = %q, want telegram_token_invalid", envelope.Error.Code)
	}
	if envelope.Error.Message != "This token was not accepted by Telegram. Copy it again from @BotFather." {
		t.Fatalf("message = %q", envelope.Error.Message)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config changed after a rejected token:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(store.home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("secrets.env exists after a rejected token: %v", err)
	}
}

// TestTelegramTokenBadShapeAC003 covers AC-TGC-003: a malformed token is a 400
// and the setup port is never called.
func TestTelegramTokenBadShapeAC003(t *testing.T) {
	s, store, cookie, csrf := newConnectServer(t, channelsBaseConfig)
	fake := &fakeTelegramSetup{fakeWebBackend: newFakeWebBackend()}
	s.SetBackend(fake)
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}

	response := serveChannels(t, s, http.MethodPut, "/v1/channels/telegram/token", telegramTokenBody("not-a-bot-token"), cookie, csrf)
	if response.code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "invalid_request" {
		t.Fatalf("code = %q, want invalid_request", envelope.Error.Code)
	}
	if fake.checkCalls != 0 {
		t.Fatalf("TelegramTokenCheck calls = %d, want 0", fake.checkCalls)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config changed after a bad token shape:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(store.home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("secrets.env exists after a bad token shape: %v", err)
	}
}

// TestTelegramDiscoverAC004 covers AC-TGC-004: the list has the contract
// fields, excludes routed chats, sorts newest first, and caps at 20.
func TestTelegramDiscoverAC004(t *testing.T) {
	s, _, cookie, csrf := newConnectServer(t, telegramTestConfig(""))
	chats := make([]TelegramSeenChat, 0, 26)
	for i := 0; i < 25; i++ {
		id := int64(1000 + i)
		chats = append(chats, TelegramSeenChat{
			ChatID:        id,
			Type:          "private",
			Name:          fmt.Sprintf("User %d", i),
			SenderID:      id,
			SenderName:    fmt.Sprintf("User %d", i),
			LastMessageAt: int64(1000 + i),
		})
	}
	// Routed chat 333 is the newest and must still be filtered out.
	chats = append(chats, TelegramSeenChat{ChatID: 333, Type: "private", Name: "Routed", SenderID: 333, SenderName: "Routed", LastMessageAt: 99999})
	s.SetBackend(&fakeTelegramSetup{fakeWebBackend: newFakeWebBackend(), discoverChats: chats, botUsername: "lana_bot"})

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/discover", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body telegramDiscoverResponse
	response.json(t, &body)
	if body.BotUsername != "lana_bot" {
		t.Fatalf("bot_username = %q, want lana_bot", body.BotUsername)
	}
	if body.BotURL != "https://t.me/lana_bot" {
		t.Fatalf("bot_url = %q, want https://t.me/lana_bot", body.BotURL)
	}
	if len(body.Chats) != 20 {
		t.Fatalf("chats = %d, want the 20 newest", len(body.Chats))
	}
	if body.Chats[0].ChatID != 1024 {
		t.Fatalf("first chat = %d, want 1024 (newest)", body.Chats[0].ChatID)
	}
	for _, chat := range body.Chats {
		if chat.ChatID == 333 {
			t.Fatalf("routed chat 333 is still listed: %+v", chat)
		}
	}

	var raw map[string]any
	if err := json.Unmarshal(response.body, &raw); err != nil {
		t.Fatal(err)
	}
	list, ok := raw["chats"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("chats = %v, want a non-empty array", raw["chats"])
	}
	first, _ := list[0].(map[string]any)
	for _, key := range []string{"chat_id", "type", "name", "sender_id", "sender_name", "last_message_at"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("first chat %v has no %q", first, key)
		}
	}
}

// TestTelegramDiscoverPendingWhilePolling covers the polling branch: the
// pending list is used and getUpdates is not called.
func TestTelegramDiscoverPendingWhilePolling(t *testing.T) {
	s, _, cookie, csrf := newConnectServer(t, telegramTestConfig(""))
	fake := &fakeTelegramSetup{
		fakeWebBackend: newFakeWebBackend(),
		discoverChats:  []TelegramSeenChat{{ChatID: 999, Type: "private", Name: "Should not be used", LastMessageAt: 1}},
	}
	s.SetBackend(fake)
	pollingSince := int64(1234)
	s.SetTelegramStatus(func() TelegramStatus {
		return TelegramStatus{
			State:        "connected",
			BotUsername:  "lana_bot",
			PollingSince: &pollingSince,
			PendingChats: []TelegramPendingChat{
				{ChatID: 333, Type: "private", Name: "Routed", UserID: 333, LastSeen: 3000},
				{ChatID: 42, Type: "private", Name: "Ridho", UserID: 42, LastSeen: 2000},
			},
		}
	})

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/discover", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body telegramDiscoverResponse
	response.json(t, &body)
	if body.BotUsername != "lana_bot" {
		t.Fatalf("bot_username = %q, want lana_bot", body.BotUsername)
	}
	if body.BotURL != "https://t.me/lana_bot" {
		t.Fatalf("bot_url = %q, want https://t.me/lana_bot", body.BotURL)
	}
	if len(body.Chats) != 1 {
		t.Fatalf("chats = %+v, want only chat 42", body.Chats)
	}
	chat := body.Chats[0]
	if chat.ChatID != 42 || chat.Type != "private" || chat.Name != "Ridho" || chat.SenderID != 42 || chat.SenderName != "Ridho" || chat.LastMessageAt != 2000 {
		t.Fatalf("pending chat = %+v", chat)
	}
	if fake.discoverCalls != 0 {
		t.Fatalf("TelegramDiscover calls = %d, want 0 while polling", fake.discoverCalls)
	}
}

// TestTelegramDiscoverConflictAC006 covers AC-TGC-006: a 409 from getUpdates is
// a conflict with the contract message.
func TestTelegramDiscoverConflictAC006(t *testing.T) {
	s, _, cookie, csrf := newConnectServer(t, telegramTestConfig(""))
	s.SetBackend(&fakeTelegramSetup{
		fakeWebBackend: newFakeWebBackend(),
		discoverErr:    ErrTelegramConflict,
		botUsername:    "lana_bot",
	})

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/discover", "", cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "telegram_conflict" {
		t.Fatalf("code = %q, want telegram_conflict", envelope.Error.Code)
	}
	if envelope.Error.Message != "Telegram is already connected elsewhere. Stop the other bot program or remove its webhook, then try again." {
		t.Fatalf("message = %q", envelope.Error.Message)
	}
}

// TestTelegramDiscoverTokenMissing covers the no-token failure of API-TGC-002.
func TestTelegramDiscoverTokenMissing(t *testing.T) {
	s, _, cookie, csrf := newConnectServer(t, telegramTestConfig(""))
	s.SetBackend(&fakeTelegramSetup{fakeWebBackend: newFakeWebBackend(), botErr: ErrTelegramTokenMissing})

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/discover", "", cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "telegram_token_missing" {
		t.Fatalf("code = %q, want telegram_token_missing", envelope.Error.Code)
	}
}

// TestTelegramConnectAC007 covers AC-TGC-007: one save writes the route, the
// owner, enabled = true, and exactly one channel.telegram.save audit entry.
func TestTelegramConnectAC007(t *testing.T) {
	raw := channelsBaseConfig + "\n[channels.telegram]\nenabled = false\ntoken_env = \"TELEGRAM_BOT_TOKEN\"\n"
	s, store, cookie, csrf := newConnectServer(t, raw)
	body := connectBody(t, channelConfigVersion(t, store), 42, 42, "coder")

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/connect", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved telegramSaveResponse
	response.json(t, &saved)
	if !saved.RestartRequired {
		t.Fatal("restart_required = false, want true")
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != shaHex(data) {
		t.Fatalf("version = %q, want the new config sha256", saved.Version)
	}
	text := string(data)
	for _, want := range []string{
		"channel = ",
		"telegram",
		"chat_id = 42",
		"agent = ",
		"coder",
		"owner_ids = [42]",
		"enabled = true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	page, err := webfiles.ReadAudit(webfiles.AuditPath(store.home), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("audit entries = %d, want 1: %+v", len(page.Entries), page.Entries)
	}
	if page.Entries[0].Kind != "channel.telegram.save" {
		t.Fatalf("audit kind = %q, want channel.telegram.save", page.Entries[0].Kind)
	}
}

// TestTelegramConnectStaleAC009 covers AC-TGC-009: a stale version is a 409
// and nothing is written.
func TestTelegramConnectStaleAC009(t *testing.T) {
	raw := channelsBaseConfig + "\n[channels.telegram]\nenabled = false\ntoken_env = \"TELEGRAM_BOT_TOKEN\"\n"
	s, store, cookie, csrf := newConnectServer(t, raw)
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}

	response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/connect", connectBody(t, "stale-version", 42, 42, "coder"), cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "stale_version" {
		t.Fatalf("code = %q, want stale_version", envelope.Error.Code)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config changed on a stale version:\n%s", after)
	}
	page, err := webfiles.ReadAudit(webfiles.AuditPath(store.home), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("audit entries = %+v, want none", page.Entries)
	}
}

// TestTelegramConnectValidation covers the 422 failures: a bad sender ID and an
// unknown agent write nothing.
func TestTelegramConnectValidation(t *testing.T) {
	raw := channelsBaseConfig + "\n[channels.telegram]\nenabled = false\ntoken_env = \"TELEGRAM_BOT_TOKEN\"\n"
	for _, test := range []struct {
		name string
		body func(t *testing.T, version string) string
		key  string
	}{
		{
			name: "sender",
			body: func(t *testing.T, version string) string {
				return connectBody(t, version, 42, 0, "coder")
			},
			key: "sender_id",
		},
		{
			name: "agent",
			body: func(t *testing.T, version string) string {
				return connectBody(t, version, 42, 42, "ghost")
			},
			key: "agent",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, store, cookie, csrf := newConnectServer(t, raw)
			before, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			response := serveChannels(t, s, http.MethodPost, "/v1/channels/telegram/connect", test.body(t, channelConfigVersion(t, store)), cookie, csrf)
			if response.code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %q", response.code, response.body)
			}
			envelope := decodeError(t, response)
			if envelope.Error.Code != "validation_failed" {
				t.Fatalf("code = %q, want validation_failed", envelope.Error.Code)
			}
			found := false
			for _, detail := range envelope.Error.Details {
				key, _ := detail["key"].(string)
				if strings.Contains(key, test.key) {
					found = true
				}
			}
			if !found {
				t.Fatalf("details do not name %q: %+v", test.key, envelope.Error.Details)
			}
			after, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("config changed after a validation failure:\n%s", after)
			}
		})
	}
}

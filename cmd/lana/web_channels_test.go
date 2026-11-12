package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/web"
)

const (
	adapterTokenEnv = "TEST_TGC_BOT_TOKEN"
	adapterToken    = "1234567890:abcdefghijklmnopqrstuvwxyzABCDE"
)

// adapterHome writes a config.toml that points the adapter at apiBase.
func adapterHome(t *testing.T, apiBase, tokenEnv string) string {
	t.Helper()
	home := t.TempDir()
	raw := fmt.Sprintf("[channels.telegram]\ntoken_env = %q\napi_base = %q\n", tokenEnv, apiBase)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// botAPIPath splits /bot<token>/<method>.
func botAPIPath(path string) (token, method string, ok bool) {
	rest, found := strings.CutPrefix(path, "/bot")
	if !found {
		return "", "", false
	}
	token, method, found = strings.Cut(rest, "/")
	if !found || token == "" || method == "" {
		return "", "", false
	}
	return token, method, true
}

type adapterCall struct {
	token  string
	method string
	params map[string]any
}

// fakeBotAPI records calls and answers getMe and getUpdates.
type fakeBotAPI struct {
	mu            sync.Mutex
	server        *httptest.Server
	calls         []adapterCall
	username      string
	getMeStatus   int
	updatesStatus int
	updates       []map[string]any
}

func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	t.Helper()
	api := &fakeBotAPI{username: "lana_bot"}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipModelList(w, r) {
			return
		}
		token, method, ok := botAPIPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		api.mu.Lock()
		api.calls = append(api.calls, adapterCall{token: token, method: method, params: params})
		username, getMeStatus, updatesStatus, updates := api.username, api.getMeStatus, api.updatesStatus, api.updates
		api.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			if getMeStatus == http.StatusUnauthorized {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"id": 42, "is_bot": true, "first_name": "Lana", "username": username,
				},
			})
		case "getUpdates":
			if updatesStatus == http.StatusConflict {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`))
				return
			}
			if updates == nil {
				updates = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeBotAPI) snapshot() []adapterCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]adapterCall(nil), a.calls...)
}

// TestTelegramTokenCheckAdapter covers getMe ok and 401 with the token from the
// request, and keeps the token out of every returned value.
func TestTelegramTokenCheckAdapter(t *testing.T) {
	t.Setenv(adapterTokenEnv, "")
	api := newFakeBotAPI(t)
	backend := &webBackend{home: adapterHome(t, api.server.URL, adapterTokenEnv)}

	result, err := backend.TelegramTokenCheck(context.Background(), adapterToken)
	if err != nil {
		t.Fatalf("good token: %v", err)
	}
	if !result.OK || result.BotUsername != "lana_bot" {
		t.Fatalf("good token result = %+v, want ok and lana_bot", result)
	}

	api.mu.Lock()
	api.getMeStatus = http.StatusUnauthorized
	api.mu.Unlock()
	result, err = backend.TelegramTokenCheck(context.Background(), "1234567890:wrongwrongwrongwrongwrongwrongwrong")
	if err != nil {
		t.Fatalf("bad token: %v", err)
	}
	if result.OK {
		t.Fatalf("bad token result = %+v, want ok false", result)
	}
	for _, call := range api.snapshot() {
		if strings.Contains(call.token, "wrong") {
			continue
		}
		if call.token != adapterToken {
			t.Fatalf("getMe used token %q, want the request token", call.token)
		}
	}
}

// TestTelegramDiscoverAdapter covers the getUpdates shape and request params.
func TestTelegramDiscoverAdapter(t *testing.T) {
	t.Setenv(adapterTokenEnv, adapterToken)
	api := newFakeBotAPI(t)
	api.updates = []map[string]any{{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 10,
			"date":       1793370000,
			"from": map[string]any{
				"id": 42, "first_name": "Ridho", "last_name": "Mujizat", "username": "ridho",
			},
			"chat": map[string]any{
				"id": 42, "type": "private", "first_name": "Ridho", "last_name": "Mujizat", "username": "ridho",
			},
			"text": "hi",
		},
	}}
	backend := &webBackend{home: adapterHome(t, api.server.URL, adapterTokenEnv)}

	chats, err := backend.TelegramDiscover(context.Background())
	if err != nil {
		t.Fatalf("TelegramDiscover: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("chats = %+v, want one", chats)
	}
	chat := chats[0]
	if chat.ChatID != 42 || chat.Type != "private" || chat.Name != "Ridho Mujizat" {
		t.Fatalf("chat = %+v", chat)
	}
	if chat.SenderID != 42 || chat.SenderName != "Ridho Mujizat" || chat.LastMessageAt != 1793370000000 {
		t.Fatalf("sender fields = %+v", chat)
	}

	var getUpdates *adapterCall
	for _, call := range api.snapshot() {
		if call.method == "getUpdates" {
			call := call
			getUpdates = &call
		}
	}
	if getUpdates == nil {
		t.Fatal("no getUpdates call")
	}
	if getUpdates.params["offset"] != float64(0) || getUpdates.params["timeout"] != float64(0) {
		t.Fatalf("getUpdates params = %v, want offset 0 and timeout 0", getUpdates.params)
	}
	allowed, _ := getUpdates.params["allowed_updates"].([]any)
	if len(allowed) != 1 || allowed[0] != "message" {
		t.Fatalf("allowed_updates = %v, want [message]", getUpdates.params["allowed_updates"])
	}
	if getUpdates.token != adapterToken {
		t.Fatalf("getUpdates token = %q, want the stored token", getUpdates.token)
	}
}

// TestTelegramDiscoverAdapterConflict covers the 409 mapping and the token-free
// error text.
func TestTelegramDiscoverAdapterConflict(t *testing.T) {
	t.Setenv(adapterTokenEnv, adapterToken)
	api := newFakeBotAPI(t)
	api.updatesStatus = http.StatusConflict
	backend := &webBackend{home: adapterHome(t, api.server.URL, adapterTokenEnv)}

	_, err := backend.TelegramDiscover(context.Background())
	if !errors.Is(err, web.ErrTelegramConflict) {
		t.Fatalf("error = %v, want ErrTelegramConflict", err)
	}
	if strings.Contains(err.Error(), adapterToken) {
		t.Fatalf("error leaked the token: %q", err)
	}
}

// TestTelegramDiscoverAdapterMissingToken covers the no-token sentinel.
func TestTelegramDiscoverAdapterMissingToken(t *testing.T) {
	t.Setenv(adapterTokenEnv, "")
	api := newFakeBotAPI(t)
	backend := &webBackend{home: adapterHome(t, api.server.URL, adapterTokenEnv)}

	_, err := backend.TelegramDiscover(context.Background())
	if !errors.Is(err, web.ErrTelegramTokenMissing) {
		t.Fatalf("error = %v, want ErrTelegramTokenMissing", err)
	}
	if len(api.snapshot()) != 0 {
		t.Fatalf("missing token still called the Bot API: %+v", api.snapshot())
	}
}

// TestTelegramAdapterNetworkError covers the unreachable sentinel and the
// token-free error text.
func TestTelegramAdapterNetworkError(t *testing.T) {
	t.Setenv(adapterTokenEnv, adapterToken)
	backend := &webBackend{home: adapterHome(t, "http://127.0.0.1:1", adapterTokenEnv)}

	if _, err := backend.TelegramTokenCheck(context.Background(), adapterToken); !errors.Is(err, web.ErrTelegramUnreachable) {
		t.Fatalf("TelegramTokenCheck error = %v, want ErrTelegramUnreachable", err)
	} else if strings.Contains(err.Error(), adapterToken) {
		t.Fatalf("TelegramTokenCheck error leaked the token: %q", err)
	}
	if _, err := backend.TelegramDiscover(context.Background()); !errors.Is(err, web.ErrTelegramUnreachable) {
		t.Fatalf("TelegramDiscover error = %v, want ErrTelegramUnreachable", err)
	} else if strings.Contains(err.Error(), adapterToken) {
		t.Fatalf("TelegramDiscover error leaked the token: %q", err)
	}
}

// TestTelegramBotUsernameAdapter covers the optional bot_username companion.
func TestTelegramBotUsernameAdapter(t *testing.T) {
	t.Setenv(adapterTokenEnv, adapterToken)
	api := newFakeBotAPI(t)
	backend := &webBackend{home: adapterHome(t, api.server.URL, adapterTokenEnv)}

	username, err := backend.TelegramBotUsername(context.Background())
	if err != nil {
		t.Fatalf("TelegramBotUsername: %v", err)
	}
	if username != "lana_bot" {
		t.Fatalf("username = %q, want lana_bot", username)
	}

	api.mu.Lock()
	api.getMeStatus = http.StatusUnauthorized
	api.mu.Unlock()
	if _, err := backend.TelegramBotUsername(context.Background()); !errors.Is(err, web.ErrTelegramTokenMissing) {
		t.Fatalf("401 error = %v, want ErrTelegramTokenMissing", err)
	}
}

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/web"
)

// telegramCheckTimeout is the limit of API-WEB-028 check.
const telegramCheckTimeout = 15 * time.Second

// telegramDiscoverTimeout is the limit of API-TGC-002 discover.
const telegramDiscoverTimeout = 15 * time.Second

// TelegramCheck implements web.TelegramChecker (API-WEB-028). It stays out of
// cmd/lana/web.go so the channels task does not edit the shared adapter file.
var _ web.TelegramChecker = (*webBackend)(nil)

// TelegramSetup implements the API-TGC-001 through API-TGC-003 port.
var _ web.TelegramSetup = (*webBackend)(nil)

// TelegramBotNamer fills bot_username in API-TGC-002 without growing the
// two-method TelegramSetup port of design.md.
var _ web.TelegramBotNamer = (*webBackend)(nil)

// TelegramCheck calls Bot API getMe with the token of the configured variable
// and returns the bot identity. The token value is read from the process
// environment or secrets.env, never from the request, and it is never returned.
func (b *webBackend) TelegramCheck(ctx context.Context) (web.TelegramCheckResult, error) {
	settings, err := b.telegramCheckSettings(ctx)
	if err != nil {
		return web.TelegramCheckResult{}, err
	}
	if settings.token == "" {
		return web.TelegramCheckResult{OK: false, Message: "Token missing"}, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, telegramCheckTimeout)
	defer cancel()
	client := telegram.NewClient(settings.apiBase, settings.token)
	bot, err := client.GetMe(checkCtx)
	if err != nil {
		if errors.Is(err, telegram.ErrUnauthorized) {
			return web.TelegramCheckResult{OK: false, Message: "invalid bot token"}, nil
		}
		// Client.scrub replaces the token in network and Bot API errors.
		return web.TelegramCheckResult{OK: false, Message: err.Error()}, nil
	}
	return web.TelegramCheckResult{OK: true, BotUsername: bot.Username}, nil
}

// TelegramTokenCheck calls getMe with the token of the save request, not the
// stored one. A 401 is an expected invalid token; any other failure means
// Telegram could not be reached.
func (b *webBackend) TelegramTokenCheck(ctx context.Context, token string) (web.TelegramCheckResult, error) {
	settings, err := b.telegramConfigSettings(ctx)
	if err != nil {
		return web.TelegramCheckResult{}, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, telegramCheckTimeout)
	defer cancel()
	bot, err := telegram.NewClient(settings.apiBase, token).GetMe(checkCtx)
	if err != nil {
		if errors.Is(err, telegram.ErrUnauthorized) {
			return web.TelegramCheckResult{OK: false}, nil
		}
		return web.TelegramCheckResult{}, web.ErrTelegramUnreachable
	}
	return web.TelegramCheckResult{OK: true, BotUsername: bot.Username}, nil
}

// TelegramBotUsername returns the stored bot identity for API-TGC-002. It is
// the optional companion of TelegramSetup.
func (b *webBackend) TelegramBotUsername(ctx context.Context) (string, error) {
	settings, err := b.telegramCheckSettings(ctx)
	if err != nil {
		return "", err
	}
	if settings.token == "" {
		return "", web.ErrTelegramTokenMissing
	}
	checkCtx, cancel := context.WithTimeout(ctx, telegramCheckTimeout)
	defer cancel()
	bot, err := telegram.NewClient(settings.apiBase, settings.token).GetMe(checkCtx)
	if err != nil {
		if errors.Is(err, telegram.ErrUnauthorized) {
			return "", web.ErrTelegramTokenMissing
		}
		return "", web.ErrTelegramUnreachable
	}
	return bot.Username, nil
}

// TelegramDiscover implements API-TGC-002 with the stored token. It only peeks
// at getUpdates, so the polling channel still receives those updates. The web
// discover handler uses TelegramStatus instead of this method while the daemon
// polls, because a second getUpdates would get a 409.
func (b *webBackend) TelegramDiscover(ctx context.Context) ([]web.TelegramSeenChat, error) {
	settings, err := b.telegramCheckSettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.token == "" {
		return nil, web.ErrTelegramTokenMissing
	}
	discoverCtx, cancel := context.WithTimeout(ctx, telegramDiscoverTimeout)
	defer cancel()
	updates, err := telegram.NewClient(settings.apiBase, settings.token).GetUpdatesPeek(discoverCtx)
	if err != nil {
		if errors.Is(err, telegram.ErrConflict) {
			return nil, web.ErrTelegramConflict
		}
		return nil, web.ErrTelegramUnreachable
	}
	chats := make([]web.TelegramSeenChat, 0, len(updates))
	for _, update := range updates {
		if update.Message == nil {
			continue
		}
		message := update.Message
		name := telegramChatName(message.Chat)
		senderID := message.Chat.ID
		senderName := name
		if message.From != nil {
			senderID = message.From.ID
			senderName = telegramUserName(message.From.FirstName, message.From.LastName, message.From.Username)
		}
		chats = append(chats, web.TelegramSeenChat{
			ChatID:        message.Chat.ID,
			Type:          message.Chat.Type,
			Name:          name,
			SenderID:      senderID,
			SenderName:    senderName,
			LastMessageAt: message.Date * 1000,
		})
	}
	return chats, nil
}

func telegramChatName(chat telegram.Chat) string {
	if chat.Title != "" {
		return chat.Title
	}
	return telegramUserName(chat.FirstName, chat.LastName, chat.Username)
}

func telegramUserName(first, last, username string) string {
	if name := strings.TrimSpace(first + " " + last); name != "" {
		return name
	}
	if username != "" {
		return "@" + username
	}
	return ""
}

// telegramCheckSettings reads the token variable and the API base from
// config.toml and resolves the token. The value never leaves this function.
type telegramCheckSettings struct {
	tokenEnv string
	apiBase  string
	token    string
}

func (b *webBackend) telegramCheckSettings(ctx context.Context) (telegramCheckSettings, error) {
	settings, err := b.telegramConfigSettings(ctx)
	if err != nil {
		return telegramCheckSettings{}, err
	}
	if value, ok := os.LookupEnv(settings.tokenEnv); ok && strings.TrimSpace(value) != "" {
		settings.token = strings.TrimSpace(value)
		return settings, nil
	}
	if b.home == "" {
		return settings, nil
	}
	values, err := webfiles.ReadSecrets(filepath.Join(b.home, "secrets.env"))
	if err != nil {
		return telegramCheckSettings{}, err
	}
	settings.token = strings.TrimSpace(values[settings.tokenEnv])
	return settings, nil
}

// telegramConfigSettings reads the token variable and API base from config.toml
// without resolving the stored token. TelegramTokenCheck uses it with the token
// of the request.
func (b *webBackend) telegramConfigSettings(ctx context.Context) (telegramCheckSettings, error) {
	if err := ctx.Err(); err != nil {
		return telegramCheckSettings{}, err
	}
	settings := telegramCheckSettings{
		tokenEnv: "TELEGRAM_BOT_TOKEN",
		apiBase:  telegram.DefaultAPIBase,
	}
	data, err := os.ReadFile(filepath.Join(b.home, "config.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return telegramCheckSettings{}, err
	}
	if len(data) > 0 {
		var document struct {
			Channels map[string]map[string]any `toml:"channels"`
		}
		if err := toml.Unmarshal(data, &document); err != nil {
			return telegramCheckSettings{}, err
		}
		if table, ok := document.Channels["telegram"]; ok {
			if value, ok := table["token_env"].(string); ok && value != "" {
				settings.tokenEnv = value
			}
			if value, ok := table["api_base"].(string); ok && value != "" {
				settings.apiBase = value
			}
		}
	}
	return settings, nil
}

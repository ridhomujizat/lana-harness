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

// TelegramCheck implements web.TelegramChecker (API-WEB-028). It stays out of
// cmd/lana/web.go so the channels task does not edit the shared adapter file.
var _ web.TelegramChecker = (*webBackend)(nil)

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

// telegramCheckSettings reads the token variable and the API base from
// config.toml and resolves the token. The value never leaves this function.
type telegramCheckSettings struct {
	tokenEnv string
	apiBase  string
	token    string
}

func (b *webBackend) telegramCheckSettings(ctx context.Context) (telegramCheckSettings, error) {
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

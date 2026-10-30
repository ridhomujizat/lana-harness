package main

import (
	"context"

	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/web"
)

// telegramResetSender implements web.TelegramResetSender with the running
// Telegram channel (REQ-WLG-004). A reset code is sent as plain text to each
// owner that has a private-chat route.
type telegramResetSender struct {
	channel *telegram.Channel
	owners  map[int64]bool
	routes  map[int64]string
	// send delivers one message to a chat. It is nil in production, where
	// SendOwnerCode uses the channel's SendOwnerMessage. Tests replace it to
	// send without the network.
	send func(ctx context.Context, chatID int64, text string) error
}

var _ web.TelegramResetSender = (*telegramResetSender)(nil)

func newTelegramResetSender(channel *telegram.Channel, cfg telegram.Config) *telegramResetSender {
	return &telegramResetSender{channel: channel, owners: cfg.OwnerIDs, routes: cfg.Routes}
}

// ResetAvailable reports a running channel with at least one owner that has a
// private-chat route. A private chat's id equals the owner's user id, so the
// owner id is also a route key.
func (t *telegramResetSender) ResetAvailable() bool {
	if t == nil || t.channel == nil {
		return false
	}
	for id := range t.owners {
		if _, ok := t.routes[id]; ok {
			return true
		}
	}
	return false
}

// SendOwnerCode sends the reset code to every owner private chat and returns
// how many messages were sent.
func (t *telegramResetSender) SendOwnerCode(ctx context.Context, code string) (int, error) {
	text := telegramResetMessage(code)
	send := t.send
	if send == nil {
		send = t.channel.SendOwnerMessage
	}
	sent := 0
	var firstErr error
	for id := range t.owners {
		if _, ok := t.routes[id]; !ok {
			continue
		}
		if err := send(ctx, id, text); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
	}
	return sent, firstErr
}

// telegramResetMessage is the plain-text reset message of REQ-WLG-004.
func telegramResetMessage(code string) string {
	return "🔐 Your lana reset code: " + code + ". It expires in 10 minutes. If you did not ask for it, ignore this message."
}

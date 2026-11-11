package telegram

import (
	"context"
	"errors"
	"html"
	"unicode/utf8"
)

// discussionMaxRunes keeps one discussion post under Telegram's 4096-character
// message limit after the name prefix.
const discussionMaxRunes = 3800

// SendDiscussion posts one room discussion message to the session's chat as
// "<b>Name</b>: text" (spec 47 REQ-RDS-033). One bot speaks for every agent.
func (c *Channel) SendDiscussion(ctx context.Context, sessionID, name, text string) error {
	chatID, ok := c.fileChatID(ctx, sessionID)
	if !ok {
		return errors.New("the session is not bound to a telegram chat")
	}
	if utf8.RuneCountInString(text) > discussionMaxRunes {
		text = string([]rune(text)[:discussionMaxRunes]) + "…"
	}
	body := "<b>" + html.EscapeString(name) + "</b>: " + html.EscapeString(text)
	_, err := c.api.SendMessage(ctx, chatID, body, SendOptions{ParseMode: "HTML"})
	return err
}

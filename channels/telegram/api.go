// Package telegram is the Telegram channel: Bot API long polling, one worker per chat,
// streamed answers through message edits, and approval through inline keyboards.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthorized means Telegram rejected the bot token (HTTP 401).
var ErrUnauthorized = errors.New("telegram: invalid bot token")

// ErrConflict means another getUpdates call is active for the bot (HTTP 409).
// GetUpdatesPeek maps a conflict to this sentinel; the other methods keep
// returning *APIError for a 409.
var ErrConflict = errors.New("telegram: getUpdates conflict")

// APIError is an error response from the Bot API.
type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

// Client is a minimal Bot API client.
type Client struct {
	base  string
	token string
	http  *http.Client
	sleep func(context.Context, time.Duration) error
}

// NewClient creates a client for base (e.g. https://api.telegram.org) and token.
func NewClient(base, token string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 60 * time.Second}, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call posts params as JSON to method and decodes the result into out. A 429 response
// is retried after retry_after, at most 3 times.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		err := c.do(ctx, method, body, out)
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusTooManyRequests && attempt < 3 {
			wait := time.Duration(apiErr.RetryAfter) * time.Second
			if wait <= 0 {
				wait = time.Second
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		return err
	}
}

func (c *Client) do(ctx context.Context, method string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return c.scrub(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	var decoded apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return &APIError{Code: resp.StatusCode, Description: "invalid response"}
	}
	if !decoded.OK {
		if decoded.ErrorCode == http.StatusUnauthorized {
			return ErrUnauthorized
		}
		code := decoded.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Code: code, Description: decoded.Description, RetryAfter: decoded.Parameters.RetryAfter}
	}
	if out != nil && len(decoded.Result) > 0 {
		return json.Unmarshal(decoded.Result, out)
	}
	return nil
}

// scrub removes the token from transport errors, which include the request URL.
func (c *Client) scrub(err error) error {
	if c.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "<token>"))
}

// User is a Telegram user or bot.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// Chat is a Telegram chat.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// Message is a Telegram message. Only the fields lana uses are decoded.
type Message struct {
	MessageID int64    `json:"message_id"`
	Date      int64    `json:"date"`
	From      *User    `json:"from"`
	Chat      Chat     `json:"chat"`
	Text      string   `json:"text"`
	ReplyTo   *Message `json:"reply_to_message"`
	Photo     []any    `json:"photo"`
	Voice     any      `json:"voice"`
	Audio     any      `json:"audio"`
	Video     any      `json:"video"`
	VideoNote any      `json:"video_note"`
	Document  any      `json:"document"`
	Sticker   any      `json:"sticker"`
	Animation any      `json:"animation"`
}

// HasMedia reports whether the message carries non-text content.
func (m *Message) HasMedia() bool {
	return len(m.Photo) > 0 || m.Voice != nil || m.Audio != nil || m.Video != nil || m.VideoNote != nil ||
		m.Document != nil || m.Sticker != nil || m.Animation != nil
}

// CallbackQuery is a button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Update is one item from getUpdates.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button is an inline keyboard button.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Keyboard is an inline keyboard.
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}

// Command is a bot command for setMyCommands.
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// GetMe returns the bot user.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var user User
	err := c.call(ctx, "getMe", struct{}{}, &user)
	return user, err
}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeout, "allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

// GetUpdatesPeek reads the current updates without confirming them. It asks for
// offset 0 and timeout 0 and never follows up with an offset > 0, so the
// polling channel still receives the updates. A 409 means another poller or a
// webhook owns the bot.
func (c *Client) GetUpdatesPeek(ctx context.Context) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset": 0, "timeout": 0, "allowed_updates": []string{"message"},
	}, &updates)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusConflict {
		return nil, ErrConflict
	}
	return updates, err
}

// SendOptions are optional sendMessage and editMessageText fields.
type SendOptions struct {
	ParseMode string
	ReplyTo   int64
	Keyboard  *Keyboard
}

// SendMessage sends text to a chat and returns the message ID.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, opts SendOptions) (int64, error) {
	params := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if opts.ParseMode != "" {
		params["parse_mode"] = opts.ParseMode
	}
	if opts.ReplyTo != 0 {
		params["reply_parameters"] = map[string]any{"message_id": opts.ReplyTo, "allow_sending_without_reply": true}
	}
	if opts.Keyboard != nil {
		params["reply_markup"] = opts.Keyboard
	}
	var msg Message
	err := c.call(ctx, "sendMessage", params, &msg)
	return msg.MessageID, err
}

// EditMessageText replaces the text of a message. A nil keyboard removes the buttons.
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, opts SendOptions) error {
	params := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if opts.ParseMode != "" {
		params["parse_mode"] = opts.ParseMode
	}
	if opts.Keyboard != nil {
		params["reply_markup"] = opts.Keyboard
	}
	return c.call(ctx, "editMessageText", params, nil)
}

// DeleteMessage deletes a message.
func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

// SendChatAction sends a chat action such as "typing".
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	return c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

// SetMessageReaction sets one emoji reaction on a message.
func (c *Client) SetMessageReaction(ctx context.Context, chatID, messageID int64, emoji string) error {
	return c.call(ctx, "setMessageReaction", map[string]any{
		"chat_id": chatID, "message_id": messageID, "reaction": []map[string]string{{"type": "emoji", "emoji": emoji}},
	}, nil)
}

// AnswerCallbackQuery acknowledges a button press, optionally with a toast.
func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	params := map[string]any{"callback_query_id": id}
	if text != "" {
		params["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
}

// SetMyCommands registers the bot command list.
func (c *Client) SetMyCommands(ctx context.Context, commands []Command) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil)
}

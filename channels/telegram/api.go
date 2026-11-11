// Package telegram is the Telegram channel: Bot API long polling, one worker per chat,
// streamed answers through message edits, and approval through inline keyboards.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/attach"
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
	// upload sends multipart file uploads with a longer timeout: a file up to
	// 20 MB takes longer than a JSON call on a slow link (REQ-AGF-004).
	upload *http.Client
	sleep  func(context.Context, time.Duration) error
}

// NewClient creates a client for base (e.g. https://api.telegram.org) and token.
func NewClient(base, token string) *Client {
	return &Client{
		base:   strings.TrimRight(base, "/"),
		token:  token,
		http:   &http.Client{Timeout: 60 * time.Second},
		upload: &http.Client{Timeout: 2 * time.Minute},
		sleep:  sleepCtx,
	}
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

// call posts params as JSON to method and decodes the result into out.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.callBody(ctx, method, "application/json", body, out)
}

// callBody posts body to method with the shared client and decodes the result
// into out. A 429 response is retried after retry_after, at most 3 times.
func (c *Client) callBody(ctx context.Context, method, contentType string, body []byte, out any) error {
	return c.callBodyClient(ctx, c.http, method, contentType, body, out)
}

// callBodyClient posts body to method with client and decodes the result into
// out. A 429 response is retried after retry_after, at most 3 times. body is
// rebuilt into a fresh reader on each attempt, so a multipart upload re-sends
// its bytes rather than the exhausted first reader.
func (c *Client) callBodyClient(ctx context.Context, client *http.Client, method, contentType string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.doBody(ctx, client, method, contentType, body, out)
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

func (c *Client) doBody(ctx context.Context, client *http.Client, method, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.scrub(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := client.Do(req)
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

// PhotoSize is one size of a photo message. A photo message carries several,
// smallest first, so the last is the largest (REQ-ATT-005).
type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

// Document is a document message: any file the owner sends as a file.
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// File is a getFile result: the file's path on the Bot API file server and its
// size in bytes.
type File struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}

// Message is a Telegram message. Only the fields lana uses are decoded.
type Message struct {
	MessageID int64    `json:"message_id"`
	Date      int64    `json:"date"`
	From      *User    `json:"from"`
	Chat      Chat     `json:"chat"`
	Text      string   `json:"text"`
	Caption   string   `json:"caption"`
	ReplyTo   *Message `json:"reply_to_message"`
	// MediaGroupID is set on every part of an album (REQ-ATT-005, AC-ATT-006).
	MediaGroupID string      `json:"media_group_id"`
	Photo        []PhotoSize `json:"photo"`
	Voice        any         `json:"voice"`
	Audio        any         `json:"audio"`
	Video        any         `json:"video"`
	VideoNote    any         `json:"video_note"`
	Document     *Document   `json:"document"`
	Sticker      any         `json:"sticker"`
	Animation    any         `json:"animation"`
}

// HasMedia reports whether the message carries non-text content.
func (m *Message) HasMedia() bool {
	return len(m.Photo) > 0 || m.Voice != nil || m.Audio != nil || m.Video != nil || m.VideoNote != nil ||
		m.Document != nil || m.Sticker != nil || m.Animation != nil
}

// isFileMessage reports whether the message carries media lana saves as an
// attachment: a photo or a document (REQ-ATT-005).
func (m *Message) isFileMessage() bool {
	return len(m.Photo) > 0 || m.Document != nil
}

// hasUnsupportedMedia reports whether the message carries media lana cannot
// read (REQ-ATT-005).
func (m *Message) hasUnsupportedMedia() bool {
	return m.Voice != nil || m.Audio != nil || m.Video != nil || m.VideoNote != nil ||
		m.Sticker != nil || m.Animation != nil
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

// GetFile resolves a file ID to its path on the Bot API file server. An answer
// the Bot API refuses because the file is over its own download limit becomes
// an attach.ErrTooLarge error (REQ-ATT-005).
func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	var file File
	err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &file)
	var apiErr *APIError
	if errors.As(err, &apiErr) && strings.Contains(apiErr.Description, "file is too big") {
		return File{}, fmt.Errorf("telegram: %s: %w", apiErr.Description, attach.ErrTooLarge)
	}
	return file, err
}

// Download streams a file from the Bot API file server into w, copying at most
// limit bytes; more than limit bytes is attach.ErrTooLarge. A 401 is
// ErrUnauthorized; another non-200 status is *APIError (REQ-ATT-005). The
// token is scrubbed from transport errors.
func (c *Client) Download(ctx context.Context, filePath string, w io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/file/bot"+c.token+"/"+filePath, nil)
	if err != nil {
		return c.scrub(err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.scrub(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		var decoded apiResponse
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			return &APIError{Code: resp.StatusCode, Description: "invalid response"}
		}
		code := decoded.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Code: code, Description: decoded.Description}
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return c.scrub(err)
	}
	if n > limit {
		return fmt.Errorf("telegram: file over %d bytes: %w", limit, attach.ErrTooLarge)
	}
	return nil
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

// SendPhoto sends an image with sendPhoto and returns the message ID
// (REQ-AGF-004). The file streams into one multipart request; the 429 retry
// reuses the built body.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, filename string, r io.Reader, caption string) (int64, error) {
	return c.sendFile(ctx, "sendPhoto", "photo", chatID, filename, r, caption)
}

// SendDocument sends a file with sendDocument and returns the message ID
// (REQ-AGF-004).
func (c *Client) SendDocument(ctx context.Context, chatID int64, filename string, r io.Reader, caption string) (int64, error) {
	return c.sendFile(ctx, "sendDocument", "document", chatID, filename, r, caption)
}

// sendFile posts one multipart file to method. field is "photo" or "document".
func (c *Client) sendFile(ctx context.Context, method, field string, chatID int64, filename string, r io.Reader, caption string) (int64, error) {
	fields := map[string]string{"chat_id": strconv.FormatInt(chatID, 10)}
	if caption != "" {
		fields["caption"] = caption
	}
	body, contentType, err := multipartForm(fields, field, filename, r)
	if err != nil {
		return 0, err
	}
	var msg Message
	if err := c.callBodyClient(ctx, c.upload, method, contentType, body, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// multipartForm builds one multipart/form-data body with text fields and a
// single file part.
func multipartForm(fields map[string]string, field, filename string, r io.Reader) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			return nil, "", err
		}
	}
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, r); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
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

// EditMessageReplyMarkup replaces the inline keyboard of a message. A nil
// keyboard removes the buttons.
func (c *Client) EditMessageReplyMarkup(ctx context.Context, chatID, messageID int64, keyboard *Keyboard) error {
	markup := keyboard
	if markup == nil {
		markup = &Keyboard{Rows: [][]Button{}}
	}
	return c.call(ctx, "editMessageReplyMarkup", map[string]any{
		"chat_id": chatID, "message_id": messageID, "reply_markup": markup,
	}, nil)
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

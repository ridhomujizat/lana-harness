package telegram

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// photoMaxBytes is the largest image Telegram accepts through sendPhoto; a
// larger image goes out as a document (REQ-AGF-004).
const photoMaxBytes = 10 << 20

// captionLimit is the Bot API caption length, in runes.
const captionLimit = 1024

// SendFile delivers a file the send_file tool sent to the chat of sessionID
// (REQ-AGF-004). A child session delivers to its nearest bound ancestor's
// chat; a session not bound in this process falls back to the chat id in its
// address. An image of at most 10 MB goes with sendPhoto, everything else with
// sendDocument.
func (c *Channel) SendFile(ctx context.Context, sessionID string, f core.SentFile) error {
	chatID, ok := c.fileChatID(ctx, sessionID)
	if !ok {
		return errors.New("the session is not bound to a telegram chat")
	}
	file, err := os.Open(f.AbsPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	caption := trimCaption(f.Caption)
	if isPhoto(f.MediaType, f.Size) {
		_, err = c.api.SendPhoto(ctx, chatID, f.Name, file, caption)
	} else {
		_, err = c.api.SendDocument(ctx, chatID, f.Name, file, caption)
	}
	return err
}

// fileChatID resolves the chat of a session or of its nearest bound ancestor,
// then falls back to the chat id in the session's address when nothing is
// bound in this process. A Telegram session's address is its chat id.
func (c *Channel) fileChatID(ctx context.Context, sessionID string) (int64, bool) {
	if w := c.chatForApproval(ctx, sessionID); w != nil {
		return w.id, true
	}
	info, err := c.backend.SessionInfo(ctx, sessionID)
	if err != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(info.Address), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// isPhoto reports whether a file goes out with sendPhoto: a JPEG, PNG, or WebP
// image of at most 10 MB (REQ-AGF-004).
func isPhoto(mediaType string, size int64) bool {
	if size > photoMaxBytes {
		return false
	}
	switch mediaType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

// trimCaption cuts a caption to the Bot API limit.
func trimCaption(caption string) string {
	runes := []rune(caption)
	if len(runes) <= captionLimit {
		return caption
	}
	return string(runes[:captionLimit])
}

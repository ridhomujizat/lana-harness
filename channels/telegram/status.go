package telegram

// Channel states of API-TG-007.
const (
	StateConnected    = "connected"
	StateTokenMissing = "token_missing"
	StateOff          = "off"
)

// Status is the in-memory snapshot of the Telegram channel for the web
// Channels page (API-TG-007). It is read-only for the web view.
type Status struct {
	State         string        `json:"state"`
	BotUsername   string        `json:"bot_username"`
	ChatCount     int           `json:"chat_count"`
	OwnerCount    int           `json:"owner_count"`
	PollingSince  *int64        `json:"polling_since"`
	LastMessageAt *int64        `json:"last_message_at"`
	PendingChats  []PendingChat `json:"pending_chats"`
}

// PendingChat is one chat that wrote to the bot without a route (REQ-TG-016).
type PendingChat struct {
	ChatID    int64  `json:"chat_id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Messages  int    `json:"messages"`
}

// StatusFunc reports the current channel status. The daemon passes one to the
// web view.
type StatusFunc func() Status

// Status returns the status of a running channel.
func (c *Channel) Status() Status {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	status := Status{
		State:        StateConnected,
		BotUsername:  c.bot.Username,
		ChatCount:    len(c.cfg.Routes),
		OwnerCount:   len(c.cfg.OwnerIDs),
		PendingChats: []PendingChat{},
	}
	if c.pending != nil {
		status.PendingChats = c.pending.list(now)
	}
	if !c.pollingSince.IsZero() {
		since := c.pollingSince.UnixMilli()
		status.PollingSince = &since
	}
	if !c.lastMessageAt.IsZero() {
		at := c.lastMessageAt.UnixMilli()
		status.LastMessageAt = &at
	}
	return status
}

// StatusFor returns the status of a channel that is not polling: off when
// disabled and token_missing when the token is absent.
func StatusFor(cfg Config, tokenPresent bool) Status {
	status := Status{
		State:        StateConnected,
		ChatCount:    len(cfg.Routes),
		OwnerCount:   len(cfg.OwnerIDs),
		PendingChats: []PendingChat{},
	}
	if !cfg.Enabled {
		status.State = StateOff
	} else if !tokenPresent {
		status.State = StateTokenMissing
	}
	return status
}

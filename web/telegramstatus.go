package web

// TelegramStatus is the in-memory Telegram channel snapshot the Channels page
// reads (API-TG-007). The daemon fills it from channels/telegram; the web
// package never imports the channel.
type TelegramStatus struct {
	State         string                `json:"state"`
	BotUsername   string                `json:"bot_username"`
	ChatCount     int                   `json:"chat_count"`
	OwnerCount    int                   `json:"owner_count"`
	PollingSince  *int64                `json:"polling_since"`
	LastMessageAt *int64                `json:"last_message_at"`
	PendingChats  []TelegramPendingChat `json:"pending_chats"`
}

// TelegramPendingChat is one chat that wrote to the bot without a route
// (REQ-TG-016).
type TelegramPendingChat struct {
	ChatID    int64  `json:"chat_id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Messages  int    `json:"messages"`
}

// SetTelegramStatus installs the status reader used by the Channels page. The
// daemon calls it before the server starts serving (API-TG-007).
func (s *Server) SetTelegramStatus(fn func() TelegramStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telegramStatus = fn
}

// TelegramStatus reads the current snapshot. It returns the zero value when no
// reader is configured, so a daemon without a Telegram channel stays safe.
func (s *Server) TelegramStatus() TelegramStatus {
	s.mu.Lock()
	fn := s.telegramStatus
	s.mu.Unlock()
	if fn == nil {
		return TelegramStatus{}
	}
	return fn()
}

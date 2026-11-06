package web

import "testing"

// TestSetTelegramStatusStoresFunc covers API-TG-007: the daemon installs a
// status reader and the Channels page reads the current snapshot through it.
func TestSetTelegramStatusStoresFunc(t *testing.T) {
	s := &Server{}
	status := TelegramStatus{
		State:        "connected",
		BotUsername:  "lana_bot",
		ChatCount:    2,
		OwnerCount:   1,
		PollingSince: int64Ptr(1000),
		PendingChats: []TelegramPendingChat{{
			ChatID: 333, Type: "private", Name: "Budi", UserID: 333,
			Username: "budi", FirstSeen: 1000, LastSeen: 2000, Messages: 2,
		}},
	}
	calls := 0
	s.SetTelegramStatus(func() TelegramStatus {
		calls++
		return status
	})

	got := s.TelegramStatus()
	if calls != 1 {
		t.Fatalf("reader calls = %d, want 1", calls)
	}
	if got.State != status.State || got.BotUsername != status.BotUsername ||
		got.ChatCount != status.ChatCount || got.OwnerCount != status.OwnerCount {
		t.Fatalf("status = %+v, want %+v", got, status)
	}
	if got.PollingSince == nil || *got.PollingSince != 1000 {
		t.Fatalf("polling_since = %v, want 1000", got.PollingSince)
	}
	if len(got.PendingChats) != 1 || got.PendingChats[0].Name != "Budi" || got.PendingChats[0].Messages != 2 {
		t.Fatalf("pending_chats = %+v", got.PendingChats)
	}

	// A later setter call replaces the reader.
	s.SetTelegramStatus(func() TelegramStatus { return TelegramStatus{State: "off"} })
	if got := s.TelegramStatus(); got.State != "off" {
		t.Fatalf("state = %q, want off", got.State)
	}
}

// TestSetTelegramStatusNilSafe covers a server started without a Telegram
// channel: reading before the setter or after a nil setter yields the zero
// snapshot instead of panicking.
func TestSetTelegramStatusNilSafe(t *testing.T) {
	s := &Server{}
	if got := s.TelegramStatus(); got.State != "" || got.PendingChats != nil {
		t.Fatalf("unset status = %+v", got)
	}
	s.SetTelegramStatus(nil)
	if got := s.TelegramStatus(); got.State != "" || got.BotUsername != "" || got.PollingSince != nil {
		t.Fatalf("nil status = %+v", got)
	}
	s.SetTelegramStatus(func() TelegramStatus { return TelegramStatus{State: "connected"} })
	s.SetTelegramStatus(nil)
	if got := s.TelegramStatus(); got.State != "" {
		t.Fatalf("status after nil setter = %+v", got)
	}
}

func int64Ptr(v int64) *int64 { return &v }

package telegram

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injectable, race-safe clock for pending-chat tests.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock(at time.Time) *fakeClock { return &fakeClock{at: at} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func pendingUpdate(chat Chat, from *User, text string) Update {
	return Update{Message: &Message{
		MessageID: 1,
		From:      from,
		Chat:      chat,
		Text:      text,
	}}
}

// waitPending polls the channel status until match holds. Wall-clock polling is
// only used to wait for the poll loop; retention uses the injected clock.
func waitPending(t *testing.T, ch *Channel, match func([]PendingChat) bool) []PendingChat {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		list := ch.Status().PendingChats
		if match(list) {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending chats = %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPendingChatAC022(t *testing.T) {
	base := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	api := newFakeAPI(t)
	ch := startStatusChannel(t, api, newFakeBackend(), clock.Now)

	api.text(333, "private", 333, "hello")
	// A routed message is the fence: once its reply is edited, the earlier
	// unrouted update has been dispatched too.
	api.text(ownerChat, "private", ownerID, "hi")
	api.waitCall("editMessageText", nil)

	list := ch.Status().PendingChats
	if len(list) != 1 {
		t.Fatalf("pending chats = %+v", list)
	}
	got := list[0]
	if got.ChatID != 333 || got.Type != "private" || got.Name != "Budi" || got.Messages != 1 {
		t.Fatalf("pending chat = %+v", got)
	}
	if got.UserID != 333 {
		t.Fatalf("pending sender = %+v", got)
	}
	if got.FirstSeen != base.UnixMilli() || got.LastSeen != base.UnixMilli() {
		t.Fatalf("pending times = %d/%d, want %d", got.FirstSeen, got.LastSeen, base.UnixMilli())
	}
	for _, call := range api.snapshot() {
		if id, ok := call.Params["chat_id"].(float64); ok && int64(id) == 333 {
			t.Fatalf("bot wrote to pending chat: %v", call)
		}
	}
}

func TestStatusPendingChatsReflectAndExpire(t *testing.T) {
	base := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	api := newFakeAPI(t)
	ch := startStatusChannel(t, api, newFakeBackend(), clock.Now)

	api.push(pendingUpdate(
		Chat{ID: -777, Type: "supergroup", Title: "Ops"},
		&User{ID: 42, FirstName: "Sari", Username: "sari"},
		"hello",
	))
	list := waitPending(t, ch, func(list []PendingChat) bool { return len(list) == 1 })
	if got := list[0]; got.Name != "Ops" || got.Type != "supergroup" || got.UserID != 42 || got.Username != "sari" {
		t.Fatalf("pending chat = %+v", got)
	}

	clock.Advance(24*time.Hour + time.Second)
	if list := ch.Status().PendingChats; len(list) != 0 {
		t.Fatalf("expired pending chats = %+v", list)
	}
}

func TestPendingChatsRetentionAC023(t *testing.T) {
	base := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	p := newPendingChats()
	p.record(Chat{ID: 333, Type: "private", FirstName: "Budi"}, &User{ID: 333}, clock.Now())

	clock.Advance(24*time.Hour - time.Second)
	if got := p.list(clock.Now()); len(got) != 1 {
		t.Fatalf("record expired before 24h: %+v", got)
	}
	clock.Advance(2 * time.Second)
	if got := p.list(clock.Now()); len(got) != 0 {
		t.Fatalf("expired record still present: %+v", got)
	}
}

func TestPendingChatsCapAC023(t *testing.T) {
	base := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	p := newPendingChats()
	for i := int64(1); i <= 51; i++ {
		p.record(
			Chat{ID: i, Type: "private", FirstName: fmt.Sprintf("user%d", i)},
			&User{ID: i, Username: fmt.Sprintf("u%d", i)},
			clock.Now(),
		)
		clock.Advance(time.Second)
	}

	list := p.list(clock.Now())
	if len(list) != 50 {
		t.Fatalf("pending chats = %d, want 50", len(list))
	}
	if list[0].ChatID != 51 {
		t.Fatalf("first pending chat = %d, want 51", list[0].ChatID)
	}
	for i, rec := range list {
		if rec.ChatID == 1 {
			t.Fatalf("oldest pending chat still present: %+v", rec)
		}
		if i > 0 && rec.LastSeen > list[i-1].LastSeen {
			t.Fatalf("pending chats not most recent first: %+v", list)
		}
	}
}

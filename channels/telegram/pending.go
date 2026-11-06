package telegram

import (
	"sort"
	"sync"
	"time"
)

const (
	pendingRetention = 24 * time.Hour
	pendingLimit     = 50
)

// pendingChats is the in-memory store of chats that wrote without a route
// (REQ-TG-016). It has its own mutex because Status holds Channel.mu.
type pendingChats struct {
	mu    sync.Mutex
	chats map[int64]*pendingRecord
}

type pendingRecord struct {
	chat     PendingChat
	lastSeen time.Time
}

func newPendingChats() *pendingChats {
	return &pendingChats{chats: map[int64]*pendingRecord{}}
}

// record adds one update from chat at now, creating the record on first sight.
func (p *pendingChats) record(chat Chat, from *User, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked(now)
	rec, ok := p.chats[chat.ID]
	if !ok {
		rec = &pendingRecord{chat: PendingChat{ChatID: chat.ID, FirstSeen: now.UnixMilli()}}
		p.chats[chat.ID] = rec
	}
	rec.chat.Type = chat.Type
	rec.chat.Name = pendingName(chat, from)
	rec.chat.LastSeen = now.UnixMilli()
	rec.chat.Messages++
	rec.lastSeen = now
	if from != nil {
		rec.chat.UserID = from.ID
		rec.chat.Username = from.Username
	}
	if len(p.chats) > pendingLimit {
		p.evictOldestLocked()
	}
}

// list returns the unexpired records, most recent last message first.
func (p *pendingChats) list(now time.Time) []PendingChat {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked(now)
	out := make([]PendingChat, 0, len(p.chats))
	for _, rec := range p.chats {
		out = append(out, rec.chat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].ChatID < out[j].ChatID
	})
	if len(out) > pendingLimit {
		out = out[:pendingLimit]
	}
	return out
}

// expireLocked drops records whose last message is pendingRetention old or older.
func (p *pendingChats) expireLocked(now time.Time) {
	for id, rec := range p.chats {
		if !now.Before(rec.lastSeen.Add(pendingRetention)) {
			delete(p.chats, id)
		}
	}
}

// evictOldestLocked drops the record with the oldest last message.
func (p *pendingChats) evictOldestLocked() {
	var oldestID int64
	var oldest time.Time
	first := true
	for id, rec := range p.chats {
		if first || rec.lastSeen.Before(oldest) || (rec.lastSeen.Equal(oldest) && id < oldestID) {
			oldestID, oldest, first = id, rec.lastSeen, false
		}
	}
	if !first {
		delete(p.chats, oldestID)
	}
}

// pendingName is the chat title for groups and the first name for private
// chats, falling back to the username and then the sender's name (REQ-TG-016).
func pendingName(chat Chat, from *User) string {
	switch {
	case chat.Title != "":
		return chat.Title
	case chat.FirstName != "":
		return chat.FirstName
	case chat.Username != "":
		return chat.Username
	case from != nil && from.FirstName != "":
		return from.FirstName
	case from != nil:
		return from.Username
	default:
		return ""
	}
}

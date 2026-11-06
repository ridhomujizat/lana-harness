package telegram

import (
	"context"
	"testing"
)

// documentMsg pushes a document message with a caption and returns it.
func (f *fakeAPI) documentMsg(chatID int64, chatType string, from int64, fileID, name string, size int64, caption string) *Message {
	m := &Message{
		MessageID: 1000 + f.nextID,
		From:      &User{ID: from, FirstName: "Budi"},
		Chat:      Chat{ID: chatID, Type: chatType},
		Caption:   caption,
		Document:  &Document{FileID: fileID, FileName: name, FileSize: size},
	}
	f.push(Update{Message: m})
	return m
}

// textMsg pushes a text message, optionally as a reply, and returns it.
func (f *fakeAPI) textMsg(chatID int64, chatType string, from int64, text string, replyTo *Message) *Message {
	m := &Message{
		MessageID: 1000 + f.nextID,
		From:      &User{ID: from, FirstName: "Budi"},
		Chat:      Chat{ID: chatID, Type: chatType},
		Text:      text,
		ReplyTo:   replyTo,
	}
	f.push(Update{Message: m})
	return m
}

// AC-KNU-004: /save as a reply to a document downloads the file and stores it
// with the file name as title (REQ-KNU-012, API-KNU-002).
func TestSaveReplyToDocument(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	api.addFile("d1", "docs/report.pdf", []byte("%PDF report"))
	startChannel(t, api, backend)

	// The target document is unaddressed in the group, so it starts no turn.
	doc := api.documentMsg(groupChat, "supergroup", memberID, "d1", "report.pdf", 12, "")
	api.textMsg(groupChat, "supergroup", ownerID, "/save", doc)

	api.waitCall("sendMessage", textIs("✓ saved sources/2026-11-01 note.md — compiling into wiki pages"))
	calls := backend.savedList()
	if len(calls) != 1 {
		t.Fatalf("save calls = %d", len(calls))
	}
	if calls[0].Address != "-500" || calls[0].Agent != "helper" {
		t.Fatalf("address/agent = %q/%q", calls[0].Address, calls[0].Agent)
	}
	item := calls[0].Item
	if item.Ext != ".pdf" || item.Title != "report" || string(item.Data) != "%PDF report" {
		t.Fatalf("item = %+v", item)
	}
}

// AC-KNU-004: /save as a reply to a text message stores that text
// (REQ-KNU-012).
func TestSaveReplyToText(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	target := api.textMsg(groupChat, "supergroup", memberID, "the Q4 deadline is 15 Nov", nil)
	api.textMsg(groupChat, "supergroup", ownerID, "/save", target)

	api.waitCall("sendMessage", textHas("✓ saved "))
	calls := backend.savedList()
	if len(calls) != 1 || calls[0].Item.Text != "the Q4 deadline is 15 Nov" || calls[0].Item.URL != "" {
		t.Fatalf("saved = %+v", calls)
	}
}

// AC-KNU-004: a single URL argument is stored as a URL, not as text
// (REQ-KNU-012, API-KNU-002).
func TestSaveURLArgument(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	api.textMsg(ownerChat, "private", ownerID, "/save https://example.com/page", nil)

	api.waitCall("sendMessage", textHas("✓ saved "))
	calls := backend.savedList()
	if len(calls) != 1 || calls[0].Item.URL != "https://example.com/page" || calls[0].Item.Text != "" {
		t.Fatalf("saved = %+v", calls)
	}
}

// AC-KNU-004: a text argument is stored as text (REQ-KNU-012).
func TestSaveTextArgument(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	api.textMsg(ownerChat, "private", ownerID, "/save buy milk and eggs", nil)

	api.waitCall("sendMessage", textHas("✓ saved "))
	calls := backend.savedList()
	if len(calls) != 1 || calls[0].Item.Text != "buy milk and eggs" || calls[0].Item.URL != "" {
		t.Fatalf("saved = %+v", calls)
	}
}

// AC-KNU-004: /save as a document caption stores the document (REQ-KNU-012,
// API-KNU-002).
func TestSaveAsCaption(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	api.addFile("d1", "docs/notes.md", []byte("# notes"))
	startChannel(t, api, backend)

	api.documentMsg(ownerChat, "private", ownerID, "d1", "notes.md", 7, "/save")

	api.waitCall("sendMessage", textHas("✓ saved "))
	calls := backend.savedList()
	if len(calls) != 1 {
		t.Fatalf("save calls = %d", len(calls))
	}
	item := calls[0].Item
	if item.Ext != ".md" || item.Title != "notes" || string(item.Data) != "# notes" {
		t.Fatalf("item = %+v", item)
	}
}

// AC-KNU-004: a non-owner in a group is refused (REQ-KNU-012, API-KNU-002).
func TestSaveNonOwnerRefused(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	api.textMsg(groupChat, "supergroup", memberID, "/save secret", nil)

	api.waitCall("sendMessage", textIs("✗ only the owner can use /save"))
	if calls := backend.savedList(); len(calls) != 0 {
		t.Fatalf("saved = %+v", calls)
	}
}

// AC-KNU-004: a document with an unsupported extension is refused without a
// download (REQ-KNU-012, API-KNU-002).
func TestSaveUnsupportedExtension(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	doc := api.documentMsg(groupChat, "supergroup", memberID, "d1", "notes.docx", 5, "")
	api.textMsg(groupChat, "supergroup", ownerID, "/save", doc)

	api.waitCall("sendMessage", textIs("✗ unsupported file type .docx"))
	if n := api.count("getFile", nil); n != 0 {
		t.Fatalf("getFile calls = %d", n)
	}
	if calls := backend.savedList(); len(calls) != 0 {
		t.Fatalf("saved = %+v", calls)
	}
}

// AC-KNU-004: a backend without the KnowledgeSaver capability answers
// "knowledge is not available" (API-KNU-002).
func TestSaveKnowledgeUnavailable(t *testing.T) {
	api := newFakeAPI(t)
	startChannel(t, api, &plainBackend{Backend: newFakeBackend()})

	api.textMsg(ownerChat, "private", ownerID, "/save hello", nil)

	api.waitCall("sendMessage", textIs("✗ knowledge is not available"))
}

// AC-KNU-004: /save with no reply and no arguments answers "nothing to save"
// (API-KNU-002).
func TestSaveNothingToSave(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	api.textMsg(ownerChat, "private", ownerID, "/save", nil)

	api.waitCall("sendMessage", textIs("✗ nothing to save: reply to a message or add text"))
	if calls := backend.savedList(); len(calls) != 0 {
		t.Fatalf("saved = %+v", calls)
	}
}

// AC-KNU-004: a duplicate names the existing path instead (API-KNU-002).
func TestSaveDuplicate(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.saveHook = func(SaveItem) (string, bool, error) { return "sources/x.md", true, nil }
	startChannel(t, api, backend)

	api.textMsg(ownerChat, "private", ownerID, "/save hello", nil)

	api.waitCall("sendMessage", textIs("· already saved as sources/x.md"))
}

// AC-KNU-004: a knowledge service error is reported in the channel style
// (API-KNU-002).
func TestSaveError(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.saveHook = func(SaveItem) (string, bool, error) { return "", false, context.DeadlineExceeded }
	startChannel(t, api, backend)

	api.textMsg(ownerChat, "private", ownerID, "/save hello", nil)

	api.waitCall("sendMessage", textIs("✗ context deadline exceeded"))
}

// The /help list mentions /save (API-KNU-002).
func TestHelpListsSave(t *testing.T) {
	api := newFakeAPI(t)
	startChannel(t, api, newFakeBackend())

	api.textMsg(ownerChat, "private", ownerID, "/help", nil)

	api.waitCall("sendMessage", textHas("/save"))
}

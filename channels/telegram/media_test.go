package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/attach"
)

// waitFor polls cond until it holds or a short deadline passes (REQ-ATT-005).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A photo with a caption is one turn: the caption is the text and the saved
// file is the attachment (REQ-ATT-005).
func TestPhotoWithCaptionBecomesOneTurn(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	workspace := t.TempDir()
	backend.workspace = workspace
	api.addFile("p1", "photos/p1.jpg", []byte("img"))
	startChannel(t, api, backend)

	api.photo(ownerChat, "private", ownerID, "p1", "look", "")
	api.waitCall("editMessageText", nil)

	waitFor(t, "one turn", func() bool { return len(backend.promptList()) == 1 })
	if got := backend.promptList(); got[0] != "look" {
		t.Fatalf("prompts = %v", got)
	}
	atts := backend.runAttachmentList()
	if len(atts) != 1 || len(atts[0]) != 1 || !strings.HasPrefix(atts[0][0], "attachments/") {
		t.Fatalf("attachments = %v", atts)
	}
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(atts[0][0])))
	if err != nil || string(data) != "img" {
		t.Fatalf("saved file = %q, %v", data, err)
	}
}

// AC-ATT-006: an album of 4 photos with one caption is one turn with 4 files in
// order.
func TestAlbumBecomesOneTurn(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("p%d", i)
		api.addFile(id, "photos/"+id+".jpg", []byte("img"))
	}
	startChannel(t, api, backend)

	api.photo(ownerChat, "private", ownerID, "p1", "bandingkan", "g1")
	api.photo(ownerChat, "private", ownerID, "p2", "", "g1")
	api.photo(ownerChat, "private", ownerID, "p3", "", "g1")
	api.photo(ownerChat, "private", ownerID, "p4", "", "g1")

	waitFor(t, "album turn", func() bool { return len(backend.promptList()) == 1 })
	if got := backend.promptList(); got[0] != "bandingkan" {
		t.Fatalf("prompts = %v", got)
	}
	atts := backend.runAttachmentList()
	if len(atts) != 1 || len(atts[0]) != 4 {
		t.Fatalf("attachments = %v", atts)
	}
	for _, p := range atts[0] {
		if !strings.HasPrefix(p, "attachments/") {
			t.Fatalf("path = %q", p)
		}
	}
}

// An album over attach.MaxFiles keeps the first 10 parts and tells the owner
// (REQ-ATT-005).
func TestAlbumOverLimitKeepsFirstTen(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	for i := 1; i <= 11; i++ {
		id := fmt.Sprintf("p%d", i)
		api.addFile(id, "photos/"+id+".jpg", []byte("img"))
	}
	startChannel(t, api, backend)

	api.photo(ownerChat, "private", ownerID, "p1", "many", "g1")
	for i := 2; i <= 11; i++ {
		api.photo(ownerChat, "private", ownerID, fmt.Sprintf("p%d", i), "", "g1")
	}

	api.waitCall("sendMessage", textIs(albumLimitReply))
	waitFor(t, "album turn", func() bool { return len(backend.promptList()) == 1 })
	atts := backend.runAttachmentList()
	if len(atts) != 1 || len(atts[0]) != 10 {
		t.Fatalf("attachments = %v", atts)
	}
}

// A document keeps its file name under attachments/<date>/ (REQ-ATT-005).
func TestDocumentSavedWithFileName(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	api.addFile("d1", "docs/report.pdf", []byte("%PDF-1.4 report"))
	startChannel(t, api, backend)

	api.document(ownerChat, "private", ownerID, "d1", "report.pdf", 15)
	waitFor(t, "document turn", func() bool { return len(backend.promptList()) == 1 })
	atts := backend.runAttachmentList()
	if len(atts) != 1 || len(atts[0]) != 1 ||
		!strings.HasPrefix(atts[0][0], "attachments/") || !strings.HasSuffix(atts[0][0], "/report.pdf") {
		t.Fatalf("attachments = %v", atts)
	}
}

// A document declared over the per-file limit is refused without a download
// (REQ-ATT-001, REQ-ATT-005).
func TestDocumentOverLimit(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	startChannel(t, api, backend)

	api.document(ownerChat, "private", ownerID, "big1", "big.pdf", 25<<20)
	api.waitCall("sendMessage", textIs("✗ big.pdf is larger than 20 MB, the limit per file."))

	if n := api.count("getFile", nil); n != 0 {
		t.Fatalf("getFile calls = %d", n)
	}
	if n := api.downloadCount(); n != 0 {
		t.Fatalf("downloads = %d", n)
	}
	if got := backend.promptList(); len(got) != 0 {
		t.Fatalf("prompts = %v", got)
	}

	api.addFile("d2", "docs/big.bin", bytes.Repeat([]byte("a"), 100))
	client := NewClient(api.srv.URL, "TOKEN123")
	var buf bytes.Buffer
	if err := client.Download(context.Background(), "docs/big.bin", &buf, 10); !errors.Is(err, attach.ErrTooLarge) {
		t.Fatalf("download err = %v", err)
	}
}

// Voice gets the unsupported-media reply (REQ-ATT-005).
func TestVoiceGetsUnsupportedReply(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	api.push(Update{Message: &Message{MessageID: 9, From: &User{ID: ownerID}, Chat: Chat{ID: ownerChat, Type: "private"}, Voice: map[string]any{"file_id": "v"}}})
	api.waitCall("sendMessage", textIs(unsupportedMediaReply))
	if got := backend.promptList(); len(got) != 0 {
		t.Fatalf("prompts = %v", got)
	}
}

// A photo sent while a turn runs is queued with its attachment and gets the 👀
// reaction (REQ-ATT-005).
func TestPhotoWhileTurnRunningGoesToInbox(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		select {
		case <-hold:
			return TurnResult{Text: "done", StopReason: "end_turn"}, nil
		case <-ctx.Done():
			return TurnResult{StopReason: "cancelled"}, nil
		}
	}
	api.addFile("p1", "photos/p1.jpg", []byte("img"))
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "long")
	api.waitCall("sendMessage", textIs("…"))
	api.photo(ownerChat, "private", ownerID, "p1", "look", "")
	api.waitCall("setMessageReaction", nil)

	waitFor(t, "inbox send", func() bool { return len(backend.sentAttachmentList()) == 1 })
	sent := backend.sentAttachmentList()
	if len(sent[0]) != 1 || !strings.HasPrefix(sent[0][0], "attachments/") {
		t.Fatalf("sent attachments = %v", sent)
	}
	backend.mu.Lock()
	texts := append([]string(nil), backend.sent...)
	backend.mu.Unlock()
	if len(texts) != 1 || texts[0] != "look" {
		t.Fatalf("sent = %v", texts)
	}
}

// A group photo with no mention and no reply is dropped silently (REQ-ATT-005).
func TestGroupPhotoWithoutMentionIgnored(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	api.addFile("p1", "photos/p1.jpg", []byte("img"))
	startChannel(t, api, backend)

	api.photo(groupChat, "supergroup", memberID, "p1", "cat photo", "")
	api.text(groupChat, "supergroup", memberID, "@lana_bot hi")
	api.waitCall("editMessageText", nil)

	waitFor(t, "text turn", func() bool { return len(backend.promptList()) == 1 })
	if got := backend.promptList(); len(got) != 1 || got[0] != "Budi: hi" {
		t.Fatalf("prompts = %v", got)
	}
	if n := api.count("getFile", nil); n != 0 {
		t.Fatalf("getFile calls = %d", n)
	}
	if n := api.downloadCount(); n != 0 {
		t.Fatalf("downloads = %d", n)
	}
}

// A caption that starts with "/" is plain text, never a command (REQ-ATT-005).
func TestPrivatePhotoCaptionNotCommand(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	api.addFile("p1", "photos/p1.jpg", []byte("img"))
	startChannel(t, api, backend)

	api.photo(ownerChat, "private", ownerID, "p1", "/cancel", "")
	api.waitCall("editMessageText", nil)

	waitFor(t, "turn", func() bool { return len(backend.promptList()) == 1 })
	if got := backend.promptList(); got[0] != "/cancel" {
		t.Fatalf("prompts = %v", got)
	}
}

// REQ-ATT-005: a part of an album that is queued behind a busy worker stays in
// its album. The worker is blocked when the flush timer fires, so the flush and
// the queued part are both ready; whichever the select picks, the part must
// join the album and start no second turn.
func TestAlbumPartQueuedBehindBusyWorkerStaysInAlbum(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.workspace = t.TempDir()
	api.addFile("p1", "photos/p1.jpg", []byte("img"))
	api.addFile("p2", "photos/p2.jpg", []byte("img"))

	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	backend.memoryHook = func() {
		once.Do(func() { close(entered) })
		<-gate
	}

	ch, _, _ := startChannel(t, api, backend)
	// Registered after startChannel so it runs before the channel stop cleanup;
	// a worker left blocked in Memory fails that stop.
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)

	api.photo(ownerChat, "private", ownerID, "p1", "bandingkan", "g1")
	api.text(ownerChat, "private", ownerID, "/memory")

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not enter Memory")
	}

	api.photo(ownerChat, "private", ownerID, "p2", "", "g1")

	ch.mu.Lock()
	w := ch.chats[ownerChat]
	ch.mu.Unlock()
	if w == nil {
		t.Fatal("no worker for chat")
	}
	waitFor(t, "queued part behind a fired flush", func() bool {
		return len(w.updates) == 1 && len(w.albumCh) == 1
	})

	release()

	waitFor(t, "one turn", func() bool { return len(backend.promptList()) == 1 })
	if got := backend.promptList(); got[0] != "bandingkan" {
		t.Fatalf("prompts = %v", got)
	}
	atts := backend.runAttachmentList()
	if len(atts) != 1 || len(atts[0]) != 2 {
		t.Fatalf("attachments = %v", atts)
	}
	for _, p := range atts[0] {
		if !strings.HasPrefix(p, "attachments/") {
			t.Fatalf("path = %q", p)
		}
	}

	time.Sleep(3 * ch.AlbumWait)
	if got := backend.promptList(); len(got) != 1 {
		t.Fatalf("prompts after wait = %v", got)
	}
	if sent := backend.sentAttachmentList(); len(sent) != 0 {
		t.Fatalf("sent attachments = %v", sent)
	}
}

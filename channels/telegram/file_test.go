package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// uploadCall captures one multipart upload request.
type uploadCall struct {
	method   string
	path     string
	chatID   string
	caption  string
	field    string
	filename string
	content  string
}

// parseUpload reads the method, path, text fields, and single file part of a
// multipart request.
func parseUpload(t *testing.T, r *http.Request) uploadCall {
	t.Helper()
	call := uploadCall{method: r.Method, path: r.URL.Path}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("parse multipart: %v", err)
		return call
	}
	call.chatID = r.FormValue("chat_id")
	call.caption = r.FormValue("caption")
	for _, field := range []string{"photo", "document"} {
		file, header, err := r.FormFile(field)
		if err != nil {
			continue
		}
		call.field = field
		call.filename = header.Filename
		data, _ := io.ReadAll(file)
		_ = file.Close()
		call.content = string(data)
		break
	}
	return call
}

// replyMessage writes a successful sendMessage-style result.
func replyMessage(w http.ResponseWriter, id int64) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": id}})
}

// REQ-AGF-004: SendPhoto and SendDocument post a multipart form to the right
// Bot API method with chat_id, caption, the file part, and the file name.
func TestClientFileUploadMultipart(t *testing.T) {
	const token = "123456:test-token"
	cases := []struct{ name, method, field string }{
		{"sendPhoto", "sendPhoto", "photo"},
		{"sendDocument", "sendDocument", "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var got uploadCall
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := parseUpload(t, r)
				mu.Lock()
				got = call
				mu.Unlock()
				replyMessage(w, 42)
			}))
			defer server.Close()

			client := NewClient(server.URL, token)
			var (
				id  int64
				err error
			)
			if tc.method == "sendPhoto" {
				id, err = client.SendPhoto(context.Background(), 111, "cat.png", strings.NewReader("image-bytes"), "look")
			} else {
				id, err = client.SendDocument(context.Background(), 111, "cat.png", strings.NewReader("image-bytes"), "look")
			}
			if err != nil {
				t.Fatal(err)
			}
			if id != 42 {
				t.Fatalf("message id=%d, want 42", id)
			}
			mu.Lock()
			call := got
			mu.Unlock()
			if call.method != http.MethodPost {
				t.Fatalf("method=%q, want POST", call.method)
			}
			if want := "/bot" + token + "/" + tc.method; call.path != want {
				t.Fatalf("path=%q, want %q", call.path, want)
			}
			if call.chatID != "111" || call.caption != "look" {
				t.Fatalf("chat_id=%q caption=%q", call.chatID, call.caption)
			}
			if call.field != tc.field || call.filename != "cat.png" || call.content != "image-bytes" {
				t.Fatalf("file part=%+v, want field %q name cat.png content image-bytes", call, tc.field)
			}
		})
	}
}

// REQ-AGF-004: a 429 retry re-sends the multipart body, so the second attempt
// carries the file bytes again.
func TestClientUploadRetriesOn429(t *testing.T) {
	const token = "123456:test-token"
	var mu sync.Mutex
	var calls []uploadCall
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempt++
		n := attempt
		calls = append(calls, parseUpload(t, r))
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
			return
		}
		replyMessage(w, 7)
	}))
	defer server.Close()

	client := NewClient(server.URL, token)
	client.sleep = func(context.Context, time.Duration) error { return nil }
	id, err := client.SendDocument(context.Background(), 111, "report.md", strings.NewReader("file-body"), "cap")
	if err != nil {
		t.Fatal(err)
	}
	if id != 7 {
		t.Fatalf("message id=%d, want 7", id)
	}
	mu.Lock()
	got := append([]uploadCall(nil), calls...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("requests=%d, want 2", len(got))
	}
	for i, call := range got {
		if call.filename != "report.md" || call.content != "file-body" {
			t.Fatalf("attempt %d body=%+v, want the file re-sent", i, call)
		}
	}
}

// newFileTestChannel returns a channel whose api points at url and a telegram
// session id bound to chat 555 through its address.
func newFileTestChannel(t *testing.T, url string) (*Channel, string) {
	t.Helper()
	backend := newFakeBackend()
	ch := New(Config{Enabled: true, OwnerIDs: map[int64]bool{111: true}}, "TOKEN123", backend)
	ch.api = NewClient(url, "TOKEN123")
	info, err := backend.NewSession(context.Background(), "555", "helper")
	if err != nil {
		t.Fatal(err)
	}
	return ch, info.ID
}

// REQ-AGF-004: an image of at most 10 MB goes with sendPhoto, everything else
// with sendDocument.
func TestChannelSendFileChoosesMethod(t *testing.T) {
	cases := []struct {
		name   string
		media  string
		size   int64
		method string
		field  string
	}{
		{"png photo", "image/png", 1024, "sendPhoto", "photo"},
		{"markdown document", "text/markdown", 1024, "sendDocument", "document"},
		{"png over 10 MB", "image/png", (10 << 20) + 1, "sendDocument", "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var got uploadCall
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := parseUpload(t, r)
				mu.Lock()
				got = call
				mu.Unlock()
				replyMessage(w, 5)
			}))
			defer server.Close()

			ch, sessionID := newFileTestChannel(t, server.URL)
			path := filepath.Join(t.TempDir(), "file.bin")
			if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
				t.Fatal(err)
			}
			err := ch.SendFile(context.Background(), sessionID, core.SentFile{
				AbsPath: path, Name: "file.bin", MediaType: tc.media, Size: tc.size,
			})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			call := got
			mu.Unlock()
			if call.method != http.MethodPost || !strings.HasSuffix(call.path, "/"+tc.method) {
				t.Fatalf("request method=%q path=%q, want POST %s", call.method, call.path, tc.method)
			}
			if call.field != tc.field {
				t.Fatalf("file field=%q, want %q", call.field, tc.field)
			}
			if call.filename != "file.bin" || call.chatID != "555" {
				t.Fatalf("filename=%q chat_id=%q", call.filename, call.chatID)
			}
		})
	}
}

// REQ-AGF-004: the caption is trimmed to the Bot API limit of 1024 runes.
func TestChannelSendFileTrimsCaption(t *testing.T) {
	var mu sync.Mutex
	var got uploadCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := parseUpload(t, r)
		mu.Lock()
		got = call
		mu.Unlock()
		replyMessage(w, 5)
	}))
	defer server.Close()

	ch, sessionID := newFileTestChannel(t, server.URL)
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ch.SendFile(context.Background(), sessionID, core.SentFile{
		AbsPath: path, Name: "file.bin", MediaType: "text/markdown", Size: 7,
		Caption: strings.Repeat("x", 2000),
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	call := got
	mu.Unlock()
	if call.caption != strings.Repeat("x", captionLimit) {
		t.Fatalf("caption length=%d, want %d", len([]rune(call.caption)), captionLimit)
	}
}

// REQ-AGF-004: multipart uploads use a longer timeout than JSON calls, so a
// file up to 20 MB may finish on a slow link while JSON calls keep 60s.
func TestClientUploadTimeout(t *testing.T) {
	client := NewClient("https://api.telegram.org", "token")
	if client.http.Timeout != 60*time.Second {
		t.Fatalf("json timeout=%v, want 60s", client.http.Timeout)
	}
	if client.upload.Timeout != 2*time.Minute {
		t.Fatalf("upload timeout=%v, want 2m", client.upload.Timeout)
	}
}

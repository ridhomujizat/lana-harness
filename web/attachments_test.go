package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
)

// multipartFiles builds a multipart/form-data body with one files part per
// entry. An entry with an empty name is written as a field with no file name.
func multipartFiles(t *testing.T, files [][2]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for _, file := range files {
		if file[0] == "" {
			if err := writer.WriteField("files", file[1]); err != nil {
				t.Fatal(err)
			}
			continue
		}
		part, err := writer.CreateFormFile("files", file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), writer.FormDataContentType()
}

func uploadRequest(id string, body []byte, contentType string, cookie *http.Cookie, csrf string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/attachments", bytes.NewReader(body))
	req.Host = authTestAddr
	req.Header.Set("Content-Type", contentType)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

// TestUploadAttachmentsCoversAPIATT001 covers AC-ATT-007 at the API: two files
// give 201 with two entries and the backend received both.
func TestUploadAttachmentsCoversAPIATT001(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	body, contentType := multipartFiles(t, [][2]string{
		{"a.png", "png-bytes"},
		{"b.pdf", "pdf-bytes"},
	})
	rr := serveAuth(s, uploadRequest(info.ID, body, contentType, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var response uploadAttachmentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(response.Attachments) != 2 {
		t.Fatalf("attachments = %+v", response.Attachments)
	}
	first := response.Attachments[0]
	if first.Path != "attachments/2026-11-01/a.png" || first.Name != "a.png" ||
		first.MediaType != "image/png" || first.Size != int64(len("png-bytes")) {
		t.Fatalf("first = %+v", first)
	}
	second := response.Attachments[1]
	if second.Name != "b.pdf" || second.MediaType != "application/pdf" {
		t.Fatalf("second = %+v", second)
	}
	backend.mu.Lock()
	saved := append([]attach.Info(nil), backend.savedAttachments...)
	backend.mu.Unlock()
	if len(saved) != 2 || saved[0].Name != "a.png" || saved[1].Name != "b.pdf" {
		t.Fatalf("saved = %+v", saved)
	}
}

// TestUploadAttachmentsRejectsTooManyFiles covers API-ATT-001: an 11th file
// part is 400 too_many_files before it is saved.
func TestUploadAttachmentsRejectsTooManyFiles(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	files := make([][2]string, attach.MaxFiles+1)
	for i := range files {
		files[i] = [2]string{fmt.Sprintf("f%d.txt", i), "x"}
	}
	body, contentType := multipartFiles(t, files)
	rr := serveAuth(s, uploadRequest(info.ID, body, contentType, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "too_many_files" {
		t.Fatalf("code = %q, want too_many_files", envelope.Error.Code)
	}
	backend.mu.Lock()
	count := len(backend.savedAttachments)
	backend.mu.Unlock()
	if count != attach.MaxFiles {
		t.Fatalf("saved %d files, want %d", count, attach.MaxFiles)
	}
}

// TestUploadAttachmentsFileTooLarge covers API-ATT-001: a file over the limit
// is 413 file_too_large and the message names the file.
func TestUploadAttachmentsFileTooLarge(t *testing.T) {
	backend, info := newTurnTestBackend()
	backend.attachmentErr["big.bin"] = fmt.Errorf("big.bin: %w", attach.ErrTooLarge)
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	body, contentType := multipartFiles(t, [][2]string{{"big.bin", "x"}})
	rr := serveAuth(s, uploadRequest(info.ID, body, contentType, cookie, csrf))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()})
	if envelope.Error.Code != "file_too_large" {
		t.Fatalf("code = %q, want file_too_large", envelope.Error.Code)
	}
	if !strings.Contains(envelope.Error.Message, "big.bin") || !strings.Contains(envelope.Error.Message, "20 MB") {
		t.Fatalf("message = %q", envelope.Error.Message)
	}
}

// TestUploadAttachmentsInvalidRequest covers API-ATT-001: a part named anything
// but files, and a request with no files part, are 400 invalid_request.
func TestUploadAttachmentsInvalidRequest(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("other", "x"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, uploadRequest(info.ID, buf.Bytes(), writer.FormDataContentType(), cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("wrong name status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "invalid_request" {
		t.Fatalf("code = %q, want invalid_request", envelope.Error.Code)
	}

	empty, contentType := multipartFiles(t, nil)
	rr = serveAuth(s, uploadRequest(info.ID, empty, contentType, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestSendMessageWithAttachmentsCoversAPIATT002 covers API-ATT-002: a send with
// a valid attachment gives 202 and the backend got the path.
func TestSendMessageWithAttachmentsCoversAPIATT002(t *testing.T) {
	backend, info := newTurnTestBackend()
	backend.attachmentPaths["attachments/2026-11-01/a.jpg"] = "/tmp/a.jpg"
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	body := `{"text":"cek ini","attachments":["attachments/2026-11-01/a.jpg"]}`
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", body, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	sent := append([]string(nil), backend.sentAttachments[info.ID]...)
	backend.mu.Unlock()
	if len(sent) != 1 || sent[0] != "attachments/2026-11-01/a.jpg" {
		t.Fatalf("sent = %v", sent)
	}
}

// TestSendMessageAttachmentWithoutText covers API-ATT-002: the text may be
// absent when a valid attachment is present.
func TestSendMessageAttachmentWithoutText(t *testing.T) {
	backend, info := newTurnTestBackend()
	backend.attachmentPaths["attachments/2026-11-01/a.jpg"] = "/tmp/a.jpg"
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"attachments":["attachments/2026-11-01/a.jpg"]}`, cookie, csrf))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	backend.mu.Lock()
	sent := append([]string(nil), backend.sentAttachments[info.ID]...)
	backend.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("sent = %v", sent)
	}
}

// TestSendMessageInvalidAttachment covers API-ATT-002: a rejected path is 400
// invalid_attachment and the message names the path.
func TestSendMessageInvalidAttachment(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", `{"text":"hi","attachments":["../x"]}`, cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()})
	if envelope.Error.Code != "invalid_attachment" {
		t.Fatalf("code = %q, want invalid_attachment", envelope.Error.Code)
	}
	if !strings.Contains(envelope.Error.Message, "../x") {
		t.Fatalf("message = %q, want the path", envelope.Error.Message)
	}
}

// TestSendMessageTooManyAttachments covers API-ATT-002: more than 10 paths is
// 400 too_many_files.
func TestSendMessageTooManyAttachments(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, csrf := newLoggedInSessionServer(t, backend)

	paths := make([]string, attach.MaxFiles+1)
	for i := range paths {
		paths[i] = fmt.Sprintf("attachments/2026-11-01/f%d.txt", i)
	}
	payload, err := json.Marshal(map[string]any{"text": "hi", "attachments": paths})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/sessions/"+info.ID+"/messages", string(payload), cookie, csrf))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "too_many_files" {
		t.Fatalf("code = %q, want too_many_files", envelope.Error.Code)
	}
}

// TestServeAttachmentCoversAPIATT003 covers API-ATT-003: a present file is
// served with its media type and a private cache header.
func TestServeAttachmentCoversAPIATT003(t *testing.T) {
	backend, info := newTurnTestBackend()
	abs := filepath.Join(t.TempDir(), "a.png")
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	if err := os.WriteFile(abs, png, 0o600); err != nil {
		t.Fatal(err)
	}
	backend.attachmentPaths["attachments/2026-11-01/a.png"] = abs
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/attachments/2026-11-01/a.png", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "private, max-age=3600" {
		t.Fatalf("cache-control = %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != "inline" {
		t.Fatalf("content-disposition = %q, want inline", got)
	}
	if !bytes.Equal(rr.Body.Bytes(), png) {
		t.Fatalf("body = %v", rr.Body.Bytes())
	}
}

// TestServeAttachmentNotFound covers API-ATT-003: a missing file and an unknown
// session are 404.
func TestServeAttachmentNotFound(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/attachments/2026-11-01/missing.png", "", cookie, ""))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/missing/attachments/2026-11-01/a.png", "", cookie, ""))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", envelope.Error.Code)
	}
}

// TestServeAttachmentRejectsOutside covers API-ATT-003: a path outside
// attachments/ is 404. The handler is called directly because ServeMux cleans
// .. segments before it runs.
func TestServeAttachmentRejectsOutside(t *testing.T) {
	backend, info := newTurnTestBackend()
	s, _, _ := newLoggedInSessionServer(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/attachments/x", nil)
	req.SetPathValue("id", info.ID)
	req.SetPathValue("path", "../secrets")
	rr := httptest.NewRecorder()
	s.handleGetAttachment(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestPublicEventPayloadStripsImageData covers API-ATT-004: image data is
// dropped from an owner message and from a tool result, while path and
// media_type stay; a text-only payload is byte-identical.
func TestPublicEventPayloadStripsImageData(t *testing.T) {
	user := json.RawMessage(`{"v":1,"content":[{"type":"text","text":"look"},{"type":"image","media_type":"image/jpeg","path":"attachments/2026-11-01/a.jpg","data":"QUFB"}]}`)
	out := publicEventPayload(core.KindUserMessage, user)
	if strings.Contains(string(out), `"data"`) {
		t.Fatalf("user payload kept data: %s", out)
	}
	if !strings.Contains(string(out), "attachments/2026-11-01/a.jpg") || !strings.Contains(string(out), "image/jpeg") {
		t.Fatalf("user payload lost path or media_type: %s", out)
	}
	if !strings.Contains(string(out), "look") {
		t.Fatalf("user payload lost text: %s", out)
	}

	tool := json.RawMessage(`{"v":1,"call_id":"c1","status":"ok","output":"done","images":[{"type":"image","media_type":"image/png","path":"attachments/2026-11-01/b.png","data":"QUFB"}]}`)
	out = publicEventPayload(core.KindToolResult, tool)
	if strings.Contains(string(out), `"data"`) {
		t.Fatalf("tool payload kept data: %s", out)
	}
	if !strings.Contains(string(out), "attachments/2026-11-01/b.png") || !strings.Contains(string(out), "image/png") {
		t.Fatalf("tool payload lost path or media_type: %s", out)
	}

	text := json.RawMessage(`{"v":1,"content":[{"type":"text","text":"hi"}]}`)
	if got := publicEventPayload(core.KindUserMessage, text); string(got) != string(text) {
		t.Fatalf("text-only payload changed: %s", got)
	}
	other := json.RawMessage(`{"v":1,"data":"x"}`)
	if got := publicEventPayload(core.KindSessionCreated, other); string(got) != string(other) {
		t.Fatalf("other kind changed: %s", got)
	}
}

// TestPublicEventPayloadFileSentUnchanged covers API-AGF-001: a file/sent
// payload carries no image data and is served byte-identical.
func TestPublicEventPayloadFileSentUnchanged(t *testing.T) {
	payload := json.RawMessage(`{"v":1,"call_id":"toolu_1","path":"attachments/2026-11-11/a.png","name":"a.png","media_type":"image/png","size":319488}`)
	if got := publicEventPayload(core.KindFileSent, payload); string(got) != string(payload) {
		t.Fatalf("file/sent payload changed: %s", got)
	}
}

// TestEventsEndpointOmitsImageData covers API-ATT-004 at the events endpoint:
// the body contains no data key but keeps the path.
func TestEventsEndpointOmitsImageData(t *testing.T) {
	backend, info := newTurnTestBackend()
	backend.appendEvent(info.ID, core.SessionEvent{
		Kind:    core.KindUserMessage,
		Payload: json.RawMessage(`{"v":1,"content":[{"type":"image","media_type":"image/jpeg","path":"attachments/2026-11-01/a.jpg","data":"QUFB"}]}`),
	})
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/sessions/"+info.ID+"/events", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"data"`) {
		t.Fatalf("events body kept data: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "attachments/2026-11-01/a.jpg") {
		t.Fatalf("events body lost path: %s", rr.Body.String())
	}
}

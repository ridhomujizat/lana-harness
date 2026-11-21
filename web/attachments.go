package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
)

// attachmentBodyLimit is the largest accepted upload body: 10 files of 20 MB
// plus 1 MB of multipart framing and other fields (API-ATT-001).
const attachmentBodyLimit = (attach.MaxFiles * attach.MaxFileBytes) + (1 << 20)

// attachmentSniffLen is how many bytes are read to detect a file's type when
// the extension is not known.
const attachmentSniffLen = 512

// registerAttachmentRoutes registers API-ATT-001 and API-ATT-003 through Handle
// so the owner session, CSRF, and host checks apply.
func (s *Server) registerAttachmentRoutes() {
	s.Handle("POST /v1/sessions/{id}/attachments", http.HandlerFunc(s.handleUploadAttachments))
	s.Handle("GET /v1/sessions/{id}/attachments/{path...}", http.HandlerFunc(s.handleGetAttachment))
}

type attachmentJSON struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

type uploadAttachmentsResponse struct {
	Attachments []attachmentJSON `json:"attachments"`
}

// handleUploadAttachments streams each files part into the session workspace
// (API-ATT-001, REQ-ATT-001, REQ-ATT-006). It never buffers a whole file.
func (s *Server) handleUploadAttachments(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, attachmentBodyLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be multipart/form-data with files parts")
		return
	}

	uploaded := make([]attachmentJSON, 0)
	sawFiles := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.writeAttachmentReadError(w, "", err)
			return
		}
		if part.FormName() != "files" {
			_ = part.Close()
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "each file part must be named files")
			return
		}
		sawFiles = true
		if len(uploaded) >= attach.MaxFiles {
			_ = part.Close()
			writeAPIError(w, http.StatusBadRequest, "too_many_files", fmt.Sprintf("a message may carry at most %d files", attach.MaxFiles))
			return
		}
		name := part.FileName()
		if name == "" {
			name = "file"
		}
		info, err := backend.SaveAttachment(r.Context(), id, name, part)
		_ = part.Close()
		if err != nil {
			s.writeAttachmentSaveError(w, id, name, err)
			return
		}
		uploaded = append(uploaded, attachmentJSON{
			Path:      info.Path,
			Name:      info.Name,
			MediaType: info.MediaType,
			Size:      info.Size,
		})
	}
	if !sawFiles {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "at least one files part is required")
		return
	}
	writeJSON(w, http.StatusCreated, uploadAttachmentsResponse{Attachments: uploaded})
}

// writeAttachmentReadError maps a multipart read failure. A body over the
// MaxBytesReader limit is 413 file_too_large (API-ATT-001).
func (s *Server) writeAttachmentReadError(w http.ResponseWriter, name string, err error) {
	if isBodyTooLarge(err) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", fileTooLargeMessage(name))
		return
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be multipart/form-data with files parts")
}

// writeAttachmentSaveError maps a SaveAttachment failure.
func (s *Server) writeAttachmentSaveError(w http.ResponseWriter, id, name string, err error) {
	switch {
	case isBodyTooLarge(err), errors.Is(err, attach.ErrTooLarge):
		writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", fileTooLargeMessage(name))
	case errors.Is(err, ErrSessionNotFound):
		s.writeBackendError(w, "upload attachment", err)
	default:
		s.writeBackendError(w, "upload attachment", err)
	}
}

func isBodyTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

func fileTooLargeMessage(name string) string {
	limitMB := attach.MaxFileBytes >> 20
	if name == "" {
		return fmt.Sprintf("the upload is over the %d MB limit", limitMB)
	}
	return fmt.Sprintf("%s is over the %d MB limit", name, limitMB)
}

// handleGetAttachment serves one file under the session workspace
// (API-ATT-003, REQ-ATT-006). The {path...} value is the remainder after the
// attachments/ prefix.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	rel := attach.Dir + "/" + r.PathValue("path")
	abs, err := backend.AttachmentPath(r.Context(), id, rel)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			s.writeBackendError(w, "get attachment", err)
			return
		}
		writeAPIError(w, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	file, err := os.Open(abs)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeAPIError(w, http.StatusNotFound, "not_found", "attachment not found")
		return
	}

	mediaType := attachmentMediaType(file, info.Name())
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if inlineAttachment(mediaType) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", "attachment")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// attachmentMediaType picks the content type from the extension, falling back
// to sniffing the first bytes of the file.
func attachmentMediaType(file *os.File, name string) string {
	if mediaType := mime.TypeByExtension(filepath.Ext(name)); mediaType != "" {
		return mediaType
	}
	head := make([]byte, attachmentSniffLen)
	n, err := io.ReadFull(file, head)
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		return "application/octet-stream"
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "application/octet-stream"
	}
	if n == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(head[:n])
}

// inlineAttachment reports whether a file type may render in place. Everything
// else is a download, so HTML or SVG never runs on the dashboard origin.
func inlineAttachment(mediaType string) bool {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	switch strings.TrimSpace(mediaType) {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "application/pdf":
		return true
	default:
		return false
	}
}

// publicEventPayload returns the event payload as served to the web UI. Image
// bytes are dropped from an owner message and from a tool result's images, while
// path and media_type stay (API-ATT-004, REQ-ATT-006). Every other kind is
// returned unchanged, and a payload with nothing to strip is returned as the
// original slice.
func publicEventPayload(kind string, payload json.RawMessage) json.RawMessage {
	switch kind {
	case core.KindUserMessage:
		var message core.UserMessage
		if len(payload) == 0 || json.Unmarshal(payload, &message) != nil {
			return payload
		}
		if !stripImageData(message.Content) {
			return payload
		}
		if out, err := json.Marshal(message); err == nil {
			return out
		}
		return payload
	case core.KindToolResult:
		var result core.ToolResultPayload
		if len(payload) == 0 || json.Unmarshal(payload, &result) != nil {
			return payload
		}
		if !stripImageData(result.Images) {
			return payload
		}
		if out, err := json.Marshal(result); err == nil {
			return out
		}
		return payload
	default:
		return payload
	}
}

// stripImageData clears the bytes of every image block. It reports whether it
// changed anything.
func stripImageData(blocks []core.ContentBlock) bool {
	stripped := false
	for i := range blocks {
		if blocks[i].Kind == core.BlockImage && len(blocks[i].Data) > 0 {
			blocks[i].Data = nil
			stripped = true
		}
	}
	return stripped
}

// chatOutputLimit is the number of runes kept from a tool result's output and
// from a string value of a tool call's input in the chat view. It matches the
// slice the web UI already shows without opening a step (REQ-UI chat).
const chatOutputLimit = 4000

// chatInputMaxDepth caps the walk of a tool call's input so a deeply nested
// argument cannot make the trim expensive.
const chatInputMaxDepth = 6

// redactedThinkingBlock is the content block type of a redacted thinking block.
// The stored form keeps the encrypted data in the signature behind the prefix;
// an adapter may also serve it as a data field, so both are handled.
const redactedThinkingBlock = "redacted_thinking"

// chatEventPayload trims an event payload for the opt-in chat view (view=chat).
// It runs after publicEventPayload, so image bytes are already gone. Every
// other kind, and a payload that cannot be parsed, is returned unchanged.
func chatEventPayload(kind string, payload json.RawMessage) json.RawMessage {
	switch kind {
	case core.KindSystemMessage:
		return chatSystemMessagePayload(payload)
	case core.KindAssistantMessage:
		return chatAssistantPayload(payload)
	case core.KindStepEnd:
		return chatStepEndPayload(payload)
	case core.KindToolResult:
		return chatToolResultPayload(payload)
	case core.KindToolCall:
		return chatToolCallPayload(payload)
	default:
		return payload
	}
}

// chatSystemMessagePayload keeps the seq of a system message but drops the
// prompt text, which the chat never shows: only the subtype (when it had one)
// and the omitted flag remain.
func chatSystemMessagePayload(payload json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &fields) != nil {
		return payload
	}
	out := map[string]json.RawMessage{"omitted": json.RawMessage(`true`)}
	if subtype, ok := fields["subtype"]; ok {
		out["subtype"] = subtype
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return payload
	}
	return encoded
}

// chatAssistantPayload drops the provider signature of a thinking block and the
// encrypted data of a redacted thinking block. The thinking text stays; every
// other field of the message and of each block is kept.
func chatAssistantPayload(payload json.RawMessage) json.RawMessage {
	var message map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &message) != nil {
		return payload
	}
	rawContent, ok := message["content"]
	if !ok {
		return payload
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(rawContent, &content) != nil {
		return payload
	}
	changed := false
	for _, block := range content {
		var blockKind string
		_ = json.Unmarshal(block["type"], &blockKind)
		switch blockKind {
		case string(core.BlockThinking):
			if _, ok := block["signature"]; ok {
				delete(block, "signature")
				changed = true
			}
		case redactedThinkingBlock:
			if _, ok := block["data"]; ok {
				delete(block, "data")
				changed = true
			}
		}
	}
	if !changed {
		return payload
	}
	encodedContent, err := json.Marshal(content)
	if err != nil {
		return payload
	}
	message["content"] = encodedContent
	encoded, err := json.Marshal(message)
	if err != nil {
		return payload
	}
	return encoded
}

// chatStepEndPayload keeps only the step number; the chat reads no other field
// of step/end (the context meter uses the session usage endpoint).
func chatStepEndPayload(payload json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &fields) != nil {
		return payload
	}
	out := map[string]json.RawMessage{}
	if step, ok := fields["step"]; ok {
		out["step"] = step
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return payload
	}
	return encoded
}

// chatToolResultPayload cuts an output longer than chatOutputLimit runes and
// reports the cut with truncated and output_length. The images and every other
// field stay as publicEventPayload left them.
func chatToolResultPayload(payload json.RawMessage) json.RawMessage {
	var result map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &result) != nil {
		return payload
	}
	rawOutput, ok := result["output"]
	if !ok {
		return payload
	}
	var output string
	if json.Unmarshal(rawOutput, &output) != nil {
		return payload
	}
	runes := []rune(output)
	if len(runes) <= chatOutputLimit {
		return payload
	}
	cut, err := json.Marshal(string(runes[:chatOutputLimit]))
	if err != nil {
		return payload
	}
	length, err := json.Marshal(len(runes))
	if err != nil {
		return payload
	}
	result["output"] = cut
	result["truncated"] = json.RawMessage(`true`)
	result["output_length"] = length
	encoded, err := json.Marshal(result)
	if err != nil {
		return payload
	}
	return encoded
}

// chatToolCallPayload cuts every string value of a tool call's input longer
// than chatOutputLimit runes and reports the cut with input_truncated.
func chatToolCallPayload(payload json.RawMessage) json.RawMessage {
	var message map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &message) != nil {
		return payload
	}
	rawInput, ok := message["input"]
	if !ok {
		return payload
	}
	var input any
	if json.Unmarshal(rawInput, &input) != nil {
		return payload
	}
	if !cutChatStrings(&input, 0) {
		return payload
	}
	encodedInput, err := json.Marshal(input)
	if err != nil {
		return payload
	}
	message["input"] = encodedInput
	message["input_truncated"] = json.RawMessage(`true`)
	encoded, err := json.Marshal(message)
	if err != nil {
		return payload
	}
	return encoded
}

// cutChatStrings walks objects and arrays and cuts every string value longer
// than chatOutputLimit runes. It stops at chatInputMaxDepth and reports whether
// it changed anything.
func cutChatStrings(value *any, depth int) bool {
	switch typed := (*value).(type) {
	case string:
		runes := []rune(typed)
		if len(runes) <= chatOutputLimit {
			return false
		}
		*value = string(runes[:chatOutputLimit])
		return true
	case map[string]any:
		if depth >= chatInputMaxDepth {
			return false
		}
		changed := false
		for key := range typed {
			child := typed[key]
			if cutChatStrings(&child, depth+1) {
				typed[key] = child
				changed = true
			}
		}
		return changed
	case []any:
		if depth >= chatInputMaxDepth {
			return false
		}
		changed := false
		for i := range typed {
			if cutChatStrings(&typed[i], depth+1) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// chatView reports whether the request opted into the trimmed payloads
// (view=chat). Any other value leaves the response unchanged.
func chatView(r *http.Request) bool {
	return r.URL.Query().Get("view") == "chat"
}

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

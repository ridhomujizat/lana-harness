package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/attach"
)

// workbenchDropBodyLimit bounds a drop body: at most MaxFiles files of
// MaxFileBytes plus 1 MB of multipart framing, like an attachment upload.
const workbenchDropBodyLimit = (attach.MaxFiles * attach.MaxFileBytes) + (1 << 20)

// workbenchDropsResponse is the POST /v1/workbench/drops answer: the absolute
// path of every saved file, in the order the parts arrived.
type workbenchDropsResponse struct {
	Paths []string `json:"paths"`
}

// handleWorkbenchDrops serves POST /v1/workbench/drops: a browser drops files
// onto a terminal, this saves them to disk, and the answer carries the
// absolute paths the page then types. It needs no Workbench core, only the
// control lock check, because files go to disk and nothing else.
func (s *Server) handleWorkbenchDrops(w http.ResponseWriter, r *http.Request) {
	if !s.controlAllowed(r) {
		refuseControlLocked(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, workbenchDropBodyLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be multipart/form-data with files parts")
		return
	}

	paths := make([]string, 0, attach.MaxFiles)
	seen := make(map[string]int)
	dir := ""
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
		if len(paths) >= attach.MaxFiles {
			_ = part.Close()
			writeAPIError(w, http.StatusBadRequest, "too_many_files", fmt.Sprintf("a drop may carry at most %d files", attach.MaxFiles))
			return
		}
		if dir == "" {
			dir, err = s.newWorkbenchDropDir()
			if err != nil {
				_ = part.Close()
				s.logger.Error("web: workbench drop dir", "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}
		}
		name := dropFileName(part.FileName(), seen)
		abs, err := writeDropFile(dir, name, part)
		_ = part.Close()
		if err != nil {
			s.writeWorkbenchDropError(w, name, err)
			return
		}
		paths = append(paths, abs)
	}
	if !sawFiles {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "at least one files part is required")
		return
	}
	browser, _ := sessionFromContext(r.Context())
	s.auditWorkbench("drop", "", browser)
	writeJSON(w, http.StatusCreated, workbenchDropsResponse{Paths: paths})
}

// writeWorkbenchDropError maps a drop write failure: an over-limit body or file
// is 413 file_too_large, like an attachment; anything else is a 500.
func (s *Server) writeWorkbenchDropError(w http.ResponseWriter, name string, err error) {
	if isBodyTooLarge(err) || errors.Is(err, attach.ErrTooLarge) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", fileTooLargeMessage(name))
		return
	}
	s.logger.Error("web: workbench drop", "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// newWorkbenchDropDir creates the directory of one drop,
// <home>/workbench/drops/<YYYYMMDD-HHMMSS>-<6 hex>, mode 0700.
func (s *Server) newWorkbenchDropDir() (string, error) {
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(s.auditHome(), "workbench", "drops",
		s.nowTime().Format("20060102-150405")+"-"+hex.EncodeToString(suffix[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// writeDropFile streams one part into dir under name, mode 0600, and returns
// the absolute path. It never buffers the whole file, and stops with
// attach.ErrTooLarge past MaxFileBytes.
func writeDropFile(dir, name string, src io.Reader) (string, error) {
	abs := filepath.Join(dir, name)
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	written, err := io.Copy(f, io.LimitReader(src, attach.MaxFileBytes+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(abs)
		return "", err
	}
	if written > attach.MaxFileBytes {
		_ = f.Close()
		_ = os.Remove(abs)
		return "", attach.ErrTooLarge
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(abs)
		return "", err
	}
	return abs, nil
}

// dropFileName reduces a part's file name to a safe base name, mapping a
// repeated name to "<stem> (n)<ext>" so no two files in one drop collide
// (e.g. "a.png", "a (2).png", "a (3).png").
func dropFileName(raw string, seen map[string]int) string {
	name := dropBaseName(raw)
	if seen[name] == 0 {
		seen[name] = 1
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	n := seen[name]
	for {
		n++
		candidate := fmt.Sprintf("%s (%d)%s", stem, n, ext)
		if seen[candidate] == 0 {
			seen[name] = n
			seen[candidate] = 1
			return candidate
		}
	}
}

// dropBaseName strips path separators, NUL bytes, and leading dots from a
// part's file name, so it can only ever name a file inside the drop directory.
// An empty result becomes "file".
func dropBaseName(raw string) string {
	name := filepath.Base(raw)
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.TrimLeft(name, ".")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "file"
	}
	return name
}

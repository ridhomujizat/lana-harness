package lana

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// PreviewPath is the POST route that resolves and classifies a local file
// link (REQ-WP-001).
const PreviewPath = "/v1/workbench/preview"

// PreviewFile is the answer of POST /v1/workbench/preview (REQ-WP-002): the
// file the link names, how the panel shows it, and the daemon URLs that serve
// it. URL and RawURL are daemon-relative paths, such as
// "/v1/workbench/preview/<token>/<name>?view=1#L12"; the caller makes them
// absolute with BaseURL.
type PreviewFile struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	URL    string `json:"url"`
	RawURL string `json:"raw_url"`
}

// previewBody is the POST body of REQ-WP-001. Dir is the space folder the
// client sends as the last relative-path candidate.
type previewBody struct {
	Session string `json:"session"`
	Link    string `json:"link"`
	Dir     string `json:"dir,omitempty"`
}

// Preview asks the daemon to resolve and classify the file the link names
// (REQ-WP-001). session is the pane's session, whose working directory the
// daemon tries first; dir is the space folder it tries last. A link that is
// not a local file, a refused path and a missing file come back as a
// *PreviewError.
func (c *Local) Preview(ctx context.Context, session, link, dir string) (PreviewFile, error) {
	body := previewBody{Session: session, Link: link, Dir: dir}
	var file PreviewFile
	if err := c.Do(ctx, http.MethodPost, PreviewPath, body, &file); err != nil {
		return PreviewFile{}, previewError(err)
	}
	return file, nil
}

// PreviewError is a failed preview call (REQ-WP-001): Status is the answer's
// HTTP status and Message the daemon's own "error" text, such as
// "no such file: ..." for a 404 and "the path is not allowed" for a 403.
type PreviewError struct {
	Status  int
	Message string
}

func (e *PreviewError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("lana: preview failed (HTTP %d)", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// previewError turns a daemon answer into a *PreviewError, and leaves any
// other error, such as a transport failure, as it is.
func previewError(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return &PreviewError{Status: apiErr.Status, Message: apiErr.Message}
	}
	return err
}

// IsPreviewNotFound reports a 404 preview answer: no such file.
func IsPreviewNotFound(err error) bool { return previewStatus(err) == http.StatusNotFound }

// IsPreviewForbidden reports a 403 preview answer: the path is not allowed.
func IsPreviewForbidden(err error) bool { return previewStatus(err) == http.StatusForbidden }

// IsPreviewBadLink reports a 400 preview answer: the link is not a local file.
func IsPreviewBadLink(err error) bool { return previewStatus(err) == http.StatusBadRequest }

// previewStatus is the HTTP status of a preview failure, 0 for any other
// error.
func previewStatus(err error) int {
	var previewErr *PreviewError
	if errors.As(err, &previewErr) {
		return previewErr.Status
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

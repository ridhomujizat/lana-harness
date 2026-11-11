package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
)

// maxCaptionRunes is the send_file caption limit (REQ-AGF-001).
const maxCaptionRunes = 1000

var sendFileSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path, workspace-relative or absolute inside the workspace."},"caption":{"type":"string","maxLength":1000,"description":"Optional short note shown with the file. At most 1000 characters."}},"required":["path"],"additionalProperties":false}`)

const sendFileDescription = "Sends one file or image from the workspace to the owner. " +
	"Use it when the owner asked for a file or image, or when a file you produced is something they would want to see (a screenshot, a chart, a report, a document). " +
	"The owner sees it in the chat and, on Telegram, right away in the conversation. " +
	"Send the file itself instead of pasting its contents into your reply. " +
	"The path must be a regular file inside the workspace, at most 20 MB."

type sendFileTool struct {
	guard *Guard
	now   func() time.Time
}

// NewSendFile creates the send_file tool.
func NewSendFile(guard *Guard) core.Tool { return &sendFileTool{guard: guard, now: time.Now} }

func (t *sendFileTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "send_file", Description: sendFileDescription, InputSchema: sendFileSpec}
}

func (t *sendFileTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *sendFileTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path    string `json:"path"`
		Caption string `json:"caption"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return inputErrorOutput(fmt.Errorf("path is required"))
	}
	if len([]rune(in.Caption)) > maxCaptionRunes {
		return errorOutput(fmt.Sprintf("caption is longer than %d characters", maxCaptionRunes))
	}
	if tc.Files == nil {
		return errorOutput("sending files is not available here")
	}
	workspace := effectiveWorkspace(t.guard, tc.Workspace)
	// send_file always resolves inside the workspace, even when the file tools
	// may reach any path: no file is ever sent from outside it (REQ-AGF-001).
	abs, err := ResolveInWorkspace(workspace, in.Path)
	if err != nil {
		if errors.Is(err, ErrOutsideWorkspace) {
			return core.ToolOutput{Status: core.ToolStatusRejected, Output: "outside workspace"}, nil
		}
		return errorOutput(err.Error())
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return errorOutput("file not found")
		}
		return errorOutput(err.Error())
	}
	if !info.Mode().IsRegular() {
		return errorOutput("not a regular file")
	}
	if info.Size() > attach.MaxFileBytes {
		return errorOutput("file is larger than 20 MB")
	}
	sent, rejected := t.stage(workspace, abs, info, in.Caption)
	if rejected != nil {
		return *rejected, nil
	}
	delivery, err := tc.Files.SendFile(ctx, tc, sent)
	if err != nil {
		return errorOutput(err.Error())
	}
	text := fmt.Sprintf("Sent %s (%s) to the owner.", sent.Name, fileSize(sent.Size))
	if delivery != "" {
		text += " " + delivery + "."
	}
	return successOutput(text)
}

// stage returns the SentFile for abs. A file already under the workspace's
// attachments directory is used as is; every other file is copied there with
// the attachment naming rules (REQ-AGF-003).
func (t *sendFileTool) stage(workspace, abs string, info os.FileInfo, caption string) (core.SentFile, *core.ToolOutput) {
	if rel, inside := attachmentRel(workspace, abs); inside {
		mediaType, err := attach.MediaTypePath(abs)
		if err != nil {
			out := core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
			return core.SentFile{}, &out
		}
		return core.SentFile{Path: rel, AbsPath: abs, Name: info.Name(), MediaType: mediaType, Size: info.Size(), Caption: caption}, nil
	}
	f, err := os.Open(abs)
	if err != nil {
		out := core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
		return core.SentFile{}, &out
	}
	defer func() { _ = f.Close() }()
	saved, err := attach.Save(workspace, info.Name(), f, t.now())
	if err != nil {
		out := core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
		return core.SentFile{}, &out
	}
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		out := core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
		return core.SentFile{}, &out
	}
	savedAbs := filepath.Join(wsAbs, filepath.FromSlash(saved.Path))
	return core.SentFile{Path: saved.Path, AbsPath: savedAbs, Name: saved.Name, MediaType: saved.MediaType, Size: saved.Size, Caption: caption}, nil
}

// attachmentRel reports abs as a workspace-relative attachments path (for
// example attachments/2026-11-11/x.png) when it lies under the workspace's
// attachments directory.
func attachmentRel(workspace, abs string) (string, bool) {
	dir := canonicalPath(filepath.Join(workspace, attach.Dir))
	target, inside := pathWithin(dir, abs)
	if !inside {
		return "", false
	}
	child, err := filepath.Rel(dir, target)
	if err != nil {
		return "", false
	}
	return attach.Dir + "/" + filepath.ToSlash(child), true
}

// fileSize renders a byte count for the tool output, for example "312 KB".
func fileSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeFileSender records the SentFile the send_file tool hands to it and
// returns a canned delivery note.
type fakeFileSender struct {
	sent []core.SentFile
	note string
	err  error
}

func (f *fakeFileSender) SendFile(_ context.Context, _ core.ToolCtx, file core.SentFile) (string, error) {
	f.sent = append(f.sent, file)
	return f.note, f.err
}

// sendFileDate is the fixed clock the tests use, so the attachments path is
// stable.
var sendFileDate = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

func fixedClockSendFile(ws string) *sendFileTool {
	tool := NewSendFile(&Guard{Workspace: ws}).(*sendFileTool)
	tool.now = func() time.Time { return sendFileDate }
	return tool
}

// pngHead is the 8-byte PNG signature http.DetectContentType reads.
var pngHead = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// REQ-AGF-003: a workspace file is copied into attachments/<date>/ and the file
// service is called with the copied path and the file's metadata.
func TestSendFileCopiesToAttachments(t *testing.T) {
	ws := t.TempDir()
	content := []byte("# report\n")
	if err := os.WriteFile(filepath.Join(ws, "report.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &fakeFileSender{}
	tc := core.ToolCtx{Workspace: ws, SessionID: "s1", TurnID: "t1", CallID: "c1", AgentName: "assistant", Files: sender}

	out := callToolCtx(t, fixedClockSendFile(ws), tc, mustJSON(t, map[string]string{"path": "report.md", "caption": "here"}))
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if want := fmt.Sprintf("Sent report.md (%s) to the owner.", fileSize(int64(len(content)))); out.Output != want {
		t.Fatalf("output=%q want %q", out.Output, want)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent=%+v, want one file", sender.sent)
	}
	got := sender.sent[0]
	want := core.SentFile{
		Path:      "attachments/2026-11-11/report.md",
		Name:      "report.md",
		MediaType: "text/markdown",
		Size:      int64(len(content)),
		Caption:   "here",
	}
	if got.Path != want.Path || got.Name != want.Name || got.MediaType != want.MediaType || got.Size != want.Size || got.Caption != want.Caption {
		t.Fatalf("sent=%+v want %+v", got, want)
	}
	if got.AbsPath != filepath.Join(ws, filepath.FromSlash(want.Path)) {
		t.Fatalf("AbsPath=%q", got.AbsPath)
	}
	data, err := os.ReadFile(got.AbsPath)
	if err != nil || string(data) != string(content) {
		t.Fatalf("copied file=%q err=%v", data, err)
	}
}

// REQ-AGF-003: a file already under attachments/ keeps its path and is not
// copied again.
func TestSendFileKeepsAttachmentPath(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, "attachments", "2026-11-11")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, append(append([]byte{}, pngHead...), 0x01), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &fakeFileSender{}
	tc := core.ToolCtx{Workspace: ws, Files: sender}

	out := callToolCtx(t, fixedClockSendFile(ws), tc, `{"path":"attachments/2026-11-11/shot.png"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent=%+v", sender.sent)
	}
	got := sender.sent[0]
	if got.Path != "attachments/2026-11-11/shot.png" || got.Name != "shot.png" || got.MediaType != "image/png" {
		t.Fatalf("sent=%+v", got)
	}
	if got.AbsPath != mustEval(t, path) {
		t.Fatalf("AbsPath=%q want %q", got.AbsPath, mustEval(t, path))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("attachments dir has %d entries, want no copy", len(entries))
	}
}

// REQ-AGF-001: bad paths, directories, missing files, oversized files, and long
// captions are rejected with the documented message.
func TestSendFileRejections(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ws, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link.txt")); err != nil {
		t.Fatal(err)
	}
	writeSparseFile(t, filepath.Join(ws, "big.bin"), (20<<20)+1)

	cases := []struct {
		name   string
		input  string
		status core.ToolStatus
		want   string
	}{
		{
			name:   "absolute path elsewhere",
			input:  mustJSON(t, map[string]string{"path": outside}),
			status: core.ToolStatusRejected, want: "outside workspace",
		},
		{
			name:   "symlink pointing outside",
			input:  `{"path":"link.txt"}`,
			status: core.ToolStatusRejected, want: "outside workspace",
		},
		{
			name:   "directory",
			input:  `{"path":"subdir"}`,
			status: core.ToolStatusError, want: "not a regular file",
		},
		{
			name:   "missing file",
			input:  `{"path":"nope.txt"}`,
			status: core.ToolStatusError, want: "file not found",
		},
		{
			name:   "over 20 MB",
			input:  `{"path":"big.bin"}`,
			status: core.ToolStatusError, want: "file is larger than 20 MB",
		},
		{
			name:   "caption over 1000 runes",
			input:  mustJSON(t, map[string]string{"path": "ok.txt", "caption": strings.Repeat("x", 1001)}),
			status: core.ToolStatusError, want: "caption is longer than 1000 characters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &fakeFileSender{}
			tcCtx := core.ToolCtx{Workspace: ws, Files: sender}
			out := callToolCtx(t, fixedClockSendFile(ws), tcCtx, tc.input)
			if out.Status != tc.status || !strings.Contains(out.Output, tc.want) {
				t.Fatalf("out=%+v, want status %q containing %q", out, tc.status, tc.want)
			}
			if len(sender.sent) != 0 {
				t.Fatalf("a rejected call still sent %+v", sender.sent)
			}
		})
	}
}

// REQ-AGF-002: without a file service the tool reports that sending is not
// available.
func TestSendFileWithoutService(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callToolCtx(t, fixedClockSendFile(ws), core.ToolCtx{Workspace: ws}, `{"path":"ok.txt"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "sending files is not available here") {
		t.Fatalf("out=%+v", out)
	}
}

// REQ-AGF-004: the delivery note of the file service is appended to the tool
// output.
func TestSendFileAppendsDeliveryNote(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	sender := &fakeFileSender{note: "Telegram delivery failed: bot is blocked"}
	tc := core.ToolCtx{Workspace: ws, Files: sender}

	out := callToolCtx(t, fixedClockSendFile(ws), tc, `{"path":"a.txt"}`)
	want := "Sent a.txt (2 B) to the owner. Telegram delivery failed: bot is blocked."
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
}

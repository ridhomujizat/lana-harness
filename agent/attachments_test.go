package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// attachAgent builds an agent config for the attachment tests.
func attachAgent(model string, vision *bool, tools ...string) map[string]core.AgentConfig {
	return map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: model, Vision: vision, Tools: tools}}
}

func boolPtr(b bool) *bool { return &b }

// pngFixture encodes a small opaque PNG.
func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 20, G: 40, B: 60, A: 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeAttachment writes data to <ws>/<rel>, creating the parent directory.
func writeAttachment(t *testing.T, ws, rel string, data []byte) {
	t.Helper()
	abs := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runWithAttachments starts a turn with attachments and waits for it.
func runWithAttachments(t *testing.T, h *harness, text string, attachments ...string) TurnResult {
	t.Helper()
	turnID, err := h.reg.Start(context.Background(), h.info.ID, text, attachments...)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(context.Background(), turnID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// lastOwnerMessage returns the newest stored user message that is not a
// runtime note.
func lastOwnerMessage(t *testing.T, h *harness) core.UserMessage {
	t.Helper()
	var out core.UserMessage
	found := false
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Runtime {
			continue
		}
		out = payload
		found = true
	}
	if !found {
		t.Fatal("no owner message")
	}
	return out
}

// ownerRequestBlocks returns the blocks of the request's first user message,
// after the leading <context> blocks.
func ownerRequestBlocks(t *testing.T, req core.ChatRequest, skip int) []core.ContentBlock {
	t.Helper()
	for _, message := range req.Messages {
		if message.Role != core.RoleUser {
			continue
		}
		if skip > len(message.Content) {
			t.Fatalf("skip %d > %d blocks", skip, len(message.Content))
		}
		return message.Content[skip:]
	}
	t.Fatal("no user message in request")
	return nil
}

// requestToolResult returns the first tool_result block of a request.
func requestToolResult(t *testing.T, req core.ChatRequest) core.ContentBlock {
	t.Helper()
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if block.Kind == core.BlockToolResult {
				return block
			}
		}
	}
	t.Fatal("no tool_result block in request")
	return core.ContentBlock{}
}

// waitIdleRegistry waits until the session's whole turn chain has finished.
func waitIdleRegistry(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !h.reg.TurnActive(h.info.ID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("turn chain did not finish")
}

// wantCaptionThenImages checks a block list is the caption text then one image
// block per path, in order.
func wantCaptionThenImages(t *testing.T, blocks []core.ContentBlock, text string, paths []string) {
	t.Helper()
	if len(blocks) != 1+len(paths) {
		t.Fatalf("blocks = %d, want %d: %+v", len(blocks), 1+len(paths), blocks)
	}
	if blocks[0].Kind != core.BlockText || blocks[0].Text != text {
		t.Fatalf("first block = %+v, want text %q", blocks[0], text)
	}
	for i, p := range paths {
		b := blocks[i+1]
		if b.Kind != core.BlockImage || b.Path != p || len(b.Data) == 0 {
			t.Fatalf("block %d = %+v, want image %q with data", i+1, b, p)
		}
	}
}

// AC-ATT-001: three images and a caption store the caption then the images,
// and the request carries them in order.
func TestAttachOwnerMessage(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, attachAgent("deepseek-flash", nil))
	reqs := captureRequests(h)
	png := pngFixture(t)
	paths := []string{
		"attachments/2026-11-01/a.png",
		"attachments/2026-11-01/b.png",
		"attachments/2026-11-01/c.png",
	}
	for _, p := range paths {
		writeAttachment(t, h.ws, p, png)
	}
	if result := runWithAttachments(t, h, "bandingkan", paths...); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}

	owner := lastOwnerMessage(t, h)
	wantCaptionThenImages(t, owner.Content[owner.ContextBlocks:], "bandingkan", paths)

	if len(*reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(*reqs))
	}
	wantCaptionThenImages(t, ownerRequestBlocks(t, (*reqs)[0], owner.ContextBlocks), "bandingkan", paths)
}

// REQ-ATT-002: an owner message with attachments and no text gets
// "(attachments)".
func TestAttachBlankText(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, attachAgent("deepseek-flash", nil))
	const path = "attachments/2026-11-01/a.png"
	writeAttachment(t, h.ws, path, pngFixture(t))

	if result := runWithAttachments(t, h, "", path); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	owner := lastOwnerMessage(t, h)
	blocks := owner.Content[owner.ContextBlocks:]
	if len(blocks) != 2 || blocks[0].Text != "(attachments)" || blocks[1].Kind != core.BlockImage {
		t.Fatalf("blocks = %+v", blocks)
	}
}

// A missing path becomes a text block and the turn still ends.
func TestAttachMissingPath(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("done")}}, attachAgent("deepseek-flash", nil))
	if err := os.MkdirAll(filepath.Join(h.ws, "attachments", "2026-11-01"), 0o700); err != nil {
		t.Fatal(err)
	}

	if result := runWithAttachments(t, h, "cek", "attachments/2026-11-01/missing.png"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	owner := lastOwnerMessage(t, h)
	blocks := owner.Content[owner.ContextBlocks:]
	found := false
	for _, b := range blocks {
		if b.Kind == core.BlockText && strings.Contains(b.Text, "was not found in the workspace attachments") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no not-found block in %+v", blocks)
	}
}

// AC-ATT-002 (light): a text file becomes a text block with its content.
func TestAttachTextFile(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, attachAgent("deepseek-flash", nil))
	const path = "attachments/2026-11-01/notes.txt"
	writeAttachment(t, h.ws, path, []byte("hello attachment"))

	runWithAttachments(t, h, "baca", path)
	owner := lastOwnerMessage(t, h)
	blocks := owner.Content[owner.ContextBlocks:]
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d: %+v", len(blocks), blocks)
	}
	b := blocks[1]
	if b.Kind != core.BlockText || !strings.HasPrefix(b.Text, "Attachment "+path+" (") || !strings.Contains(b.Text, "hello attachment") {
		t.Fatalf("attachment block = %q", b.Text)
	}
}

// AC-ATT-003 (light): a text PDF becomes one text block with page markers.
func TestAttachPDF(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, attachAgent("deepseek-flash", nil))
	const path = "attachments/2026-11-01/doc.pdf"
	writeAttachment(t, h.ws, path, agentTestPDF([]string{"Hello page one", "Second page text"}))

	runWithAttachments(t, h, "baca pdf", path)
	owner := lastOwnerMessage(t, h)
	blocks := owner.Content[owner.ContextBlocks:]
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d: %+v", len(blocks), blocks)
	}
	b := blocks[1]
	if b.Kind != core.BlockText || !strings.HasPrefix(b.Text, "Attachment "+path+" (PDF, 2 pages):") {
		t.Fatalf("pdf block = %q", b.Text)
	}
	if got := strings.Count(b.Text, "--- page "); got != 2 {
		t.Fatalf("page markers = %d, want 2", got)
	}
}

// AC-ATT-004: the request uses the placeholder on a non-vision model while the
// stored event keeps the image; the agent key and a vision catalog model send
// the image.
func TestAttachVisionPlaceholder(t *testing.T) {
	const path = "attachments/2026-11-01/a.png"
	png := pngFixture(t)
	cases := []struct {
		name      string
		model     string
		vision    *bool
		wantImage bool
	}{
		{"pro placeholder", "deepseek-v4-pro", nil, false},
		{"pro vision on", "deepseek-v4-pro", boolPtr(true), true},
		{"flash image", "deepseek-flash", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, attachAgent(tc.model, tc.vision))
			reqs := captureRequests(h)
			writeAttachment(t, h.ws, path, png)

			runWithAttachments(t, h, "lihat", path)

			owner := lastOwnerMessage(t, h)
			stored := owner.Content[owner.ContextBlocks:]
			if len(stored) != 2 || stored[1].Kind != core.BlockImage || stored[1].Path != path {
				t.Fatalf("stored blocks = %+v, want the image kept", stored)
			}

			requested := ownerRequestBlocks(t, (*reqs)[0], owner.ContextBlocks)
			if tc.wantImage {
				if len(requested) != 2 || requested[1].Kind != core.BlockImage || requested[1].Path != path {
					t.Fatalf("request blocks = %+v, want the image", requested)
				}
				return
			}
			want := "[image " + path + ": the current model cannot see images]"
			if len(requested) != 2 || requested[1].Kind != core.BlockText || requested[1].Text != want {
				t.Fatalf("request blocks = %+v, want placeholder %q", requested, want)
			}
			for _, b := range requested {
				if b.Kind == core.BlockImage {
					t.Fatalf("request still has an image block: %+v", requested)
				}
			}
		})
	}
}

// imageResultTool returns a fixed image block.
type imageResultTool struct{}

func (imageResultTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "image_result", Description: "returns an image", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (imageResultTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (imageResultTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{
		Status: core.ToolStatusSuccess,
		Output: "Image attachments/2026-11-01/a.png (4x4).",
		Images: []core.ContentBlock{{Kind: core.BlockImage, MediaType: "image/png", Path: "attachments/2026-11-01/a.png", Data: []byte{1, 2, 3}}},
	}, nil
}

// AC-ATT-005 (partial): a tool result carries its images into the event and the
// next request; a non-vision model gets the placeholder instead.
func TestToolResultImages(t *testing.T) {
	const path = "attachments/2026-11-01/a.png"
	cases := []struct {
		name      string
		model     string
		wantImage bool
	}{
		{"flash carries images", "deepseek-flash", true},
		{"pro uses placeholder", "deepseek-v4-pro", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{
				toolResponse("c1", "image_result", `{}`),
				textResponse("done"),
			}}, attachAgent(tc.model, nil, "image_result"))
			if err := h.tools.Register(imageResultTool{}); err != nil {
				t.Fatal(err)
			}
			reqs := captureRequests(h)

			if result := h.run(t, "lihat"); result.StopReason != "end_turn" {
				t.Fatalf("result=%+v", result)
			}

			var payload core.ToolResultPayload
			found := false
			for _, event := range h.events(t) {
				if event.Kind != core.KindToolResult {
					continue
				}
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				found = true
			}
			if !found || len(payload.Images) != 1 || payload.Images[0].Path != path {
				t.Fatalf("tool/result images = %+v", payload.Images)
			}

			if len(*reqs) < 2 {
				t.Fatalf("requests = %d, want 2", len(*reqs))
			}
			block := requestToolResult(t, (*reqs)[1])
			if tc.wantImage {
				if len(block.Images) != 1 || block.Images[0].Path != path {
					t.Fatalf("tool_result images = %+v", block.Images)
				}
				return
			}
			if len(block.Images) != 0 {
				t.Fatalf("tool_result still has images: %+v", block.Images)
			}
			want := "[image " + path + ": the current model cannot see images]"
			if !strings.Contains(block.Output, want) {
				t.Fatalf("tool_result output = %q, want the placeholder %q", block.Output, want)
			}
		})
	}
}

// A message queued while a turn runs keeps its attachments on the next turn.
func TestQueuedAttachments(t *testing.T) {
	delay := 100
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: strptr("a")}, {Stop: strptr("end_turn")}}},
		textResponse("b"),
	}}, attachAgent("deepseek-flash", nil))
	const path = "attachments/2026-11-01/a.png"
	writeAttachment(t, h.ws, path, pngFixture(t))

	turnID, err := h.reg.Start(context.Background(), h.info.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := h.reg.SendWithAttachments(context.Background(), h.info.ID, "second", path); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	waitIdleRegistry(t, h)

	owner := lastOwnerMessage(t, h)
	blocks := owner.Content[owner.ContextBlocks:]
	if len(blocks) != 2 || blocks[0].Kind != core.BlockText || blocks[0].Text != "second" || blocks[1].Kind != core.BlockImage || blocks[1].Path != path {
		t.Fatalf("second owner blocks = %+v", blocks)
	}
}

// agentTestPDF writes a minimal text PDF with one string per page. It mirrors
// the builder in internal/attach/attach_test.go.
func agentTestPDF(pages []string) []byte {
	n := len(pages)
	var buf bytes.Buffer
	offsets := map[int]int{}
	buf.WriteString("%PDF-1.4\n")

	writeObj := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}

	pageNums := make([]int, n)
	contentNums := make([]int, n)
	next := 4
	for i := 0; i < n; i++ {
		pageNums[i] = next
		next++
		contentNums[i] = next
		next++
	}

	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	var kids strings.Builder
	for _, pn := range pageNums {
		fmt.Fprintf(&kids, "%d 0 R ", pn)
	}
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), n))
	writeObj(3, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i := 0; i < n; i++ {
		writeObj(pageNums[i], fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>",
			contentNums[i]))
		stream := ""
		if pages[i] != "" {
			stream = fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", pages[i])
		}
		writeObj(contentNums[i], fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", next)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i < next; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", next, xref)
	return buf.Bytes()
}

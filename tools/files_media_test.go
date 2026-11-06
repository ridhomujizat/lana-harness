package tools

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestReadFileImage(t *testing.T) {
	ws := t.TempDir()
	writePNG(t, filepath.Join(ws, "pic.png"), 10, 20)
	tool := NewReadFile(&Guard{Workspace: ws})

	out := callTool(t, tool, ws, `{"path":"pic.png"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "Image pic.png (10x20)." {
		t.Fatalf("out=%+v", out)
	}
	if len(out.Images) != 1 {
		t.Fatalf("images=%d want 1", len(out.Images))
	}
	img := out.Images[0]
	if img.Kind != core.BlockImage || img.Path != "pic.png" || img.MediaType != "image/jpeg" {
		t.Fatalf("image=%+v", img)
	}

	out = callTool(t, tool, ws, `{"path":"pic.png","offset":5,"limit":1}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "Image pic.png (10x20)." {
		t.Fatalf("offset/limit out=%+v", out)
	}
}

func TestReadFileLargeImage(t *testing.T) {
	ws := t.TempDir()
	writePNG(t, filepath.Join(ws, "big.png"), 3000, 2000)

	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"big.png"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "Image big.png (3000x2000)." {
		t.Fatalf("out=%+v", out)
	}
	if len(out.Images) != 1 {
		t.Fatalf("images=%d want 1", len(out.Images))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out.Images[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 1568 || cfg.Height != 1045 {
		t.Fatalf("scaled=%dx%d want 1568x1045", cfg.Width, cfg.Height)
	}
}

func TestReadFilePDF(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "doc.pdf"), buildPDF([]string{"alpha", "beta"}), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile(&Guard{Workspace: ws})

	out := callTool(t, tool, ws, `{"path":"doc.pdf"}`)
	want := fmt.Sprintf("%6d\t--- page 1 ---\n%6d\talpha\n%6d\t--- page 2 ---\n%6d\tbeta", 1, 2, 3, 4)
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
	if len(out.Images) != 0 {
		t.Fatalf("images=%d want 0", len(out.Images))
	}

	out = callTool(t, tool, ws, `{"path":"doc.pdf","offset":2,"limit":2}`)
	want = fmt.Sprintf("%6d\talpha\n%6d\t--- page 2 ---\n(Showing lines 2–3 of 4. Use offset=4 to read more.)", 2, 3)
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("offset/limit out=%+v want %q", out, want)
	}
}

func TestReadFileScannedPDF(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "scan.pdf"), buildPDF([]string{"", ""}), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"scan.pdf"}`)
	want := "Attachment scan.pdf (PDF, 2 pages) has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead."
	if out.Status != core.ToolStatusError || out.Output != want {
		t.Fatalf("out=%+v want %q", out, want)
	}
}

func TestReadFileBinaryAllBytes(t *testing.T) {
	ws := t.TempDir()
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(ws, "all.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"all.bin"}`)
	if out.Status != core.ToolStatusError || out.Output != "binary file" {
		t.Fatalf("out=%+v", out)
	}
	if len(out.Images) != 0 {
		t.Fatalf("images=%d want 0", len(out.Images))
	}
}

func TestReadFileTextNoImages(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, NewReadFile(&Guard{Workspace: ws}), ws, `{"path":"a.txt"}`)
	if out.Status != core.ToolStatusSuccess || out.Images != nil {
		t.Fatalf("out=%+v want no images", out)
	}
}

// writePNG writes an opaque w x h PNG, which attach re-encodes as JPEG.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// buildPDF writes a minimal PDF with one text string per page; an empty string
// leaves the page without a content stream. It computes the xref byte offsets
// while writing.
func buildPDF(pages []string) []byte {
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

package attach

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// wsDir creates a temp workspace with an attachments directory.
func wsDir(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	return ws
}

// writeFile writes data to <ws>/<rel>.
func writeFile(t *testing.T, ws, rel string, data []byte) string {
	t.Helper()
	abs := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return abs
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// opaque returns a w x h RGBA filled with one opaque color.
func opaque(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 30, G: 90, B: 150, A: 255}), image.Point{}, draw.Src)
	return img
}

func TestSave(t *testing.T) {
	ws := t.TempDir()
	info, err := Save(ws, "hello.txt", strings.NewReader("hello world"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if want := "attachments/2026-11-01/hello.txt"; info.Path != want {
		t.Errorf("Path = %q, want %q", info.Path, want)
	}
	if info.Name != "hello.txt" {
		t.Errorf("Name = %q, want hello.txt", info.Name)
	}
	if info.MediaType != "text/plain" {
		t.Errorf("MediaType = %q, want text/plain", info.MediaType)
	}
	if info.Size != int64(len("hello world")) {
		t.Errorf("Size = %d, want %d", info.Size, len("hello world"))
	}
	abs := filepath.Join(ws, filepath.FromSlash(info.Path))
	if got, err := os.ReadFile(abs); err != nil || string(got) != "hello world" {
		t.Errorf("content = %q, %v", got, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(abs))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
	}
}

func TestSaveClash(t *testing.T) {
	ws := t.TempDir()
	for i, want := range []string{"photo.png", "photo-1.png", "photo-2.png"} {
		info, err := Save(ws, "photo.png", strings.NewReader("x"), testNow)
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if info.Name != want {
			t.Errorf("save %d: Name = %q, want %q", i, info.Name, want)
		}
	}
	for i, want := range []string{"notes", "notes-1"} {
		info, err := Save(ws, "notes", strings.NewReader("x"), testNow)
		if err != nil {
			t.Fatalf("ext-less save %d: %v", i, err)
		}
		if info.Name != want {
			t.Errorf("ext-less save %d: Name = %q, want %q", i, info.Name, want)
		}
	}
}

// zeros yields an endless stream of zero bytes.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestSaveSizeLimit(t *testing.T) {
	ws := t.TempDir()
	_, err := Save(ws, "big.bin", io.LimitReader(zeros{}, MaxFileBytes+1), testNow)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	dir := filepath.Join(ws, Dir, testNow.Format("2006-01-02"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("directory has %d entries after a too-large save, want 0", len(entries))
	}

	info, err := Save(ws, "ok.bin", io.LimitReader(zeros{}, MaxFileBytes), testNow)
	if err != nil {
		t.Fatalf("exact-size save: %v", err)
	}
	if info.Size != MaxFileBytes {
		t.Errorf("Size = %d, want %d", info.Size, MaxFileBytes)
	}
}

func TestCleanName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"../../etc/passwd", "passwd"},
		{`a\b\c.txt`, "c.txt"},
		{"bad\x00name\n.txt", "badname.txt"},
		{"", "file"},
		{"/", "file"},
		{"..", "file"},
	}
	for _, c := range cases {
		if got := cleanName(c.in); got != c.want {
			t.Errorf("cleanName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolve(t *testing.T) {
	ws := wsDir(t)
	writeFile(t, ws, "attachments/ok.txt", []byte("hi"))

	rejects := []struct {
		rel   string
		outer bool
	}{
		{"", true},
		{"../x", true},
		{"/etc/passwd", true},
		{"attachments/../secret", true},
		{"attachments/missing.txt", false},
	}
	for _, c := range rejects {
		_, err := Resolve(ws, c.rel)
		if err == nil {
			t.Errorf("Resolve(%q) = nil error, want error", c.rel)
			continue
		}
		if c.outer && !errors.Is(err, ErrOutside) {
			t.Errorf("Resolve(%q) = %v, want ErrOutside", c.rel, err)
		}
	}
	if _, err := Resolve(ws, "attachments/missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: errors.Is(err, fs.ErrNotExist) = false (%v)", err)
	}

	abs, err := Resolve(ws, "attachments/ok.txt")
	if err != nil {
		t.Fatalf("Resolve(ok.txt): %v", err)
	}
	if !filepath.IsAbs(abs) {
		t.Errorf("Resolve returned a relative path %q", abs)
	}

	if err := os.Mkdir(filepath.Join(ws, Dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ws, "attachments/sub"); err == nil {
		t.Error("Resolve(directory) = nil error, want error")
	}

	outside := t.TempDir()
	outFile := writeFile(t, outside, "secret.txt", []byte("s"))
	if err := os.Symlink(outFile, filepath.Join(ws, Dir, "link-out.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ws, "attachments/link-out.txt"); !errors.Is(err, ErrOutside) {
		t.Errorf("symlink outside: err = %v, want ErrOutside", err)
	}

	inFile := filepath.Join(ws, Dir, "ok.txt")
	if err := os.Symlink(inFile, filepath.Join(ws, Dir, "link-in.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ws, "attachments/link-in.txt"); err != nil {
		t.Errorf("symlink inside: %v", err)
	}
}

func TestResolveAcceptsDotDotPrefixedName(t *testing.T) {
	ws := wsDir(t)
	info, err := Save(ws, "..notes.txt", strings.NewReader("notes"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if want := "attachments/2026-11-01/..notes.txt"; info.Path != want {
		t.Errorf("Path = %q, want %q", info.Path, want)
	}
	if info.Name != "..notes.txt" {
		t.Errorf("Name = %q, want ..notes.txt", info.Name)
	}

	abs, err := Resolve(ws, info.Path)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", info.Path, err)
	}
	if got, err := os.ReadFile(abs); err != nil || string(got) != "notes" {
		t.Errorf("content = %q, %v", got, err)
	}

	if _, err := Resolve(ws, "attachments/../attachments/2026-11-01/..notes.txt"); !errors.Is(err, ErrOutside) {
		t.Errorf("traversal: err = %v, want ErrOutside", err)
	}
}

func TestImageLarge(t *testing.T) {
	ws := wsDir(t)
	writeFile(t, ws, "attachments/big.png", encodePNG(t, opaque(3000, 2000)))

	abs := filepath.Join(ws, Dir, "big.png")
	b, w, h, err := Image(abs, "attachments/big.png")
	if err != nil {
		t.Fatal(err)
	}
	if w != 3000 || h != 2000 {
		t.Errorf("original size = %dx%d, want 3000x2000", w, h)
	}
	if b.Kind != core.BlockImage {
		t.Errorf("Kind = %q, want image", b.Kind)
	}
	if b.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg", b.MediaType)
	}
	if b.Path != "attachments/big.png" {
		t.Errorf("Path = %q", b.Path)
	}
	dec, _, err := image.Decode(bytes.NewReader(b.Data))
	if err != nil {
		t.Fatal(err)
	}
	db := dec.Bounds()
	if max(db.Dx(), db.Dy()) != MaxImageEdge {
		t.Errorf("scaled long edge = %d, want %d", max(db.Dx(), db.Dy()), MaxImageEdge)
	}
	if min(db.Dx(), db.Dy()) != 1045 {
		t.Errorf("scaled short edge = %d, want 1045", min(db.Dx(), db.Dy()))
	}
}

func TestImageTransparent(t *testing.T) {
	ws := wsDir(t)
	img := opaque(40, 30)
	img.SetRGBA(5, 5, color.RGBA{R: 100, A: 128})
	writeFile(t, ws, "attachments/t.png", encodePNG(t, img))

	b, w, h, err := Image(filepath.Join(ws, Dir, "t.png"), "attachments/t.png")
	if err != nil {
		t.Fatal(err)
	}
	if b.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", b.MediaType)
	}
	if w != 40 || h != 30 {
		t.Errorf("original size = %dx%d, want 40x30", w, h)
	}
	dec, _, err := image.Decode(bytes.NewReader(b.Data))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != 40 || dec.Bounds().Dy() != 30 {
		t.Errorf("decoded size = %dx%d, want 40x30 (unscaled)", dec.Bounds().Dx(), dec.Bounds().Dy())
	}
}

func TestImageSmallOpaque(t *testing.T) {
	ws := wsDir(t)
	writeFile(t, ws, "attachments/s.png", encodePNG(t, opaque(40, 30)))

	b, _, _, err := Image(filepath.Join(ws, Dir, "s.png"), "attachments/s.png")
	if err != nil {
		t.Fatal(err)
	}
	if b.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg", b.MediaType)
	}
	dec, _, err := image.Decode(bytes.NewReader(b.Data))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != 40 || dec.Bounds().Dy() != 30 {
		t.Errorf("decoded size = %dx%d, want 40x30 (not upscaled)", dec.Bounds().Dx(), dec.Bounds().Dy())
	}
}

func TestImageGIF(t *testing.T) {
	ws := wsDir(t)
	pal := color.Palette{color.RGBA{A: 255}, color.RGBA{R: 255, A: 255}}
	f1 := image.NewPaletted(image.Rect(0, 0, 40, 30), pal)
	f2 := image.NewPaletted(image.Rect(0, 0, 20, 15), pal)
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{f1, f2}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, ws, "attachments/anim.gif", buf.Bytes())

	_, w, h, err := Image(filepath.Join(ws, Dir, "anim.gif"), "attachments/anim.gif")
	if err != nil {
		t.Fatal(err)
	}
	if w != 40 || h != 30 {
		t.Errorf("GIF size = %dx%d, want first frame 40x30", w, h)
	}
}

const webp1x1 = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func decodeWebP(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(webp1x1)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImageWebP(t *testing.T) {
	data := decodeWebP(t)
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Skipf("hard-coded webp did not decode: %v", err)
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("webp decoded to %dx%d, want 1x1", img.Bounds().Dx(), img.Bounds().Dy())
	}

	ws := wsDir(t)
	writeFile(t, ws, "attachments/w.webp", data)
	_, w, h, err := Image(filepath.Join(ws, Dir, "w.webp"), "attachments/w.webp")
	if err != nil {
		t.Fatal(err)
	}
	if w != 1 || h != 1 {
		t.Errorf("Image size = %dx%d, want 1x1", w, h)
	}
}

// buildPDF writes a minimal PDF with one text string per page. It computes the
// xref byte offsets while writing.
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

func TestPDFText(t *testing.T) {
	ws := wsDir(t)
	writeFile(t, ws, "attachments/doc.pdf", buildPDF([]string{"Hello page one", "Second page text"}))

	pages, err := PDFText(filepath.Join(ws, Dir, "doc.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(pages))
	}
	if !strings.Contains(pages[0], "Hello page one") {
		t.Errorf("page 1 = %q, want the page text", pages[0])
	}
	if !strings.Contains(pages[1], "Second page text") {
		t.Errorf("page 2 = %q, want the page text", pages[1])
	}

	blocks, err := Blocks(ws, []string{"attachments/doc.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	text := blocks[0].Text
	if got := strings.Count(text, "--- page "); got != 2 {
		t.Errorf("page markers = %d, want 2 (%q)", got, text)
	}
	want := "Attachment attachments/doc.pdf (PDF, 2 pages):\n--- page 1 ---\n" + pages[0] + "\n--- page 2 ---\n" + pages[1]
	if text != want {
		t.Errorf("block = %q, want %q", text, want)
	}
}

func TestScannedPDF(t *testing.T) {
	ws := wsDir(t)
	writeFile(t, ws, "attachments/scan.pdf", buildPDF([]string{""}))

	pages, err := PDFText(filepath.Join(ws, Dir, "scan.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if p != "" {
			t.Errorf("page %d = %q, want empty", i+1, p)
		}
	}

	blocks, err := Blocks(ws, []string{"attachments/scan.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Attachment attachments/scan.pdf (PDF, 1 pages) has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead."
	if len(blocks) != 1 || blocks[0].Text != want {
		t.Errorf("block = %v", blocks)
	}
}

func TestTextCut(t *testing.T) {
	ws := wsDir(t)
	writeFile(t, ws, "attachments/long.txt", []byte(strings.Repeat("é", 16005)))

	blocks, err := Blocks(ws, []string{"attachments/long.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	header := "Attachment attachments/long.txt (text/plain):\n"
	notice := "(Showing the first 16000 characters. Use read_file on attachments/long.txt with offset to read more.)"
	want := header + strings.Repeat("é", InlineChars) + "\n" + notice
	if blocks[0].Text != want {
		t.Errorf("block = %q, want %q", blocks[0].Text, want)
	}
}

func TestKind(t *testing.T) {
	img := opaque(2, 2)
	webpData := decodeWebP(t)
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"x.png", encodePNG(t, img), "image"},
		{"x.jpg", encodeJPEG(t, img), "image"},
		{"x.gif", encodeGIF(t, img), "image"},
		{"x.webp", webpData, "image"},
		{"x.pdf", []byte("%PDF-1.4"), "pdf"},
		{"main.go", []byte("package main"), "text"},
		{"a.txt", []byte("hello"), "text"},
		{"a.md", []byte("# heading"), "text"},
		{"a.csv", []byte("a,b,c"), "text"},
		{"a.json", []byte(`{"a":1}`), "text"},
		{"a.txt", []byte{0xff, 0xfe, 0x00}, "other"},
		{"a.zip", []byte("PK\x03\x04rest"), "other"},
		{"a.txt", append([]byte("hello"), 0xe2, 0x82), "text"},
	}
	for _, c := range cases {
		if got := Kind(c.head, c.name); got != c.want {
			t.Errorf("Kind(%q, %q) = %q, want %q", c.head, c.name, got, c.want)
		}
	}
}

func TestBlocksReadFailures(t *testing.T) {
	ws := wsDir(t)
	badPNG := append([]byte("\x89PNG\r\n\x1a\n"), []byte("not really a png")...)
	writeFile(t, ws, "attachments/bad.png", badPNG)
	writeFile(t, ws, "attachments/a.zip", []byte("PK\x03\x04rest"))

	blocks, err := Blocks(ws, []string{"attachments/bad.png", "attachments/a.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if !strings.HasPrefix(blocks[0].Text, "Attachment attachments/bad.png could not be read: ") {
		t.Errorf("bad png block = %q", blocks[0].Text)
	}
	want := "Attachment attachments/a.zip (application/zip) was saved, but this file type cannot be read."
	if blocks[1].Text != want {
		t.Errorf("zip block = %q, want %q", blocks[1].Text, want)
	}

	if _, err := Blocks(ws, []string{"../x"}); err == nil {
		t.Error("Blocks(../x) = nil error, want error")
	}
	if got, err := Blocks(ws, nil); err != nil || got != nil {
		t.Errorf("Blocks(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestPlaceholder(t *testing.T) {
	b := core.ContentBlock{Kind: core.BlockImage, Path: "attachments/x.png"}
	if got, want := Placeholder(b), "[image attachments/x.png]"; got != want {
		t.Errorf("Placeholder = %q, want %q", got, want)
	}
}

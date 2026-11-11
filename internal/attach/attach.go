// Package attach saves and reads the files an owner sends with a message.
//
// An attachment lives under <workspace>/attachments/<YYYY-MM-DD>/<name>. Save
// writes new files there; Resolve accepts only regular files inside that
// directory; Blocks turns attachment paths into the core content blocks of a
// turn; Image and PDFText read the two file kinds the model can consume.
package attach

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	// MaxFileBytes is the largest attachment Save accepts.
	MaxFileBytes = 20 << 20
	// MaxFiles is the number of attachments one message may carry.
	MaxFiles = 10
	// MaxImageEdge is the long-edge limit an image is scaled down to.
	MaxImageEdge = 1568
	// InlineChars is how many characters of a text attachment are inlined.
	InlineChars = 16000
	// Dir is the workspace directory that holds attachments.
	Dir = "attachments"
)

const (
	sniffLen       = 512
	maxNameTries   = 1000
	maxImagePixels = 50_000_000 // decompression guard: reject absurd dimensions
)

// ErrTooLarge is returned by Save for a file over MaxFileBytes.
var ErrTooLarge = errors.New("file too large")

// ErrOutside is returned by Resolve for a path not under the workspace's
// attachments directory.
var ErrOutside = errors.New("path outside attachments")

// Info describes a saved attachment.
type Info struct {
	Path      string // workspace-relative, forward slashes
	Name      string // final file name on disk
	MediaType string
	Size      int64
}

// Save writes r to <workspace>/attachments/<YYYY-MM-DD>/<name>, mode 0600,
// naming a clash <stem>-1<ext>, <stem>-2<ext>, .... It stops with ErrTooLarge
// past MaxFileBytes and removes the partial file.
func Save(workspace, name string, r io.Reader, now time.Time) (Info, error) {
	date := now.Format("2006-01-02")
	dir := filepath.Join(workspace, Dir, date)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Info{}, err
	}

	head := make([]byte, sniffLen)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Info{}, err
	}
	head = head[:n]

	clean := cleanName(name)
	ext := filepath.Ext(clean)
	stem := strings.TrimSuffix(clean, ext)

	var (
		f        *os.File
		filePath string
		out      string
	)
	for i := 0; i < maxNameTries; i++ {
		out = clean
		if i > 0 {
			out = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		p := filepath.Join(dir, out)
		f, err = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			filePath = p
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return Info{}, err
		}
		f = nil
	}
	if f == nil {
		return Info{}, fmt.Errorf("attach: no free name for %q after %d tries", clean, maxNameTries)
	}

	src := io.MultiReader(bytes.NewReader(head), r)
	written, copyErr := io.Copy(f, io.LimitReader(src, MaxFileBytes+1))
	if copyErr != nil {
		_ = f.Close()
		_ = os.Remove(filePath)
		return Info{}, copyErr
	}
	if written > MaxFileBytes {
		_ = f.Close()
		_ = os.Remove(filePath)
		return Info{}, fmt.Errorf("attach: %s is over %d bytes: %w", out, int64(MaxFileBytes), ErrTooLarge)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(filePath)
		return Info{}, err
	}

	return Info{
		Path:      Dir + "/" + date + "/" + out,
		Name:      out,
		MediaType: mediaType(head, out),
		Size:      written,
	}, nil
}

// Resolve checks that rel names a regular file under <workspace>/attachments
// and returns its absolute path. A path below the symlinked root, a missing
// file, or a non-regular file is an error; a path outside the directory is
// ErrOutside.
func Resolve(workspace, rel string) (string, error) {
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("attach: %q: %w", rel, ErrOutside)
	}
	for _, elem := range strings.Split(rel, "/") {
		if elem == ".." {
			return "", fmt.Errorf("attach: %q: %w", rel, ErrOutside)
		}
	}
	if !strings.HasPrefix(rel, Dir+"/") {
		return "", fmt.Errorf("attach: %q: %w", rel, ErrOutside)
	}

	root, err := filepath.EvalSymlinks(filepath.Join(ws, Dir))
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(ws, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("attach: %q: %w", rel, err)
	}
	inside, err := filepath.Rel(root, target)
	if err != nil || inside == "." || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("attach: %q: %w", rel, ErrOutside)
	}
	fi, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("attach: %q is not a regular file", rel)
	}
	return target, nil
}

// Blocks builds the attachment parts of an owner message, in the order given.
// A file that cannot be read becomes a text block that says so; Blocks fails
// only on a path Resolve rejects.
func Blocks(workspace string, paths []string) ([]core.ContentBlock, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]core.ContentBlock, 0, len(paths))
	for _, p := range paths {
		abs, err := Resolve(workspace, p)
		if err != nil {
			return nil, fmt.Errorf("attach: %s: %w", p, err)
		}
		rel := path.Clean(p)
		name := path.Base(rel)

		head, err := headOf(abs)
		if err != nil {
			out = append(out, couldNotRead(rel, err))
			continue
		}
		switch Kind(head, name) {
		case "image":
			b, _, _, err := Image(abs, rel)
			if err != nil {
				out = append(out, couldNotRead(rel, err))
				continue
			}
			out = append(out, b)
		case "pdf":
			pages, err := PDFText(abs)
			if err != nil {
				out = append(out, couldNotRead(rel, err))
				continue
			}
			if allEmpty(pages) {
				out = append(out, scannedPDF(rel, len(pages)))
				continue
			}
			out = append(out, pdfTextBlock(rel, pages))
		case "text":
			data, err := readBounded(abs, MaxFileBytes)
			if err != nil {
				out = append(out, couldNotRead(rel, err))
				continue
			}
			if !utf8.Valid(data) {
				out = append(out, savedOther(rel, mediaType(head, name)))
				continue
			}
			out = append(out, textBlock(rel, mediaType(head, name), string(data)))
		default:
			out = append(out, savedOther(rel, mediaType(head, name)))
		}
	}
	return out, nil
}

// Image decodes, scales (long edge at most MaxImageEdge, never up), and
// re-encodes an image. It returns the block, then the original width and
// height. The output is JPEG quality 85, or PNG when any source pixel is not
// opaque. A GIF yields its first frame.
func Image(abs, rel string) (core.ContentBlock, int, int, error) {
	f, err := os.Open(abs)
	if err != nil {
		return core.ContentBlock{}, 0, 0, err
	}
	defer func() { _ = f.Close() }()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return core.ContentBlock{}, 0, 0, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return core.ContentBlock{}, 0, 0, fmt.Errorf("image %s: invalid size %dx%d", rel, cfg.Width, cfg.Height)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return core.ContentBlock{}, 0, 0, fmt.Errorf("image %s: %dx%d exceeds %d pixels", rel, cfg.Width, cfg.Height, maxImagePixels)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return core.ContentBlock{}, 0, 0, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return core.ContentBlock{}, 0, 0, err
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	transparent := hasTransparency(src)

	dstW, dstH := w, h
	if long := max(w, h); long > MaxImageEdge {
		if w >= h {
			dstW = MaxImageEdge
			dstH = int(math.Round(float64(h) * MaxImageEdge / float64(w)))
		} else {
			dstH = MaxImageEdge
			dstW = int(math.Round(float64(w) * MaxImageEdge / float64(h)))
		}
		dstW = max(dstW, 1)
		dstH = max(dstH, 1)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	if dstW == w && dstH == h {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	}

	var buf bytes.Buffer
	var mt string
	if transparent {
		mt = "image/png"
		err = png.Encode(&buf, dst)
	} else {
		mt = "image/jpeg"
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return core.ContentBlock{}, 0, 0, err
	}
	return withPath(core.ContentBlock{Kind: core.BlockImage, MediaType: mt, Data: buf.Bytes()}, rel), w, h, nil
}

// PDFText returns the plain text of each page, spaces trimmed. All empty
// strings mean a scanned PDF. A malformed file that panics the reader is
// returned as an error.
func PDFText(abs string) (pages []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			pages = nil
			err = fmt.Errorf("read pdf: %v", r)
		}
	}()

	f, r, err := pdf.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	n := r.NumPage()
	pages = make([]string, 0, n)
	for i := 1; i <= n; i++ {
		text, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			return nil, err
		}
		pages = append(pages, strings.TrimSpace(text))
	}
	return pages, nil
}

// Kind classifies a file from the first bytes of its content and its name:
// "image", "pdf", "text", or "other".
func Kind(head []byte, name string) string {
	mt := mediaType(head, name)
	switch mt {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return "image"
	case "application/pdf":
		return "pdf"
	}
	if !utf8.Valid(dropIncompleteRune(head)) {
		return "other"
	}
	if strings.HasPrefix(mt, "text/") || mt == "application/json" || textExt[strings.ToLower(filepath.Ext(name))] {
		return "text"
	}
	return "other"
}

// Placeholder is the text form of an image block for consumers that cannot
// show images.
func Placeholder(b core.ContentBlock) string {
	return "[image " + b.Path + "]"
}

// withPath returns b with its workspace-relative path set.
func withPath(b core.ContentBlock, rel string) core.ContentBlock {
	b.Path = rel
	return b
}

// mediaType returns the content type of an attachment. It trusts
// http.DetectContentType, except for the shapes it reports as plain text,
// where the extension picks a more specific type.
func mediaType(head []byte, name string) string {
	s := http.DetectContentType(head)
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	if s != "text/plain" {
		return s
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".xml":
		return "text/xml"
	case ".yaml", ".yml":
		return "text/yaml"
	default:
		return "text/plain"
	}
}

var textExt = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
	".java": true, ".rb": true, ".rs": true, ".c": true, ".h": true,
	".cpp": true, ".cs": true, ".php": true, ".sh": true, ".sql": true,
	".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".html": true,
	".css": true, ".md": true, ".csv": true, ".txt": true, ".log": true,
}

// cleanName reduces name to a safe file name: the part after the last path
// separator, without control characters, trimmed, and never empty, "." or "..".
func cleanName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "file"
	}
	return out
}

// headOf reads the first sniffLen bytes of abs.
func headOf(abs string) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return head[:n], nil
}

// readBounded reads at most max bytes of abs; a larger file is an error.
func readBounded(abs string, max int64) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file is larger than %d bytes", max)
	}
	return data, nil
}

// hasTransparency reports whether any source pixel has an alpha below full.
func hasTransparency(src image.Image) bool {
	b := src.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := src.At(x, y).RGBA(); a < 0xffff {
				return true
			}
		}
	}
	return false
}

// dropIncompleteRune removes a multi-byte rune truncated at the end of b, so a
// head cut mid-rune still reads as UTF-8.
func dropIncompleteRune(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
		c := b[len(b)-i]
		if c&0xC0 == 0x80 {
			continue // continuation byte, keep scanning for the lead
		}
		switch want := runeLen(c); {
		case want == 0:
			return b
		case want > i:
			return b[:len(b)-i]
		default:
			return b
		}
	}
	return b
}

// runeLen returns the encoded length of the rune whose lead byte is c, or 0
// when c cannot start a rune.
func runeLen(c byte) int {
	switch {
	case c < 0x80:
		return 1
	case c&0xE0 == 0xC0:
		return 2
	case c&0xF0 == 0xE0:
		return 3
	case c&0xF8 == 0xF0:
		return 4
	default:
		return 0
	}
}

// cutText applies the inline limit to a block body.
func cutText(body, rel string) string {
	r := []rune(body)
	if len(r) <= InlineChars {
		return body
	}
	notice := fmt.Sprintf("(Showing the first %d characters. Use read_file on %s with offset to read more.)", InlineChars, rel)
	return string(r[:InlineChars]) + "\n" + notice
}

// The block builders below and IsBlockText share these fixed pieces, so the
// text a block emits and the text IsBlockText accepts cannot drift apart.
const (
	attachPrefix = "Attachment "
	attachSep    = " ("
	attachClose  = "):"

	attachPDF   = "PDF, "
	attachPages = " pages"

	attachReadSep   = " could not be read: "
	attachSavedTail = ") was saved, but this file type cannot be read."
	attachScanTail  = ") has no text layer. It is probably a scan: ask the owner to send photos or screenshots of the pages instead."
)

// IsBlockText reports whether text is one of the attachment blocks the builders
// below write. Only the first line is inspected: it opens with the attachment
// prefix and carries a non-empty path followed by one of the five headers.
// Owner text such as "Attachment is fine, thanks" is not a block.
func IsBlockText(text string) bool {
	line, _, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(line, attachPrefix) {
		return false
	}
	body := line[len(attachPrefix):]
	switch {
	case strings.HasSuffix(body, attachClose):
		_, _, ok := attachmentHead(strings.TrimSuffix(body, attachClose))
		return ok
	case strings.HasSuffix(body, attachSavedTail):
		_, _, ok := attachmentHead(strings.TrimSuffix(body, attachSavedTail))
		return ok
	case strings.HasSuffix(body, attachScanTail):
		_, desc, ok := attachmentHead(strings.TrimSuffix(body, attachScanTail))
		return ok && strings.HasPrefix(desc, attachPDF) && strings.HasSuffix(desc, attachPages)
	case strings.Contains(body, attachReadSep):
		rel, _, _ := strings.Cut(body, attachReadSep)
		return rel != ""
	}
	return false
}

// attachmentHead splits the part of an attachment first line that follows the
// prefix into the workspace-relative path and the parenthesized descriptor. A
// path may itself contain " (", so the last separator wins.
func attachmentHead(body string) (rel, desc string, ok bool) {
	i := strings.LastIndex(body, attachSep)
	if i <= 0 {
		return "", "", false
	}
	rel, desc = body[:i], body[i+len(attachSep):]
	return rel, desc, rel != "" && desc != ""
}

// couldNotRead is the block for an image or PDF that could not be read.
func couldNotRead(rel string, err error) core.ContentBlock {
	msg := strings.TrimSuffix(err.Error(), ".")
	return core.ContentBlock{
		Kind: core.BlockText,
		Text: attachPrefix + rel + attachReadSep + msg + ".",
	}
}

// scannedPDF is the block for a PDF with no text layer.
func scannedPDF(rel string, pages int) core.ContentBlock {
	return core.ContentBlock{
		Kind: core.BlockText,
		Text: attachPrefix + rel + attachSep + attachPDF + strconv.Itoa(pages) + attachPages + attachScanTail,
	}
}

// savedOther is the block for a file that was saved but cannot be read.
func savedOther(rel, mt string) core.ContentBlock {
	return core.ContentBlock{
		Kind: core.BlockText,
		Text: attachPrefix + rel + attachSep + mt + attachSavedTail,
	}
}

// textBlock is the block for a text file.
func textBlock(rel, mt, content string) core.ContentBlock {
	return core.ContentBlock{
		Kind: core.BlockText,
		Text: attachPrefix + rel + attachSep + mt + attachClose + "\n" + cutText(content, rel),
	}
}

// pdfTextBlock is the block for a PDF with text.
func pdfTextBlock(rel string, pages []string) core.ContentBlock {
	var b strings.Builder
	for i, page := range pages {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "--- page %d ---", i+1)
		b.WriteByte('\n')
		b.WriteString(page)
	}
	return core.ContentBlock{
		Kind: core.BlockText,
		Text: attachPrefix + rel + attachSep + attachPDF + strconv.Itoa(len(pages)) + attachPages + attachClose + "\n" + cutText(b.String(), rel),
	}
}

// allEmpty reports whether every page string is empty.
func allEmpty(pages []string) bool {
	for _, p := range pages {
		if p != "" {
			return false
		}
	}
	return true
}

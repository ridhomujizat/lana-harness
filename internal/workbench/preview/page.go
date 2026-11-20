// The generated preview pages (REQ-WP-005): a Markdown file rendered with
// goldmark, a text file shown as code with line numbers, and an image centred
// on the dark background. Each page is a full HTML document with no external
// scripts or links.
package preview

import (
	"bytes"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// mdRenderer renders Markdown with the GFM extension (tables, task lists,
// strikethrough, autolinks). Raw HTML is dropped because WithUnsafe is not set
// (REQ-WP-005). Its Convert is safe to call from several goroutines.
var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

// RenderMarkdownPage renders a Markdown file as a full HTML document in the
// dark style (REQ-WP-005).
func RenderMarkdownPage(source []byte) ([]byte, error) {
	var body bytes.Buffer
	if err := mdRenderer.Convert(source, &body); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(markdownHead)
	out.Write(body.Bytes())
	out.WriteString(pageFoot)
	return out.Bytes(), nil
}

// RenderTextPage renders a text file as a page of lines with a gutter of line
// numbers (REQ-WP-005). Every row carries the id L<n>, so the browser scrolls
// to the "#L<n>" fragment and highlights it through the :target rule; the
// line given also gets the hl class when it is known.
func RenderTextPage(source []byte, line int) []byte {
	var out bytes.Buffer
	out.WriteString(textHead)
	for i, text := range strings.Split(string(source), "\n") {
		n := i + 1
		cls := "row"
		if n == line {
			cls = "row hl"
		}
		fmt.Fprintf(&out, "<span class=%q id=%q><span class=\"ln\">%d</span><span class=\"src\">%s</span></span>",
			cls, "L"+strconv.Itoa(n), n, html.EscapeString(strings.TrimSuffix(text, "\r")))
	}
	out.WriteString(textFoot)
	return out.Bytes()
}

// RenderImagePage renders the page an image is shown in (REQ-WP-005): a flex
// container centring the picture on the dark background. src is the raw URL of
// the file, so the bytes are served by the GET handler.
func RenderImagePage(src string) []byte {
	var out bytes.Buffer
	out.WriteString(imageHead)
	fmt.Fprintf(&out, "<img src=\"%s\" alt=\"\">\n", html.EscapeString(src))
	out.WriteString(pageFoot)
	return out.Bytes()
}

// markdownHead is the page a Markdown file is rendered into (REQ-WP-005).
const markdownHead = `<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  html, body { background: #1D1C1B; }
  body { margin: 0; padding: 18px 22px; color: #ECEAE5;
    font: 14px/1.65 Geist, system-ui, -apple-system, "Segoe UI", sans-serif; }
  h1, h2, h3, h4 { line-height: 1.25; margin: 0.6em 0 0.4em; }
  a { color: #8AA8F0; }
  code { font-family: "Geist Mono", ui-monospace, Menlo, monospace; font-size: 0.92em; }
  pre { background: #262523; padding: 12px 14px; border-radius: 8px; overflow: auto; }
  pre code { font-size: 0.9em; }
  blockquote { margin: 0; padding-left: 12px; border-left: 3px solid #3A3936; color: #B5B2AB; }
  table { border-collapse: collapse; }
  th, td { border: 1px solid #3A3936; padding: 4px 9px; }
  img { max-width: 100%; }
  hr { border: none; border-top: 1px solid #3A3936; }
</style></head><body>
`

// textHead is the page a text file is rendered into: a <pre> of line numbers
// beside the code, in a monospace font (REQ-WP-005). The :target rule
// highlights the row the #L<n> fragment names.
const textHead = `<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  html, body { background: #1D1C1B; }
  body { margin: 0; color: #ECEAE5;
    font: 13px/1.5 "Geist Mono", ui-monospace, Menlo, monospace; }
  pre.code { margin: 0; padding: 0; font: inherit; white-space: normal; }
  .row { display: flex; }
  .row .ln { flex: 0 0 auto; width: 52px; padding-right: 14px; text-align: right;
    color: #6F6C66; user-select: none; position: sticky; left: 0; background: #1D1C1B; }
  .row .src { white-space: pre; padding-right: 18px; }
  .row.hl, .row:target { background: rgba(255, 255, 255, 0.08); }
  .row.hl .ln, .row:target .ln { background: #262523; }
</style></head><body><pre class="code">
`

// textFoot closes the text page.
const textFoot = `</pre></body></html>
`

// imageHead wraps an image so it is centred on the dark background
// (REQ-WP-005): the body is a flex container and the picture keeps its ratio,
// scaled down to fit the viewport.
const imageHead = `<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  html, body { background: #1D1C1B; margin: 0; height: 100%; }
  body { display: flex; align-items: center; justify-content: center; }
  img { max-width: 100%; max-height: 100vh; width: auto; height: auto; object-fit: contain; }
</style></head><body>
`

// pageFoot closes the Markdown and image pages.
const pageFoot = `</body></html>
`

// The generated preview pages (REQ-WP-005): a Markdown file rendered with
// goldmark, a text file shown as code with line numbers, and an image centred
// on the background. Each page is a full HTML document that follows the app's
// light/dark theme. The Markdown page loads one script from the app's own
// /assets (Mermaid diagrams); no page loads anything from another host.
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

// writeDocOpen writes the doctype and the opening <html> tag. A theme of
// exactly "light" or "dark" adds the matching data-theme attribute; any other
// value is ignored, so no caller input is ever echoed into the page.
func writeDocOpen(out *bytes.Buffer, theme string) {
	out.WriteString("<!doctype html>\n")
	switch theme {
	case "light", "dark":
		out.WriteString(`<html data-theme="` + theme + `">`)
	default:
		out.WriteString("<html>")
	}
}

// RenderMarkdownPage renders a Markdown file as a full HTML document in the
// app's light/dark theme (REQ-WP-005). theme is "light", "dark" or empty; any
// other value is ignored.
func RenderMarkdownPage(source []byte, theme string) ([]byte, error) {
	var body bytes.Buffer
	if err := mdRenderer.Convert(source, &body); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	writeDocOpen(&out, theme)
	out.WriteString(markdownHead)
	out.Write(body.Bytes())
	out.WriteString(markdownFoot)
	return out.Bytes(), nil
}

// RenderTextPage renders a text file as a page of lines with a gutter of line
// numbers (REQ-WP-005). theme is "light", "dark" or empty; any other value is
// ignored. Every row carries the id L<n>, so the browser scrolls to the
// "#L<n>" fragment and highlights it through the :target rule; the line given
// also gets the hl class when it is known.
func RenderTextPage(source []byte, line int, theme string) []byte {
	var out bytes.Buffer
	writeDocOpen(&out, theme)
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
// container centring the picture on the background. theme is "light", "dark"
// or empty; any other value is ignored. src is the raw URL of the file, so the
// bytes are served by the GET handler.
func RenderImagePage(src, theme string) []byte {
	var out bytes.Buffer
	writeDocOpen(&out, theme)
	out.WriteString(imageHead)
	fmt.Fprintf(&out, "<img src=\"%s\" alt=\"\">\n", html.EscapeString(src))
	out.WriteString(pageFoot)
	return out.Bytes()
}

// themeVars is the light/dark palette shared by the three pages (REQ-WP-005).
// Light is the default on :root; dark applies when the system asks for it and
// the page is not pinned to light, or when the page carries data-theme="dark".
const themeVars = `  :root {
    color-scheme: light;
    --bg: #fbfaf8;
    --code-bg: #f6f5f2;
    --line-bg: #eeede9;
    --border: #dad7d0;
    --text: #1d1c1a;
    --text-2: #57544e;
    --text-4: #8c8880;
    --link: #4b5bd6;
    --hl: rgba(0,0,0,0.06);
  }
  @media (prefers-color-scheme: dark) {
    :root:not([data-theme="light"]) {
      color-scheme: dark;
      --bg: #1D1C1B;
      --code-bg: #262523;
      --line-bg: #262523;
      --border: #3A3936;
      --text: #ECEAE5;
      --text-2: #B5B2AB;
      --text-4: #6F6C66;
      --link: #8AA8F0;
      --hl: rgba(255,255,255,0.08);
    }
  }
  :root[data-theme="dark"] {
    color-scheme: dark;
    --bg: #1D1C1B;
    --code-bg: #262523;
    --line-bg: #262523;
    --border: #3A3936;
    --text: #ECEAE5;
    --text-2: #B5B2AB;
    --text-4: #6F6C66;
    --link: #8AA8F0;
    --hl: rgba(255,255,255,0.08);
  }
`

// markdownHead is the page a Markdown file is rendered into (REQ-WP-005).
const markdownHead = `<head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
` + themeVars + `  html, body { background: var(--bg); }
  body { margin: 0; padding: 18px 22px; color: var(--text);
    font: 14px/1.65 Geist, system-ui, -apple-system, "Segoe UI", sans-serif; }
  h1, h2, h3, h4 { line-height: 1.25; margin: 0.6em 0 0.4em; }
  a { color: var(--link); }
  code { font-family: "Geist Mono", ui-monospace, Menlo, monospace; font-size: 0.92em; }
  pre { background: var(--code-bg); padding: 12px 14px; border-radius: 8px; overflow: auto; }
  pre code { font-size: 0.9em; }
  blockquote { margin: 0; padding-left: 12px; border-left: 3px solid var(--border); color: var(--text-2); }
  table { border-collapse: collapse; }
  th, td { border: 1px solid var(--border); padding: 4px 9px; }
  img { max-width: 100%; }
  hr { border: none; border-top: 1px solid var(--border); }
</style></head><body>
`

// markdownFoot closes the Markdown page. The page loads md-preview.js from the
// app's own /assets to draw Mermaid diagrams; nothing comes from another host
// (REQ-WP-005).
const markdownFoot = `<script src="/assets/vendor/md-preview.js" defer></script>
</body></html>
`

// textHead is the page a text file is rendered into: a <pre> of line numbers
// beside the code, in a monospace font (REQ-WP-005). The :target rule
// highlights the row the #L<n> fragment names.
const textHead = `<head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
` + themeVars + `  html, body { background: var(--bg); }
  body { margin: 0; color: var(--text);
    font: 13px/1.5 "Geist Mono", ui-monospace, Menlo, monospace; }
  pre.code { margin: 0; padding: 0; font: inherit; white-space: normal; }
  .row { display: flex; }
  .row .ln { flex: 0 0 auto; width: 52px; padding-right: 14px; text-align: right;
    color: var(--text-4); user-select: none; position: sticky; left: 0; background: var(--bg); }
  .row .src { white-space: pre; padding-right: 18px; }
  .row.hl, .row:target { background: var(--hl); }
  .row.hl .ln, .row:target .ln { background: var(--line-bg); }
</style></head><body><pre class="code">
`

// textFoot closes the text page.
const textFoot = `</pre></body></html>
`

// imageHead wraps an image so it is centred on the background (REQ-WP-005):
// the body is a flex container and the picture keeps its ratio, scaled down to
// fit the viewport.
const imageHead = `<head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
` + themeVars + `  html, body { background: var(--bg); margin: 0; height: 100%; }
  body { display: flex; align-items: center; justify-content: center; }
  img { max-width: 100%; max-height: 100vh; width: auto; height: auto; object-fit: contain; }
</style></head><body>
`

// pageFoot closes the image page.
const pageFoot = `</body></html>
`

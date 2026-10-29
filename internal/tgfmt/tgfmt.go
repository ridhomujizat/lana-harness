// Package tgfmt converts Markdown to Telegram HTML and splits long messages.
package tgfmt

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limit is the Telegram message length limit.
const Limit = 4096

var (
	codeSpanRE = regexp.MustCompile("`([^`\n]+)`")
	linkRE     = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	boldRE     = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	italicStar = regexp.MustCompile(`(^|[^\w*])\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	italicLine = regexp.MustCompile(`(^|[^\w_])_([^_\s](?:[^_\n]*[^_\s])?)_($|[^\w_])`)
	headingRE  = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	listRE     = regexp.MustCompile(`^(\s*)[-*]\s+(.*)$`)
	fenceRE    = regexp.MustCompile("^\\s*```\\s*([\\w+#.-]*)\\s*$")
	tagRE      = regexp.MustCompile(`<[^>]*>`)
	preOpenRE  = regexp.MustCompile(`<pre><code(?: class="[^"]*")?>`)
)

// ToHTML converts the Markdown subset of API-TG-003 to Telegram HTML.
func ToHTML(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	var code []string
	lang := ""
	for _, line := range lines {
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			if !inFence {
				inFence, lang, code = true, m[1], nil
				continue
			}
			out = append(out, preBlock(lang, code))
			inFence = false
			continue
		}
		if inFence {
			code = append(code, line)
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			out = append(out, "<b>"+inline(m[1])+"</b>")
			continue
		}
		if m := listRE.FindStringSubmatch(line); m != nil {
			out = append(out, m[1]+"• "+inline(m[2]))
			continue
		}
		out = append(out, inline(line))
	}
	if inFence {
		out = append(out, preBlock(lang, code))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func preBlock(lang string, code []string) string {
	open := "<pre><code>"
	if lang != "" {
		open = fmt.Sprintf(`<pre><code class="language-%s">`, html.EscapeString(lang))
	}
	return open + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>"
}

func inline(text string) string {
	var spans []string
	text = codeSpanRE.ReplaceAllStringFunc(text, func(m string) string {
		spans = append(spans, m[1:len(m)-1])
		return fmt.Sprintf("\x00%d\x00", len(spans)-1)
	})
	text = escape(text)
	text = linkRE.ReplaceAllString(text, `<a href="$2">$1</a>`)
	text = boldRE.ReplaceAllStringFunc(text, func(m string) string {
		return "<b>" + m[2:len(m)-2] + "</b>"
	})
	text = italicStar.ReplaceAllString(text, "$1<i>$2</i>")
	text = italicLine.ReplaceAllString(text, "$1<i>$2</i>$3")
	for i, span := range spans {
		text = strings.Replace(text, fmt.Sprintf("\x00%d\x00", i), "<code>"+escape(span)+"</code>", 1)
	}
	return text
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// Plain turns Telegram HTML back into plain text.
func Plain(s string) string { return html.UnescapeString(tagRE.ReplaceAllString(s, "")) }

// Split cuts HTML into parts of at most limit characters. A cut prefers a blank line,
// then a newline. A code block cut in two is closed and reopened.
func Split(s string, limit int) []string {
	var parts []string
	for utf8.RuneCountInString(s) > limit {
		runes := []rune(s)
		budget := limit - len("</code></pre>")
		head := string(runes[:budget])
		cut := strings.LastIndex(head, "\n\n")
		if cut <= 0 {
			cut = strings.LastIndex(head, "\n")
		}
		if cut <= 0 {
			cut = safeCut(head)
		}
		part, rest := s[:cut], strings.TrimLeft(s[cut:], "\n")
		if open := openPre(part); open != "" {
			part += "</code></pre>"
			rest = open + rest
		}
		parts = append(parts, strings.TrimSpace(part))
		s = rest
	}
	if strings.TrimSpace(s) != "" {
		parts = append(parts, strings.TrimSpace(s))
	}
	return parts
}

// safeCut returns a byte index in head that is not inside a tag or an entity.
func safeCut(head string) int {
	cut := len(head)
	if i := strings.LastIndex(head, "<"); i > strings.LastIndex(head, ">") {
		cut = i
	}
	if i := strings.LastIndex(head[:cut], "&"); i >= 0 && !strings.Contains(head[i:cut], ";") {
		cut = i
	}
	return cut
}

// openPre returns the opening tag of a code block left open at the end of s, or "".
func openPre(s string) string {
	opens := preOpenRE.FindAllString(s, -1)
	if len(opens) > strings.Count(s, "</pre>") {
		return opens[len(opens)-1]
	}
	return ""
}

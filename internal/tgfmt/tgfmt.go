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
	codeSpanRE  = regexp.MustCompile("`([^`\n]+)`")
	linkRE      = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	boldRE      = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	italicStar  = regexp.MustCompile(`(^|[^\w*])\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	italicLine  = regexp.MustCompile(`(^|[^\w_])_([^_\s](?:[^_\n]*[^_\s])?)_($|[^\w_])`)
	headingRE   = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	listRE      = regexp.MustCompile(`^(\s*)[-*]\s+(.*)$`)
	fenceRE     = regexp.MustCompile("^\\s*```\\s*([\\w+#.-]*)\\s*$")
	tagRE       = regexp.MustCompile(`<[^>]*>`)
	preOpenRE   = regexp.MustCompile(`<pre>(?:<code(?: class="[^"]*")?>)?`)
	tableSepRE  = regexp.MustCompile(`^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$`)
	plainLinkRE = regexp.MustCompile(`\[([^\]\n]+)\]\([^)\n]*\)`)
)

// ToHTML converts the Markdown subset of API-TG-003 to Telegram HTML.
func ToHTML(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	var code []string
	lang := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
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
		if strings.Contains(line, "|") && i+1 < len(lines) && tableSepRE.MatchString(lines[i+1]) {
			header := splitCells(line)
			j := i + 2
			var rows [][]string
			for j < len(lines) && strings.Contains(lines[j], "|") {
				rows = append(rows, splitCells(lines[j]))
				j++
			}
			out = append(out, renderTable(header, rows))
			i = j - 1
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

// splitCells splits a Markdown table row on unescaped pipes and trims each cell.
func splitCells(line string) []string {
	s := strings.TrimPrefix(strings.TrimSpace(line), "|")
	if n := len(s); n > 0 && s[n-1] == '|' && (n < 2 || s[n-2] != '\\') {
		s = s[:n-1]
	}
	cells := make([]string, 0, strings.Count(s, "|")+1)
	var cell strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '|' {
			cell.WriteByte('|')
			i++
			continue
		}
		if s[i] == '|' {
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
			continue
		}
		cell.WriteByte(s[i])
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// renderTable converts a Markdown table to Telegram HTML. A table that fits in 40
// runes is rendered as a <pre> block; otherwise each body row becomes its own block.
func renderTable(header []string, rows [][]string) string {
	n := len(header)
	normHeader := normalizeRow(header, n)
	normRows := make([][]string, len(rows))
	for i, row := range rows {
		normRows[i] = normalizeRow(row, n)
	}
	widths := make([]int, n)
	for i := range widths {
		widths[i] = utf8.RuneCountInString(plainCell(normHeader[i]))
	}
	for _, row := range normRows {
		for i, cell := range row {
			if w := utf8.RuneCountInString(plainCell(cell)); w > widths[i] {
				widths[i] = w
			}
		}
	}
	if tableWidth(widths) <= 40 {
		return narrowTable(normHeader, normRows, widths)
	}
	return wideTable(normHeader, normRows)
}

// normalizeRow pads a row with empty cells or drops extra cells to n columns.
func normalizeRow(row []string, n int) []string {
	out := make([]string, n)
	copy(out, row)
	return out
}

// tableWidth returns the aligned width: sum of column widths + 3 × (columns − 1).
func tableWidth(widths []int) int {
	total := 3 * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	return total
}

// narrowTable renders an aligned table as a plain <pre> block.
func narrowTable(header []string, rows [][]string, widths []int) string {
	rowLine := func(cells []string) string {
		parts := make([]string, len(widths))
		for i, w := range widths {
			parts[i] = escape(padRight(plainCell(cells[i]), w))
		}
		return strings.TrimRight(strings.Join(parts, " │ "), " ")
	}
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, rowLine(header))
	sep := make([]string, len(widths))
	for i, w := range widths {
		dashes := w + 1
		if i > 0 && i < len(widths)-1 {
			dashes = w + 2
		}
		sep[i] = strings.Repeat("─", dashes)
	}
	lines = append(lines, strings.Join(sep, "┼"))
	for _, row := range rows {
		lines = append(lines, rowLine(row))
	}
	return "<pre>" + strings.Join(lines, "\n") + "</pre>"
}

// wideTable renders one block per body row with the first cell in bold.
func wideTable(header []string, rows [][]string) string {
	blocks := make([]string, 0, len(rows))
	for _, row := range rows {
		lines := make([]string, 0, len(header))
		if row[0] != "" {
			lines = append(lines, "<b>"+inline(row[0])+"</b>")
		}
		for i := 1; i < len(header); i++ {
			if row[i] != "" {
				lines = append(lines, inline(header[i])+": "+inline(row[i]))
			}
		}
		if len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// plainCell strips the inline Markdown markers from a table cell for narrow output.
func plainCell(s string) string {
	s = codeSpanRE.ReplaceAllString(s, "$1")
	s = plainLinkRE.ReplaceAllString(s, "$1")
	s = boldRE.ReplaceAllStringFunc(s, func(m string) string { return m[2 : len(m)-2] })
	s = italicStar.ReplaceAllString(s, "$1$2")
	return italicLine.ReplaceAllString(s, "$1$2$3")
}

// padRight pads s with spaces to width runes.
func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
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
			close := "</code></pre>"
			if open == "<pre>" {
				close = "</pre>"
			}
			part += close
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

// openPre returns the opening tag of a <pre> block left open at the end of s, or "".
func openPre(s string) string {
	opens := preOpenRE.FindAllStringIndex(s, -1)
	if len(opens) == 0 {
		return ""
	}
	last := opens[len(opens)-1]
	if i := strings.LastIndex(s, "</pre>"); i > last[0] {
		return ""
	}
	return s[last[0]:last[1]]
}

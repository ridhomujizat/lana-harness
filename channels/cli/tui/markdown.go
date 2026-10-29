package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var (
	reBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic = regexp.MustCompile(`\*([^\s*][^*\n]*[^\s*])\*`)
	reCode   = regexp.MustCompile("`([^`\\n]+)`")
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reOrder  = regexp.MustCompile(`^(\d+)[.)]\s+(.*)$`)
	reHead   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
)

// renderInline menerapkan gaya inline Markdown pada satu baris.
func renderInline(s string) string {
	if s == "" {
		return s
	}
	s = reLink.ReplaceAllString(s, "$1 ($2)")
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		return styleBold.Render(m[2 : len(m)-2])
	})
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		return styleCode.Render(strings.Trim(m, "`"))
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		return styleItalic.Render(m[1 : len(m)-1])
	})
	return s
}

// wrap mengubah satu baris bergaya menjadi beberapa baris selebar width.
func wrap(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	out := strings.Split(ansi.Wordwrap(s, width, ""), "\n")
	for i := range out {
		out[i] = strings.TrimRight(out[i], " ")
	}
	return out
}

// wrapPrefixed membungkus teks dengan prefiks pada baris pertama dan indentasi
// selebar prefiks pada baris berikutnya.
func wrapPrefixed(prefix, text string, width, indent int) []string {
	lines := wrap(text, width-indent)
	if len(lines) == 0 {
		return []string{prefix}
	}
	out := make([]string, 0, len(lines))
	out = append(out, prefix+lines[0])
	pad := strings.Repeat(" ", indent)
	for _, line := range lines[1:] {
		out = append(out, pad+line)
	}
	return out
}

// renderMarkdownLines merender blok Markdown menjadi baris terminal bergaya.
func renderMarkdownLines(text string, width int) []string {
	if width < 24 {
		width = 24
	}
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	inFence := false
	fenceLang := ""
	var code []string

	flushCode := func() {
		if len(code) == 0 {
			fenceLang = ""
			return
		}
		if fenceLang != "" {
			out = append(out, styleDim.Render(fenceLang))
		}
		for _, line := range code {
			out = append(out, styleDim.Render("│ ")+line)
		}
		code = nil
		fenceLang = ""
	}

	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if inFence {
				inFence = false
				flushCode()
			} else {
				inFence = true
				fenceLang = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "```"), "~~~"))
			}
			continue
		}
		if inFence {
			code = append(code, line)
			continue
		}
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if m := reHead.FindStringSubmatch(trimmed); m != nil {
			out = append(out, wrap(styleHeading.Render(stripInline(renderInline(m[2]))), width)...)
			continue
		}
		if isRule(trimmed) {
			n := width
			if n > 60 {
				n = 60
			}
			out = append(out, styleDim.Render(strings.Repeat("─", n)))
			continue
		}
		if strings.HasPrefix(trimmed, "|") {
			out = append(out, styleMuted.Render(trimmed))
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			quote := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			out = append(out, wrapPrefixed(styleDim.Render("│ "), styleItalic.Render(renderInline(quote)), width, 2)...)
			continue
		}
		if m := reOrder.FindStringSubmatch(trimmed); m != nil {
			prefix := m[1] + ". "
			out = append(out, wrapPrefixed(styleAccent.Render(prefix), renderInline(m[2]), width, len(prefix))...)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
			out = append(out, wrapPrefixed(styleAccent.Render("• "), renderInline(trimmed[2:]), width, 2)...)
			continue
		}
		out = append(out, wrap(renderInline(trimmed), width)...)
	}
	flushCode()
	for len(out) > 0 && strings.TrimSpace(ansi.Strip(out[len(out)-1])) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func isRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, marker := range []string{"-", "*", "_"} {
		if strings.Trim(s, marker) == "" {
			return true
		}
	}
	return false
}

func stripInline(s string) string {
	return strings.TrimSpace(ansi.Strip(s))
}

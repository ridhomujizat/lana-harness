package notify

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Ellipsis ends a cut excerpt.
const Ellipsis = "…"

var (
	// fencedCode is a fenced code block, ``` or ~~~, closed or left open at
	// the end of the text.
	fencedCode   = regexp.MustCompile("(?s)(```|~~~)[^\n]*\n?.*?(```|~~~|\\z)")
	inlineCode   = regexp.MustCompile("`([^`\n]*)`")
	imageLink    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	inlineLink   = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	refLink      = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
	autoLink     = regexp.MustCompile(`<(https?://[^>\s]+)>`)
	htmlTag      = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9-]*(\s[^<>]*)?/?>`)
	linePrefix   = regexp.MustCompile(`^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+\[[ xX]\]\s+|[-*+]\s+|\d{1,3}[.)]\s+)`)
	tableRule    = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	thematic     = regexp.MustCompile(`^\s{0,3}([-*_]\s*){3,}$`)
	strongMarks  = regexp.MustCompile(`(\*\*|__|~~)(\S(?:.*?\S)?)(\*\*|__|~~)`)
	emphasisStar = regexp.MustCompile(`(^|[^\w*])\*(\S(?:[^*]*\S)?)\*([^\w*]|$)`)
	emphasisUnd  = regexp.MustCompile(`(^|[^\w_])_(\S(?:[^_]*\S)?)_([^\w_]|$)`)
	refDef       = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s+\S+.*$`)
	headingLine  = regexp.MustCompile(`^\s{0,3}#{1,6}\s+\S`)
)

// Excerpt turns markdown into a plain one-line excerpt of at most max runes.
// Fenced code blocks become "[code]", links and images keep their text,
// heading, quote and list markers and emphasis marks are dropped, and every
// run of white space becomes one space. A longer text is cut at a word
// boundary when one is close and ends with "…". max <= 0 means no limit.
func Excerpt(markdown string, max int) string {
	text := strings.ReplaceAll(markdown, "\r\n", "\n")
	text = fencedCode.ReplaceAllString(text, "\n[code]\n")
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	headings := map[int]bool{}
	for _, line := range lines {
		if headingLine.MatchString(line) {
			headings[len(kept)] = true
		}
		if tableRule.MatchString(line) && strings.Contains(line, "-") && strings.Contains(line, "|") {
			continue
		}
		if thematic.MatchString(line) || refDef.MatchString(line) {
			continue
		}
		for {
			next := linePrefix.ReplaceAllString(line, "")
			if next == line {
				break
			}
			line = next
		}
		if strings.Contains(line, "|") && strings.Count(line, "|") >= 2 {
			cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			line = strings.Join(cells, " · ")
		}
		kept = append(kept, line)
	}
	// A heading reads as a lead-in on one line: "Summary: the fix …".
	for i := range kept {
		if !headings[i] || !hasTextAfter(kept, i) {
			continue
		}
		head := strings.TrimRight(kept[i], " #")
		if head != "" && !strings.ContainsAny(head[len(head)-1:], ".:!?") {
			kept[i] = head + ":"
		}
	}
	text = strings.Join(kept, "\n")
	text = imageLink.ReplaceAllString(text, "$1")
	text = inlineLink.ReplaceAllString(text, "$1")
	text = refLink.ReplaceAllString(text, "$1")
	text = autoLink.ReplaceAllString(text, "$1")
	text = inlineCode.ReplaceAllString(text, "$1")
	text = htmlTag.ReplaceAllString(text, "")
	for i := 0; i < 3; i++ {
		text = strongMarks.ReplaceAllString(text, "$2")
	}
	text = emphasisStar.ReplaceAllString(text, "$1$2$3")
	text = emphasisUnd.ReplaceAllString(text, "$1$2$3")
	text = strings.Join(strings.Fields(text), " ")
	return Cut(text, max)
}

// Cut shortens one line to at most max runes, the ellipsis included. It cuts
// at the last space when that keeps at least two thirds of the room, so a
// word is not split in the middle. max <= 0 means no limit.
func Cut(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return text
	}
	if max == 1 {
		return Ellipsis
	}
	runes := []rune(text)
	room := max - 1
	cut := room
	for i := room; i > room*2/3; i-- {
		if unicode.IsSpace(runes[i]) {
			cut = i
			break
		}
	}
	head := strings.TrimRightFunc(string(runes[:cut]), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) && r != ')' && r != ']' && r != '"'
	})
	if head == "" {
		head = string(runes[:room])
	}
	return head + Ellipsis
}

// OneLine collapses white space to single spaces and cuts the line to max
// runes. It does not touch markdown; use it for plain text such as a tool
// argument or an error.
func OneLine(text string, max int) string {
	return Cut(strings.Join(strings.Fields(text), " "), max)
}

func hasTextAfter(lines []string, i int) bool {
	for _, line := range lines[i+1:] {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

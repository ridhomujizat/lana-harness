package tools

import (
	"fmt"
	"strings"
)

const (
	// editHintMaxChars caps the not-found error text (REQ-TRL-005).
	editHintMaxChars = 2000
	// editHintWhitespaceLines caps the numbered lines shown by the
	// whitespace-insensitive hint.
	editHintWhitespaceLines = 30
	// editHintFirstLineMatches is how many first-line matches are shown.
	editHintFirstLineMatches = 3
	// editHintContext is the number of context lines around each match.
	editHintContext = 2
	// multipleMatchLineLimit is how many duplicate match lines are listed.
	multipleMatchLineLimit = 10
	// editSnippetMaxLines caps the changed region in a successful edit.
	editSnippetMaxLines = 40
)

// notFoundHint explains why old_string did not match and suggests a recovery
// step (REQ-TRL-005). The text is capped at editHintMaxChars characters.
func notFoundHint(path, content, old string) string {
	prefix := fmt.Sprintf("old_string not found in %s. ", path)
	body := whitespaceHint(content, old)
	if body == "" {
		body = firstLineHint(content, old)
	}
	if body == "" {
		body = "Read the file with read_file and copy old_string exactly."
	}
	return capHintText(prefix+body, editHintMaxChars)
}

// whitespaceHint reports a window of content that matches old once leading and
// trailing whitespace per line is ignored, showing the numbered lines.
func whitespaceHint(content, old string) string {
	lines := fileLines(content)
	oldLines := fileLines(old)
	if len(oldLines) == 0 || len(oldLines) > len(lines) {
		return ""
	}
	for start := 0; start+len(oldLines) <= len(lines); start++ {
		matches := true
		for i := range oldLines {
			if strings.TrimSpace(lines[start+i]) != strings.TrimSpace(oldLines[i]) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		end := start + len(oldLines)
		shown := end
		if shown-start > editHintWhitespaceLines {
			shown = start + editHintWhitespaceLines
		}
		var b strings.Builder
		fmt.Fprintf(&b, "It matches lines %d-%d after ignoring leading and trailing whitespace:\n", start+1, end)
		b.WriteString(formatNumberedLines(lines, start, shown))
		if shown < end {
			fmt.Fprintf(&b, "\n(Showing the first %d of %d lines.)", shown-start, len(oldLines))
		}
		return b.String()
	}
	return ""
}

// firstLineHint reports up to three file lines that contain the first non-blank
// line of old, each with editHintContext lines of numbered context.
func firstLineHint(content, old string) string {
	lines := fileLines(content)
	needle := ""
	for _, line := range fileLines(old) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			needle = trimmed
			break
		}
	}
	if needle == "" {
		return ""
	}
	var matches []int
	for i, line := range lines {
		if strings.Contains(strings.TrimSpace(line), needle) {
			matches = append(matches, i)
			if len(matches) == editHintFirstLineMatches {
				break
			}
		}
	}
	if len(matches) == 0 {
		return ""
	}
	numbers := make([]string, len(matches))
	for i, match := range matches {
		numbers[i] = fmt.Sprintf("%d", match+1)
	}
	label := "line"
	if len(numbers) > 1 {
		label = "lines"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The first line of old_string appears at %s %s. Here is the surrounding text:\n", label, strings.Join(numbers, ", "))
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n...\n")
		}
		start := match - editHintContext
		if start < 0 {
			start = 0
		}
		end := match + editHintContext + 1
		if end > len(lines) {
			end = len(lines)
		}
		b.WriteString(formatNumberedLines(lines, start, end))
	}
	return b.String()
}

// capHintText truncates s to limit characters (runes).
func capHintText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

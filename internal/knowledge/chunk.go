package knowledge

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// chunkRunes is the longest chunk text in runes (design §3).
const chunkRunes = 1500

// supersededSpan matches one struck-through span and an optional until mark
// directly after it on the same line (REQ-DKN-026). The span must not cross a
// line, so ".*?" stops at the first "~~" on its line.
var supersededSpan = regexp.MustCompile(`~~.*?~~(?:[ \t]*\*\(until \d{4}-\d{2}-\d{2}\)\*)?`)

// stripSuperseded removes the struck-through spans and their until marks from
// chunk text before it is stored, so search and snippets only hold the current
// facts (REQ-DKN-026). Removal stays within a line, so start_line is unchanged.
func stripSuperseded(text string) string {
	return supersededSpan.ReplaceAllString(text, "")
}

// Chunk is one indexed piece of a file. StartLine is the 1-based line of the
// chunk's first source line in the file; for PDFs it is the line in the
// extracted text (design §3).
type Chunk struct {
	Heading   string
	StartLine int
	Text      string
}

// ChunkMarkdown splits a markdown document at heading levels 1-3. Frontmatter
// is not chunk text, and the first chunk starts with "title — description"
// (design §3). raw is the whole file, so line numbers count from its first
// line.
func ChunkMarkdown(title, description, raw string) []Chunk {
	lines := splitLines(raw)
	start := 0
	if _, _, ok := splitFrontmatter(raw); ok {
		start = frontmatterBodyStart(lines)
	}

	// A heading of level 1-3 starts a new section that carries the heading
	// line and every following line until the next such heading.
	type section struct {
		heading string
		from    int
		to      int
	}
	var sections []section
	from, heading := start, ""
	flush := func(to int) {
		if sectionHasText(lines, from, to) {
			sections = append(sections, section{heading: heading, from: from, to: to})
		}
	}
	for i := start; i < len(lines); i++ {
		level, text, ok := parseHeading(lines[i])
		if !ok || level > 3 {
			continue
		}
		flush(i)
		from, heading = i, text
	}
	flush(len(lines))
	if len(sections) == 0 {
		sections = append(sections, section{from: start, to: len(lines)})
	}

	header := title
	if description != "" {
		header = title + " — " + description
	}
	var out []Chunk
	first := true
	for _, sec := range sections {
		numbered := make([]srcLine, 0, sec.to-sec.from)
		for i := sec.from; i < sec.to; i++ {
			numbered = append(numbered, srcLine{no: i + 1, text: lines[i]})
		}
		numbered = trimBlank(numbered)
		for _, group := range splitNumbered(numbered, chunkRunes) {
			text := joinSrcLines(group)
			if text == "" {
				continue
			}
			if first {
				text = header + "\n\n" + text
				first = false
			}
			out = append(out, Chunk{Heading: sec.heading, StartLine: group[0].no, Text: text})
		}
	}
	if len(out) == 0 {
		out = append(out, Chunk{StartLine: start + 1, Text: header})
	}
	return out
}

// ChunkText splits plain text (a .txt or a PDF's extracted text) at blank lines
// into chunks of at most chunkRunes (design §3).
func ChunkText(text string) []Chunk {
	lines := splitLines(text)
	numbered := make([]srcLine, len(lines))
	for i, line := range lines {
		numbered[i] = srcLine{no: i + 1, text: line}
	}
	var out []Chunk
	for _, group := range splitNumbered(trimBlank(numbered), chunkRunes) {
		if joined := joinSrcLines(group); joined != "" {
			out = append(out, Chunk{StartLine: group[0].no, Text: joined})
		}
	}
	return out
}

// trimBlank drops leading and trailing blank lines, keeping line numbers.
func trimBlank(lines []srcLine) []srcLine {
	for len(lines) > 0 && strings.TrimSpace(lines[0].text) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1].text) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// splitLines normalizes line endings and splits a document into lines.
func splitLines(raw string) []string {
	return strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
}

// frontmatterBodyStart returns the index of the first line after a leading
// frontmatter block, assuming one is present.
func frontmatterBodyStart(lines []string) int {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return i + 1
		}
	}
	return 0
}

// sectionHasText reports whether lines in [from, to) hold anything but blanks.
func sectionHasText(lines []string, from, to int) bool {
	for i := from; i < to && i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return true
		}
	}
	return false
}

// srcLine is a source line with its 1-based number, kept while splitting so a
// chunk can report where its text starts.
type srcLine struct {
	no   int
	text string
}

// splitNumbered splits lines into groups of at most max runes. It breaks at
// blank lines first, then hard-cuts a single line that is still too long.
func splitNumbered(lines []srcLine, max int) [][]srcLine {
	if len(lines) == 0 {
		return nil
	}
	if joinedRunes(lines) <= max {
		return [][]srcLine{lines}
	}

	// Paragraphs separated by blank lines are the units to pack.
	var units [][]srcLine
	var unit []srcLine
	for _, line := range lines {
		if strings.TrimSpace(line.text) == "" {
			if len(unit) > 0 {
				units = append(units, unit)
				unit = nil
			}
			continue
		}
		unit = append(unit, line)
	}
	if len(unit) > 0 {
		units = append(units, unit)
	}

	// A unit that is still too long is hard-cut.
	var pieces [][]srcLine
	for _, u := range units {
		if joinedRunes(u) <= max {
			pieces = append(pieces, u)
			continue
		}
		var piece []srcLine
		size := 0
		flushPiece := func() {
			if len(piece) > 0 {
				pieces = append(pieces, piece)
				piece, size = nil, 0
			}
		}
		for _, line := range u {
			runes := []rune(line.text)
			if len(runes) > max {
				flushPiece()
				for start := 0; start < len(runes); start += max {
					end := start + max
					if end > len(runes) {
						end = len(runes)
					}
					pieces = append(pieces, []srcLine{{no: line.no, text: string(runes[start:end])}})
				}
				continue
			}
			if size > 0 && size+len(runes)+1 > max {
				flushPiece()
			}
			if size > 0 {
				size++
			}
			piece = append(piece, line)
			size += len(runes)
		}
		flushPiece()
	}

	// Pack pieces into groups that fit max.
	var groups [][]srcLine
	var group []srcLine
	size := 0
	for _, p := range pieces {
		add := joinedRunes(p)
		if len(group) > 0 {
			add++
		}
		if len(group) > 0 && size+add > max {
			groups = append(groups, group)
			group, size = nil, 0
			add = joinedRunes(p)
		}
		group = append(group, p...)
		size += add
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	return groups
}

// joinedRunes is the rune count of lines joined with newlines.
func joinedRunes(lines []srcLine) int {
	total := 0
	for i, line := range lines {
		if i > 0 {
			total++
		}
		total += utf8.RuneCountInString(line.text)
	}
	return total
}

// joinSrcLines joins the text of numbered lines with newlines.
func joinSrcLines(lines []srcLine) string {
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = line.text
	}
	return strings.Join(parts, "\n")
}

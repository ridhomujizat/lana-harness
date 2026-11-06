package toolbox

import (
	"html"
	"regexp"
	"strings"
)

// timingRE matches a WebVTT or SRT cue timing line such as
// "00:00:01.120 --> 00:00:03.000 align:start" or "00:00:01,120 --> 00:00:03,000".
var timingRE = regexp.MustCompile(`^\s*(?:\d+:)?\d{1,2}:\d{2}[.,]\d+\s*-->`)

// tagRE matches an inline cue tag: <c>, </c>, <i>, <v Speaker> and the
// <00:00:01.120> word timestamps of YouTube auto captions.
var tagRE = regexp.MustCompile(`<[^>]*>`)

// assTagRE matches an ASS override tag some SRT files carry, such as {\an8}.
var assTagRE = regexp.MustCompile(`\{\\[^}]*\}`)

// Line lengths of the joined transcript: a line breaks after a sentence once
// it reaches softLineLen characters, and at the next cue once it reaches
// hardLineLen.
const (
	softLineLen = 80
	hardLineLen = 200
)

// SubtitlesToText turns a WebVTT or SRT document into plain transcript text
// (REQ-TB-012). It drops the header, NOTE/STYLE/REGION blocks, cue ids and
// numbers, timing lines and inline tags, decodes HTML entities, and drops the
// rolling duplicates of YouTube auto captions, where each cue repeats the
// previous cue's last line. The cue text is joined into readable lines. Text
// with no cue timing line is returned trimmed and otherwise unchanged.
func SubtitlesToText(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if !hasTimingLine(lines) {
		return strings.TrimSpace(text)
	}

	var fragments, cue, prevCue []string
	last := ""
	flush := func() {
		for _, line := range cue {
			if line == last || containsString(prevCue, line) {
				continue
			}
			fragments = append(fragments, line)
			last = line
		}
		if len(cue) > 0 {
			prevCue = cue
		}
		cue = nil
	}

	inCue := false
	for _, raw := range lines {
		switch {
		case timingRE.MatchString(raw):
			flush()
			inCue = true
		case raw == "":
			// Only a truly empty line ends a cue: YouTube auto captions put a
			// line holding one space inside a cue.
			if inCue {
				flush()
				inCue = false
			}
		case inCue:
			if line := cleanCueLine(raw); line != "" {
				cue = append(cue, line)
			}
		}
		// Lines outside a cue are the header, NOTE/STYLE/REGION blocks and cue
		// ids or SRT numbers; they are dropped.
	}
	flush()
	return joinFragments(fragments)
}

// hasTimingLine reports whether any line is a cue timing line.
func hasTimingLine(lines []string) bool {
	for _, line := range lines {
		if timingRE.MatchString(line) {
			return true
		}
	}
	return false
}

// cleanCueLine strips the tags of one cue text line, decodes its entities and
// collapses its whitespace.
func cleanCueLine(line string) string {
	line = tagRE.ReplaceAllString(line, "")
	line = assTagRE.ReplaceAllString(line, "")
	line = html.UnescapeString(line)
	line = strings.ReplaceAll(line, "\u00a0", " ")
	return strings.Join(strings.Fields(line), " ")
}

// joinFragments joins cue lines with spaces into readable lines: a line ends
// after a sentence once it is softLineLen long, or at hardLineLen.
func joinFragments(fragments []string) string {
	var out []string
	var line strings.Builder
	for _, fragment := range fragments {
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(fragment)
		if (line.Len() >= softLineLen && endsSentence(fragment)) || line.Len() >= hardLineLen {
			out = append(out, line.String())
			line.Reset()
		}
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	return strings.Join(out, "\n")
}

// endsSentence reports whether a fragment ends a sentence or a sung line.
func endsSentence(fragment string) bool {
	fragment = strings.TrimRight(fragment, `"')]”’`)
	return strings.HasSuffix(fragment, ".") || strings.HasSuffix(fragment, "!") ||
		strings.HasSuffix(fragment, "?") || strings.HasSuffix(fragment, "…") ||
		strings.HasSuffix(fragment, "♪")
}

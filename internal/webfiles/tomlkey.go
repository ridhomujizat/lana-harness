package webfiles

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ConfigWriteMu is the process-wide lock for config.toml writes. The web
// handlers and every other writer hold it across read, validate, and write, so
// two saves never interleave (REQ-AMCP-008).
var ConfigWriteMu sync.Mutex

// ErrTableNotFound is returned by SetTableKey when the requested table is not
// present in the data.
var ErrTableNotFound = errors.New("webfiles: table not found")

// SetTableKey replaces the value of key inside the table named by header (for
// example "[agents.lana]") with value. A multi-line array value ends at the
// matching "]" outside strings and comments. When key is absent from the table,
// it appends "key = value" at the end of the table body. It only searches the
// table's own keys, so a key that sits in a dotted sub-table is not touched.
// Every byte outside the replaced value stays identical, including comments,
// other keys, other tables, and line endings.
func SetTableKey(data []byte, header, key, value string) ([]byte, error) {
	target, err := requestedHeader(header)
	if err != nil {
		return nil, err
	}
	keyName, ok := parseKeyPath(key)
	if !ok || keyName == "" {
		return nil, fmt.Errorf("webfiles: invalid key %q", key)
	}
	lines := splitSourceLines(data)
	headers := scanHeaders(data, lines)
	index := tableHeaderIndex(headers, target)
	if index < 0 {
		return nil, fmt.Errorf("%w: %s", ErrTableNotFound, header)
	}
	first := headers[index].line
	bodyEnd := len(data)
	if index+1 < len(headers) {
		bodyEnd = lines[headers[index+1].line].start
	}
	text := string(data)
	state := tomlScanState{}
	for line := first + 1; line < len(lines) && lines[line].start < bodyEnd; line++ {
		lineText := text[lines[line].start:lines[line].end]
		if state.atTopLevel() {
			if name, equals, ok := assignmentKey(lineText); ok && name == keyName {
				valueStart := skipHorizontalSpace(text, lines[line].start+equals+1)
				valueEnd := valueEndOffset(text, valueStart)
				out := make([]byte, 0, len(data)-(valueEnd-valueStart)+len(value))
				out = append(out, data[:valueStart]...)
				out = append(out, value...)
				out = append(out, data[valueEnd:]...)
				return out, nil
			}
		}
		state.scanLine(lineText)
	}
	return appendTableKey(data, lines, first, bodyEnd, key, value), nil
}

// assignmentKey returns the canonical key and the byte offset of "=" when line
// is a key/value assignment at the top level of a table.
func assignmentKey(line string) (string, int, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", 0, false
	}
	equals := assignmentEquals(line)
	if equals < 0 {
		return "", 0, false
	}
	name, ok := parseKeyPath(line[:equals])
	if !ok {
		return "", 0, false
	}
	return name, equals, true
}

// assignmentEquals returns the offset of the first "=" outside strings and
// comments, or -1 when the line has none.
func assignmentEquals(line string) int {
	for i := 0; i < len(line); {
		switch line[i] {
		case '"':
			next, ok := skipBasicString(line, i)
			if !ok {
				return -1
			}
			i = next
		case '\'':
			next, ok := skipLiteralString(line, i)
			if !ok {
				return -1
			}
			i = next
		case '#':
			return -1
		case '=':
			return i
		default:
			i++
		}
	}
	return -1
}

// skipHorizontalSpace returns the first offset at or after start that is not a
// space or a tab.
func skipHorizontalSpace(text string, start int) int {
	for start < len(text) && (text[start] == ' ' || text[start] == '\t') {
		start++
	}
	return start
}

// valueEndOffset returns the first offset at or after start that is outside the
// value: a newline or a comment at the top level. Brackets and braces inside
// basic strings, literal strings, and comments do not count.
func valueEndOffset(text string, start int) int {
	i := start
	arrayDepth := 0
	inlineDepth := 0
	multiBasic := false
	multiLiteral := false
	for i < len(text) {
		if multiBasic {
			if text[i] == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(text[i:], `"""`) {
				i = skipQuoteRun(text, i, '"')
				multiBasic = false
				continue
			}
			i++
			continue
		}
		if multiLiteral {
			if strings.HasPrefix(text[i:], `'''`) {
				i = skipQuoteRun(text, i, '\'')
				multiLiteral = false
				continue
			}
			i++
			continue
		}
		switch text[i] {
		case '#':
			if arrayDepth == 0 && inlineDepth == 0 {
				return trimValueSpace(text, start, i)
			}
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case '"':
			if strings.HasPrefix(text[i:], `"""`) {
				multiBasic = true
				i += 3
				continue
			}
			next, ok := skipBasicString(text, i)
			if !ok {
				return len(text)
			}
			i = next
		case '\'':
			if strings.HasPrefix(text[i:], `'''`) {
				multiLiteral = true
				i += 3
				continue
			}
			next, ok := skipLiteralString(text, i)
			if !ok {
				return len(text)
			}
			i = next
		case '[':
			arrayDepth++
			i++
		case ']':
			if arrayDepth > 0 {
				arrayDepth--
			}
			i++
		case '{':
			inlineDepth++
			i++
		case '}':
			if inlineDepth > 0 {
				inlineDepth--
			}
			i++
		case '\n':
			if arrayDepth == 0 && inlineDepth == 0 {
				return trimValueSpace(text, start, i)
			}
			i++
		default:
			i++
		}
	}
	return trimValueSpace(text, start, len(text))
}

// skipQuoteRun returns the offset after a run of quote bytes. A multi-line
// string may end with up to two extra quotes before its closing delimiter, so
// the whole run belongs to the value.
func skipQuoteRun(text string, i int, quote byte) int {
	for i < len(text) && text[i] == quote {
		i++
	}
	return i
}

// trimValueSpace moves end back over trailing horizontal space and the "\r" of
// a CRLF line ending, which sit outside the value.
func trimValueSpace(text string, start, end int) int {
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return end
}

// appendTableKey inserts "key = value" at the end of the table body, before the
// blank and comment lines that separate the table from the next header.
func appendTableKey(data []byte, lines []sourceLine, headerLine, bodyEnd int, key, value string) []byte {
	end := bodyEnd
	for last := len(lines) - 1; last > headerLine; last-- {
		if lines[last].start >= bodyEnd {
			continue
		}
		text := string(data[lines[last].start:lines[last].end])
		if !isBlankLine(text) && !isCommentLine(text) {
			break
		}
		end = lines[last].start
	}
	line := key + " = " + value
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	out := make([]byte, 0, len(data)+len(line)+1)
	out = append(out, data[:end]...)
	if end > 0 && data[end-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, line...)
	out = append(out, data[end:]...)
	return out
}

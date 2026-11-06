package webfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ErrStaleVersion is returned when expected_version does not match the bytes
// the caller read. The save handlers map it to 409 stale_version.
var ErrStaleVersion = errors.New("webfiles: stale config version")

// ConfigVersion returns the SHA-256 hex of data, the version string that
// API-WEB-013 and the entity save endpoints use.
func ConfigVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// VerifyVersion checks data against the expected version read by the caller.
// It returns ErrStaleVersion when they differ; the file is never changed here.
func VerifyVersion(data []byte, expected string) error {
	if ConfigVersion(data) != expected {
		return ErrStaleVersion
	}
	return nil
}

// RewriteTable replaces the span of the table headed by header (for example
// "[agents.coder]") with table, appends table when the table is missing, and
// validates the candidate by calling check, which the caller supplies. In
// production the caller passes a function that calls config.Check with the home
// directory and returns its error. It returns the candidate and the sorted
// changed key paths for the audit line. A rejected candidate returns the
// checker error and no candidate.
//
// Every byte outside the rewritten span stays the same (REQ-WEB-043,
// AC-WEB-045). A dotted sub-table such as "[agents.coder.rules]" belongs to
// the span of its parent.
func RewriteTable(ctx context.Context, check func(ctx context.Context, candidate []byte) error, data []byte, header, table string) ([]byte, []string, error) {
	target, err := requestedHeader(header)
	if err != nil {
		return nil, nil, err
	}
	var candidate []byte
	if span, ok := findTableSpan(data, target); ok {
		candidate = replaceSpan(data, span, table)
	} else {
		candidate = appendTable(data, table)
	}
	return checkCandidate(ctx, check, data, candidate)
}

// RewriteRoute replaces the [[routes]] entry whose match.chat_id equals chatID
// with entry and appends entry at the end of the [[routes]] group when no entry
// matches. The candidate is validated like RewriteTable: the caller supplies
// check, which in production calls config.Check with the home directory
// (REQ-WEB-043).
func RewriteRoute(ctx context.Context, check func(ctx context.Context, candidate []byte) error, data []byte, chatID int64, entry string) ([]byte, []string, error) {
	var candidate []byte
	if span, ok := findRouteEntry(data, chatID); ok {
		candidate = replaceSpan(data, span, entry)
	} else {
		candidate = appendRouteEntry(data, entry)
	}
	return checkCandidate(ctx, check, data, candidate)
}

// checkCandidate calls check and computes the changed key paths. A failed
// check returns the checker error unchanged so the handler can list file and
// key, matching the 422 validation_failed response. A nil checker is a
// programming error and returns an error instead of panicking.
func checkCandidate(ctx context.Context, check func(ctx context.Context, candidate []byte) error, old, candidate []byte) ([]byte, []string, error) {
	if check == nil {
		return nil, nil, errors.New("webfiles: nil checker")
	}
	if err := check(ctx, candidate); err != nil {
		return nil, nil, err
	}
	changed, err := changedKeyPaths(old, candidate)
	if err != nil {
		return nil, nil, err
	}
	return candidate, changed, nil
}

// tableHeader is a parsed table header. name is the canonical dotted key path;
// quoted segments keep their quotes.
type tableHeader struct {
	name  string
	array bool
}

// sourceLine is one line of the file, including its line ending.
type sourceLine struct {
	start int
	end   int
}

// headerAt is a detected table header and the line it starts on.
type headerAt struct {
	line   int
	header tableHeader
}

// sectionSpan is a half-open byte range [start, end) of one table or route
// entry.
type sectionSpan struct {
	start int
	end   int
}

// requestedHeader parses a caller header. Both "[agents.coder]" and
// "agents.coder" are accepted.
func requestedHeader(header string) (tableHeader, error) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return tableHeader{}, errors.New("webfiles: empty table header")
	}
	if strings.HasPrefix(trimmed, "[") {
		parsed, ok := parseTableHeader(trimmed)
		if !ok {
			return tableHeader{}, fmt.Errorf("webfiles: invalid table header %q", header)
		}
		return parsed, nil
	}
	name, ok := parseKeyPath(trimmed)
	if !ok {
		return tableHeader{}, fmt.Errorf("webfiles: invalid table header %q", header)
	}
	return tableHeader{name: name}, nil
}

// parseTableHeader parses one line as a table header. It accepts "[name]" and
// "[[name]]" with optional surrounding space and a trailing comment. A value
// line such as ["a", "b"], or [1, 2] fails because the name is not a dotted key.
func parseTableHeader(line string) (tableHeader, bool) {
	rest := strings.TrimSpace(line)
	header := tableHeader{}
	switch {
	case strings.HasPrefix(rest, "[["):
		header.array = true
		rest = rest[2:]
	case strings.HasPrefix(rest, "["):
		rest = rest[1:]
	default:
		return tableHeader{}, false
	}
	for i := 0; i < len(rest); {
		switch rest[i] {
		case '"':
			next, ok := skipBasicString(rest, i)
			if !ok {
				return tableHeader{}, false
			}
			i = next
		case '\'':
			next, ok := skipLiteralString(rest, i)
			if !ok {
				return tableHeader{}, false
			}
			i = next
		case '[':
			return tableHeader{}, false
		case ']':
			if !closeHeader(rest, i, header.array) {
				return tableHeader{}, false
			}
			name, ok := parseKeyPath(strings.TrimSpace(rest[:i]))
			if !ok {
				return tableHeader{}, false
			}
			header.name = name
			return header, true
		default:
			i++
		}
	}
	return tableHeader{}, false
}

// closeHeader checks the closing bracket sequence and the optional trailing
// comment of a header line.
func closeHeader(rest string, i int, array bool) bool {
	after := i + 1
	if array {
		if !strings.HasPrefix(rest[i:], "]]") {
			return false
		}
		after = i + 2
	}
	trailing := strings.TrimSpace(rest[after:])
	return trailing == "" || strings.HasPrefix(trailing, "#")
}

// parseKeyPath parses a dotted TOML key, with bare or quoted segments, and
// returns its canonical form.
func parseKeyPath(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	parts := make([]string, 0, 2)
	for i := 0; ; {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return "", false
		}
		start := i
		switch s[i] {
		case '"':
			next, ok := skipBasicString(s, i)
			if !ok {
				return "", false
			}
			i = next
		case '\'':
			next, ok := skipLiteralString(s, i)
			if !ok {
				return "", false
			}
			i = next
		default:
			for i < len(s) && isBareKeyChar(s[i]) {
				i++
			}
			if i == start {
				return "", false
			}
		}
		parts = append(parts, s[start:i])
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] != '.' {
			return "", false
		}
		i++
	}
	return strings.Join(parts, "."), true
}

func isBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func skipBasicString(s string, i int) (int, bool) {
	for i++; i < len(s); {
		switch s[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1, true
		default:
			i++
		}
	}
	return 0, false
}

func skipLiteralString(s string, i int) (int, bool) {
	for i++; i < len(s); i++ {
		if s[i] == '\'' {
			return i + 1, true
		}
	}
	return 0, false
}

// tomlScanState is the state that crosses lines while looking for headers.
// Single-line strings cannot cross a line, so only multi-line strings and
// multi-line arrays are tracked.
type tomlScanState struct {
	multiBasic   bool
	multiLiteral bool
	arrayDepth   int
}

// scanHeaders returns the table headers of data. A line is a header only when it
// starts outside a multi-line string or array. Header lines are not scanned for
// brackets, so the "]]" of a "[[table]]" header does not count as an array.
func scanHeaders(data []byte, lines []sourceLine) []headerAt {
	headers := make([]headerAt, 0, 4)
	state := tomlScanState{}
	for index, line := range lines {
		text := string(data[line.start:line.end])
		if state.atTopLevel() {
			if header, ok := parseTableHeader(text); ok {
				headers = append(headers, headerAt{line: index, header: header})
				continue
			}
		}
		state.scanLine(text)
	}
	return headers
}

func (s tomlScanState) atTopLevel() bool {
	return !s.multiBasic && !s.multiLiteral && s.arrayDepth == 0
}

// scanLine updates the multi-line state of one line. Comments end a line, and
// quoted strings hide their contents.
func (s *tomlScanState) scanLine(line string) {
	for i := 0; i < len(line); {
		if s.multiBasic {
			if line[i] == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], `"""`) {
				s.multiBasic = false
				i += 3
				continue
			}
			i++
			continue
		}
		if s.multiLiteral {
			if strings.HasPrefix(line[i:], `'''`) {
				s.multiLiteral = false
				i += 3
				continue
			}
			i++
			continue
		}
		switch line[i] {
		case '#':
			return
		case '"':
			if strings.HasPrefix(line[i:], `"""`) {
				s.multiBasic = true
				i += 3
				continue
			}
			next, ok := skipBasicString(line, i)
			if !ok {
				return
			}
			i = next
		case '\'':
			if strings.HasPrefix(line[i:], `'''`) {
				s.multiLiteral = true
				i += 3
				continue
			}
			next, ok := skipLiteralString(line, i)
			if !ok {
				return
			}
			i = next
		case '[':
			s.arrayDepth++
			i++
		case ']':
			if s.arrayDepth > 0 {
				s.arrayDepth--
			}
			i++
		default:
			i++
		}
	}
}

// findTableSpan returns the span of the first table whose header equals target.
func findTableSpan(data []byte, target tableHeader) (sectionSpan, bool) {
	lines := splitSourceLines(data)
	headers := scanHeaders(data, lines)
	index := tableHeaderIndex(headers, target)
	if index < 0 {
		return sectionSpan{}, false
	}
	return spanAt(data, lines, headers, index), true
}

// tableHeaderIndex returns the index of the first header equal to target, or -1
// when the data has no such table.
func tableHeaderIndex(headers []headerAt, target tableHeader) int {
	for index, header := range headers {
		if header.header == target {
			return index
		}
	}
	return -1
}

// spanAt returns the span of the header at index. The span includes dotted
// sub-tables (for example "[agents.coder.rules]" under "[agents.coder]") and
// stops at the next unrelated header or at EOF. Trailing blank and comment
// lines stay outside the span so comments and separators survive the rewrite.
func spanAt(data []byte, lines []sourceLine, headers []headerAt, index int) sectionSpan {
	target := headers[index].header
	first := headers[index].line
	stop := len(lines)
	for next := index + 1; next < len(headers); next++ {
		if strings.HasPrefix(headers[next].header.name, target.name+".") {
			continue
		}
		stop = headers[next].line
		break
	}
	end := len(data)
	if stop < len(lines) {
		end = lines[stop].start
	}
	for last := stop - 1; last > first; last-- {
		text := string(data[lines[last].start:lines[last].end])
		if !isBlankLine(text) && !isCommentLine(text) {
			break
		}
		end = lines[last].start
	}
	return sectionSpan{start: lines[first].start, end: end}
}

func isBlankLine(line string) bool {
	return strings.TrimSpace(line) == ""
}

func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

// findRouteEntry returns the span of the [[routes]] entry whose
// match.chat_id equals chatID.
func findRouteEntry(data []byte, chatID int64) (sectionSpan, bool) {
	lines := splitSourceLines(data)
	headers := scanHeaders(data, lines)
	for index, header := range headers {
		if header.header.name != "routes" || !header.header.array {
			continue
		}
		span := spanAt(data, lines, headers, index)
		if id, ok := routeEntryChatID(data[span.start:span.end]); ok && id == chatID {
			return span, true
		}
	}
	return sectionSpan{}, false
}

// routeEntryChatID parses one [[routes]] entry and returns its chat_id.
func routeEntryChatID(entry []byte) (int64, bool) {
	var document struct {
		Routes []struct {
			Match map[string]any `toml:"match"`
		} `toml:"routes"`
	}
	if err := toml.Unmarshal(entry, &document); err != nil {
		return 0, false
	}
	for _, route := range document.Routes {
		if id, ok := chatIDValue(route.Match["chat_id"]); ok {
			return id, true
		}
	}
	return 0, false
}

func chatIDValue(value any) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case int:
		return int64(number), true
	default:
		return 0, false
	}
}

// replaceSpan returns data with the span replaced by text. Text that does not
// end with a newline gets one so the next block stays on its own line. An empty
// text removes the span.
func replaceSpan(data []byte, span sectionSpan, text string) []byte {
	replacement := ensureTrailingNewline(text)
	out := make([]byte, 0, len(data)-(span.end-span.start)+len(replacement))
	out = append(out, data[:span.start]...)
	out = append(out, replacement...)
	out = append(out, data[span.end:]...)
	return out
}

// appendTable appends a new table at the end of the file after one blank line.
func appendTable(data []byte, table string) []byte {
	text := ensureTrailingNewline(table)
	if text == "" {
		return data
	}
	out := make([]byte, 0, len(data)+len(text)+2)
	out = append(out, data...)
	if len(data) > 0 {
		trailing := countTrailingNewlines(data)
		if trailing == 0 {
			out = append(out, '\n')
			trailing = 1
		}
		if trailing < 2 {
			out = append(out, '\n')
		}
	}
	out = append(out, text...)
	return out
}

// appendRouteEntry appends a [[routes]] entry after the last existing entry, so
// the entry stays inside the routes group. It falls back to the end of the file
// when the file has no routes yet.
func appendRouteEntry(data []byte, entry string) []byte {
	text := ensureTrailingNewline(entry)
	if text == "" {
		return data
	}
	lines := splitSourceLines(data)
	headers := scanHeaders(data, lines)
	last := -1
	for index, header := range headers {
		if header.header.name == "routes" && header.header.array {
			last = index
		}
	}
	if last < 0 {
		return appendTable(data, text)
	}
	position := spanAt(data, lines, headers, last).end
	out := make([]byte, 0, len(data)+len(text)+1)
	out = append(out, data[:position]...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, text...)
	out = append(out, data[position:]...)
	return out
}

func ensureTrailingNewline(text string) string {
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func countTrailingNewlines(data []byte) int {
	count := 0
	for i := len(data) - 1; i >= 0 && data[i] == '\n'; i-- {
		count++
	}
	return count
}

// splitSourceLines returns each line of data with its byte range, including
// the line ending. A trailing newline does not add an empty last line.
func splitSourceLines(data []byte) []sourceLine {
	lines := make([]sourceLine, 0, 16)
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, sourceLine{start: start, end: i + 1})
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, sourceLine{start: start, end: len(data)})
	}
	return lines
}

// changedKeyPaths compares the parsed old and new documents and returns the
// sorted dotted key paths whose value changed, appeared, or disappeared.
func changedKeyPaths(oldData, newData []byte) ([]string, error) {
	oldKeys, _ := flattenedKeys(oldData) // an unparsable old file contributes no keys
	newKeys, err := flattenedKeys(newData)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{}, len(oldKeys)+len(newKeys))
	for key := range oldKeys {
		keys[key] = struct{}{}
	}
	for key := range newKeys {
		keys[key] = struct{}{}
	}
	changed := make([]string, 0, len(keys))
	for key := range keys {
		if key == "" {
			continue
		}
		oldValue, hadOld := oldKeys[key]
		newValue, hasNew := newKeys[key]
		if !hadOld || !hasNew || !reflect.DeepEqual(oldValue, newValue) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func flattenedKeys(data []byte) (map[string]any, error) {
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	out := make(map[string]any)
	flattenKeys("", document, out)
	return out, nil
}

// flattenKeys turns a parsed TOML document into path/value leaves. Array
// positions use a numeric path segment, for example routes.0.match.chat_id.
func flattenKeys(prefix string, value any, out map[string]any) {
	switch node := value.(type) {
	case map[string]any:
		if len(node) == 0 {
			out[prefix] = node
			return
		}
		for key, child := range node {
			flattenKeys(joinKeyPath(prefix, key), child, out)
		}
	case []any:
		if len(node) == 0 {
			out[prefix] = node
			return
		}
		for index, child := range node {
			flattenKeys(joinKeyPath(prefix, fmt.Sprintf("%d", index)), child, out)
		}
	default:
		out[prefix] = value
	}
}

func joinKeyPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

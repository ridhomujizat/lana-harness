package knowledge

import (
	"encoding/json"
	"strings"
)

// decodeLLMJSON decodes a model reply into v, tolerating the small JSON
// mistakes a model makes: a surrounding ``` code fence, prose around the JSON,
// and raw control characters inside a string value (for example a literal
// newline in a "body" field, which is invalid JSON).
//
// It tries json.Unmarshal on the fenced and trimmed text first, then on the
// same text with raw control characters inside string literals repaired, and
// finally on the outermost JSON array or object extracted from the text and
// repaired the same way. It returns nil on the first success and the last error
// when every attempt fails.
func decodeLLMJSON(text string, v any) error {
	trimmed := stripFence(strings.TrimSpace(text))
	lastErr := json.Unmarshal([]byte(trimmed), v)
	if lastErr == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(repairJSONStrings(trimmed)), v); err == nil {
		return nil
	} else {
		lastErr = err
	}
	if value, ok := extractJSONValue(trimmed); ok {
		if err := json.Unmarshal([]byte(repairJSONStrings(value)), v); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

// repairJSONStrings replaces raw control characters (< 0x20) inside JSON string
// literals with their escape sequences: \n, \r, \t, or \u00XX for the rest.
// Bytes outside a string literal are left unchanged. Escapes are tracked so an
// escaped quote does not end the string.
func repairJSONStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				b.WriteByte(c)
				continue
			}
			switch {
			case c == '\\':
				escaped = true
				b.WriteByte(c)
			case c == '"':
				inString = false
				b.WriteByte(c)
			case c < 0x20:
				writeEscapedControl(&b, c)
			default:
				b.WriteByte(c)
			}
			continue
		}
		if c == '"' {
			inString = true
		}
		b.WriteByte(c)
	}
	return b.String()
}

// writeEscapedControl writes the JSON escape sequence of one control byte.
func writeEscapedControl(b *strings.Builder, c byte) {
	switch c {
	case '\n':
		b.WriteString(`\n`)
	case '\r':
		b.WriteString(`\r`)
	case '\t':
		b.WriteString(`\t`)
	default:
		const hex = "0123456789abcdef"
		b.WriteString(`\u00`)
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
}

// extractJSONValue returns the outermost JSON array or object in s: from the
// first "[" or "{", to the matching last "]" or "}" whichever applies.
func extractJSONValue(s string) (string, bool) {
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return "", false
	}
	switch s[start] {
	case '[':
		if end := strings.LastIndexByte(s, ']'); end > start {
			return s[start : end+1], true
		}
	case '{':
		if end := strings.LastIndexByte(s, '}'); end > start {
			return s[start : end+1], true
		}
	}
	return "", false
}

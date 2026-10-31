package agent

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// unknownToolEmptyText is the REQ-LPG-021 error for an empty tool name.
const unknownToolEmptyText = "Tool call rejected: the tool name was empty. " +
	"To call a tool, use a valid name from your tool list; otherwise reply in plain text."

// unknownToolSimilarity is the smallest normalized Levenshtein similarity that
// makes an offered name the "Did you mean" suggestion (REQ-LPG-021).
const unknownToolSimilarity = 0.7

// offeredToolListFull and offeredToolListShow cap the tool list in the
// unknown-name error (REQ-LPG-021).
const (
	offeredToolListFull = 15
	offeredToolListShow = 12
)

// normalizeToolName normalizes a tool name for exact repair matching
// (REQ-LPG-020): it trims spaces, quotes, and angle brackets, lowercases,
// turns '-' and spaces into '_', converts CamelCase to snake_case, and strips
// a trailing "_tool" or "tool".
func normalizeToolName(name string) string {
	trimmed := strings.Trim(name, " \t\r\n\"'`<>")
	runes := []rune(trimmed)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		if r == '-' || unicode.IsSpace(r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	out := strings.Trim(b.String(), "_")
	out = strings.TrimSuffix(out, "_tool")
	out = strings.TrimSuffix(out, "tool")
	return out
}

// mcpToolPart returns the "<tool>" part of an offered "mcp__<server>__<tool>"
// name (REQ-LPG-020).
func mcpToolPart(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", false
	}
	i := strings.Index(rest, "__")
	if i <= 0 || i+2 >= len(rest) {
		return "", false
	}
	return rest[i+2:], true
}

// repairToolName returns the single offered name that name normalizes to, or
// "" when no offered name or several offered names match. It never runs a
// fuzzy match (REQ-LPG-020).
func repairToolName(name string, offered []string) string {
	norm := normalizeToolName(name)
	if norm == "" {
		return ""
	}
	match := ""
	for _, candidate := range offered {
		if !toolNameMatches(norm, candidate) {
			continue
		}
		if match != "" {
			return ""
		}
		match = candidate
	}
	return match
}

// toolNameMatches reports whether the normalized name equals the offered name
// or the "<tool>" part of an offered MCP name.
func toolNameMatches(norm, offered string) bool {
	if normalizeToolName(offered) == norm {
		return true
	}
	part, ok := mcpToolPart(offered)
	return ok && normalizeToolName(part) == norm
}

// similarity is the normalized Levenshtein similarity of two names:
// 1 - editDistance/max(len) (design §3). It is 1 for equal names.
func similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	if longest == 0 {
		return 1
	}
	return 1 - float64(editDistance(a, b))/float64(longest)
}

// nearestTool returns the offered name most similar to name, or "" when none
// reaches the cutoff (REQ-LPG-021). Similarity is checked against the whole
// normalized name and against each underscore-separated word of an offered
// name, so a bare word such as "read" suggests "read_file".
func nearestTool(name string, offered []string) string {
	norm := normalizeToolName(name)
	if norm == "" {
		return ""
	}
	best := ""
	bestScore := 0.0
	for _, candidate := range offered {
		score := toolSimilarity(norm, candidate)
		if score < unknownToolSimilarity {
			continue
		}
		if score > bestScore || (score == bestScore && (best == "" || candidate < best)) {
			best, bestScore = candidate, score
		}
	}
	return best
}

// toolSimilarity is the best normalized Levenshtein similarity of norm against
// an offered name: the whole name, its MCP "<tool>" part, and its words.
func toolSimilarity(norm, offered string) float64 {
	best := similarity(norm, normalizeToolName(offered))
	if part, ok := mcpToolPart(offered); ok {
		if s := similarity(norm, normalizeToolName(part)); s > best {
			best = s
		}
	}
	for _, word := range strings.Split(normalizeToolName(offered), "_") {
		if s := similarity(norm, word); s > best {
			best = s
		}
	}
	return best
}

// editDistance returns the Levenshtein distance of a and b in runes.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// unknownToolText returns the REQ-LPG-021 error text for a call whose name
// repair found no single match.
func unknownToolText(name string, offered []string) string {
	if strings.TrimSpace(name) == "" {
		return unknownToolEmptyText
	}
	var b strings.Builder
	b.WriteString("Tool '")
	b.WriteString(name)
	b.WriteString("' does not exist.")
	if nearest := nearestTool(name, offered); nearest != "" {
		b.WriteString(" Did you mean '")
		b.WriteString(nearest)
		b.WriteString("'?")
	}
	b.WriteString(" Available tools: ")
	b.WriteString(offeredToolList(offered))
	b.WriteString(".")
	return b.String()
}

// offeredToolList returns the sorted offered names, shortened to the first 12
// plus a total when there are more than 15 (REQ-LPG-021).
func offeredToolList(offered []string) string {
	sorted := append([]string(nil), offered...)
	sort.Strings(sorted)
	if len(sorted) > offeredToolListFull {
		head := strings.Join(sorted[:offeredToolListShow], ", ")
		return fmt.Sprintf("%s, ... (%d total)", head, len(sorted))
	}
	return strings.Join(sorted, ", ")
}

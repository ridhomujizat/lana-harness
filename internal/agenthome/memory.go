package agenthome

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The memory view and relevance limits (REQ-ECX-021, REQ-ECX-025).
const (
	// MemoryInlineLimit is the largest memory, in code points, shown in full
	// in the head prompt. Above it the prompt carries an index instead.
	MemoryInlineLimit = 2000
	// MemoryIndexMax is the most index lines in the head prompt. The newest
	// entries win when memory holds more.
	MemoryIndexMax = 80
	// MemoryIndexLine is the longest first line of an index entry.
	MemoryIndexLine = 100
	// MemoryMinScore is the lowest relevance score an entry needs to be
	// offered as relevant memory or returned by a memory_read query.
	MemoryMinScore = 2.0
)

// MemoryView renders the agent memory for the head prompt. It returns the full
// text while it fits MemoryInlineLimit and an index of "m<n>: <first line>"
// lines otherwise. indexed reports which form was used (REQ-ECX-021).
func MemoryView(entries []string) (text string, indexed bool) {
	if Size(entries) <= MemoryInlineLimit {
		return Join(entries), false
	}
	start := 0
	if len(entries) > MemoryIndexMax {
		start = len(entries) - MemoryIndexMax
	}
	lines := make([]string, 0, len(entries)-start+1)
	for i := start; i < len(entries); i++ {
		lines = append(lines, fmt.Sprintf("m%d: %s", i+1, indexLine(entries[i])))
	}
	lines = append(lines, "Full text: memory_read with query or ids.")
	return strings.Join(lines, "\n"), true
}

// indexLine returns the first line of an entry, cut at MemoryIndexLine runes.
func indexLine(entry string) string {
	line, _, _ := strings.Cut(entry, "\n")
	runes := []rune(line)
	if len(runes) <= MemoryIndexLine {
		return line
	}
	return string(runes[:MemoryIndexLine])
}

// Ranked is one entry's relevance score. ID is the entry's 1-based position in
// file order, the same number memory_read shows as m<n> (REQ-ECX-025).
type Ranked struct {
	ID    int
	Score float64
}

// Rank scores entries against query and returns the entries at or above
// MemoryMinScore, best first. Ties keep file order.
func Rank(query string, entries []string) []Ranked {
	if len(entries) == 0 {
		return nil
	}
	df := make(map[string]int, len(entries))
	for _, entry := range entries {
		for _, token := range Tokens(entry) {
			df[token]++
		}
	}
	ranked := make([]Ranked, 0, len(entries))
	for i, entry := range entries {
		score := Score(query, entry, df, len(entries))
		if score < MemoryMinScore {
			continue
		}
		ranked = append(ranked, Ranked{ID: i + 1, Score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked
}

// Score is the relevance of one entry: the sum over tokens shared with query of
// 1 + ln(N/df), where N is the entry count and df the number of entries holding
// the token. df and n come from Rank; a missing token scores nothing
// (REQ-ECX-025).
func Score(query, entry string, df map[string]int, n int) float64 {
	if n <= 0 {
		return 0
	}
	held := make(map[string]bool)
	for _, token := range Tokens(entry) {
		held[token] = true
	}
	score := 0.0
	for _, token := range Tokens(query) {
		if !held[token] {
			continue
		}
		count := df[token]
		if count <= 0 {
			continue
		}
		score += 1 + math.Log(float64(n)/float64(count))
	}
	return score
}

// Tokens returns the distinct lower-case word tokens of s: runs of letters and
// digits that are at least 3 runes long and not in the stop-word list.
func Tokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, token := range fields {
		if utf8.RuneCountInString(token) < 3 || stopWords[token] || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

// stopWords is the fixed English and Indonesian list removed from tokens
// (REQ-ECX-025).
var stopWords = map[string]bool{
	// English.
	"about": true, "after": true, "again": true, "against": true, "all": true,
	"also": true, "and": true, "any": true, "are": true, "because": true,
	"been": true, "before": true, "being": true, "between": true, "both": true,
	"but": true, "can": true, "could": true, "did": true, "does": true,
	"doing": true, "down": true, "during": true, "each": true, "few": true,
	"for": true, "from": true, "further": true, "had": true, "has": true,
	"have": true, "having": true, "her": true, "here": true, "hers": true,
	"him": true, "his": true, "how": true, "into": true, "its": true,
	"just": true, "more": true, "most": true, "not": true, "now": true,
	"off": true, "once": true, "only": true, "other": true, "our": true,
	"out": true, "over": true, "own": true, "same": true, "she": true,
	"should": true, "some": true, "such": true, "than": true, "that": true,
	"the": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "those": true, "through": true,
	"too": true, "under": true, "until": true, "very": true, "was": true,
	"were": true, "what": true, "when": true, "where": true, "which": true,
	"while": true, "who": true, "whom": true, "why": true, "will": true,
	"with": true, "would": true, "you": true, "your": true, "yours": true,
	// Indonesian.
	"ada": true, "adalah": true, "agar": true, "akan": true, "anda": true,
	"antara": true, "atas": true, "atau": true, "bagi": true, "bahwa": true,
	"banyak": true, "bawah": true, "begitu": true, "belum": true, "bisa": true,
	"dalam": true, "dan": true, "dapat": true, "dari": true, "dengan": true,
	"dia": true, "dimana": true, "ini": true, "itu": true, "jadi": true,
	"jika": true, "juga": true, "kalau": true, "kami": true, "kamu": true,
	"karena": true, "kapan": true, "ke": true, "kita": true, "lebih": true,
	"maka": true, "masih": true, "mau": true, "mereka": true, "mungkin": true,
	"nah": true, "namun": true, "nya": true, "oleh": true, "pada": true,
	"para": true, "pun": true, "saat": true, "saja": true, "sangat": true,
	"saya": true, "sebagai": true, "sebuah": true, "sedang": true, "sejak": true,
	"setelah": true, "seperti": true, "sudah": true, "supaya": true, "tapi": true,
	"telah": true, "tentang": true, "terhadap": true, "tetapi": true, "tidak": true,
	"untuk": true, "yang": true,
}

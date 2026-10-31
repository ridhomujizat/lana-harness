package agenthome

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// REQ-ECX-021: memory is shown in full up to the inline limit and as an index
// above it.
func TestMemoryViewSwitch(t *testing.T) {
	full := []string{strings.Repeat("m", MemoryInlineLimit)}
	text, indexed := MemoryView(full)
	if indexed || text != Join(full) {
		t.Fatalf("at the inline limit: indexed=%v text=%q", indexed, text)
	}

	over := []string{strings.Repeat("m", MemoryInlineLimit+1)}
	text, indexed = MemoryView(over)
	if !indexed {
		t.Fatal("above the inline limit must be indexed")
	}
	if !strings.HasPrefix(text, "m1: "+strings.Repeat("m", MemoryIndexLine)) {
		t.Fatalf("index line = %q", text)
	}
	if !strings.HasSuffix(text, "\nFull text: memory_read with query or ids.") {
		t.Fatalf("index lacks the full-text line: %q", text)
	}
}

// REQ-ECX-021: BuildSections uses the same switch for the memory section.
func TestBuildMemoryViewSwitch(t *testing.T) {
	atLimit := strings.Repeat("m", MemoryInlineLimit)
	if got := Build(PromptInput{Agent: "a", Memory: []string{atLimit}}); !strings.Contains(got, "# Your memory\n"+atLimit) || strings.Contains(got, "Full text: memory_read") {
		t.Fatalf("memory at the inline limit is not shown in full:\n%s", got)
	}
	over := strings.Repeat("m", MemoryInlineLimit+1)
	got := Build(PromptInput{Agent: "a", Memory: []string{over}})
	if !strings.Contains(got, "# Your memory\nm1: "+strings.Repeat("m", MemoryIndexLine)) || !strings.Contains(got, "Full text: memory_read with query or ids.") {
		t.Fatalf("memory above the inline limit is not indexed:\n%s", got)
	}
	if strings.Contains(got, over) {
		t.Fatalf("indexed memory leaked the full text:\n%s", got)
	}
}

// REQ-ECX-021: the index shows at most MemoryIndexMax lines, newest first from
// the file-order id of the oldest shown entry.
func TestMemoryViewIndexCapsNewest(t *testing.T) {
	entries := make([]string, 100)
	for i := range entries {
		entries[i] = fmt.Sprintf("entry %03d %s", i+1, strings.Repeat("x", 20))
	}
	text, indexed := MemoryView(entries)
	if !indexed {
		t.Fatal("100 entries must be indexed")
	}
	lines := strings.Split(text, "\n")
	if len(lines) != MemoryIndexMax+1 {
		t.Fatalf("lines = %d, want %d index lines plus the full-text line", len(lines), MemoryIndexMax)
	}
	if !strings.HasPrefix(lines[0], "m21: entry 021 ") {
		t.Fatalf("first index line = %q, want the 21st entry", lines[0])
	}
	if !strings.HasPrefix(lines[MemoryIndexMax-1], "m100: entry 100 ") {
		t.Fatalf("last index line = %q, want the 100th entry", lines[MemoryIndexMax-1])
	}
	if strings.Contains(text, "m20:") {
		t.Fatalf("index kept an entry older than the newest %d:\n%s", MemoryIndexMax, text)
	}
}

// REQ-ECX-021: an index line holds only the first line of an entry, cut at
// MemoryIndexLine characters.
func TestMemoryViewIndexLineCut(t *testing.T) {
	entry := strings.Repeat("x", MemoryIndexLine+50) + "\n" + strings.Repeat("y", MemoryInlineLimit)
	text, indexed := MemoryView([]string{entry})
	if !indexed {
		t.Fatal("a long entry must be indexed")
	}
	want := "m1: " + strings.Repeat("x", MemoryIndexLine) + "\nFull text: memory_read with query or ids."
	if text != want {
		t.Fatalf("index = %q, want %q", text, want)
	}

	multiline := strings.Repeat("a", 50) + "\n" + strings.Repeat("b", MemoryInlineLimit)
	text, _ = MemoryView([]string{multiline})
	if !strings.HasPrefix(text, "m1: "+strings.Repeat("a", 50)+"\n") || strings.Contains(text, strings.Repeat("b", 10)) {
		t.Fatalf("index must use only the first line: %q", text)
	}
}

// REQ-ECX-025: tokens are lower-case, at least 3 runes, distinct, and drop the
// English and Indonesian stop words.
func TestTokens(t *testing.T) {
	got := Tokens("The server and the KENARI, yang dan dengan; a ab abc 123 ABC server")
	want := []string{"server", "kenari", "abc", "123"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

// REQ-ECX-025: a rare shared token outweighs a common one, and the best score
// comes first.
func TestRankRareTokenBeatsCommon(t *testing.T) {
	entries := make([]string, 10)
	for i := range entries {
		entries[i] = "server node"
	}
	entries[2] = "server beta node"        // m3
	entries[5] = "server kenari beta node" // m6

	ranked := Rank("server kenari beta", entries)
	if len(ranked) != 2 {
		t.Fatalf("ranked = %+v, want the two rare-token entries", ranked)
	}
	if ranked[0].ID != 6 || ranked[1].ID != 3 {
		t.Fatalf("ids = %d,%d want 6,3", ranked[0].ID, ranked[1].ID)
	}
	want := 1 + (1 + math.Log(10.0/1.0)) + (1 + math.Log(10.0/2.0))
	if math.Abs(ranked[0].Score-want) > 1e-9 {
		t.Fatalf("top score = %v, want %v", ranked[0].Score, want)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Fatalf("scores not best first: %+v", ranked)
	}
}

// REQ-ECX-025: entries below MemoryMinScore are not ranked.
func TestRankMinimumScore(t *testing.T) {
	common := []string{"server node", "server node"}
	if ranked := Rank("server", common); len(ranked) != 0 {
		t.Fatalf("a token present in every entry scored above the minimum: %+v", ranked)
	}
	if ranked := Rank("", common); len(ranked) != 0 {
		t.Fatalf("empty query ranked: %+v", ranked)
	}
	if ranked := Rank("kenari", nil); len(ranked) != 0 {
		t.Fatalf("no entries ranked: %+v", ranked)
	}
}

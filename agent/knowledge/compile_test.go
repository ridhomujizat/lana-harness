package knowledge

import (
	"strings"
	"testing"
)

// REQ-KNW-022: a ```json fence around the reply is stripped before parsing.
func TestParseCompiledOpsStripsFence(t *testing.T) {
	ops, err := parseCompiledOps("```json\n[{\"op\":\"create\",\"title\":\"A\"}]\n```")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Op != "create" || ops[0].Title != "A" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestParseCompiledOpsRejectsInvalid(t *testing.T) {
	for _, reply := range []string{"not json", `{"op":"create"}`, `[{"op":}]`} {
		if _, err := parseCompiledOps(reply); err == nil {
			t.Fatalf("parseCompiledOps(%q) accepted an invalid reply", reply)
		}
	}
}

// REQ-KNW-022: at most compileMaxParts parts are compiled; the source is cut.
func TestSplitPartsCapsAtEight(t *testing.T) {
	big := strings.Repeat("x", compilePartRunes)
	paras := make([]string, compileMaxParts+1)
	for i := range paras {
		paras[i] = big
	}
	parts, cut := splitParts(strings.Join(paras, "\n\n"))
	if !cut || len(parts) != compileMaxParts {
		t.Fatalf("parts = %d cut = %v, want %d", len(parts), cut, compileMaxParts)
	}
}

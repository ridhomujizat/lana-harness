package knowledge

import (
	"context"
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

// One operation with a wrong field type must not drop the whole reply.
func TestParseCompiledOpsSkipsMalformedElement(t *testing.T) {
	reply := `[{"op":"create","title":"A","aliases":"Budi"},{"op":"create","title":"B"}]`
	ops, err := parseCompiledOps(reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Title != "B" {
		t.Fatalf("ops = %+v, want only the well-formed operation", ops)
	}
}

// REQ-KNW-023: a failure in a later part does not lose the re-indexing of the
// pages an earlier part already wrote.
func TestCompileSyncsPagesWhenLaterPartFails(t *testing.T) {
	provider := &fakeProvider{
		text:  `[{"op":"create","title":"Topik Satu","type":"topic","description":"d","body":"isi"}]`,
		errAt: 2,
	}
	env := newVaultEnv(t, provider)
	big := strings.Repeat("x", compilePartRunes)
	if _, err := env.svc.compile(context.Background(), compileInput{
		Text:        big + "\n\n" + big,
		SourceLink:  "[[sources/x.md]]",
		SourceTitle: "x",
		SourcePage:  false,
		Agent:       "assistant",
		Actor:       "test",
	}); err == nil {
		t.Fatal("compile succeeded, want the part-2 provider error")
	}
	_, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := idx.Catalog(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range catalog {
		if strings.HasPrefix(line, "Topik Satu") {
			return
		}
	}
	t.Fatalf("page from part 1 was not re-indexed: %v", catalog)
}

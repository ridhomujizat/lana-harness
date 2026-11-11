package knowledge

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// AC-KGR-001: ParseLinks reads every target form and normalizes it.
func TestParseLinksTargets(t *testing.T) {
	raw := "# Page\n\n[[A]] and [[A|x]] and [[A#h]] and [[agents/dana/wiki/B]] and [[B.md]]\n"
	got := ParseLinks(raw)
	if len(got) != 5 {
		t.Fatalf("links = %d, want 5: %+v", len(got), got)
	}
	want := []struct {
		target string
		norm   string
	}{
		{"A", "a"},
		{"A", "a"},
		{"A", "a"},
		{"agents/dana/wiki/B", "agents/dana/wiki/b"},
		{"B.md", "b.md"},
	}
	for i, w := range want {
		if got[i].Target != w.target || got[i].Norm != w.norm || got[i].Ord != i {
			t.Fatalf("link %d = %+v, want target %q norm %q ord %d", i, got[i], w.target, w.norm, i)
		}
	}
}

// Design §1: links inside fenced code blocks are skipped.
func TestParseLinksSkipsFences(t *testing.T) {
	raw := "[[A]]\n```\n[[Fence]]\n```\n~~~\n[[Tilde]]\n~~~\n[[B]]\n"
	got := ParseLinks(raw)
	if len(got) != 2 || got[0].Target != "A" || got[1].Target != "B" {
		t.Fatalf("links = %+v, want A and B", got)
	}
}

// Design §1: the context drops a leading list marker and, on a long line, is a
// 200-rune window centred on the link with "…" at each cut end.
func TestParseLinksContext(t *testing.T) {
	short := ParseLinks("- [[A]] is a note\n")
	if len(short) != 1 || short[0].Context != "[[A]] is a note" {
		t.Fatalf("short context = %+v", short)
	}

	long := strings.Repeat("z", 150) + " [[Target]] " + strings.Repeat("y", 150) + "\n"
	got := ParseLinks(long)
	if len(got) != 1 {
		t.Fatalf("links = %+v", got)
	}
	ctx := got[0].Context
	if !strings.Contains(ctx, "[[Target]]") {
		t.Fatalf("context misses the link: %q", ctx)
	}
	if !strings.HasPrefix(ctx, "…") || !strings.HasSuffix(ctx, "…") {
		t.Fatalf("context is not cut at both ends: %q", ctx)
	}
	if n := utf8.RuneCountInString(ctx); n < 200 || n > 204 {
		t.Fatalf("context runes = %d, want ~200: %q", n, ctx)
	}
}

// AC-KGR-002: nodes, hubs (shared last), deduplicated edges, no self links, the
// broken count, the scope filter, and the journal and source toggles.
func TestGraphScopesEdgesHubs(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/A.md", "# A\n\n[[B]] [[C]] [[Missing]] [[A]]\n")
	writeVaultFile(t, dir, "wiki/B.md", "# B\n\n[[A]] [[A]]\n")
	writeVaultFile(t, dir, "agents/dana/wiki/C.md", "# C\n\n[[A]]\n")
	writeVaultFile(t, dir, "journal/2026-11-01.md", "# J\n\n[[A]]\n")
	writeVaultFile(t, dir, "sources/Note.md", "# Note\n\n[[B]]\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	g, err := idx.Graph(ctx, GraphQuery{})
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(g.Nodes) != 3 || g.Pages != 3 {
		t.Fatalf("nodes = %d, pages = %d; want 3 and 3", len(g.Nodes), g.Pages)
	}
	if len(g.Edges) != 4 || g.Links != 4 {
		t.Fatalf("edges = %+v, links = %d; want 4", g.Edges, g.Links)
	}
	if g.Broken != 1 {
		t.Fatalf("broken = %d, want 1", g.Broken)
	}
	if hasEdge(g, "wiki/A.md", "wiki/A.md") {
		t.Fatalf("self link kept: %+v", g.Edges)
	}
	for _, e := range [][2]string{
		{"wiki/A.md", "wiki/B.md"},
		{"wiki/A.md", "agents/dana/wiki/C.md"},
		{"wiki/B.md", "wiki/A.md"},
		{"agents/dana/wiki/C.md", "wiki/A.md"},
	} {
		if !hasEdge(g, e[0], e[1]) {
			t.Fatalf("edge %v missing: %+v", e, g.Edges)
		}
	}
	if n := nodeByID(g, "wiki/A.md"); n == nil || n.Degree != 4 {
		t.Fatalf("A degree = %v, want 4", n)
	}
	if len(g.Hubs) != 2 || g.Hubs[0].Scope != "dana" || g.Hubs[0].Pages != 1 ||
		g.Hubs[1].Scope != ScopeShared || g.Hubs[1].Pages != 2 {
		t.Fatalf("hubs = %+v", g.Hubs)
	}

	// A scope filter keeps only that scope's nodes; a link to a filtered-out
	// page is neither an edge nor broken.
	dana, err := idx.Graph(ctx, GraphQuery{Scope: "dana"})
	if err != nil {
		t.Fatalf("Graph(dana): %v", err)
	}
	if len(dana.Nodes) != 1 || dana.Nodes[0].ID != "agents/dana/wiki/C.md" || dana.Pages != 1 {
		t.Fatalf("dana nodes = %+v", dana.Nodes)
	}
	if dana.Links != 0 || dana.Broken != 0 || len(dana.Hubs) != 1 || dana.Hubs[0].Scope != "dana" {
		t.Fatalf("dana graph = %+v", dana)
	}

	// The journal and the source join the graph only when asked.
	withJournals, err := idx.Graph(ctx, GraphQuery{Journals: true})
	if err != nil {
		t.Fatalf("Graph(journals): %v", err)
	}
	if nodeByID(withJournals, "journal/2026-11-01.md") == nil || !hasEdge(withJournals, "journal/2026-11-01.md", "wiki/A.md") {
		t.Fatalf("journal not included: %+v", withJournals.Nodes)
	}
	if withJournals.Links != 5 {
		t.Fatalf("links with journals = %d, want 5", withJournals.Links)
	}
	withSources, err := idx.Graph(ctx, GraphQuery{Sources: true})
	if err != nil {
		t.Fatalf("Graph(sources): %v", err)
	}
	if nodeByID(withSources, "sources/Note.md") == nil || !hasEdge(withSources, "sources/Note.md", "wiki/B.md") {
		t.Fatalf("source not included: %+v", withSources.Nodes)
	}
}

// AC-KGR-002: over the cap the highest-degree nodes stay and Truncated is set.
func TestGraphMaxCap(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/X.md", "# X\n\n[[Y]]\n")
	writeVaultFile(t, dir, "wiki/Y.md", "# Y\n\n[[X]]\n")
	writeVaultFile(t, dir, "wiki/I1.md", "# I1\n\nIsi.\n")
	writeVaultFile(t, dir, "wiki/I2.md", "# I2\n\nIsi.\n")
	writeVaultFile(t, dir, "wiki/I3.md", "# I3\n\nIsi.\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	if err := idx.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	g, err := idx.Graph(context.Background(), GraphQuery{Max: 2})
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if !g.Truncated {
		t.Fatalf("not truncated")
	}
	if len(g.Nodes) != 2 || nodeByID(g, "wiki/X.md") == nil || nodeByID(g, "wiki/Y.md") == nil {
		t.Fatalf("kept nodes = %+v", g.Nodes)
	}
	if g.Links != 2 {
		t.Fatalf("links = %d, want 2", g.Links)
	}
}

// AC-KGR-003: outgoing, unresolved, and inbound links with context, and the
// scope preference when the same title sits in shared and in a domain scope.
func TestPageLinksScopePreference(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	writeVaultFile(t, dir, "wiki/B.md", "---\ntype: topic\naliases:\n  - Bee\n---\n# B\n\nIsi B.\n")
	writeVaultFile(t, dir, "agents/dana/wiki/B.md", "# B\n\nIsi B dana.\n")
	writeVaultFile(t, dir, "wiki/A.md", "# A\n\nSee [[B]], [[Bee]], [[Missing]].\n")
	writeVaultFile(t, dir, "agents/dana/wiki/D.md", "# D\n\nLink [[B]].\n")

	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now
	ctx := context.Background()
	if err := idx.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	out, unresolved, in, err := idx.PageLinks(ctx, "wiki/A.md")
	if err != nil {
		t.Fatalf("PageLinks(A): %v", err)
	}
	if len(out) != 1 || out[0].Path != "wiki/B.md" {
		t.Fatalf("A out = %+v", out)
	}
	if len(unresolved) != 1 || unresolved[0] != "Missing" {
		t.Fatalf("A unresolved = %+v", unresolved)
	}
	if len(in) != 0 {
		t.Fatalf("A in = %+v", in)
	}

	_, _, inB, err := idx.PageLinks(ctx, "wiki/B.md")
	if err != nil {
		t.Fatalf("PageLinks(shared B): %v", err)
	}
	if len(inB) != 1 || inB[0].Path != "wiki/A.md" || !strings.Contains(inB[0].Context, "[[B]]") {
		t.Fatalf("shared B in = %+v", inB)
	}

	_, _, inDana, err := idx.PageLinks(ctx, "agents/dana/wiki/B.md")
	if err != nil {
		t.Fatalf("PageLinks(dana B): %v", err)
	}
	if len(inDana) != 1 || inDana[0].Path != "agents/dana/wiki/D.md" {
		t.Fatalf("dana B in = %+v", inDana)
	}
}

// hasEdge reports whether the graph holds a directed edge.
func hasEdge(g Graph, source, target string) bool {
	for _, e := range g.Edges {
		if e.Source == source && e.Target == target {
			return true
		}
	}
	return false
}

// nodeByID returns the node with the given id, or nil.
func nodeByID(g Graph, id string) *GraphNode {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

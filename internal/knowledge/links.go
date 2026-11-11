package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// linkContextRunes is the longest context stored around a link (design §1).
const linkContextRunes = 200

// linkPattern matches a [[target]] wikilink, the same pattern as
// agent/knowledge/lint.go (design §1). Group 1 is the target without a
// #heading or |label.
var linkPattern = regexp.MustCompile(`\[\[([^\[\]|#]+)(#[^\]|]*)?(\|[^\]]*)?\]\]`)

// listMarker matches a leading markdown list marker (design §1).
var listMarker = regexp.MustCompile(`^(?:[-*+]|\d+\.)[ \t]+`)

// Link is one [[target]] in a vault markdown file.
type Link struct {
	Ord     int    // position in the file, from 0
	Target  string // raw target, trimmed, without #heading and |label
	Norm    string // lower-case Target; for a path target also without a trailing ".md"
	Context string // the source line, trimmed, at most 200 runes, centred on the link
}

// ParseLinks returns the links of a markdown document in file order. Lines
// inside fenced code blocks are skipped (design §1).
func ParseLinks(raw string) []Link {
	var out []Link
	inFence := false
	var fence rune
	for _, line := range splitLines(raw) {
		if c := fenceMarker(line); c != 0 {
			switch {
			case !inFence:
				inFence, fence = true, c
			case c == fence:
				inFence, fence = false, 0
			}
			continue
		}
		if inFence {
			continue
		}
		text := listMarker.ReplaceAllString(strings.TrimSpace(line), "")
		for _, m := range linkPattern.FindAllStringSubmatchIndex(text, -1) {
			target := strings.TrimSpace(text[m[2]:m[3]])
			if target == "" {
				continue
			}
			out = append(out, Link{
				Ord:     len(out),
				Target:  target,
				Norm:    normTarget(target),
				Context: linkContext(text, m[0], m[1]),
			})
		}
	}
	return out
}

// fenceMarker returns the fence rune when line opens or closes a fenced code
// block (three or more backticks or tildes), else 0.
func fenceMarker(line string) rune {
	s := strings.TrimLeft(line, " \t")
	if len(s) >= 3 && (s[0] == '`' || s[0] == '~') && s[1] == s[0] && s[2] == s[0] {
		return rune(s[0])
	}
	return 0
}

// normTarget lower-cases a link target and, for a path target, drops a trailing
// ".md" (design §1).
func normTarget(target string) string {
	n := strings.ToLower(target)
	if strings.Contains(n, "/") {
		n = strings.TrimSuffix(n, ".md")
	}
	return n
}

// linkContext returns line, at most linkContextRunes runes, centred on the link
// at byte range [start, end) with "…" at a cut end (design §1).
func linkContext(line string, start, end int) string {
	runes := []rune(line)
	if len(runes) <= linkContextRunes {
		return line
	}
	from := utf8.RuneCountInString(line[:start])
	to := utf8.RuneCountInString(line[:end])
	lo := from + (to-from)/2 - linkContextRunes/2
	hi := lo + linkContextRunes
	if lo < 0 {
		lo, hi = 0, linkContextRunes
	}
	if hi > len(runes) {
		lo, hi = len(runes)-linkContextRunes, len(runes)
	}
	ctx := string(runes[lo:hi])
	if lo > 0 {
		ctx = "…" + ctx
	}
	if hi < len(runes) {
		ctx += "…"
	}
	return ctx
}

// writeLinks replaces the stored links of one file inside tx (design §1).
func writeLinks(ctx context.Context, tx *sql.Tx, rel, raw string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM links WHERE src = ?", rel); err != nil {
		return err
	}
	for _, link := range ParseLinks(raw) {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO links (src, ord, target, norm, context) VALUES (?, ?, ?, ?, ?)",
			rel, link.Ord, link.Target, link.Norm, link.Context); err != nil {
			return err
		}
	}
	return nil
}

// FileRef is one indexed file that a link can resolve to (design §1).
type FileRef struct {
	Path    string
	Title   string
	Scope   string
	Kind    string
	Type    string
	Updated string
}

// resolver resolves link targets to indexed files (design §1).
type resolver struct {
	refs    []FileRef
	byTitle map[string][]FileRef // lower title and lower aliases
	byPath  map[string]FileRef   // lower path, with and without ".md"
	byExact map[string]FileRef   // lower path exactly
}

// loadResolver reads every indexed file into a resolver (design §1).
func (i *Index) loadResolver(ctx context.Context) (*resolver, error) {
	rows, err := i.db.QueryContext(ctx, "SELECT path, title, scope, kind, type, updated, aliases FROM files ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	r := &resolver{
		byTitle: map[string][]FileRef{},
		byPath:  map[string]FileRef{},
		byExact: map[string]FileRef{},
	}
	for rows.Next() {
		var (
			ref     FileRef
			aliases string
		)
		if err := rows.Scan(&ref.Path, &ref.Title, &ref.Scope, &ref.Kind, &ref.Type, &ref.Updated, &aliases); err != nil {
			return nil, err
		}
		r.refs = append(r.refs, ref)
		low := strings.ToLower(ref.Path)
		r.byExact[low] = ref
		r.byPath[low] = ref
		if bare := strings.TrimSuffix(low, ".md"); bare != low {
			if _, ok := r.byPath[bare]; !ok {
				r.byPath[bare] = ref
			}
		}
		r.addTitle(ref.Title, ref)
		for _, alias := range parseAliases(aliases) {
			r.addTitle(alias, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for key := range r.byTitle {
		list := r.byTitle[key]
		sort.Slice(list, func(a, b int) bool { return list[a].Path < list[b].Path })
	}
	return r, nil
}

// addTitle adds ref under a lower-case title or alias, ignoring blanks and
// duplicates.
func (r *resolver) addTitle(title string, ref FileRef) {
	key := strings.ToLower(strings.TrimSpace(title))
	if key == "" {
		return
	}
	for _, c := range r.byTitle[key] {
		if c.Path == ref.Path {
			return
		}
	}
	r.byTitle[key] = append(r.byTitle[key], ref)
}

// resolve returns the file a normalized target points at. A target with a "/"
// resolves by path only; otherwise by title or alias with candidates taken from
// the source scope first, then shared, then the lowest path (design §1).
func (r *resolver) resolve(srcScope, norm string) (FileRef, bool) {
	if norm == "" {
		return FileRef{}, false
	}
	if strings.Contains(norm, "/") {
		ref, ok := r.byPath[norm]
		return ref, ok
	}
	cands := r.byTitle[norm]
	if len(cands) == 0 {
		return FileRef{}, false
	}
	for _, c := range cands {
		if c.Scope == srcScope {
			return c, true
		}
	}
	for _, c := range cands {
		if c.Scope == ScopeShared {
			return c, true
		}
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.Path < best.Path {
			best = c
		}
	}
	return best, true
}

// parseAliases reads the JSON alias list stored on a file row.
func parseAliases(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// graphNodeMax is the default number of nodes Graph returns (design §1).
const graphNodeMax = 2000

// GraphQuery is one knowledge graph request (design §1).
type GraphQuery struct {
	Scope    string
	Journals bool
	Sources  bool
	Max      int
}

// GraphNode is one node of the knowledge graph (design §1).
type GraphNode struct {
	ID      string
	Title   string
	Scope   string
	Kind    string
	Type    string
	Updated string
	Degree  int
}

// GraphEdge is one resolved page-to-page link (design §1).
type GraphEdge struct {
	Source string
	Target string
}

// GraphHub is the page count of one scope (design §1).
type GraphHub struct {
	Scope string
	Pages int
}

// Graph is the knowledge graph of a vault (design §1).
type Graph struct {
	Nodes     []GraphNode
	Edges     []GraphEdge
	Hubs      []GraphHub
	Pages     int
	Links     int
	Broken    int
	Truncated bool
}

// Graph returns the knowledge graph of the index: page nodes (plus journals and
// sources when asked), resolved and deduplicated page-to-page edges, one hub per
// scope (shared last), and the page, link, and broken-link counts (design §1).
func (i *Index) Graph(ctx context.Context, q GraphQuery) (Graph, error) {
	res, err := i.loadResolver(ctx)
	if err != nil {
		return Graph{}, err
	}

	nodes := map[string]FileRef{}
	for _, ref := range res.refs {
		if !graphKindIncluded(ref.Kind, q.Journals, q.Sources) {
			continue
		}
		if !graphScope(q.Scope, ref.Scope) {
			continue
		}
		nodes[ref.Path] = ref
	}

	type outLink struct {
		src    string
		target string
		ok     bool
	}
	var links []outLink
	rows, err := i.db.QueryContext(ctx, "SELECT src, norm FROM links ORDER BY src, ord")
	if err != nil {
		return Graph{}, err
	}
	for rows.Next() {
		var src, norm string
		if err := rows.Scan(&src, &norm); err != nil {
			_ = rows.Close()
			return Graph{}, err
		}
		if _, ok := nodes[src]; !ok {
			continue
		}
		ref, found := res.resolve(ScopeOf(src), norm)
		switch {
		case !found:
			links = append(links, outLink{src: src})
		case ref.Path == src:
			// A self link is neither an edge nor broken.
		default:
			links = append(links, outLink{src: src, target: ref.Path, ok: true})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Graph{}, err
	}
	_ = rows.Close()

	edges := map[[2]string]bool{}
	for _, l := range links {
		if !l.ok {
			continue
		}
		if _, ok := nodes[l.target]; !ok {
			continue
		}
		edges[[2]string{l.src, l.target}] = true
	}

	truncated := false
	max := q.Max
	if max <= 0 {
		max = graphNodeMax
	}
	if len(nodes) > max {
		degree := map[string]int{}
		for e := range edges {
			degree[e[0]]++
			degree[e[1]]++
		}
		kept := make([]FileRef, 0, len(nodes))
		for _, ref := range nodes {
			kept = append(kept, ref)
		}
		sort.Slice(kept, func(a, b int) bool {
			da, db := degree[kept[a].Path], degree[kept[b].Path]
			if da != db {
				return da > db
			}
			if kept[a].Updated != kept[b].Updated {
				return kept[a].Updated > kept[b].Updated
			}
			return kept[a].Path < kept[b].Path
		})
		kept = kept[:max]
		keep := map[string]bool{}
		for _, ref := range kept {
			keep[ref.Path] = true
		}
		for path := range nodes {
			if !keep[path] {
				delete(nodes, path)
			}
		}
		for e := range edges {
			if !keep[e[0]] || !keep[e[1]] {
				delete(edges, e)
			}
		}
		truncated = true
	}

	degree := map[string]int{}
	for e := range edges {
		degree[e[0]]++
		degree[e[1]]++
	}
	broken := 0
	for _, l := range links {
		if !l.ok {
			if _, ok := nodes[l.src]; ok {
				broken++
			}
		}
	}

	g := Graph{Truncated: truncated, Broken: broken}
	for _, ref := range nodes {
		g.Nodes = append(g.Nodes, GraphNode{
			ID: ref.Path, Title: ref.Title, Scope: ref.Scope, Kind: ref.Kind,
			Type: ref.Type, Updated: ref.Updated, Degree: degree[ref.Path],
		})
		if ref.Kind == kindPage {
			g.Pages++
		}
	}
	sort.Slice(g.Nodes, func(a, b int) bool { return g.Nodes[a].ID < g.Nodes[b].ID })
	for e := range edges {
		g.Edges = append(g.Edges, GraphEdge{Source: e[0], Target: e[1]})
	}
	sort.Slice(g.Edges, func(a, b int) bool {
		if g.Edges[a].Source != g.Edges[b].Source {
			return g.Edges[a].Source < g.Edges[b].Source
		}
		return g.Edges[a].Target < g.Edges[b].Target
	})
	g.Links = len(g.Edges)

	present := map[string]bool{}
	pages := map[string]int{}
	for _, n := range g.Nodes {
		present[n.Scope] = true
		if n.Kind == kindPage {
			pages[n.Scope]++
		}
	}
	var scopes []string
	for scope := range present {
		if scope != ScopeShared {
			scopes = append(scopes, scope)
		}
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		g.Hubs = append(g.Hubs, GraphHub{Scope: scope, Pages: pages[scope]})
	}
	if present[ScopeShared] {
		g.Hubs = append(g.Hubs, GraphHub{Scope: ScopeShared, Pages: pages[ScopeShared]})
	}
	return g, nil
}

// graphKindIncluded reports whether a file kind is a graph node with the given
// toggles (design §1).
func graphKindIncluded(kind string, journals, sources bool) bool {
	switch kind {
	case kindPage:
		return true
	case kindJournal:
		return journals
	case kindSource:
		return sources
	}
	return false
}

// graphScope reports whether a file scope passes a graph scope filter (design §1).
func graphScope(want, scope string) bool {
	return want == "" || want == "all" || want == scope
}

// Backlink is one resolved inbound link with the context of the linking line
// (design §1).
type Backlink struct {
	FileRef
	Context string
}

// PageLinks returns the resolved outgoing links of a page in file order with the
// unresolved targets, and its inbound links with the context of the linking line
// (design §1).
func (i *Index) PageLinks(ctx context.Context, path string) (out []FileRef, unresolved []string, in []Backlink, err error) {
	res, err := i.loadResolver(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var (
		ref     FileRef
		aliases string
	)
	ref.Path = path
	err = i.db.QueryRowContext(ctx,
		"SELECT title, scope, kind, type, updated, aliases FROM files WHERE path = ?", path).
		Scan(&ref.Title, &ref.Scope, &ref.Kind, &ref.Type, &ref.Updated, &aliases)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, fmt.Errorf("knowledge index: page %q not found", path)
	}
	if err != nil {
		return nil, nil, nil, err
	}

	rows, err := i.db.QueryContext(ctx, "SELECT norm, target FROM links WHERE src = ? ORDER BY ord", path)
	if err != nil {
		return nil, nil, nil, err
	}
	seenOut := map[string]bool{}
	seenTarget := map[string]bool{}
	for rows.Next() {
		var norm, target string
		if err := rows.Scan(&norm, &target); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		found, ok := res.resolve(ref.Scope, norm)
		if !ok {
			if !seenTarget[norm] {
				seenTarget[norm] = true
				unresolved = append(unresolved, target)
			}
			continue
		}
		if seenOut[found.Path] {
			continue
		}
		seenOut[found.Path] = true
		out = append(out, found)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, err
	}
	_ = rows.Close()

	keys := pageKeys(ref, aliases)
	if len(keys) > 0 {
		args := make([]any, 0, len(keys))
		for _, key := range keys {
			args = append(args, key)
		}
		query := "SELECT src, norm, context FROM links WHERE norm IN (" + placeholders(len(keys)) + ") ORDER BY src, ord"
		rows, err := i.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, nil, nil, err
		}
		seenSrc := map[string]bool{}
		for rows.Next() {
			var src, norm, context string
			if err := rows.Scan(&src, &norm, &context); err != nil {
				_ = rows.Close()
				return nil, nil, nil, err
			}
			if src == path || seenSrc[src] {
				continue
			}
			found, ok := res.resolve(ScopeOf(src), norm)
			if !ok || found.Path != path {
				continue
			}
			srcRef, ok := res.byExact[strings.ToLower(src)]
			if !ok {
				continue
			}
			seenSrc[src] = true
			in = append(in, Backlink{FileRef: srcRef, Context: context})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		_ = rows.Close()
	}
	return out, unresolved, in, nil
}

// pageKeys returns the lower-case titles, aliases, and paths a link can use to
// reach a page (design §1).
func pageKeys(ref FileRef, aliases string) []string {
	set := map[string]bool{}
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			set[s] = true
		}
	}
	add(ref.Title)
	for _, alias := range parseAliases(aliases) {
		add(alias)
	}
	add(ref.Path)
	if bare := strings.TrimSuffix(strings.ToLower(ref.Path), ".md"); bare != strings.ToLower(ref.Path) {
		add(bare)
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

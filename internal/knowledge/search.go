package knowledge

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// Search bounds (REQ-KNW-031, -032, design §3).
const (
	keywordTop   = 40
	vectorTop    = 40
	rrfK         = 60.0
	snippetRunes = 300

	recallCosine    = 0.45
	recallMinTokens = 2
	recallTop       = 200

	queryTermMin = 2
	queryTermMax = 12
)

// Query is one search request. Kind and Type optionally narrow the results;
// "" means any (REQ-KNW-040). Scopes narrows by file scope; nil or empty means
// every scope (REQ-DKN-010).
type Query struct {
	Text   string
	Limit  int
	Kind   string
	Type   string
	Scopes []string
}

// Hit is one search result: a file with its best matching chunk (REQ-KNW-032).
type Hit struct {
	Path        string
	Title       string
	Kind        string
	Type        string
	Scope       string
	Description string
	Heading     string
	Snippet     string
	Updated     string
	StartLine   int
	Score       float64
}

// Search returns files, best first, each with its best chunk as a snippet
// (REQ-KNW-031, -032). Keyword and vector lists are fused with reciprocal rank
// fusion; an embedding error falls back to keyword-only.
func (i *Index) Search(ctx context.Context, q Query) ([]Hit, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 8
	}
	terms := queryTerms(q.Text)

	keyword, err := i.keywordHits(ctx, terms, q)
	if err != nil {
		return nil, err
	}

	var vector []vectorHit
	if i.Embedder != nil && i.EmbeddingModel != "" && strings.TrimSpace(q.Text) != "" {
		vectors, err := i.Embedder.Embed(ctx, i.EmbeddingModel, []string{q.Text})
		if err != nil {
			i.logger.Warn("knowledge search: embedding query failed; using keyword only", "error", err)
		} else if len(vectors) == 1 && len(vectors[0]) > 0 {
			vector, err = i.vectorHits(ctx, vectors[0], q)
			if err != nil {
				return nil, err
			}
		}
	}
	return i.fuse(ctx, keyword, vector, limit)
}

// keywordHit is one keyword-ranked chunk.
type keywordHit struct {
	chunkID   int64
	path      string
	title     string
	heading   string
	startLine int
	kind      string
	typ       string
	scope     string
	desc      string
	updated   string
	snippet   string
}

// vectorHit is one cosine-ranked chunk.
type vectorHit struct {
	chunkID int64
	path    string
	kind    string
	typ     string
	scope   string
	cosine  float64
}

// keywordHits runs the BM25 search. Title words weigh most, then the heading,
// then the text (design §3). No terms means no keyword list.
func (i *Index) keywordHits(ctx context.Context, terms []string, q Query) ([]keywordHit, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	query := `
SELECT c.id, c.path, c.title, c.heading, c.start_line, f.kind, f.type, f.scope, f.description, f.updated,
       snippet(chunks_fts, 2, '«', '»', '…', 20)
FROM chunks_fts
JOIN chunks c ON c.id = chunks_fts.rowid
JOIN files f ON f.path = c.path
WHERE chunks_fts MATCH ?`
	args := []any{matchTerms(terms)}
	if q.Kind != "" {
		query += " AND f.kind = ?"
		args = append(args, q.Kind)
	}
	if q.Type != "" {
		query += " AND f.type = ?"
		args = append(args, q.Type)
	}
	if len(q.Scopes) > 0 {
		query += " AND f.scope IN (" + placeholders(len(q.Scopes)) + ")"
		for _, scope := range q.Scopes {
			args = append(args, scope)
		}
	}
	query += " ORDER BY bm25(chunks_fts, 5.0, 2.0, 1.0) LIMIT ?"
	args = append(args, keywordTop)

	rows, err := i.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []keywordHit
	for rows.Next() {
		var h keywordHit
		if err := rows.Scan(&h.chunkID, &h.path, &h.title, &h.heading, &h.startLine, &h.kind, &h.typ, &h.scope, &h.desc, &h.updated, &h.snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// vectorHits returns the top cosine chunks for the query vector, filtered by
// kind, type, and scope (design §3).
func (i *Index) vectorHits(ctx context.Context, query []float32, q Query) ([]vectorHit, error) {
	m, err := i.matrix(ctx, i.EmbeddingModel)
	if err != nil {
		return nil, err
	}
	type scored struct {
		row   int
		score float64
	}
	cands := make([]scored, 0, len(m.rows))
	for idx := range m.rows {
		row := m.rows[idx]
		if q.Kind != "" && row.kind != q.Kind {
			continue
		}
		if q.Type != "" && row.typ != q.Type {
			continue
		}
		if !scopeAllowed(q.Scopes, row.scope) {
			continue
		}
		cands = append(cands, scored{row: idx, score: cosine(query, m.vecs[idx])})
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].score > cands[b].score })
	if len(cands) > vectorTop {
		cands = cands[:vectorTop]
	}
	out := make([]vectorHit, 0, len(cands))
	for _, c := range cands {
		row := m.rows[c.row]
		out = append(out, vectorHit{chunkID: row.chunkID, path: row.path, kind: row.kind, typ: row.typ, scope: row.scope, cosine: c.score})
	}
	return out, nil
}

// fuseCand is one chunk's fused score during fusion.
type fuseCand struct {
	chunkID int64
	path    string
	score   float64
	keyword *keywordHit
}

// fuse combines the keyword and vector lists with reciprocal rank fusion
// (design §3), keeps the best chunk per file, and returns the top limit files.
func (i *Index) fuse(ctx context.Context, keyword []keywordHit, vector []vectorHit, limit int) ([]Hit, error) {
	byChunk := map[int64]*fuseCand{}
	var order []int64
	get := func(id int64, rel string) *fuseCand {
		c, ok := byChunk[id]
		if !ok {
			c = &fuseCand{chunkID: id, path: rel}
			byChunk[id] = c
			order = append(order, id)
		}
		return c
	}
	for rank, h := range keyword {
		c := get(h.chunkID, h.path)
		c.score += rrf(rank)
		hit := h
		c.keyword = &hit
	}
	for rank, h := range vector {
		c := get(h.chunkID, h.path)
		c.score += rrf(rank)
	}

	best := map[string]*fuseCand{}
	for _, id := range order {
		c := byChunk[id]
		if b, ok := best[c.path]; !ok || c.score > b.score {
			best[c.path] = c
		}
	}
	ranked := make([]*fuseCand, 0, len(best))
	for _, c := range best {
		ranked = append(ranked, c)
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].score != ranked[b].score {
			return ranked[a].score > ranked[b].score
		}
		return ranked[a].path < ranked[b].path
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	out := make([]Hit, 0, len(ranked))
	for _, c := range ranked {
		hit, err := i.hitFor(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, nil
}

// hitFor renders one fused candidate. A keyword chunk carries its FTS snippet;
// a vector-only chunk shows the first snippetRunes of its text.
func (i *Index) hitFor(ctx context.Context, c *fuseCand) (Hit, error) {
	if c.keyword != nil {
		k := c.keyword
		return Hit{
			Path: k.path, Title: k.title, Kind: k.kind, Type: k.typ, Scope: k.scope, Description: k.desc,
			Heading: k.heading, Snippet: k.snippet, Updated: k.updated, StartLine: k.startLine, Score: c.score,
		}, nil
	}
	var (
		hit  Hit
		text string
	)
	err := i.db.QueryRowContext(ctx, `
SELECT c.title, c.heading, c.start_line, c.text, f.kind, f.type, f.scope, f.description, f.updated
FROM chunks c JOIN files f ON f.path = c.path WHERE c.id = ?`, c.chunkID).
		Scan(&hit.Title, &hit.Heading, &hit.StartLine, &text, &hit.Kind, &hit.Type, &hit.Scope, &hit.Description, &hit.Updated)
	if err != nil {
		return Hit{}, err
	}
	hit.Path = c.path
	hit.Score = c.score
	hit.Snippet = firstRunes(text, snippetRunes)
	return hit, nil
}

// rrf is one reciprocal rank fusion contribution for a 1-based rank.
func rrf(rank int) float64 { return 1 / (rrfK + float64(rank+1)) }

// vecRow is a vector's chunk identity and filters.
type vecRow struct {
	chunkID int64
	path    string
	kind    string
	typ     string
	scope   string
}

// vectorMatrix is the in-memory vectors of one model (design §3).
type vectorMatrix struct {
	rows []vecRow
	vecs [][]float32
}

// matrix loads and caches the vectors of a model. The cache is dropped when
// rows change.
func (i *Index) matrix(ctx context.Context, model string) (*vectorMatrix, error) {
	i.vecMu.Lock()
	defer i.vecMu.Unlock()
	if m, ok := i.vecCache[model]; ok && m != nil {
		return m, nil
	}
	rows, err := i.db.QueryContext(ctx, `
SELECT v.chunk_id, c.path, f.kind, f.type, f.scope, v.dim, v.vec
FROM vectors v JOIN chunks c ON c.id = v.chunk_id JOIN files f ON f.path = c.path
WHERE v.model = ?`, model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	m := &vectorMatrix{}
	for rows.Next() {
		var (
			row  vecRow
			dim  int
			blob []byte
		)
		if err := rows.Scan(&row.chunkID, &row.path, &row.kind, &row.typ, &row.scope, &dim, &blob); err != nil {
			return nil, err
		}
		m.rows = append(m.rows, row)
		m.vecs = append(m.vecs, decodeVec(blob, dim))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if i.vecCache == nil {
		i.vecCache = map[string]*vectorMatrix{}
	}
	i.vecCache[model] = m
	return m, nil
}

// recallCand is one page chunk considered by Recall.
type recallCand struct {
	chunkID   int64
	path      string
	scope     string
	matched   int
	cosine    float64
	title     string
	heading   string
	typ       string
	desc      string
	updated   string
	text      string
	startLine int
	haveMeta  bool
}

// Recall returns up to limit wiki pages relevant to text, best first, applying
// the recall rule: a page qualifies when its best chunk holds at least two
// distinct query tokens, or, with an embedder, has cosine >= 0.45
// (REQ-KNW-050). Only pages (kind page) are considered, and only those whose
// scope is in scopes (nil or empty means every scope). On an equal score a hit
// whose scope is prefer comes first (REQ-DKN-012).
func (i *Index) Recall(ctx context.Context, text string, limit int, scopes []string, prefer string) ([]Hit, error) {
	if limit <= 0 {
		limit = 3
	}
	terms := queryTerms(text)
	if len(terms) == 0 {
		return nil, nil
	}
	byChunk := map[int64]*recallCand{}
	keyword, err := i.recallKeyword(ctx, terms, scopes)
	if err != nil {
		return nil, err
	}
	for _, c := range keyword {
		byChunk[c.chunkID] = c
	}

	if i.Embedder != nil && i.EmbeddingModel != "" {
		vectors, err := i.Embedder.Embed(ctx, i.EmbeddingModel, []string{text})
		if err != nil {
			i.logger.Warn("knowledge recall: embedding query failed; using keyword only", "error", err)
		} else if len(vectors) == 1 && len(vectors[0]) > 0 {
			m, err := i.matrix(ctx, i.EmbeddingModel)
			if err != nil {
				return nil, err
			}
			for idx := range m.rows {
				row := m.rows[idx]
				if row.kind != kindPage || !scopeAllowed(scopes, row.scope) {
					continue
				}
				cos := cosine(vectors[0], m.vecs[idx])
				if c, ok := byChunk[row.chunkID]; ok {
					if cos > c.cosine {
						c.cosine = cos
					}
					continue
				}
				if cos >= recallCosine {
					byChunk[row.chunkID] = &recallCand{chunkID: row.chunkID, path: row.path, scope: row.scope, cosine: cos}
				}
			}
		}
	}

	best := map[string]*recallCand{}
	for _, c := range byChunk {
		if c.matched < recallMinTokens && c.cosine < recallCosine {
			continue
		}
		if b, ok := best[c.path]; !ok || recallBetter(c, b) {
			best[c.path] = c
		}
	}
	ranked := make([]*recallCand, 0, len(best))
	for _, c := range best {
		ranked = append(ranked, c)
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		x, y := ranked[a], ranked[b]
		xs, ys := recallScore(x), recallScore(y)
		if xs != ys {
			return xs > ys
		}
		xp, yp := x.scope == prefer, y.scope == prefer
		if xp != yp {
			return xp
		}
		return x.path < y.path
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	out := make([]Hit, 0, len(ranked))
	for _, c := range ranked {
		if !c.haveMeta {
			if err := i.fillRecallMeta(ctx, c); err != nil {
				return nil, err
			}
		}
		out = append(out, Hit{
			Path: c.path, Title: c.title, Kind: kindPage, Type: c.typ, Scope: c.scope, Description: c.desc,
			Heading: c.heading, Snippet: firstRunes(c.text, snippetRunes), Updated: c.updated,
			StartLine: c.startLine, Score: recallScore(c),
		})
	}
	return out, nil
}

// recallScore is the recall rank key of a candidate: its distinct token count
// plus its best cosine.
func recallScore(c *recallCand) float64 { return float64(c.matched) + c.cosine }

// recallKeyword returns the page chunks that match at least one query term,
// with their token match count, restricted to scopes.
func (i *Index) recallKeyword(ctx context.Context, terms, scopes []string) ([]*recallCand, error) {
	query := `
SELECT c.id, c.path, c.title, c.heading, c.start_line, c.text, f.type, f.scope, f.description, f.updated
FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid JOIN files f ON f.path = c.path
WHERE chunks_fts MATCH ? AND f.kind = ?`
	args := []any{matchTerms(terms), kindPage}
	if len(scopes) > 0 {
		query += " AND f.scope IN (" + placeholders(len(scopes)) + ")"
		for _, scope := range scopes {
			args = append(args, scope)
		}
	}
	query += " ORDER BY bm25(chunks_fts, 5.0, 2.0, 1.0) LIMIT ?"
	args = append(args, recallTop)

	rows, err := i.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*recallCand
	for rows.Next() {
		var (
			c    recallCand
			text string
		)
		if err := rows.Scan(&c.chunkID, &c.path, &c.title, &c.heading, &c.startLine, &text, &c.typ, &c.scope, &c.desc, &c.updated); err != nil {
			return nil, err
		}
		c.text = text
		c.haveMeta = true
		c.matched = matchedTokens(terms, tokenSet(text))
		out = append(out, &c)
	}
	return out, rows.Err()
}

// fillRecallMeta loads the chunk details of a vector-only recall candidate.
func (i *Index) fillRecallMeta(ctx context.Context, c *recallCand) error {
	err := i.db.QueryRowContext(ctx, `
SELECT c.title, c.heading, c.start_line, c.text, f.type, f.scope, f.description, f.updated
FROM chunks c JOIN files f ON f.path = c.path WHERE c.id = ?`, c.chunkID).
		Scan(&c.title, &c.heading, &c.startLine, &c.text, &c.typ, &c.scope, &c.desc, &c.updated)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	c.haveMeta = true
	return nil
}

// recallBetter reports whether c beats b for the same page: more matched query
// tokens first, then higher cosine.
func recallBetter(c, b *recallCand) bool {
	if c.matched != b.matched {
		return c.matched > b.matched
	}
	return c.cosine > b.cosine
}

// matchedTokens counts the distinct query terms present in a chunk's tokens.
func matchedTokens(terms []string, tokens map[string]bool) int {
	n := 0
	for _, term := range terms {
		if tokens[term] {
			n++
		}
	}
	return n
}

// queryTerms tokenizes a query: lower-case runs of letters and digits, at least
// queryTermMin runes, no stop words, deduplicated, the first queryTermMax
// (REQ-KNW-031).
func queryTerms(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if utf8.RuneCountInString(field) < queryTermMin || agenthome.IsStopWord(field) || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
		if len(out) == queryTermMax {
			break
		}
	}
	return out
}

// tokenSet is the set of lower-case word tokens of text used by the recall
// rule.
func tokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(field) >= queryTermMin {
			out[field] = true
		}
	}
	return out
}

// matchTerms builds an FTS5 MATCH string: every term is a quoted string, so
// FTS5 operators and syntax characters are searched literally (REQ-KNW-031).
func matchTerms(terms []string) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " OR ")
}

// scopeAllowed reports whether scope passes a scopes filter. An empty filter
// allows every scope.
func scopeAllowed(scopes []string, scope string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// firstRunes returns the first n runes of s.
func firstRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// encodeVec serializes a float vector as little-endian float32s.
func encodeVec(vec []float32) []byte {
	out := make([]byte, len(vec)*4)
	for idx, f := range vec {
		binary.LittleEndian.PutUint32(out[idx*4:], math.Float32bits(f))
	}
	return out
}

// decodeVec reads a little-endian float32 vector of dim values.
func decodeVec(blob []byte, dim int) []float32 {
	n := dim
	if n*4 > len(blob) {
		n = len(blob) / 4
	}
	out := make([]float32, n)
	for idx := 0; idx < n; idx++ {
		out[idx] = math.Float32frombits(binary.LittleEndian.Uint32(blob[idx*4:]))
	}
	return out
}

// cosine is the cosine similarity of two vectors; a zero vector scores 0.
func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for idx := 0; idx < n; idx++ {
		x, y := float64(a[idx]), float64(b[idx])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// The session_search bounds and display cuts (REQ-SSR-011–013, -016).
const (
	sessionSearchLimitDefault  = 5
	sessionSearchLimitMin      = 1
	sessionSearchLimitMax      = 10
	sessionSearchContext       = 3
	sessionSearchRadius        = 8
	sessionSearchContextCut    = 400
	sessionSearchScrollCut     = 1500
	sessionSearchOutputCut     = 12000
	sessionSearchTermMin       = 2
	sessionSearchTermMax       = 12
	sessionSearchTimeFormat    = "2006-01-02 15:04"
	sessionSearchOutputCutMark = "\n[output cut]"
)

var sessionSearchSpec = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Words to look for in earlier conversations."},"agent":{"type":"string","description":"Only this agent's sessions. Optional."},"limit":{"type":"integer","description":"Sessions to return, 1-10. Default 5."},"session_id":{"type":"string","description":"Read a session around around_seq instead of searching."},"around_seq":{"type":"integer","description":"Message seq to center on, from a search result."}}}`)

// sessionSearchGuidance is the head-prompt note shown when session_search is
// offered (REQ-SSR-017).
const sessionSearchGuidance = "Use session_search when the owner mentions an earlier conversation or you need a detail that is not in memory or the current context. Quote what you find; do not guess."

// sessionSearchTool searches the conversation index of past sessions and reads
// a window around one message (REQ-SSR-010).
type sessionSearchTool struct{}

// NewSessionSearch creates the session_search tool (REQ-SSR-010).
func NewSessionSearch() core.Tool { return &sessionSearchTool{} }

func (t *sessionSearchTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "session_search",
		Description: "Search earlier conversations with the owner, across sessions and agents. Give query to find matches, or session_id with around_seq to read more of one session.",
		InputSchema: sessionSearchSpec,
	}
}

// Risk is read: the tool only reads the conversation index (REQ-SSR-010).
func (t *sessionSearchTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

// PromptGuidance renders the session_search usage note (REQ-SSR-017).
func (t *sessionSearchTool) PromptGuidance() string { return sessionSearchGuidance }

func (t *sessionSearchTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Query     string `json:"query"`
		Agent     string `json:"agent"`
		Limit     *int   `json:"limit"`
		SessionID string `json:"session_id"`
		AroundSeq *int64 `json:"around_seq"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Sessions == nil {
		return errorOutput("session search is not available")
	}
	if in.SessionID != "" {
		if in.AroundSeq == nil || *in.AroundSeq < 1 {
			return inputErrorOutput(errors.New("around_seq is required with session_id"))
		}
		return t.scroll(ctx, tc, in.SessionID, *in.AroundSeq)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return inputErrorOutput(errors.New("query is required"))
	}
	return t.discovery(ctx, tc, query, in.Agent, sessionSearchLimit(in.Limit))
}

// discovery runs the query mode: tokenize, search, and render the ranked hits
// (REQ-SSR-011, -012, -014, -015).
func (t *sessionSearchTool) discovery(ctx context.Context, tc core.ToolCtx, query, agent string, limit int) (core.ToolOutput, error) {
	terms := sessionSearchTerms(query)
	if len(terms) == 0 {
		return successOutput(sessionSearchNoMatch(query))
	}
	hits, err := tc.Sessions.SearchText(ctx, core.TextQuery{
		Terms:         terms,
		Agent:         agent,
		CallerSession: tc.SessionID,
		Limit:         limit,
		Context:       sessionSearchContext,
	})
	if err != nil {
		return errorOutput(err.Error())
	}
	if len(hits) == 0 {
		return successOutput(sessionSearchNoMatch(query))
	}
	return successOutput(sessionSearchCut(formatSessionHits(query, hits)))
}

// scroll runs the read-around mode (REQ-SSR-013).
func (t *sessionSearchTool) scroll(ctx context.Context, tc core.ToolCtx, sessionID string, aroundSeq int64) (core.ToolOutput, error) {
	info, rows, err := tc.Sessions.TextWindow(ctx, sessionID, aroundSeq, sessionSearchRadius)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(sessionSearchCut(formatSessionWindow(info, rows, aroundSeq)))
}

func sessionSearchNoMatch(query string) string {
	return fmt.Sprintf("No past conversation matched %q.", query)
}

// formatSessionHits renders the discovery result (REQ-SSR-012). The first hit
// also carries the rows around its matching message; every other hit shows
// only the FTS snippet.
func formatSessionHits(query string, hits []core.TextHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d sessions for %q:\n\n", len(hits), query)
	for i, hit := range hits {
		fmt.Fprintf(&b, "%d. session %s (%s, %s, %q), %s\n", i+1, hit.Session.ID, hit.Session.Agent, hit.Session.Channel, hit.Session.Title, sessionSearchTime(hit.Row.CreatedAt))
		if i == 0 {
			writeSessionAround(&b, hit)
			continue
		}
		fmt.Fprintf(&b, " > seq %d %s: %s\n", hit.Row.Seq, sessionSearchRole(hit.Row.Role, hit.Session.Agent), sessionSearchClip(hit.Snippet, sessionSearchContextCut))
	}
	fmt.Fprintf(&b, "\nRead more with session_search session_id=%s around_seq=%d.\n", hits[0].Session.ID, hits[0].Row.Seq)
	return b.String()
}

// writeSessionAround renders the rows around the first hit: the matching row
// carries the FTS snippet and a ">" mark, its neighbours the row text cut to
// 400 runes (REQ-SSR-012).
func writeSessionAround(b *strings.Builder, hit core.TextHit) {
	if len(hit.Around) == 0 {
		fmt.Fprintf(b, " > seq %d %s: %s\n", hit.Row.Seq, sessionSearchRole(hit.Row.Role, hit.Session.Agent), sessionSearchClip(hit.Snippet, sessionSearchContextCut))
		return
	}
	for _, row := range hit.Around {
		if row.Seq == hit.Row.Seq {
			fmt.Fprintf(b, " > seq %d %s: %s\n", row.Seq, sessionSearchRole(row.Role, hit.Session.Agent), sessionSearchClip(hit.Snippet, sessionSearchContextCut))
			continue
		}
		fmt.Fprintf(b, "   seq %d %s: %s\n", row.Seq, sessionSearchRole(row.Role, hit.Session.Agent), sessionSearchClip(row.Text, sessionSearchContextCut))
	}
}

// formatSessionWindow renders the scroll result (REQ-SSR-013): the session
// header, then one line per row in seq order with the target row marked.
func formatSessionWindow(info core.SessionInfo, rows []core.TextRow, aroundSeq int64) string {
	var b strings.Builder
	if len(rows) == 0 {
		fmt.Fprintf(&b, "session %s (%s, %s, %q), seq %d: no conversation text around it.\n", info.ID, info.Agent, info.Channel, info.Title, aroundSeq)
		return b.String()
	}
	fmt.Fprintf(&b, "session %s (%s, %s, %q), seq %d–%d:\n", info.ID, info.Agent, info.Channel, info.Title, rows[0].Seq, rows[len(rows)-1].Seq)
	for _, row := range rows {
		marker := ""
		if row.Seq == aroundSeq {
			marker = "> "
		}
		fmt.Fprintf(&b, "%sseq %d %s %s: %s\n", marker, row.Seq, sessionSearchTime(row.CreatedAt), sessionSearchRole(row.Role, info.Agent), sessionSearchClip(row.Text, sessionSearchScrollCut))
	}
	return b.String()
}

// sessionSearchTerms tokenizes a query: lower-case runs of letters and digits,
// at least two runes, no stop words, deduplicated, the first 12
// (REQ-SSR-011).
func sessionSearchTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if utf8.RuneCountInString(field) < sessionSearchTermMin || agenthome.IsStopWord(field) || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
		if len(terms) == sessionSearchTermMax {
			break
		}
	}
	return terms
}

// sessionSearchLimit clamps the requested limit to 1..10, default 5
// (REQ-SSR-011).
func sessionSearchLimit(limit *int) int {
	if limit == nil {
		return sessionSearchLimitDefault
	}
	switch {
	case *limit < sessionSearchLimitMin:
		return sessionSearchLimitMin
	case *limit > sessionSearchLimitMax:
		return sessionSearchLimitMax
	default:
		return *limit
	}
}

// sessionSearchRole labels a row's author: owner for the user, the session's
// agent for the assistant (REQ-SSR-012).
func sessionSearchRole(role, agent string) string {
	if role == "user" {
		return "owner"
	}
	return agent
}

// sessionSearchTime renders a row's created_at (unix ms) in local time
// (REQ-SSR-012).
func sessionSearchTime(ms int64) string {
	return time.UnixMilli(ms).Local().Format(sessionSearchTimeFormat)
}

// sessionSearchClip flattens text to one line and cuts it to limit runes,
// marking the cut (REQ-SSR-012, -013).
func sessionSearchClip(text string, limit int) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) <= limit {
		return flat
	}
	return string(runes[:limit]) + "…"
}

// sessionSearchCut cuts the whole output to 12,000 runes and appends the cut
// marker (REQ-SSR-016).
func sessionSearchCut(text string) string {
	runes := []rune(text)
	if len(runes) <= sessionSearchOutputCut {
		return text
	}
	return string(runes[:sessionSearchOutputCut]) + sessionSearchOutputCutMark
}

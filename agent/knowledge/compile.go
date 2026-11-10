package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// Compile-pass bounds (design §5).
const (
	compilePartRunes      = 24000
	compileMaxParts       = 8
	compileMaxOps         = 12
	compileMaxTokens      = 8192
	compileCallTimeout    = 180 * time.Second
	compileCatalogMax     = 400
	compileRelatedMax     = 8
	compileRelatedRunes   = 6000
	compileSearchRunes    = 3000
	compileDescriptionCut = 160
)

// compileSystem is the system prompt of one compile call (design §5).
const compileSystem = `You maintain a personal knowledge wiki in Obsidian markdown. You receive one part of a source and the existing pages most related to it. Turn the source into durable knowledge:
- Create a page of type "source" for the source (title = the source title) with a summary and the key points, on the first part only.
- Create or update pages for the people, projects, organizations, places, and topics the source covers. Prefer updating an existing page (append under a fitting ## heading) over creating a near-duplicate; use the exact existing title.
- Link related pages with [[Title]]. Use only titles that exist or that you create in this reply.
- Keep facts specific: names, dates, numbers, decisions. Do not invent facts.
- When the source contradicts a page, append "**Conflict:** <page says> vs <source says>".
- Never include passwords, API keys, or tokens.
Return valid JSON: escape newlines inside strings as \n.
Reply with only a JSON array of operations, at most 12. Each operation is one of these objects, with exactly these keys:
{"op": "create", "title": "Page Title", "type": "person|project|organization|place|topic|source", "description": "one line", "aliases": ["..."], "tags": ["..."], "body": "markdown body without frontmatter"}
{"op": "append", "title": "Existing Page Title", "heading": "optional ## heading to add under", "text": "markdown to add"}
{"op": "replace", "title": "Existing Page Title", "old_text": "exact text that occurs once in the page", "new_text": "replacement"}
{"op": "meta", "title": "Existing Page Title", "description": "...", "aliases": ["..."], "tags": ["..."]}
The title is the page name only: no folder, no ".md", no frontmatter in body. lana writes the frontmatter and the sources field itself.`

// compileJournalNote is the extra system-prompt line of a promotion compile:
// the source is the journal, so no source summary page is created
// (REQ-KNU-021, design §7).
const compileJournalNote = "This source is the owner's journal. Do not create a page for the journal itself."

// wikilinkPattern matches a [[Title]] wikilink in source text.
var wikilinkPattern = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// compiledOp is one operation of a compile reply (design §5 step 4).
type compiledOp struct {
	Op          string   `json:"op"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	Tags        []string `json:"tags"`
	Body        string   `json:"body"`
	Heading     string   `json:"heading"`
	Text        string   `json:"text"`
	OldText     string   `json:"old_text"`
	NewText     string   `json:"new_text"`
}

// compileInput is one compile request: the text to compile, the wikilink the
// resulting pages cite, its title, whether to create a source summary page, the
// agent whose provider and model run the calls, and the vault-log actor
// (design §5, §7).
type compileInput struct {
	Text        string
	SourceLink  string
	SourceTitle string
	SourcePage  bool
	Agent       string
	Actor       string
}

// compileResult is what one compile pass applied and what it spent.
type compileResult struct {
	Ops   []core.KnowledgeCompiledOp
	Usage core.Usage
	Cut   bool
}

// compile runs one compile pass over in.Text (REQ-KNW-021, -022, -024, -025).
// It applies page operations and returns them; a hard failure stops the pass
// and is returned. The source queue and promotion share compileMu, so only one
// pass runs at a time (REQ-KNW-023, design §7).
func (s *Service) compile(ctx context.Context, in compileInput) (compileResult, error) {
	s.compileMu.Lock()
	defer s.compileMu.Unlock()

	vault, idx, err := s.open()
	if err != nil {
		return compileResult{}, err
	}
	agentCfg, ok := s.lookupAgent(in.Agent)
	if !ok {
		return compileResult{}, fmt.Errorf("unknown agent %q", in.Agent)
	}
	call, err := s.agentCall(in.Agent, agentCfg)
	if err != nil {
		return compileResult{}, err
	}

	parts, cut := splitParts(in.Text)
	res := compileResult{Cut: cut}
	if cut {
		slog.Warn("knowledge compile: source cut", "source", linkRef(in.SourceLink), "parts", compileMaxParts)
	}

	var changed []string
	// Pages an earlier part already wrote are re-indexed even when a later part
	// fails, so the queue's next attempt and search see them (REQ-KNW-023).
	defer func() {
		for _, rel := range changed {
			if err := idx.SyncFile(filepath.Join(vault.Root(), filepath.FromSlash(rel))); err != nil {
				slog.Warn("knowledge compile: re-index failed", "path", rel, "error", err)
			}
		}
	}()
	for i, part := range parts {
		reply, u, err := s.compilePart(ctx, vault, idx, call, in, i+1, len(parts), part)
		res.Usage = addUsage(res.Usage, u)
		if err != nil {
			return res, err
		}
		compiled, err := parseCompiledOps(reply)
		if err != nil {
			return res, err
		}
		if len(compiled) > compileMaxOps {
			compiled = compiled[:compileMaxOps]
		}
		for _, op := range compiled {
			r, err := s.applyCompiledOp(vault, in, op)
			if err != nil {
				slog.Debug("knowledge compile: op skipped", "source", linkRef(in.SourceLink), "title", op.Title, "error", err)
				continue
			}
			res.Ops = append(res.Ops, core.KnowledgeCompiledOp{Op: r.Op, Title: titleOfPath(r.Path)})
			changed = append(changed, r.Path)
		}
		if i == 0 && in.SourcePage {
			created, err := s.ensureSourcePage(vault, in.SourceLink, in.SourceTitle, in.Text)
			if err != nil {
				slog.Debug("knowledge compile: source page failed", "source", linkRef(in.SourceLink), "error", err)
			} else if created != "" {
				changed = append(changed, created)
			}
		}
	}
	return res, nil
}

// agentCall is the resolved chain of the agent a knowledge pass runs for.
type agentCall struct {
	agent     string
	chain     []core.LLMTarget
	requested string
}

// agentCall resolves the chain of an agent's model and fallback, so a pass
// for a combo agent gets the combo's fallback (REQ-RTR-007).
func (s *Service) agentCall(name string, cfg core.AgentConfig) (agentCall, error) {
	if s.llm == nil {
		return agentCall{}, errors.New("no llm registry")
	}
	// REQ-CCT-007: knowledge passes never use Claude Code.
	chain, requested, err := llmcall.AgentBackgroundChain(s.llm, cfg)
	if errors.Is(err, llmcall.ErrNoBackgroundModel) {
		llmcall.WarnNoBackground(name, "knowledge")
	}
	if err != nil {
		return agentCall{}, err
	}
	return agentCall{agent: name, chain: chain, requested: requested}, nil
}

// stream makes one knowledge call through the router with source
// "knowledge".
func (s *Service) stream(ctx context.Context, call agentCall, sessionID string, req core.ChatRequest) (core.ChatStream, error) {
	stream, _, err := llmcall.Stream(ctx, s.llm, call.chain, req, core.LLMCallMeta{
		Source: llmcall.SourceKnowledge, Agent: call.agent, SessionID: sessionID, Requested: call.requested,
	}, nil)
	return stream, err
}

// compileSource is the queue worker's compile of one ingested source: it reads
// the source state and text, runs compile, then records the done or failed state
// plus the knowledge/compiled event (REQ-KNW-023, -025, design §5).
func (s *Service) compileSource(src string) {
	vault, idx, err := s.open()
	if err != nil {
		slog.Warn("knowledge compile: open failed", "source", src, "error", err)
		return
	}
	st, ok, err := idx.GetSourceState(src)
	if err != nil || !ok {
		return
	}

	text, err := sourceText(vault.Root(), src)
	if err != nil {
		s.finishSource(idx, st, nil, core.Usage{}, err.Error())
		return
	}

	res, err := s.compile(s.base, compileInput{
		Text:        text,
		SourceLink:  sourceLink(src),
		SourceTitle: sourceTitle(src),
		SourcePage:  true,
		Agent:       st.Agent,
		Actor:       "compile " + src,
	})
	errMsg := ""
	if res.Cut {
		errMsg = fmt.Sprintf("source cut after %d parts", compileMaxParts)
	}
	if err != nil {
		// A pass cut short because the process is stopping stays pending, so
		// the next start compiles it again (REQ-KNW-023).
		if s.base.Err() != nil {
			slog.Info("knowledge compile: stopped, source stays pending", "source", src)
			return
		}
		errMsg = err.Error()
	}
	s.finishSource(idx, st, res.Ops, res.Usage, errMsg)
}

// lookupAgent resolves the agent config of a source. It reports false when the
// registry has no lookup or no such agent (REQ-KNW-025).
func (s *Service) lookupAgent(name string) (core.AgentConfig, bool) {
	if s.LookupAgent == nil {
		return core.AgentConfig{}, false
	}
	return s.LookupAgent(name)
}

// finishSource writes the done or failed state and, when the source came from a
// session, appends the knowledge/compiled event (REQ-KNW-024, API-KNW-002).
func (s *Service) finishSource(idx *kv.Index, st kv.SourceState, ops []core.KnowledgeCompiledOp, usage core.Usage, errMsg string) {
	status := stateDone
	if errMsg != "" {
		status = stateFailed
	}
	st.Status = status
	st.Error = errMsg
	st.UpdatedAt = s.now().UnixMilli()
	if err := idx.SetSourceState(st); err != nil {
		slog.Warn("knowledge compile: saving state failed", "source", st.Path, "error", err)
	}
	if status == stateFailed {
		slog.Warn("knowledge compiled", "source", st.Path, "status", status, "error", errMsg)
	}
	if st.SessionID == "" || s.store == nil {
		return
	}
	payload := core.KnowledgeCompiled{V: core.PayloadVersion, Source: st.Path, Ops: ops, Usage: usage, Error: errMsg}
	if _, err := s.store.Append(s.base, st.SessionID, []core.EventDraft{{
		Kind: core.KindKnowledgeCompiled, TurnID: st.TurnID, Payload: payload,
	}}); err != nil {
		slog.Warn("knowledge compile: appending event failed", "source", st.Path, "session_id", st.SessionID, "error", err)
	}
	if status == stateDone {
		slog.Info("knowledge compiled", "source", st.Path, "status", status, "ops", len(ops),
			"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens)
	}
}

// compilePart makes one provider call for a part of the source and returns its
// reply text and usage (design §5 step 3).
func (s *Service) compilePart(ctx context.Context, vault *kv.Vault, idx *kv.Index, call agentCall, in compileInput, part, total int, text string) (string, core.Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, compileCallTimeout)
	defer cancel()
	instruction := s.compileInstruction(callCtx, vault, idx, in, part, total, text)
	stream, err := s.stream(callCtx, call, "", core.ChatRequest{
		System: compileSystemFor(in.SourcePage),
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: instruction},
		}}},
		MaxTokens: compileMaxTokens,
	})
	if err != nil {
		return "", core.Usage{}, err
	}
	return collectReply(stream)
}

// compileSystemFor returns the compile system prompt, adding the journal note
// when the source is the owner's journal (design §7).
func compileSystemFor(sourcePage bool) string {
	if sourcePage {
		return compileSystem
	}
	return compileSystem + "\n" + compileJournalNote
}

// compileInstruction builds the user message of one part: the conventions, the
// page catalog, the related pages, and the part text (design §5 step 3).
func (s *Service) compileInstruction(ctx context.Context, vault *kv.Vault, idx *kv.Index, in compileInput, part, total int, text string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source: %s\n", linkRef(in.SourceLink))
	fmt.Fprintf(&b, "Source title: %s\n", in.SourceTitle)
	fmt.Fprintf(&b, "Part %d of %d\n", part, total)
	if conv, err := os.ReadFile(filepath.Join(vault.Root(), "KNOWLEDGE.md")); err == nil {
		b.WriteString("\nConventions (KNOWLEDGE.md):\n")
		b.Write(conv)
		b.WriteString("\n")
	}
	if catalog, err := idx.Catalog(compileCatalogMax); err == nil && len(catalog) > 0 {
		b.WriteString("\nExisting pages:\n")
		for _, line := range catalog {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	for _, rel := range s.relatedPages(ctx, vault, idx, text) {
		body, err := os.ReadFile(filepath.Join(vault.Root(), filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n", titleOfPath(rel), firstRunes(string(body), compileRelatedRunes))
	}
	b.WriteString("\nSource text:\n")
	b.WriteString(text)
	return b.String()
}

// relatedPages returns the shared wiki pages named in the part or found by a
// page search over its first compileSearchRunes runes. Compile passes write the
// shared wiki only, so only shared pages are related (design §5 step 3,
// REQ-DKN-022).
func (s *Service) relatedPages(ctx context.Context, vault *kv.Vault, idx *kv.Index, text string) []string {
	return s.relatedPagesIn(ctx, vault, idx, text, []string{kv.ScopeShared})
}

// relatedPagesIn returns up to compileRelatedMax pages named in text or found by
// a page search over its first compileSearchRunes runes, restricted to the given
// scopes (design §5 step 3, §4.2 step 2).
func (s *Service) relatedPagesIn(ctx context.Context, vault *kv.Vault, idx *kv.Index, text string, scopes []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(rel string) {
		if rel == "" || seen[rel] || len(out) >= compileRelatedMax {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	for _, name := range wikilinkPattern.FindAllStringSubmatch(text, -1) {
		for _, scope := range scopes {
			page, ok, err := vault.FindPageIn(scope, strings.TrimSpace(name[1]))
			if err == nil && ok {
				add(page.Path)
				break
			}
		}
	}
	hits, err := idx.Search(ctx, kv.Query{Text: firstRunes(text, compileSearchRunes), Limit: compileRelatedMax, Kind: "page", Scopes: scopes})
	if err == nil {
		for _, h := range hits {
			add(h.Path)
		}
	}
	return out
}

// applyCompiledOp turns one compile operation into a vault write with the
// compile actor and merges the source wikilink into create and append ops
// (design §5 step 5).
func (s *Service) applyCompiledOp(vault *kv.Vault, in compileInput, op compiledOp) (kv.Result, error) {
	if strings.TrimSpace(op.Op) == "" || strings.TrimSpace(op.Title) == "" {
		return kv.Result{}, errors.New("operation is missing op or title")
	}
	ko := kv.Op{
		Action:      op.Op,
		Title:       op.Title,
		Type:        op.Type,
		Description: op.Description,
		Body:        op.Body,
		Text:        op.Text,
		Heading:     op.Heading,
		OldText:     op.OldText,
		NewText:     op.NewText,
		Aliases:     op.Aliases,
		Tags:        op.Tags,
	}
	if op.Op == "create" || op.Op == "append" {
		ko.Sources = []string{in.SourceLink}
	}
	return vault.Apply(ko, in.Actor)
}

// ensureSourcePage creates the fallback source page when no page of type source
// links the source yet (design §5 step 6).
func (s *Service) ensureSourcePage(vault *kv.Vault, link, title, text string) (string, error) {
	if sourcePageHasLink(vault.Root(), link) {
		return "", nil
	}
	res, err := vault.Apply(kv.Op{
		Action:      "create",
		Title:       title,
		Type:        "source",
		Description: firstLine(text, compileDescriptionCut),
		Body:        "Source: " + link,
		Sources:     []string{link},
	}, "compile "+linkRef(link))
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

// sourcePageHasLink reports whether any page under wiki/ has type source and
// cites link.
func sourcePageHasLink(root, link string) bool {
	found := false
	_ = filepath.WalkDir(filepath.Join(root, "wiki"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || found || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		page := kv.ParsePage(filepath.ToSlash(rel), string(data))
		if page.Meta.Type != "source" {
			return nil
		}
		for _, s := range page.Meta.Sources {
			if strings.TrimSpace(s) == link {
				found = true
				return fs.SkipAll
			}
		}
		return nil
	})
	return found
}

// linkRef returns the vault path a wikilink cites, without the brackets.
func linkRef(link string) string {
	s := strings.TrimSpace(link)
	s = strings.TrimPrefix(s, "[[")
	s = strings.TrimSuffix(s, "]]")
	return strings.TrimSpace(s)
}

// sourceText reads the compile text of a source: markdown and text as is, a PDF
// through PDFText (design §5 step 1).
func sourceText(root, rel string) (string, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if strings.EqualFold(filepath.Ext(rel), ".pdf") {
		pages, err := attach.PDFText(abs)
		if err != nil {
			return "", err
		}
		return strings.Join(pages, "\n"), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// splitParts splits source text into parts of at most compilePartRunes, cutting
// between paragraphs. It reports true when the source was cut to
// compileMaxParts (design §5 step 1).
func splitParts(text string) ([]string, bool) {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.TrimRight(string(cur), "\n"))
			cur = nil
		}
	}
	for _, para := range splitParagraphs(text) {
		runes := []rune(para)
		if len(runes) > compilePartRunes {
			flush()
			for start := 0; start < len(runes); start += compilePartRunes {
				end := start + compilePartRunes
				if end > len(runes) {
					end = len(runes)
				}
				parts = append(parts, string(runes[start:end]))
			}
			continue
		}
		need := len(runes)
		if len(cur) > 0 {
			need += 2
		}
		if len(cur) > 0 && len(cur)+need > compilePartRunes {
			flush()
		}
		if len(cur) > 0 {
			cur = append(cur, '\n', '\n')
		}
		cur = append(cur, runes...)
	}
	flush()
	if len(parts) == 0 {
		parts = []string{""}
	}
	if len(parts) > compileMaxParts {
		return parts[:compileMaxParts], true
	}
	return parts, false
}

// splitParagraphs splits text at blank lines.
func splitParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// parseCompiledOps strips a ```json fence and parses the reply as a JSON array
// of operations, skipping an element whose shape does not decode so one bad
// operation does not drop the whole reply (design §5 step 4).
func parseCompiledOps(text string) ([]compiledOp, error) {
	trimmed := stripFence(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, errors.New("compile reply is not a JSON array")
	}
	var raw []json.RawMessage
	if err := decodeLLMJSON(trimmed, &raw); err != nil {
		return nil, err
	}
	var ops []compiledOp
	for _, item := range raw {
		var op compiledOp
		if err := json.Unmarshal(item, &op); err != nil {
			slog.Debug("knowledge compile: op skipped: malformed JSON", "error", err)
			continue
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// stripFence removes one leading ```/```json fence and its closing fence.
func stripFence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	if i := strings.LastIndex(text, "```"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// collectReply reads the reply text and usage of one stream.
func collectReply(stream core.ChatStream) (string, core.Usage, error) {
	var b strings.Builder
	var usage core.Usage
	for event, err := range stream {
		if err != nil {
			return "", usage, err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			b.WriteString(event.Text)
		case core.ChatBlock:
			if event.Block.Kind == core.BlockText {
				b.WriteString(event.Block.Text)
			}
		case core.ChatUsage:
			usage = event.Usage
		}
	}
	return b.String(), usage, nil
}

// addUsage sums two usage values across parts.
func addUsage(a, b core.Usage) core.Usage {
	return core.Usage{
		InputTokens:       a.InputTokens + b.InputTokens,
		OutputTokens:      a.OutputTokens + b.OutputTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens,
	}
}

// firstLine returns the first non-empty line, trimmed and cut to max runes.
func firstLine(text string, max int) string {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return firstRunes(trimmed, max)
		}
	}
	return ""
}

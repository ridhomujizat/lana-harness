package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/ridhomujizat/lana-harness/core"
)

// The knowledge tool bounds (design §6).
const (
	knowledgeSearchLimitDefault = 8
	knowledgeSearchLimitMin     = 1
	knowledgeSearchLimitMax     = 20
	knowledgeReadMaxLines       = 2000
	knowledgeReadMaxRunes       = 20000
	knowledgeIngestTitleMax     = 80
)

var knowledgeSearchSpec = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Words to look for in the knowledge vault."},"limit":{"type":"integer","minimum":1,"maximum":20,"description":"Maximum number of results, 1-20. Defaults to 8."},"kind":{"type":"string","enum":["page","source","journal"],"description":"Only this kind of file. Optional."},"type":{"type":"string","description":"Only pages of this type: person, project, organization, place, topic, or source. Optional."}},"required":["query"],"additionalProperties":false}`)
var knowledgeReadSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Vault-relative file path, for example wiki/Pak Budi.md."},"title":{"type":"string","description":"Page title or alias. Used when path is empty."},"from_line":{"type":"integer","minimum":1,"description":"1-based first line to read. Defaults to 1."},"to_line":{"type":"integer","minimum":1,"description":"1-based last line to read. Defaults to the end of the file."}},"additionalProperties":false}`)
var knowledgeWriteSpec = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["create","append","replace","meta"],"description":"create makes a page, append adds text, replace swaps one exact text, meta updates frontmatter."},"title":{"type":"string","description":"Page title. A create whose title or alias exists becomes an append."},"type":{"type":"string","enum":["person","project","organization","place","topic","source"],"description":"Page type, used by create and meta. Optional."},"description":{"type":"string","description":"One-line page description, used by create and meta. Optional."},"body":{"type":"string","description":"Page body, required for create."},"text":{"type":"string","description":"Text to append, required for append."},"heading":{"type":"string","description":"Section to append under, for append. Optional."},"old_text":{"type":"string","description":"Text to replace, required for replace. Must occur once in the body."},"new_text":{"type":"string","description":"Replacement text, required for replace."},"aliases":{"type":"array","items":{"type":"string"},"description":"Aliases to merge, used by create and meta. Optional."},"tags":{"type":"array","items":{"type":"string"},"description":"Tags to merge, used by create and meta. Optional."}},"required":["action","title"],"additionalProperties":false}`)
var knowledgeIngestSpec = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace file to ingest: .md, .txt, .pdf, or .html."},"url":{"type":"string","format":"uri","pattern":"^https?://","description":"Web page to fetch and convert to markdown."},"text":{"type":"string","description":"Text to store as a markdown source."},"title":{"type":"string","description":"Source title. Optional; defaults to the file name, the page title, or the first line."}},"additionalProperties":false}`)

// knowledgeSearchGuidance is the head-prompt note shown when knowledge_search
// is offered (REQ-KNW-044).
const knowledgeSearchGuidance = "Search knowledge_search before answering about people, projects, or things the owner shared earlier. " +
	"Save lasting knowledge with knowledge_write, or knowledge_ingest for documents, links, and long notes. " +
	"Keep memory_write for short facts about the owner and yourself."

type knowledgeSearchTool struct{}
type knowledgeReadTool struct{}
type knowledgeWriteTool struct{}
type knowledgeIngestTool struct{}

// NewKnowledgeSearch creates the knowledge_search tool (REQ-KNW-040).
func NewKnowledgeSearch() core.Tool { return &knowledgeSearchTool{} }

// NewKnowledgeRead creates the knowledge_read tool (REQ-KNW-041).
func NewKnowledgeRead() core.Tool { return &knowledgeReadTool{} }

// NewKnowledgeWrite creates the knowledge_write tool (REQ-KNW-040).
func NewKnowledgeWrite() core.Tool { return &knowledgeWriteTool{} }

// NewKnowledgeIngest creates the knowledge_ingest tool (REQ-KNW-042).
func NewKnowledgeIngest() core.Tool { return &knowledgeIngestTool{} }

func (t *knowledgeSearchTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "knowledge_search",
		Description: "Search the knowledge vault. Returns matching pages and sources, best first, each with its best chunk. Read a file with knowledge_read.",
		InputSchema: knowledgeSearchSpec,
	}
}

// Risk is read: the tool only searches the vault index (REQ-KNW-040).
func (t *knowledgeSearchTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

// PromptGuidance renders the knowledge usage note (REQ-KNW-044).
func (t *knowledgeSearchTool) PromptGuidance() string { return knowledgeSearchGuidance }

func (t *knowledgeSearchTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Query string `json:"query"`
		Limit *int   `json:"limit"`
		Kind  string `json:"kind"`
		Type  string `json:"type"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Knowledge == nil {
		return errorOutput("knowledge is not available")
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return inputErrorOutput(errors.New("query is required"))
	}
	switch in.Kind {
	case "", "page", "source", "journal":
	default:
		return inputErrorOutput(errors.New("kind must be page, source, or journal"))
	}
	hits, err := tc.Knowledge.Search(ctx, core.KnowledgeQuery{
		Text:  query,
		Limit: knowledgeSearchLimit(in.Limit),
		Kind:  in.Kind,
		Type:  in.Type,
	})
	if err != nil {
		return errorOutput(err.Error())
	}
	if len(hits) == 0 {
		return successOutput(fmt.Sprintf("Nothing in knowledge matched %q.", query))
	}
	return successOutput(formatKnowledgeHits(query, hits))
}

// knowledgeSearchLimit clamps the requested limit to 1..20, default 8
// (design §6).
func knowledgeSearchLimit(limit *int) int {
	if limit == nil {
		return knowledgeSearchLimitDefault
	}
	switch {
	case *limit < knowledgeSearchLimitMin:
		return knowledgeSearchLimitMin
	case *limit > knowledgeSearchLimitMax:
		return knowledgeSearchLimitMax
	default:
		return *limit
	}
}

// formatKnowledgeHits renders the search results (design §6). A page-like hit
// (one with a description or an update date) shows its best chunk on a new
// indented line; a bare hit keeps the chunk on its header line.
func formatKnowledgeHits(query string, hits []core.KnowledgeHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d results for %q:\n", len(hits), query)
	for i, hit := range hits {
		header := fmt.Sprintf("%d. %s", i+1, hit.Path)
		if hit.Type != "" {
			header += " — " + hit.Type
		}
		if hit.Description != "" {
			header += " — " + hit.Description
		}
		if hit.Updated != "" {
			header += " (updated " + hit.Updated + ")"
		}
		chunk := knowledgeChunkLine(hit)
		if chunk != "" && hit.Description == "" && hit.Updated == "" {
			b.WriteString(header + ", " + chunk + "\n")
			continue
		}
		b.WriteString(header + "\n")
		if chunk != "" {
			b.WriteString("   " + chunk + "\n")
		}
	}
	b.WriteString("Read a file with knowledge_read path=<path>.")
	return b.String()
}

// knowledgeChunkLine renders a hit's best chunk: the heading, the start line,
// and the snippet.
func knowledgeChunkLine(hit core.KnowledgeHit) string {
	if hit.Heading == "" && hit.Snippet == "" && hit.StartLine == 0 {
		return ""
	}
	line := ""
	if hit.Heading != "" {
		line = "## " + hit.Heading + ", "
	}
	return fmt.Sprintf("%sline %d: %s", line, hit.StartLine, hit.Snippet)
}

func (t *knowledgeReadTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "knowledge_read",
		Description: "Read a knowledge vault file by path, or a page by title or alias, with numbered lines. Give from_line and to_line to read a range.",
		InputSchema: knowledgeReadSpec,
	}
}

// Risk is read: the tool only reads vault files (REQ-KNW-040).
func (t *knowledgeReadTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *knowledgeReadTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path     string `json:"path"`
		Title    string `json:"title"`
		FromLine int    `json:"from_line"`
		ToLine   int    `json:"to_line"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Knowledge == nil {
		return errorOutput("knowledge is not available")
	}
	ref := strings.TrimSpace(in.Path)
	if ref == "" {
		ref = strings.TrimSpace(in.Title)
	}
	if ref == "" {
		return inputErrorOutput(errors.New("path or title is required"))
	}
	from := in.FromLine
	if from < 1 {
		from = 1
	}
	if in.ToLine > 0 && in.ToLine < from {
		return inputErrorOutput(errors.New("to_line must be at least from_line"))
	}
	doc, err := tc.Knowledge.Read(ctx, ref, from, in.ToLine)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(formatKnowledgeDoc(doc))
}

// formatKnowledgeDoc numbers the document's lines like read_file and appends a
// "[more: from_line=<n>]" marker when later lines remain (REQ-KNW-041). One call
// returns at most 2,000 lines or 20,000 runes.
func formatKnowledgeDoc(doc core.KnowledgeDoc) string {
	var b strings.Builder
	runes := 0
	shown := 0
	last := doc.From - 1
	for i, line := range doc.Lines {
		if shown >= knowledgeReadMaxLines {
			break
		}
		count := utf8.RuneCountInString(line)
		if shown > 0 && runes+count > knowledgeReadMaxRunes {
			break
		}
		if shown > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%6d\t%s", doc.From+i, line)
		runes += count
		shown++
		last = doc.From + i
	}
	out := b.String()
	if last < doc.Total {
		out += fmt.Sprintf("\n[more: from_line=%d]", last+1)
	}
	return out
}

func (t *knowledgeWriteTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "knowledge_write",
		Description: "Create a knowledge page, append text, replace one exact text, or update frontmatter. A create whose title or alias exists appends instead. Never deletes a page.",
		InputSchema: knowledgeWriteSpec,
	}
}

// Risk is memory: the tool changes the shared knowledge vault (REQ-KNW-040).
func (t *knowledgeWriteTool) Risk(json.RawMessage) core.Risk { return core.RiskMemory }

func (t *knowledgeWriteTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Action      string   `json:"action"`
		Title       string   `json:"title"`
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Body        string   `json:"body"`
		Text        string   `json:"text"`
		Heading     string   `json:"heading"`
		OldText     string   `json:"old_text"`
		NewText     string   `json:"new_text"`
		Aliases     []string `json:"aliases"`
		Tags        []string `json:"tags"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Knowledge == nil {
		return errorOutput("knowledge is not available")
	}
	if err := validateKnowledgeWrite(in.Action, in.Title, in.Body, in.Text, in.OldText, in.NewText,
		in.Description, in.Aliases, in.Tags); err != nil {
		return inputErrorOutput(err)
	}
	res, err := tc.Knowledge.Write(ctx, core.KnowledgeWrite{
		Action:      in.Action,
		Title:       strings.TrimSpace(in.Title),
		Type:        in.Type,
		Description: in.Description,
		Body:        in.Body,
		Text:        in.Text,
		Heading:     in.Heading,
		OldText:     in.OldText,
		NewText:     in.NewText,
		Actor:       "agent " + tc.AgentName,
		Aliases:     in.Aliases,
		Tags:        in.Tags,
	})
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(knowledgeWriteMessage(res))
}

// validateKnowledgeWrite checks the required fields of one action (design §6).
func validateKnowledgeWrite(action, title, body, text, oldText, newText, description string, aliases, tags []string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("title is required")
	}
	switch action {
	case "create":
		if strings.TrimSpace(body) == "" {
			return errors.New("body is required for create")
		}
	case "append":
		if strings.TrimSpace(text) == "" {
			return errors.New("text is required for append")
		}
	case "replace":
		if strings.TrimSpace(oldText) == "" {
			return errors.New("old_text is required for replace")
		}
		if strings.TrimSpace(newText) == "" {
			return errors.New("new_text is required for replace")
		}
	case "meta":
		if strings.TrimSpace(description) == "" && len(aliases) == 0 && len(tags) == 0 {
			return errors.New("metadata is required for meta: description, aliases, or tags")
		}
	default:
		return errors.New("action must be create, append, replace, or meta")
	}
	return nil
}

// knowledgeWriteMessage renders the applied change with the same verbs as the
// vault log (design §6).
func knowledgeWriteMessage(res core.KnowledgeWriteResult) string {
	verb := res.Op
	switch res.Op {
	case "create":
		verb = "created"
	case "append":
		verb = "appended to"
	case "replace":
		verb = "replaced text in"
	case "meta":
		verb = "updated details of"
	}
	return fmt.Sprintf("%s %s (%d characters)", verb, res.Path, res.Size)
}

func (t *knowledgeIngestTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "knowledge_ingest",
		Description: "Save a workspace file, a URL, or text as a knowledge source. It returns at once; the pages appear after a background compile.",
		InputSchema: knowledgeIngestSpec,
	}
}

// Risk is network when the input fetches a URL, so a URL ingest needs the same
// owner approval as any other network access, and memory otherwise: an ingest
// of a file or text only stores a source and schedules a compile pass
// (REQ-KNW-040).
func (t *knowledgeIngestTool) Risk(raw json.RawMessage) core.Risk {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &in); err == nil && in.URL != "" {
		return core.RiskNetwork
	}
	return core.RiskMemory
}

func (t *knowledgeIngestTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Path  string `json:"path"`
		URL   string `json:"url"`
		Text  string `json:"text"`
		Title string `json:"title"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Knowledge == nil {
		return errorOutput("knowledge is not available")
	}
	provided := 0
	for _, value := range []string{in.Path, in.URL, in.Text} {
		if strings.TrimSpace(value) != "" {
			provided++
		}
	}
	if provided != 1 {
		return inputErrorOutput(errors.New("provide exactly one of path, url, or text"))
	}

	var (
		data         []byte
		ext          string
		sourceURL    string
		defaultTitle string
	)
	switch {
	case strings.TrimSpace(in.Path) != "":
		abs, rejected := resolveToolPath(nil, tc, in.Path)
		if rejected != nil {
			return *rejected, nil
		}
		if err := statReadablePath(abs); err != nil {
			return errorOutput(err.Error())
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				return errorOutput("file not found")
			}
			return errorOutput(err.Error())
		}
		switch strings.ToLower(filepath.Ext(abs)) {
		case ".md":
			data, ext = raw, "md"
		case ".txt":
			data, ext = raw, "txt"
		case ".pdf":
			data, ext = raw, "pdf"
		case ".html":
			md, err := htmlToMarkdown(raw)
			if err != nil {
				return errorOutput(err.Error())
			}
			data, ext = []byte(md), "md"
		default:
			return errorOutput(fmt.Sprintf("unsupported file type %q; use .md, .txt, .pdf, or .html", filepath.Ext(abs)))
		}
		defaultTitle = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	case strings.TrimSpace(in.URL) != "":
		title, md, err := FetchMarkdown(ctx, in.URL)
		if err != nil {
			return errorOutput(err.Error())
		}
		sourceURL = in.URL
		defaultTitle = title
		if strings.TrimSpace(defaultTitle) == "" {
			defaultTitle = firstNonEmptyLine(md)
		}
		data = []byte(sourceURLFrontmatter(in.URL) + md)
		ext = "md"
	default:
		data = []byte(in.Text)
		ext = "md"
		defaultTitle = firstNonEmptyLine(in.Text)
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = cutTitle(defaultTitle, knowledgeIngestTitleMax)
	}
	res, err := tc.Knowledge.Ingest(ctx, core.KnowledgeIngest{
		Title:     title,
		Ext:       ext,
		SourceURL: sourceURL,
		SessionID: tc.SessionID,
		TurnID:    tc.TurnID,
		AgentName: tc.AgentName,
		Data:      data,
	})
	if err != nil {
		return errorOutput(err.Error())
	}
	if res.Duplicate {
		msg := "Already saved as " + res.Path + "."
		if res.Queued {
			msg += " Compiling again."
		}
		return successOutput(msg)
	}
	return successOutput(fmt.Sprintf("Saved %s. Compiling it into wiki pages in the background; they appear in knowledge_search when done.", res.Path))
}

// htmlToMarkdown strips scripts and styles, then converts an HTML document to
// Markdown. It shares stripHTML with web_fetch.
func htmlToMarkdown(body []byte) (string, error) {
	cleaned, err := stripHTML(body)
	if err != nil {
		return "", err
	}
	return htmltomarkdown.ConvertString(cleaned)
}

// firstNonEmptyLine returns the first non-blank line of text, trimmed.
func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// cutTitle trims a default title and caps it at max runes.
func cutTitle(text string, max int) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > max {
		return strings.TrimSpace(string(runes[:max]))
	}
	return text
}

// sourceURLFrontmatter renders a YAML frontmatter block that records where a
// fetched source came from (REQ-KNW-020).
func sourceURLFrontmatter(rawURL string) string {
	return "---\nsource_url: " + strconv.Quote(rawURL) + "\n---\n\n"
}

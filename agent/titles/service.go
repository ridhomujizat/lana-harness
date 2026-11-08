// Package titles names sessions (spec 40). After the first completed turn
// with an owner message, a background pass asks the agent's background chain
// for a short title. When no background model is available or the call
// fails, the first owner message becomes the title.
package titles

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// Source is the call-record source of a title pass (REQ-TTL-002).
const Source = "title"

const (
	// passTimeout bounds one pass, the model call included.
	passTimeout = 45 * time.Second
	// maxTokens caps the title reply (REQ-TTL-002).
	maxTokens = 48
	// inputRunes caps the owner message and the reply sent to the model.
	inputRunes = 2000
	// MaxAutoRunes is the longest generated title (REQ-TTL-003).
	MaxAutoRunes = 60
	// MaxFallbackRunes is the longest fallback title, "…" included
	// (REQ-TTL-004).
	MaxFallbackRunes = 50
	// MaxOwnerRunes is the longest title the owner may give (REQ-TTL-008).
	MaxOwnerRunes = 80
)

var (
	// ErrBusy is returned by Regenerate while a pass runs for the session
	// (REQ-TTL-010).
	ErrBusy = errors.New("a title is already being generated")
	// ErrNothingToTitle is returned by Regenerate when the session has no
	// owner message yet.
	ErrNothingToTitle = errors.New("the session has no message to name it from yet")
	// ErrNoTitleStore is returned when the session store cannot store titles.
	ErrNoTitleStore = errors.New("the session store does not store titles")
)

// systemPrompt is the instruction of the title call (REQ-TTL-003).
const systemPrompt = "You name chat sessions. Reply with the title only: 3 to 6 words that say what the conversation is about, " +
	"in the same language as the owner's message. No quotes, no trailing period, no emoji, no prefix such as \"Title:\", at most 60 characters."

// Service implements core.SessionTitler.
type Service struct {
	base  context.Context
	store core.SessionStore
	llm   core.LLMRegistry
	// LookupAgent returns the config of an agent, or of a team's lead.
	LookupAgent func(name string) (core.AgentConfig, bool)

	mu       sync.Mutex
	inFlight map[string]bool
	wg       sync.WaitGroup
}

// New creates a Service whose passes derive from base. Cancel base to stop
// passes; Wait joins them.
func New(base context.Context, store core.SessionStore, llm core.LLMRegistry) *Service {
	if base == nil {
		base = context.Background()
	}
	return &Service{base: base, store: store, llm: llm, inFlight: map[string]bool{}}
}

// TurnEnded starts one background pass for a session that has no title yet
// and returns at once (REQ-TTL-001). A pass already running for the session
// makes it a no-op.
func (s *Service) TurnEnded(req core.TitleRequest) {
	if req.SessionID == "" || s.base.Err() != nil || !s.claim(req.SessionID) {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.release(req.SessionID)
		ctx, cancel := context.WithTimeout(s.base, passTimeout)
		defer cancel()
		info, err := s.store.Info(ctx, req.SessionID)
		if err != nil || info.TitleSource != "" || !autoChannel(info.Channel) {
			return
		}
		if _, err := s.run(ctx, req, false); err != nil && !errors.Is(err, ErrNothingToTitle) && s.base.Err() == nil {
			slog.Warn("session title pass failed", "session_id", req.SessionID, "agent", req.AgentName, "error", err)
		}
	}()
}

// autoChannel reports whether sessions of channel get an automatic title
// after a turn (REQ-TTL-001): web, CLI, Telegram, and other owner channels.
// Library sessions belong to the program that embeds lana, which names them
// itself, so no model call is spent on them.
func autoChannel(channel string) bool {
	return channel != "library"
}

// Regenerate names the session now, replacing any title, and returns the new
// title (REQ-TTL-010). It runs in the caller's goroutine on the service base
// context, so a client that goes away does not lose the title.
func (s *Service) Regenerate(sessionID, agentName string) (string, error) {
	if !s.claim(sessionID) {
		return "", ErrBusy
	}
	s.wg.Add(1)
	defer s.wg.Done()
	defer s.release(sessionID)
	ctx, cancel := context.WithTimeout(s.base, passTimeout)
	defer cancel()
	return s.run(ctx, core.TitleRequest{SessionID: sessionID, AgentName: agentName}, true)
}

// Pending reports whether a pass runs for the session in this process.
func (s *Service) Pending(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight[sessionID]
}

// Wait blocks until every running pass has returned.
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) claim(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight == nil {
		s.inFlight = map[string]bool{}
	}
	if s.inFlight[sessionID] {
		return false
	}
	s.inFlight[sessionID] = true
	return true
}

func (s *Service) release(sessionID string) {
	s.mu.Lock()
	delete(s.inFlight, sessionID)
	s.mu.Unlock()
}

// run generates and stores one title. force replaces any stored title; else
// the write applies only while the session is still untitled, so an owner
// rename made during the call wins (REQ-TTL-006).
func (s *Service) run(ctx context.Context, req core.TitleRequest, force bool) (string, error) {
	titleStore, ok := s.store.(core.SessionTitleStore)
	if !ok {
		return "", ErrNoTitleStore
	}
	events, err := s.store.ChainEvents(ctx, req.SessionID)
	if err != nil {
		return "", err
	}
	owner, reply, selection := Conversation(events)
	if owner == "" {
		return "", ErrNothingToTitle
	}
	title := s.generate(ctx, req, owner, reply, selection)
	if title == "" {
		title = Fallback(owner)
	}
	if title == "" {
		return "", ErrNothingToTitle
	}
	if _, err := titleStore.SetSessionTitle(ctx, req.SessionID, title, core.TitleSourceAuto, !force); err != nil {
		return "", err
	}
	return title, nil
}

// generate asks the background chain for a title and returns "" when no
// background model is available or the call fails (REQ-TTL-004).
func (s *Service) generate(ctx context.Context, req core.TitleRequest, owner, reply string, selection core.ModelSelection) string {
	if s.llm == nil {
		return ""
	}
	chain, requested, err := s.chain(req, selection)
	if errors.Is(err, llmcall.ErrNoBackgroundModel) {
		llmcall.WarnNoBackground(req.AgentName, Source)
		return ""
	}
	if err != nil {
		slog.Debug("session title: no chain", "session_id", req.SessionID, "agent", req.AgentName, "error", err)
		return ""
	}
	stream, _, err := llmcall.Stream(ctx, s.llm, chain, core.ChatRequest{
		System:          systemPrompt,
		Messages:        []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: Prompt(owner, reply)}}}},
		MaxTokens:       maxTokens,
		ReasoningEffort: "off",
	}, core.LLMCallMeta{Source: Source, Agent: req.AgentName, SessionID: req.SessionID, Requested: requested}, nil)
	if err != nil {
		slog.Debug("session title: call failed", "session_id", req.SessionID, "agent", req.AgentName, "error", err)
		return ""
	}
	text, err := collect(stream)
	if err != nil {
		slog.Debug("session title: stream failed", "session_id", req.SessionID, "agent", req.AgentName, "error", err)
		return ""
	}
	return Clean(text)
}

// chain returns the background chain of the pass (REQ-TTL-002): the turn's
// chain when the request carries it, else the agent's configured model with
// the session's model selection applied, without Claude Code targets, else
// [router] background_model.
func (s *Service) chain(req core.TitleRequest, selection core.ModelSelection) ([]core.LLMTarget, string, error) {
	if len(req.Targets) > 0 {
		requested := req.Requested
		if requested == "" {
			requested = llmcall.Address(req.Targets[0])
		}
		return llmcall.BackgroundChain(s.llm, req.Targets, requested)
	}
	agentName := req.AgentName
	if selection.Agent != "" {
		agentName = selection.Agent
	}
	if s.LookupAgent == nil {
		return nil, "", errors.New("no agent lookup")
	}
	cfg, ok := s.LookupAgent(agentName)
	if !ok {
		return nil, "", errors.New("unknown agent " + agentName)
	}
	cfg = llmcall.NormalizeAgent(cfg)
	cfg.Provider, cfg.Model = llmcall.Select(cfg.Provider, cfg.Model, selection)
	return llmcall.AgentBackgroundChain(s.llm, cfg)
}

// collect reads the reply text of a stream.
func collect(stream core.ChatStream) (string, error) {
	var text strings.Builder
	for event, err := range stream {
		if err != nil {
			return "", err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			text.WriteString(event.Text)
		case core.ChatBlock:
			if event.Block.Kind == core.BlockText {
				text.WriteString(event.Block.Text)
			}
		}
	}
	return text.String(), nil
}

// Conversation returns the first owner message of events, the assistant text
// that answered it (until the next owner message), and the last model
// selection. Runtime notes and the injected <context> blocks are skipped.
func Conversation(events []core.SessionEvent) (owner, reply string, selection core.ModelSelection) {
	var replies []string
	done := false
	for _, event := range events {
		switch event.Kind {
		case core.KindModelSelection:
			var sel core.ModelSelection
			if json.Unmarshal(event.Payload, &sel) == nil {
				if sel.Agent == "" {
					sel.Agent = selection.Agent
				}
				selection = sel
			}
		case core.KindUserMessage:
			if done {
				continue
			}
			text := ownerText(event.Payload)
			if text == "" {
				continue
			}
			if owner == "" {
				owner = text
				continue
			}
			done = true
		case core.KindAssistantMessage:
			if owner == "" || done {
				continue
			}
			var msg core.AssistantMessage
			if json.Unmarshal(event.Payload, &msg) != nil {
				continue
			}
			if text := strings.TrimSpace(joinText(msg.Content)); text != "" {
				replies = append(replies, text)
			}
		}
	}
	return owner, strings.Join(replies, "\n\n"), selection
}

// ownerText returns the owner text of a user/message payload, or "" for a
// runtime note, a continue message, or a message without text.
func ownerText(payload json.RawMessage) string {
	var msg core.UserMessage
	if json.Unmarshal(payload, &msg) != nil || msg.Runtime || msg.Continue {
		return ""
	}
	blocks := msg.Content
	if msg.ContextBlocks > 0 {
		if msg.ContextBlocks >= len(blocks) {
			return ""
		}
		blocks = blocks[msg.ContextBlocks:]
	}
	for len(blocks) > 0 && blocks[0].Kind == core.BlockText && injected(blocks[0].Text) {
		blocks = blocks[1:]
	}
	return strings.TrimSpace(joinText(blocks))
}

// injected reports whether a text block is a lana-injected prefix block.
func injected(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<context>") || strings.HasPrefix(text, "<system-update>")
}

func joinText(blocks []core.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Kind == core.BlockText {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Prompt is the user message of the title call: the owner message and the
// reply, each cut to 2,000 characters (REQ-TTL-002).
func Prompt(owner, reply string) string {
	var b strings.Builder
	b.WriteString("Owner message:\n")
	b.WriteString(truncate(owner, inputRunes))
	if reply = strings.TrimSpace(reply); reply != "" {
		b.WriteString("\n\nAssistant reply:\n")
		b.WriteString(truncate(reply, inputRunes))
	}
	b.WriteString("\n\nWrite the session title.")
	return b.String()
}

func truncate(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

// titlePrefixes are label prefixes some models put before the title.
var titlePrefixes = []string{"title:", "session title:", "judul:", "titel:", "título:", "titre:"}

// Clean turns a model reply into a title (REQ-TTL-003): the first non-empty
// line without reasoning tags, markdown, a "Title:" prefix, surrounding
// quotes, or trailing punctuation, with whitespace collapsed and cut to 60
// characters at a word boundary. It returns "" when nothing is left.
func Clean(raw string) string {
	text := stripThink(raw)
	line := ""
	for _, candidate := range strings.Split(text, "\n") {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			line = candidate
			break
		}
	}
	for range 3 {
		before := line
		line = strings.TrimLeft(line, "#>*-• \t")
		line = strings.Trim(line, "*_`~ \t")
		lower := strings.ToLower(line)
		for _, prefix := range titlePrefixes {
			if strings.HasPrefix(lower, prefix) {
				line = strings.TrimSpace(line[len(prefix):])
				break
			}
		}
		line = trimQuotes(line)
		line = strings.TrimRightFunc(line, func(r rune) bool {
			return r == '.' || r == '。' || r == ',' || r == ';' || r == ':' || r == '…' || unicode.IsSpace(r)
		})
		if line == before {
			break
		}
	}
	line = collapse(line)
	return cutWords(line, MaxAutoRunes, "")
}

// stripThink removes <think>…</think> blocks a reasoning model may emit.
func stripThink(text string) string {
	for {
		start := strings.Index(text, "<think>")
		if start < 0 {
			return text
		}
		end := strings.Index(text[start:], "</think>")
		if end < 0 {
			return text[:start]
		}
		text = text[:start] + text[start+end+len("</think>"):]
	}
}

var quotePairs = [][2]string{{`"`, `"`}, {"'", "'"}, {"“", "”"}, {"‘", "’"}, {"«", "»"}, {"„", "“"}, {"「", "」"}, {"`", "`"}}

func trimQuotes(text string) string {
	for {
		trimmed := false
		for _, pair := range quotePairs {
			if len(text) >= len(pair[0])+len(pair[1]) && strings.HasPrefix(text, pair[0]) && strings.HasSuffix(text, pair[1]) {
				text = strings.TrimSpace(text[len(pair[0]) : len(text)-len(pair[1])])
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	// A lone leading or trailing quote is dropped too.
	text = strings.TrimLeft(text, "\"'“”‘’«»`")
	text = strings.TrimRight(text, "\"'“”‘’«»`")
	return strings.TrimSpace(text)
}

// collapse joins every run of whitespace, newlines included, into one space.
func collapse(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// cutWords cuts text to at most limit runes, ellipsis included, at the last
// word boundary that fits; a single long word is cut hard.
func cutWords(text string, limit int, ellipsis string) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	room := limit - utf8.RuneCountInString(ellipsis)
	if room <= 0 {
		return string(runes[:limit])
	}
	cut := string(runes[:room])
	// Keep whole words when the next rune starts a new word.
	if !unicode.IsSpace(runes[room]) {
		if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > 0 {
			cut = cut[:i]
		}
	}
	cut = strings.TrimRightFunc(cut, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == ';' || r == ':' || r == '-'
	})
	return cut + ellipsis
}

// Fallback is the title used without a model (REQ-TTL-004): the first owner
// message on one line, cut at a word boundary to at most 50 characters with
// "…".
func Fallback(owner string) string {
	return cutWords(collapse(owner), MaxFallbackRunes, "…")
}

// OwnerTitle normalizes an owner rename (REQ-TTL-008): whitespace runs,
// newlines included, become one space and the ends are trimmed. ok is false
// when the result is longer than 80 characters.
func OwnerTitle(raw string) (string, bool) {
	title := collapse(raw)
	return title, utf8.RuneCountInString(title) <= MaxOwnerRunes
}

// Fixed builds a fixed title such as "Workflow · <name>" (REQ-TTL-007), cut
// to the owner limit.
func Fixed(prefix, name string) string {
	name = collapse(name)
	if name == "" {
		return prefix
	}
	return cutWords(prefix+" · "+name, MaxOwnerRunes, "…")
}

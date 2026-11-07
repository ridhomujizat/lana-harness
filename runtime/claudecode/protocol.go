package claudecode

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// The stream-json shapes below were observed from Claude Code 2.1.319 and are
// kept in testdata/*.jsonl. Fields lana does not use are not decoded.

// envelope is the part every stdout line shares.
type envelope struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	RequestID       string          `json:"request_id"`
	Request         json.RawMessage `json:"request"`
	Response        json.RawMessage `json:"response"`
}

// fromSubagent reports whether the line belongs to a Claude Code subagent
// (Task tool). Those lines keep the turn alive but are not part of the main
// reply.
func (e envelope) fromSubagent() bool {
	return e.ParentToolUseID != nil && *e.ParentToolUseID != ""
}

// systemInit is the system/init line, sent at the start of every turn.
type systemInit struct {
	SessionID string   `json:"session_id"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools"`
	Version   string   `json:"claude_code_version"`
}

type streamEvent struct {
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type assistantLine struct {
	Message struct {
		Model   string         `json:"model"`
		Content []contentBlock `json:"content"`
	} `json:"message"`
	// Error is set on synthetic error messages, for example
	// "authentication_failed" when Claude Code is not logged in.
	Error             string `json:"error"`
	IsAPIErrorMessage bool   `json:"is_api_error_message"`
}

type userLine struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type rateLimitLine struct {
	Info struct {
		Status         string   `json:"status"`
		ResetsAt       int64    `json:"resetsAt"`
		RateLimitType  string   `json:"rateLimitType"`
		Utilization    *float64 `json:"utilization"`
		UnifiedWindows map[string]struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    int64    `json:"resetsAt"`
			Status      string   `json:"status"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

type resultUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type modelUsage struct {
	CostUSD float64 `json:"costUSD"`
}

// resultLine ends a turn. total_cost_usd is cumulative for the Claude session;
// model_usage_since_last_result holds this turn's cost per model.
type resultLine struct {
	Subtype        string                `json:"subtype"`
	IsError        bool                  `json:"is_error"`
	StopReason     *string               `json:"stop_reason"`
	TerminalReason string                `json:"terminal_reason"`
	Result         *string               `json:"result"`
	Errors         []string              `json:"errors"`
	DurationMS     int64                 `json:"duration_ms"`
	TotalCostUSD   float64               `json:"total_cost_usd"`
	Usage          resultUsage           `json:"usage"`
	SinceLast      map[string]modelUsage `json:"model_usage_since_last_result"`
	ModelUsage     map[string]modelUsage `json:"modelUsage"`
}

// canUseTool is the request of a control_request with subtype can_use_tool.
type canUseTool struct {
	Subtype        string          `json:"subtype"`
	ToolName       string          `json:"tool_name"`
	Input          json.RawMessage `json:"input"`
	Description    string          `json:"description"`
	DecisionReason string          `json:"decision_reason"`
	ToolUseID      string          `json:"tool_use_id"`
}

// controlResponseBody is the response of a control_response line.
type controlResponseBody struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
	Error     string          `json:"error"`
}

// --- lines lana writes ---

func controlRequestLine(requestID string, request any) []byte {
	return mustLine(map[string]any{"type": "control_request", "request_id": requestID, "request": request})
}

func controlSuccessLine(requestID string, response any) []byte {
	return mustLine(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": requestID, "response": response,
	}})
}

func controlErrorLine(requestID, message string) []byte {
	return mustLine(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "error", "request_id": requestID, "error": message,
	}})
}

// permissionResponse is the answer to can_use_tool.
func permissionResponse(allow bool, message string, input json.RawMessage) map[string]any {
	if allow {
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return map[string]any{"behavior": "allow", "updatedInput": input}
	}
	if message == "" {
		message = "denied by lana"
	}
	return map[string]any{"behavior": "deny", "message": message}
}

// userMessageLine renders the user input as a stream-json user line. Image
// blocks become base64 image blocks; other block kinds are skipped.
func userMessageLine(blocks []core.ContentBlock) []byte {
	content := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Kind {
		case core.BlockText:
			if b.Text != "" {
				content = append(content, map[string]any{"type": "text", "text": b.Text})
			}
		case core.BlockImage:
			if len(b.Data) == 0 {
				continue
			}
			media := b.MediaType
			if media == "" {
				media = "image/png"
			}
			content = append(content, map[string]any{"type": "image", "source": map[string]any{
				"type": "base64", "media_type": media, "data": base64.StdEncoding.EncodeToString(b.Data),
			}})
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	return mustLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}})
}

func mustLine(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Only maps of strings and raw JSON reach here.
		panic("claudecode: marshal: " + err.Error())
	}
	return append(b, '\n')
}

// --- stdout line mapping ---

// mapper turns the stdout lines of one turn into RuntimeEvents. Control
// requests and the result line are returned to the caller, which owns the
// process.
type mapper struct {
	emit func(core.RuntimeEvent)
	log  *slog.Logger
	// proc holds the per-process state shared across turns.
	proc *procState
	// authFailed is set when Claude Code reports it is not logged in.
	authFailed bool
	apiError   string
}

// procState is the state of one Claude process that outlives a turn.
type procState struct {
	metaSent  bool
	claudeID  string
	model     string
	prevTotal float64
	hasTotal  bool
}

// lineResult is what handle hands back to the turn loop.
type lineResult struct {
	result        *resultLine
	controlID     string
	canUseTool    *canUseTool
	otherControl  string // subtype of an unsupported control_request
	cancelControl string // request_id of a control_cancel_request
}

func (m *mapper) handle(line []byte) lineResult {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		m.log.Debug("claude: unreadable line", "err", err)
		return lineResult{}
	}
	switch env.Type {
	case "system":
		if env.Subtype == "init" {
			m.handleInit(line)
		}
	case "stream_event":
		if env.fromSubagent() {
			return lineResult{}
		}
		var ev streamEvent
		if json.Unmarshal(line, &ev) != nil || ev.Event.Type != "content_block_delta" {
			return lineResult{}
		}
		switch ev.Event.Delta.Type {
		case "text_delta":
			if ev.Event.Delta.Text != "" {
				m.emit(core.RuntimeEvent{Kind: core.RuntimeTextDelta, Text: ev.Event.Delta.Text})
			}
		case "thinking_delta":
			if ev.Event.Delta.Thinking != "" {
				m.emit(core.RuntimeEvent{Kind: core.RuntimeThinkingDelta, Text: ev.Event.Delta.Thinking})
			}
		}
	case "assistant":
		if env.fromSubagent() {
			return lineResult{}
		}
		m.handleAssistant(line)
	case "user":
		if env.fromSubagent() {
			return lineResult{}
		}
		m.handleUser(line)
	case "rate_limit_event":
		m.handleRateLimit(line)
	case "control_request":
		var req canUseTool
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return lineResult{controlID: env.RequestID, otherControl: "invalid"}
		}
		if req.Subtype == "can_use_tool" {
			return lineResult{controlID: env.RequestID, canUseTool: &req}
		}
		return lineResult{controlID: env.RequestID, otherControl: req.Subtype}
	case "control_cancel_request":
		return lineResult{cancelControl: env.RequestID}
	case "result":
		var res resultLine
		if err := json.Unmarshal(line, &res); err != nil {
			m.log.Debug("claude: unreadable result", "err", err)
			res = resultLine{IsError: true, Subtype: "unreadable"}
		}
		return lineResult{result: &res}
	default:
		m.log.Debug("claude: ignored line", "type", env.Type, "subtype", env.Subtype)
	}
	return lineResult{}
}

func (m *mapper) handleInit(line []byte) {
	var init systemInit
	if json.Unmarshal(line, &init) != nil {
		return
	}
	if init.SessionID != "" {
		m.proc.claudeID = init.SessionID
	}
	if init.Model != "" {
		m.proc.model = init.Model
	}
	if m.proc.metaSent {
		return
	}
	m.proc.metaSent = true
	m.emit(core.RuntimeEvent{
		Kind: core.RuntimeSessionMeta, ClaudeSessionID: init.SessionID, Model: init.Model,
		Tools: init.Tools, Version: init.Version,
	})
}

func (m *mapper) handleAssistant(line []byte) {
	var msg assistantLine
	if json.Unmarshal(line, &msg) != nil {
		return
	}
	if msg.IsAPIErrorMessage || msg.Error != "" {
		// Synthetic message, for example "Not logged in · Please run /login".
		if msg.Error == "authentication_failed" {
			m.authFailed = true
		}
		var text []string
		for _, b := range msg.Message.Content {
			if b.Type == "text" && b.Text != "" {
				text = append(text, b.Text)
			}
		}
		m.apiError = strings.Join(text, "\n")
		return
	}
	var text []string
	var calls []core.RuntimeEvent
	for _, b := range msg.Message.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				text = append(text, b.Text)
			}
		case "tool_use":
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			calls = append(calls, core.RuntimeEvent{
				Kind: core.RuntimeToolCall, ID: b.ID, Name: b.Name, Input: input, Risk: ToolRisk(b.Name),
			})
		}
	}
	if len(text) > 0 {
		m.emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: strings.Join(text, "\n\n"), Model: msg.Message.Model})
	}
	for _, c := range calls {
		m.emit(c)
	}
}

func (m *mapper) handleUser(line []byte) {
	var msg userLine
	if json.Unmarshal(line, &msg) != nil {
		return
	}
	var blocks []contentBlock
	if json.Unmarshal(msg.Message.Content, &blocks) != nil {
		return // plain string content: not a tool result
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		m.emit(core.RuntimeEvent{
			Kind: core.RuntimeToolResult, ID: b.ToolUseID, Output: toolResultText(b.Content), IsError: b.IsError,
		})
	}
}

// toolResultText flattens tool_result content: a string, or a list of blocks
// whose text parts are joined. Image parts are noted as "[image]".
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return string(raw)
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func (m *mapper) handleRateLimit(line []byte) {
	windows := parseRateLimit(line)
	if len(windows) > 0 {
		m.emit(core.RuntimeEvent{Kind: core.RuntimeLimits, Windows: windows})
	}
}

// parseRateLimit reads a rate_limit_event. unifiedWindows, when present, lists
// every plan window with its utilization (a fraction); otherwise the single
// window named by rateLimitType is used.
func parseRateLimit(line []byte) []core.RuntimeLimitWindow {
	var ev rateLimitLine
	if json.Unmarshal(line, &ev) != nil {
		return nil
	}
	info := ev.Info
	var windows []core.RuntimeLimitWindow
	if len(info.UnifiedWindows) > 0 {
		names := make([]string, 0, len(info.UnifiedWindows))
		for name := range info.UnifiedWindows {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w := info.UnifiedWindows[name]
			windows = append(windows, core.RuntimeLimitWindow{
				Type: name, Status: w.Status, Utilization: w.Utilization, ResetsAt: unixTime(w.ResetsAt),
			})
		}
		return windows
	}
	if info.RateLimitType == "" && info.Status == "" {
		return nil
	}
	return []core.RuntimeLimitWindow{{
		Type: info.RateLimitType, Status: info.Status, Utilization: info.Utilization, ResetsAt: unixTime(info.ResetsAt),
	}}
}

func unixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// usageEvent builds the usage event of a result. lana's InputTokens counts all
// prompt tokens, so Claude's uncached, cache-write, and cache-read input tokens
// are summed; CachedInputTokens is the cache-read part.
func (m *mapper) usageEvent(res *resultLine) core.RuntimeEvent {
	u := res.Usage
	cost := 0.0
	model := m.proc.model
	if res.SinceLast != nil {
		names := make([]string, 0, len(res.SinceLast))
		for name, mu := range res.SinceLast {
			cost += mu.CostUSD
			names = append(names, name)
		}
		if len(names) == 1 {
			model = names[0]
		}
	} else if m.proc.hasTotal {
		cost = res.TotalCostUSD - m.proc.prevTotal
		if cost < 0 {
			cost = 0
		}
	} else {
		cost = res.TotalCostUSD
	}
	m.proc.prevTotal, m.proc.hasTotal = res.TotalCostUSD, true
	return core.RuntimeEvent{
		Kind: core.RuntimeUsage,
		Usage: core.Usage{
			InputTokens:       u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
			OutputTokens:      u.OutputTokens,
			CachedInputTokens: u.CacheReadInputTokens,
		},
		CostUSD: cost, Model: model, DurationMS: res.DurationMS,
	}
}

// notLoggedInMessage is the turn error when Claude Code has no login (REQ-CCR-003).
const notLoggedInMessage = "Claude Code is not logged in. Run `claude`, then `/login`."

// endEvent builds the end event of a result.
func (m *mapper) endEvent(res *resultLine, cancelled bool) core.RuntimeEvent {
	if cancelled {
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopCancelled}
	}
	text := ""
	if res.Result != nil {
		text = *res.Result
	}
	if m.authFailed || (res.IsError && looksNotLoggedIn(text)) || looksNotLoggedIn(m.apiError) {
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: notLoggedInMessage}
	}
	if res.IsError || (res.Subtype != "" && res.Subtype != "success") {
		msg := text
		if msg == "" && m.apiError != "" {
			msg = m.apiError
		}
		if msg == "" && len(res.Errors) > 0 {
			msg = strings.Join(res.Errors, "; ")
		}
		if msg == "" {
			msg = "Claude Code turn failed (" + firstNonEmpty(res.Subtype, res.TerminalReason, "error") + ")"
		}
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopError, IsError: true, ErrorText: truncate(msg, 2000)}
	}
	reason := core.RuntimeStopEndTurn
	if res.StopReason != nil && *res.StopReason != "" {
		reason = *res.StopReason
	}
	return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: reason}
}

func looksNotLoggedIn(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "not logged in") || strings.Contains(t, "please run /login") ||
		strings.Contains(t, "invalid api key") && strings.Contains(t, "/login")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

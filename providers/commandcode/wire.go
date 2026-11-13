package commandcode

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// maxTokensLimit is the ceiling /alpha/generate enforces on max_tokens; a
// larger budget answers 400, so it is clamped (9router-go
// commandCodeMaxTokensLimit).
const maxTokensLimit = 200000

// maxTokensDefault is the budget of a request without one, and of a model
// outside the catalog. It matches the Command Code CLI default
// (max_tokens: maxOutputTokens ?? 64000). A live probe on 2026-11-13
// accepted 8192 and 32000 on /alpha/generate, so the old free-tier cap was
// wrong.
const maxTokensDefault = 64000

// defaultTemperature is the temperature the CLI sends when the caller sets
// none (9router-go buildCommandcodeBody).
const defaultTemperature = 0.3

// envelope is the /alpha/generate body. Struct fields keep the encoding
// order fixed, so one conversation serialises the same prefix every turn
// (the provider caches by prefix).
type envelope struct {
	ThreadID string     `json:"threadId"`
	Memory   string     `json:"memory"`
	Config   wireConfig `json:"config"`
	Params   wireParams `json:"params"`
}

type wireConfig struct {
	WorkingDir    string `json:"workingDir"`
	Date          string `json:"date"`
	Environment   string `json:"environment"`
	Structure     []any  `json:"structure"`
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	MainBranch    string `json:"mainBranch"`
	GitStatus     string `json:"gitStatus"`
	RecentCommits []any  `json:"recentCommits"`
}

type wireParams struct {
	Model           string     `json:"model"`
	Messages        []wireMsg  `json:"messages"`
	Stream          bool       `json:"stream"`
	MaxTokens       int        `json:"max_tokens"`
	Temperature     float64    `json:"temperature"`
	Tools           []wireTool `json:"tools,omitempty"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	System          string     `json:"system,omitempty"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// wireMsg is one message. Content is always a block array: the provider
// never accepts a raw string. Tool results travel as role "tool" messages.
type wireMsg struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

// workingDir is the daemon's working directory, reported as
// config.workingDir and, slugified, as x-project-slug, as the CLI does.
var workingDir = func() string {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		return wd
	}
	return "/"
}()

// projectSlug is the x-project-slug header. The provider rejects an empty
// slug, so a working dir that slugifies to nothing becomes "lana".
var projectSlug = func() string {
	if s := slugify(workingDir); s != "" {
		return s
	}
	return "lana"
}()

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a path into a lower-case dash slug:
// "/Users/u/My Project" → "users-u-my-project".
func slugify(v string) string {
	return strings.Trim(slugNonAlnum.ReplaceAllString(strings.ToLower(v), "-"), "-")
}

// nodePlatform is the platform name in Node's process.platform spelling,
// which the CLI reports as config.environment.
func nodePlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

// buildEnvelope converts a chat request into the /alpha/generate body.
func buildEnvelope(req core.ChatRequest, now time.Time) envelope {
	params := wireParams{
		Model:       req.Model,
		Messages:    convertMessages(req.Messages),
		Stream:      true,
		MaxTokens:   clampMaxTokens(req.MaxTokens, req.Model),
		Temperature: defaultTemperature,
		System:      req.System,
	}
	if req.Temperature != nil {
		params.Temperature = *req.Temperature
	}
	params.ReasoningEffort = reasoningEffort(req.ReasoningEffort)
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		params.Tools = append(params.Tools, wireTool{Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}
	return envelope{
		ThreadID: threadID(req),
		Memory:   "",
		Config: wireConfig{
			WorkingDir:    workingDir,
			Date:          now.UTC().Format("2006-01-02"),
			Environment:   nodePlatform(),
			Structure:     []any{},
			RecentCommits: []any{},
		},
		Params: params,
	}
}

// clampMaxTokens returns the budget sent: the request's, else the model's
// catalog output limit, else the default, never above the provider ceiling.
func clampMaxTokens(n int, model string) int {
	if n <= 0 {
		m, ok := models.Lookup(model)
		if !ok || m.MaxOutput <= 0 {
			return maxTokensDefault
		}
		if m.MaxOutput > maxTokensLimit {
			return maxTokensLimit
		}
		return m.MaxOutput
	}
	if n > maxTokensLimit {
		return maxTokensLimit
	}
	return n
}

// reasoningEffort maps lana's effort ("", off, low, high, max) to the
// OpenAI-style value the provider takes. "" sends no field; off turns thinking
// off at the provider (REQ-KBG-001).
func reasoningEffort(effort string) string {
	switch effort {
	case "off":
		return "off"
	case "low":
		return "low"
	case "high", "max":
		return "high"
	default:
		return ""
	}
}

// threadID derives a conversation-stable UUID from the system prompt and the
// first user turn, which stay fixed while later turns append. One
// conversation keeps one threadId, so the provider prefix cache hits
// (9router-go commandcodePrefixKey + commandcodeSessionUUID).
func threadID(req core.ChatRequest) string {
	firstUser := ""
	for _, msg := range req.Messages {
		if msg.Role == core.RoleUser {
			firstUser = blocksText(msg.Content)
			break
		}
	}
	sum := sha256.Sum256([]byte(req.System + "\n\n\x00" + firstUser))
	h := hex.EncodeToString(sum[:])
	variant := "89ab"[hexNibble(h[16])&0x3]
	return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-" + string(variant) + h[17:20] + "-" + h[20:32]
}

func hexNibble(c byte) int {
	if c >= 'a' {
		return int(c-'a') + 10
	}
	return int(c - '0')
}

// blocksText joins the text blocks of a message.
func blocksText(blocks []core.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Kind == core.BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// convertMessages turns lana messages into provider messages:
//
//   - a user message's tool_result blocks become one role "tool" message
//     each, sent before the rest of the turn; images a tool returned follow
//     in a user message, since a tool result carries text only;
//   - user text and images become one user message of blocks;
//   - an assistant message becomes reasoning, text, and tool-call blocks.
func convertMessages(messages []core.Message) []wireMsg {
	out := make([]wireMsg, 0, len(messages))
	toolNames := map[string]string{}
	for _, msg := range messages {
		switch msg.Role {
		case core.RoleAssistant:
			out = append(out, wireMsg{Role: "assistant", Content: assistantBlocks(msg.Content, toolNames)})
		default:
			var user []any
			var toolImages []any
			for _, b := range msg.Content {
				switch b.Kind {
				case core.BlockToolResult:
					out = append(out, toolResultMessage(b, toolNames[b.ID]))
					for _, img := range b.Images {
						if block := imageBlock(img); block != nil {
							toolImages = append(toolImages, block)
						}
					}
				case core.BlockText:
					user = append(user, map[string]any{"type": "text", "text": b.Text})
				case core.BlockImage:
					if block := imageBlock(b); block != nil {
						user = append(user, block)
					}
				}
			}
			user = append(toolImages, user...)
			if len(user) > 0 {
				out = append(out, wireMsg{Role: "user", Content: user})
			}
		}
	}
	return out
}

// toolResultMessage is the role "tool" message of one tool result. The
// provider validates role "tool" with exactly this block (9router-go
// commandcodeToolResultMessage, verified live 2026-10-12).
func toolResultMessage(b core.ContentBlock, name string) wireMsg {
	output := b.Output
	if output == "" {
		output = b.Text
	}
	if b.IsError && output != "" {
		output = "Error: " + output
	}
	return wireMsg{Role: "tool", Content: []any{map[string]any{
		"type":       "tool-result",
		"toolCallId": b.ID,
		"toolName":   name,
		"output":     map[string]any{"type": "text", "value": output},
	}}}
}

// assistantBlocks converts an assistant turn. A turn with tool calls but no
// reasoning gets a " " reasoning block, as the CLI sends. Tool names are
// recorded so later tool results can name their tool.
func assistantBlocks(blocks []core.ContentBlock, toolNames map[string]string) []any {
	var reasoning strings.Builder
	var text []string
	var calls []any
	for _, b := range blocks {
		switch b.Kind {
		case core.BlockThinking:
			reasoning.WriteString(b.Text)
		case core.BlockText:
			if b.Text != "" {
				text = append(text, b.Text)
			}
		case core.BlockToolUse:
			toolNames[b.ID] = b.Name
			calls = append(calls, map[string]any{
				"type":       "tool-call",
				"toolCallId": b.ID,
				"toolName":   b.Name,
				"input":      toolInput(b.Input),
			})
		}
	}
	out := make([]any, 0, len(calls)+2)
	if reasoning.Len() > 0 || len(calls) > 0 {
		r := reasoning.String()
		if r == "" {
			r = " "
		}
		out = append(out, map[string]any{"type": "reasoning", "text": r})
	}
	if len(text) > 0 {
		out = append(out, map[string]any{"type": "text", "text": strings.Join(text, "\n")})
	}
	out = append(out, calls...)
	if len(out) == 0 {
		out = append(out, map[string]any{"type": "text", "text": ""})
	}
	return out
}

// toolInput returns a tool call's input as an object; empty or invalid
// input becomes {} so it never serialises as null.
func toolInput(raw json.RawMessage) any {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil || v == nil {
		return map[string]any{}
	}
	return v
}

// imageBlock converts an image block to the provider's data-URI image
// block, or nil when it has no data.
func imageBlock(b core.ContentBlock) map[string]any {
	if len(b.Data) == 0 {
		return nil
	}
	mime := b.MediaType
	if mime == "" {
		mime = "image/png"
	}
	uri := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b.Data)
	return map[string]any{"type": "image", "image": uri, "mimeType": mime, "mediaType": mime}
}

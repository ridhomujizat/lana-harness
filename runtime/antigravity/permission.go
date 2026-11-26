package antigravity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// permissionParams is the params of a session/request_permission request.
type permissionParams struct {
	SessionID string `json:"sessionId"`
	ToolCall  struct {
		ToolCallID string          `json:"toolCallId"`
		Name       string          `json:"name"`
		Kind       string          `json:"kind"`
		Title      string          `json:"title"`
		RawInput   json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []permissionOption `json:"options"`
}

// permissionOption is one option of a permission request. The reply selects it
// by kind, not by id (REQ-AGY-006).
type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// riskForKind maps an ACP tool kind to a lana risk class (REQ-AGY-006): read,
// search, think, and fetch are the low read class; edit and write are write;
// execute is exec; anything else is exec, the strictest class that still
// allows an approval.
func riskForKind(kind string) core.Risk {
	switch kind {
	case "read", "search", "think", "fetch":
		return core.RiskRead
	case "edit", "write":
		return core.RiskWrite
	}
	return core.RiskExec
}

// permCall returns the lana tool name and input lana's permission engine
// matches for an ACP tool call, so rules written for native tools apply the
// way they match a native one: execute -> bash{command}, edit -> edit_file{path},
// read -> read_file{path}, search -> grep{path}, fetch -> web_fetch{url}.
// Other kinds keep the lowercased agy name and raw input (REQ-AGY-006).
func permCall(kind, name string, rawInput json.RawMessage) (string, json.RawMessage) {
	value := decodeInput(rawInput)
	switch kind {
	case "execute":
		return "bash", mustRaw(map[string]any{"command": stringField(value, "CommandLine")})
	case "edit":
		return "edit_file", mustRaw(map[string]any{"path": editPath(value)})
	case "read":
		return "read_file", mustRaw(map[string]any{"path": readPath(value)})
	case "search":
		return "grep", mustRaw(map[string]any{"path": searchPath(value)})
	case "fetch":
		return "web_fetch", mustRaw(map[string]any{"url": fetchURL(value)})
	}
	if len(rawInput) == 0 {
		rawInput = json.RawMessage("{}")
	}
	return strings.ToLower(name), rawInput
}

// ruleString renders the rule-match string of an ACP tool call, for example
// "bash(ls -la)", "edit_file(a.txt)", or "read_file(/etc/hosts)".
func ruleString(kind, name string, rawInput json.RawMessage) string {
	value := decodeInput(rawInput)
	switch kind {
	case "execute":
		return "bash(" + stringField(value, "CommandLine") + ")"
	case "edit":
		return "edit_file(" + editPath(value) + ")"
	case "read":
		return "read_file(" + readPath(value) + ")"
	case "search":
		return "grep(" + searchPath(value) + ")"
	case "fetch":
		return "web_fetch(" + fetchURL(value) + ")"
	}
	return strings.ToLower(name)
}

// editPath reads the path-ish field of an edit tool call.
func editPath(value map[string]any) string {
	return firstNonEmpty(stringField(value, "FilePath"), stringField(value, "file_path"),
		stringField(value, "path"), stringField(value, "TargetFile"), stringField(value, "target_file"))
}

// readPath reads the path-ish field of a read tool call.
func readPath(value map[string]any) string {
	return firstNonEmpty(stringField(value, "AbsolutePath"), stringField(value, "FilePath"),
		stringField(value, "file_path"), stringField(value, "path"), stringField(value, "TargetFile"))
}

// searchPath reads the folder-ish field of a search tool call.
func searchPath(value map[string]any) string {
	return firstNonEmpty(stringField(value, "SearchPath"), stringField(value, "DirectoryPath"),
		stringField(value, "path"))
}

// fetchURL reads the URL field of a fetch tool call.
func fetchURL(value map[string]any) string {
	return firstNonEmpty(stringField(value, "Url"), stringField(value, "url"))
}

// answerPermission routes a session/request_permission through lana's approval
// path and writes lana's reply: allow_once when it was allowed, reject_once
// when denied, cancelled when no matching option exists or the turn was
// cancelled (REQ-AGY-006).
func (r *Runtime) answerPermission(ctx context.Context, p *process, turn core.RuntimeTurn, id json.RawMessage, params json.RawMessage, asks map[string]context.CancelFunc) {
	var req permissionParams
	if json.Unmarshal(params, &req) != nil {
		_ = p.write(rpcErrorLine(id, -32602, "invalid permission request"))
		return
	}
	tc := req.ToolCall
	permTool, permInput := permCall(tc.Kind, tc.Name, tc.RawInput)
	approval := core.RuntimeApproval{
		CallID: tc.ToolCallID, ToolName: tc.Name, Input: tc.RawInput, Risk: riskForKind(tc.Kind),
		Rule: ruleString(tc.Kind, tc.Name, tc.RawInput), PermTool: permTool, PermInput: permInput,
		Description: tc.Title,
	}
	actx, cancel := context.WithCancel(ctx)
	asks[string(id)] = cancel
	go func() {
		defer cancel()
		allow := false
		if turn.Approve != nil {
			func() {
				defer func() {
					if v := recover(); v != nil {
						allow = false
						r.log.Error("antigravity: approve panicked", "panic", v)
					}
				}()
				allow, _ = turn.Approve(actx, approval)
			}()
		}
		var result map[string]any
		switch {
		case actx.Err() != nil:
			result = cancelledOutcome()
		default:
			kind := "reject_once"
			if allow {
				kind = "allow_once"
			}
			if optionID := optionIDByKind(req.Options, kind); optionID != "" {
				result = map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
			} else {
				result = cancelledOutcome()
			}
		}
		_ = p.write(rpcResultLine(id, result))
	}()
}

func cancelledOutcome() map[string]any {
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

// optionIDByKind returns the id of the first option of the given kind.
func optionIDByKind(options []permissionOption, kind string) string {
	for _, o := range options {
		if o.Kind == kind {
			return o.OptionID
		}
	}
	return ""
}

// --- small input helpers ---

func decodeInput(raw json.RawMessage) map[string]any {
	var value map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value
}

func stringField(value map[string]any, key string) string {
	s, _ := value[key].(string)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

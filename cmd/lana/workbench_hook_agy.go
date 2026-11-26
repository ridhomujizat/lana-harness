package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// Antigravity (agy) hooks of spec 77 REQ-WRA-009: `lana workbench hook
// session-start --agent agy` and `lana workbench hook post-edit --agent agy`.
// agy sends camelCase JSON on stdin and needs a JSON object on stdout; both
// hooks print `{}` whenever they have nothing to say, on any parse error, and
// exit 0, so a hook never breaks agy.

// agyEmptyAnswer is the answer agy accepts when a hook adds nothing.
const agyEmptyAnswer = "{}"

// agyHookInput is the part of the agy hook JSON the hooks read.
type agyHookInput struct {
	ConversationID string   `json:"conversationId"`
	Cwd            string   `json:"cwd"`
	WorkspacePaths []string `json:"workspacePaths"`
	ToolCall       struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"toolCall"`
}

// agyInjectAnswer is the SessionStart answer that adds a system message to the
// conversation: {"injectSteps":[{"systemMessage":{"systemMessage":"…"}}]}.
type agyInjectAnswer struct {
	InjectSteps []agyInjectStep `json:"injectSteps"`
}

type agyInjectStep struct {
	SystemMessage agySystemMessage `json:"systemMessage"`
}

type agySystemMessage struct {
	SystemMessage string `json:"systemMessage"`
}

// agyPathKeys are the argument names an agy edit tool may give the file in,
// in the order they are tried: write_file/edit_file use Path, others
// TargetFile or AbsolutePath.
var agyPathKeys = []string{"Path", "TargetFile", "AbsolutePath", "path", "targetFile", "target_file", "absolutePath", "file_path"}

// readAgyHookInput decodes the hook JSON. ok is false when it is not a JSON
// object.
func readAgyHookInput(in io.Reader) (agyHookInput, bool) {
	var input agyHookInput
	if err := json.NewDecoder(io.LimitReader(in, hookInputMax)).Decode(&input); err != nil {
		return agyHookInput{}, false
	}
	return input, true
}

// workDir is the agent's working folder: cwd, else the first workspace path.
// "" lets the caller fall back to the process's folder (agy runs a hook in the
// hooks.json folder, so that is a last resort).
func (in agyHookInput) workDir() string {
	if in.Cwd != "" {
		return in.Cwd
	}
	for _, p := range in.WorkspacePaths {
		if p != "" {
			return p
		}
	}
	return ""
}

// editedPath returns the file an edit tool call wrote, "" when none is found.
// The args may be an object or a JSON string holding one.
func (in agyHookInput) editedPath() string {
	raw := bytes.TrimSpace(in.ToolCall.Args)
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return ""
		}
		raw = []byte(text)
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	for _, key := range agyPathKeys {
		var value string
		if json.Unmarshal(args[key], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

// workbenchAgySessionStart is the agy SessionStart hook: inside a Workbench
// pane it injects the same notice the Claude Code SessionStart hook adds;
// everywhere else, and on input it cannot read, it prints `{}`.
func workbenchAgySessionStart(in io.Reader, out io.Writer) int {
	input, ok := readAgyHookInput(in)
	if !ok {
		agyWrite(out, agyEmptyAnswer)
		return 0
	}
	id, ok := hookInPane()
	if !ok {
		agyWrite(out, agyEmptyAnswer)
		return 0
	}
	workbenchAgyRecord(id, input.ConversationID)
	cwd := input.workDir()
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	answer := agyInjectAnswer{InjectSteps: []agyInjectStep{{
		SystemMessage: agySystemMessage{SystemMessage: workbenchHookText(id, hookGitRoot(cwd))},
	}}}
	data, err := json.Marshal(answer)
	if err != nil {
		agyWrite(out, agyEmptyAnswer)
		return 0
	}
	agyWrite(out, string(data))
	return 0
}

// workbenchAgyPostEdit is the agy PostToolUse hook for the edit tools: it
// reports the worktree of the written file exactly like the Claude Code
// pre-edit hook (after the edit instead of before: agy's PreToolUse needs a
// decision, and "allow" would auto-approve the tool). It always prints `{}`
// and exits 0, a panic included.
func workbenchAgyPostEdit(in io.Reader, out io.Writer) int {
	defer agyWrite(out, agyEmptyAnswer)
	defer func() { _ = recover() }()
	input, ok := readAgyHookInput(in)
	if !ok {
		return 0
	}
	id, ok := hookInPane()
	if !ok {
		return 0
	}
	workbenchAgyRecord(id, input.ConversationID)
	return workbenchAgyReport(id, input.workDir(), input.editedPath())
}

// agyWrite prints one answer line; a write error is ignored (agy then reads
// nothing, which it treats like no answer).
func agyWrite(out io.Writer, answer string) {
	_, _ = fmt.Fprintln(out, answer)
}

// workbenchAgyReport reports an edit; a variable so tests can see what the
// post-edit hook passes on without a daemon or a repo.
var workbenchAgyReport = workbenchReportEdit

// workbenchAgyRecord records the agy conversation of a pane for the Workbench
// core (spec 83 REQ-WAG-001); a variable so tests never write a real home.
var workbenchAgyRecord = recordAgyConversation

// recordAgyConversation writes <Lana home>/workbench/agents/<pane>.json with the
// conversation id and this process's HOME (the pane's account). The Lana home
// is the core's LANA_WORKBENCH_HOME, else LANA_HOME; without either, or with an
// id of another shape, nothing is written. A failure is ignored: the hook's
// answer never depends on it.
func recordAgyConversation(pane, conversation string) {
	if conversation == "" {
		return
	}
	lanaHome := os.Getenv(workbench.EnvWorkbenchHome)
	if lanaHome == "" {
		lanaHome = os.Getenv("LANA_HOME")
	}
	if lanaHome == "" {
		return
	}
	home, _ := os.UserHomeDir()
	_ = workbench.WriteAgentRecord(lanaHome, pane, workbench.AgentRecord{
		Agent: workbench.AgentAgy, ConversationID: conversation, Home: home, At: time.Now(),
	})
}

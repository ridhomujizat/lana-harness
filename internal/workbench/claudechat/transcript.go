package claudechat

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const (
	maxTranscriptLine = 8 << 20 // lines larger than this are skipped (REQ-WC-010)
	maxInputString    = 4096    // tool input strings longer than this are cut
	maxResultBytes    = 4096    // tool result text cut
	maxResultLines    = 60      // tool result lines cut
	maxDiffLines      = 200     // diff lines cut
	defaultWindow     = 200000
	longWindow        = 1000000
	maxTasks          = 50  // newest tasks kept (REQ-WC-016)
	maxTaskTitle      = 120 // task title cut
)

// Transcript reads one Claude Code transcript file incrementally. Only the
// methods are safe for concurrent use; Poll advances the read offset.
type Transcript struct {
	mu sync.Mutex

	// longSeen is set once usage passed the default window (a 1M session).
	longSeen bool

	// includeSidechain keeps isSidechain lines, which is what a subagent
	// transcript holds (REQ-WC-017).
	includeSidechain bool

	path   string
	cwd    string
	offset int64

	items   []Item
	index   map[string]int // item id -> position in items
	pending map[string]int // tool_use_id -> position in items
	changed map[int]bool   // positions changed by the current Poll

	meta        Meta
	metaChanged bool

	turn *turnState

	lineOffsets map[string]int64 // line id -> byte offset, for ImageBytes
}

// turnState tracks the turn that starts at the last user prompt.
type turnState struct {
	startedAt    time.Time
	lastAt       time.Time
	hasAssistant bool
	outputTokens int64
	seenIDs      map[string]bool
}

// Open returns a reader for the transcript at path. cwd is the conversation's
// working directory, used to relativize file paths in tool summaries.
func Open(path, cwd string) *Transcript {
	return &Transcript{
		path:        path,
		cwd:         cwd,
		index:       map[string]int{},
		pending:     map[string]int{},
		lineOffsets: map[string]int64{},
	}
}

// OpenSubagent returns a reader for a subagent's own transcript. It is the same
// reader as Open, except that isSidechain lines are kept: in a subagent file
// every line is a sidechain (REQ-WC-017).
func OpenSubagent(path, cwd string) *Transcript {
	t := Open(path, cwd)
	t.includeSidechain = true
	return t
}

// agentIDPattern is the shape of an agent id accepted from a client: 1 to 64
// letters, digits, underscores or dashes (REQ-WC-017).
var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidAgentID reports whether id may name a subagent.
func ValidAgentID(id string) bool {
	return agentIDPattern.MatchString(id)
}

// SubagentPaths returns the transcript and meta paths of a subagent, given the
// main transcript path and the agent id: <dir>/<base>/subagents/agent-<id>.jsonl
// and .meta.json. Both paths are "" when the id is not valid (REQ-WC-017).
func SubagentPaths(transcriptPath, agentID string) (jsonl, meta string) {
	if transcriptPath == "" || !ValidAgentID(agentID) {
		return "", ""
	}
	base := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	prefix := filepath.Join(filepath.Dir(transcriptPath), base, "subagents", "agent-"+agentID)
	return prefix + ".jsonl", prefix + ".meta.json"
}

// ReadSubagentMeta reads a subagent meta file. It reports false when the file is
// missing or unreadable, which is not an error (REQ-WC-017).
func ReadSubagentMeta(path string) (SubagentMeta, bool) {
	if path == "" {
		return SubagentMeta{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return SubagentMeta{}, false
	}
	var m SubagentMeta
	if json.Unmarshal(raw, &m) != nil {
		return SubagentMeta{}, false
	}
	return m, true
}

// Poll reads everything appended since the last call and returns the items that
// are new or changed, in transcript order. A partial last line (no trailing
// newline) is left for the next call. A missing file is not an error: Poll
// returns no changes and a nil error.
func (t *Transcript) Poll() ([]Item, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	f, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, false, err
	}

	t.metaChanged = false
	t.changed = map[int]bool{}

	r := bufio.NewReaderSize(f, 64*1024)
	pos := t.offset
	for {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 {
			if err == nil || err == io.EOF {
				break
			}
			return nil, false, err
		}
		if err != nil && err != io.EOF {
			return nil, false, err
		}
		if line[len(line)-1] != '\n' {
			break // partial last line, leave it for the next call
		}
		start := pos
		pos += int64(len(line))
		if len(line) > maxTranscriptLine {
			t.bumpUnknown()
			t.offset = pos
			continue
		}
		t.processLine(bytes.TrimRight(line, "\r\n"), start)
		t.offset = pos
	}

	if len(t.changed) == 0 {
		return nil, t.metaChanged, nil
	}
	idxs := make([]int, 0, len(t.changed))
	for i := range t.changed {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	out := make([]Item, len(idxs))
	for k, i := range idxs {
		out[k] = t.items[i]
	}
	return out, t.metaChanged, nil
}

// Snapshot returns the last max items. truncatedBefore reports that older items
// exist before the returned window (REQ-WC-015).
func (t *Transcript) Snapshot(max int) ([]Item, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if max <= 0 || max >= len(t.items) {
		return append([]Item(nil), t.items...), false
	}
	return append([]Item(nil), t.items[len(t.items)-max:]...), true
}

// Before returns the n items before the item with id. more reports that older
// items exist before those returned.
func (t *Transcript) Before(id string, n int) ([]Item, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx, ok := t.index[id]
	if !ok {
		return nil, false
	}
	if n <= 0 {
		return nil, idx > 0
	}
	start := idx - n
	if start < 0 {
		start = 0
	}
	return append([]Item(nil), t.items[start:idx]...), start > 0
}

// Meta returns the transcript's current meta state. The task list is a copy, so
// a caller cannot alias the reader's slice (REQ-WC-016).
func (t *Transcript) Meta() Meta {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.meta
	if len(t.meta.Tasks) > 0 {
		m.Tasks = append([]Task(nil), t.meta.Tasks...)
	}
	return m
}

// ImageBytes returns the decoded bytes and media type of the image block a ref
// points at ("<line id>:<block index>"). Only base64 image sources are served.
// It re-reads that one transcript line; image bytes are never held in memory.
func (t *Transcript) ImageBytes(ref string) (string, []byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	i := strings.LastIndexByte(ref, ':')
	if i < 0 {
		return "", nil, false
	}
	base := ref[:i]
	block, err := strconv.Atoi(ref[i+1:])
	if err != nil || block < 0 {
		return "", nil, false
	}
	off, ok := t.lineOffsets[base]
	if !ok {
		return "", nil, false
	}

	f, err := os.Open(t.path)
	if err != nil {
		return "", nil, false
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return "", nil, false
	}
	line, err := bufio.NewReaderSize(f, 64*1024).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return "", nil, false
	}
	var ln rawLine
	if json.Unmarshal(bytes.TrimRight(line, "\r\n"), &ln) != nil {
		return "", nil, false
	}
	var blocks []struct {
		Type   string `json:"type"`
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if json.Unmarshal(ln.Message.Content, &blocks) != nil || block >= len(blocks) {
		return "", nil, false
	}
	blk := blocks[block]
	if blk.Type != "image" || blk.Source.Type != "base64" {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(blk.Source.Data)
	if err != nil {
		return "", nil, false
	}
	return blk.Source.MediaType, data, true
}

// rawLine is the part of a transcript line the reader uses. Fields it does not
// know are ignored.
type rawLine struct {
	Type             string     `json:"type"`
	Subtype          string     `json:"subtype"`
	UUID             string     `json:"uuid"`
	Timestamp        string     `json:"timestamp"`
	Cwd              string     `json:"cwd"`
	Version          string     `json:"version"`
	Effort           string     `json:"effort"`
	IsSidechain      bool       `json:"isSidechain"`
	IsMeta           bool       `json:"isMeta"`
	IsCompactSummary bool       `json:"isCompactSummary"`
	PermissionMode   string     `json:"permissionMode"`
	Message          rawMessage `json:"message"`

	// Task metadata (REQ-WC-016): a queue-operation carries a task-notification
	// in Content, and a tool result line carries toolUseResult.
	Operation     string          `json:"operation"`
	Content       json.RawMessage `json:"content"`
	ToolUseResult toolUseResult   `json:"toolUseResult"`
}

// toolUseResult is the per-tool metadata Claude Code attaches to a result line.
// Only the fields background tasks and subagents need are read (REQ-WC-016).
type toolUseResult struct {
	IsAsync          bool   `json:"isAsync"`
	Status           string `json:"status"`
	AgentID          string `json:"agentId"`
	Description      string `json:"description"`
	BackgroundTaskID string `json:"backgroundTaskId"`
}

type rawMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *usage          `json:"usage"`
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// processLine parses one complete transcript line and maps it to items. start is
// the line's byte offset, kept so an image block can be re-read later.
func (t *Transcript) processLine(raw []byte, start int64) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	var ln rawLine
	if err := json.Unmarshal(trimmed, &ln); err != nil {
		t.bumpUnknown()
		return
	}
	t.lineOffsets[t.baseID(ln, start)] = start

	if ln.Version != "" && ln.Version != t.meta.Version {
		t.meta.Version = ln.Version
		t.metaChanged = true
	}
	// permission-mode / mode lines and user lines all carry permissionMode.
	if ln.PermissionMode != "" && ln.PermissionMode != t.meta.Mode {
		t.meta.Mode = ln.PermissionMode
		t.metaChanged = true
	}

	switch ln.Type {
	case "user":
		if (ln.IsSidechain && !t.includeSidechain) || ln.IsMeta {
			return
		}
		if ln.IsCompactSummary {
			t.addDivider(ln, start)
			return
		}
		t.processUser(ln, start)
	case "assistant":
		if ln.IsSidechain && !t.includeSidechain {
			return
		}
		t.processAssistant(ln, start)
	case "system":
		if ln.Subtype == "compact_boundary" {
			t.addDivider(ln, start)
		}
	case "queue-operation":
		t.processQueueOperation(ln)
	}
}

func (t *Transcript) baseID(ln rawLine, start int64) string {
	if ln.UUID != "" {
		return ln.UUID
	}
	return fmt.Sprintf("%s@%d", ln.Type, start)
}

func (t *Transcript) addDivider(ln rawLine, start int64) {
	t.addItem(Item{ID: t.baseID(ln, start), Kind: "divider", At: ln.Timestamp, Text: "Conversation compacted"})
}

func (t *Transcript) processUser(ln rawLine, start int64) {
	content := ln.Message.Content
	if len(content) == 0 || isNull(content) {
		return
	}
	switch firstNonSpace(content) {
	case '"':
		var s string
		if err := json.Unmarshal(content, &s); err != nil {
			t.bumpUnknown()
			return
		}
		t.processUserString(ln, start, s)
	case '[':
		t.processUserArray(ln, start, content)
	default:
		t.bumpUnknown()
	}
}

// processUserString maps a user line whose content is a plain string: a slash
// command, its local stdout, a skipped caveat, or a chat message.
func (t *Transcript) processUserString(ln rawLine, start int64, s string) {
	// A task-notification can arrive inside a user string; it is task metadata,
	// not a chat message (REQ-WC-016).
	if t.processNotification(ln.Timestamp, s) {
		return
	}
	id := t.baseID(ln, start)
	if name, ok := extractTag(s, "command-name"); ok {
		args, _ := extractTag(s, "command-args")
		name = strings.TrimSpace(name)
		if name != "" && !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		t.addUserItem(Item{ID: id, Kind: "command", At: ln.Timestamp, Name: name, Args: strings.TrimSpace(args)}, true)
		return
	}
	if out, ok := extractTag(s, "local-command-stdout"); ok {
		t.addUserItem(Item{ID: id, Kind: "output", At: ln.Timestamp, Text: strings.TrimSpace(ansi.Strip(out))}, false)
		return
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "<local-command-caveat>") || strings.HasPrefix(trimmed, "Caveat:") {
		return
	}
	t.addUserItem(Item{ID: id, Kind: "user", At: ln.Timestamp, Text: s}, true)
}

// processUserArray maps a user line whose content is an array of blocks: text
// and image blocks become one user item; tool_result blocks update the item
// they answer.
func (t *Transcript) processUserArray(ln rawLine, start int64, content json.RawMessage) {
	var blocks []json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		t.bumpUnknown()
		return
	}
	id := t.baseID(ln, start)
	var texts []string
	var images []ImageRef
	for i, raw := range blocks {
		var blk struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
			Source    struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
			} `json:"source"`
		}
		if err := json.Unmarshal(raw, &blk); err != nil {
			t.bumpUnknown()
			continue
		}
		switch blk.Type {
		case "text":
			if blk.Text != "" {
				texts = append(texts, blk.Text)
			}
		case "image":
			if blk.Source.Type == "base64" && blk.Source.MediaType != "" {
				images = append(images, ImageRef{Ref: fmt.Sprintf("%s:%d", id, i), MediaType: blk.Source.MediaType})
			}
		case "tool_result":
			t.applyToolResult(ln, blk.ToolUseID, blk.Content, blk.IsError)
		default:
			t.bumpUnknown()
		}
	}
	text := strings.Join(texts, "\n")
	if text != "" || len(images) > 0 {
		t.addUserItem(Item{ID: id, Kind: "user", At: ln.Timestamp, Text: text, Images: images}, true)
	}
}

func (t *Transcript) processAssistant(ln rawLine, start int64) {
	if ln.Message.Model != "" {
		t.meta.Model = ln.Message.Model
		if !t.longSeen {
			t.meta.Context.Window = contextWindow(ln.Message.Model)
		}
		t.metaChanged = true
	}
	if ln.Effort != "" && ln.Effort != t.meta.Effort {
		t.meta.Effort = ln.Effort
		t.metaChanged = true
	}
	if u := ln.Message.Usage; u != nil {
		t.meta.Context.Used = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
		t.meta.Context.Input = u.InputTokens
		t.meta.Context.CacheRead = u.CacheReadInputTokens
		t.meta.Context.Output = u.OutputTokens
		t.meta.Context.Window = contextWindow(ln.Message.Model)
		// A 1M-context session does not always say so in its model id; usage
		// past the default window proves it.
		if t.meta.Context.Used > defaultWindow || t.longSeen {
			t.longSeen = true
			t.meta.Context.Window = longWindow
		}
		t.metaChanged = true
	}
	t.touchedTurn(ln)

	content := ln.Message.Content
	if len(content) == 0 || isNull(content) {
		return
	}
	if firstNonSpace(content) != '[' {
		t.bumpUnknown()
		return
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		t.bumpUnknown()
		return
	}
	for i, raw := range blocks {
		t.processAssistantBlock(ln, start, i, raw)
	}
}

func (t *Transcript) processAssistantBlock(ln rawLine, start int64, i int, raw json.RawMessage) {
	id := fmt.Sprintf("%s:%d", t.baseID(ln, start), i)
	var blk struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &blk); err != nil {
		t.bumpUnknown()
		return
	}
	switch blk.Type {
	case "text":
		if blk.Text == "" {
			return
		}
		t.addItem(Item{ID: id, Kind: "assistant", At: ln.Timestamp, Text: blk.Text, Model: ln.Message.Model})
	case "thinking":
		t.addItem(Item{ID: id, Kind: "thinking", At: ln.Timestamp, Text: blk.Thinking})
	case "tool_use":
		t.addToolUse(ln, id, blk.ID, blk.Name, blk.Input)
	default:
		t.bumpUnknown()
	}
}

func (t *Transcript) addToolUse(ln rawLine, id, toolUseID, name string, input json.RawMessage) {
	switch name {
	case "AskUserQuestion":
		t.addItem(Item{ID: id, Kind: "question", At: ln.Timestamp, ToolUseID: toolUseID, Questions: parseQuestions(input)})
	case "ExitPlanMode":
		t.addItem(Item{ID: id, Kind: "plan", At: ln.Timestamp, ToolUseID: toolUseID, Plan: planText(input), State: "pending"})
	default:
		it := Item{ID: id, Kind: "tool", At: ln.Timestamp, ToolUseID: toolUseID, Name: name, Input: cutInput(input), Status: "running"}
		applyToolSummary(&it, name, input, t.cwd)
		t.addItem(it)
	}
	if toolUseID != "" {
		t.pending[toolUseID] = t.index[id]
	}
}

// applyToolResult updates the tool, question or plan item a tool_result answers
// and marks it changed. An unknown tool_use_id is ignored. ln is the result
// line, whose toolUseResult may carry background-task metadata (REQ-WC-016).
func (t *Transcript) applyToolResult(ln rawLine, toolUseID string, content json.RawMessage, isError bool) {
	if toolUseID == "" {
		return
	}
	idx, ok := t.pending[toolUseID]
	if !ok {
		return
	}
	it := &t.items[idx]
	text, lines, truncated := parseResult(content)
	switch it.Kind {
	case "question":
		it.Answered = true
		it.Answer = text
	case "plan":
		if isError {
			it.State = "rejected"
		} else {
			it.State = "approved"
		}
	default:
		if isError {
			it.Status = "error"
		} else {
			it.Status = "done"
		}
		it.Result = &ToolResult{Text: text, Lines: lines, Truncated: truncated, IsError: isError}
	}
	t.changed[idx] = true
	delete(t.pending, toolUseID)
	t.applyTaskResult(ln, it, toolUseID, isError)
}

// applyTaskResult folds a tool result's Claude Code metadata into the task list:
// an Agent / Task call becomes an agent task, a background Bash call a shell
// task, and a successful TaskStop / KillShell marks its task killed
// (REQ-WC-016).
func (t *Transcript) applyTaskResult(ln rawLine, it *Item, toolUseID string, isError bool) {
	var in struct {
		Description     string `json:"description"`
		SubagentType    string `json:"subagent_type"`
		RunInBackground bool   `json:"run_in_background"`
		Command         string `json:"command"`
		TaskID          string `json:"task_id"`
		ShellID         string `json:"shell_id"`
	}
	_ = json.Unmarshal(it.Input, &in)

	switch it.Name {
	case "Task", "Agent":
		res := ln.ToolUseResult
		if res.AgentID == "" {
			return
		}
		background := res.IsAsync || in.RunInBackground
		it.Agent = &AgentRef{AgentID: res.AgentID, AgentType: in.SubagentType, Background: background}
		title := res.Description
		if title == "" {
			title = in.Description
		}
		task := Task{
			ID:        res.AgentID,
			Kind:      "agent",
			ToolUseID: toolUseID,
			Title:     title,
			AgentType: in.SubagentType,
			Status:    "running",
			Started:   it.At,
		}
		if !background {
			task.Status = "completed"
			task.Ended = ln.Timestamp
		}
		t.addTask(task)
	case "Bash":
		if ln.ToolUseResult.BackgroundTaskID == "" {
			return
		}
		title := in.Description
		if title == "" {
			title = firstLine(in.Command, maxTaskTitle)
		}
		t.addTask(Task{
			ID:        ln.ToolUseResult.BackgroundTaskID,
			Kind:      "shell",
			ToolUseID: toolUseID,
			Title:     title,
			Status:    "running",
			Started:   it.At,
		})
	case "TaskStop", "KillShell":
		if isError {
			return
		}
		id := in.TaskID
		if id == "" {
			id = in.ShellID
		}
		t.setTaskStatus(id, "killed", "", ln.Timestamp)
	}
}

// processQueueOperation reads the task notifications a queue-operation line
// carries. Other queue operations are not chat items and are ignored
// (REQ-WC-016).
func (t *Transcript) processQueueOperation(ln rawLine) {
	if ln.Operation != "enqueue" || len(ln.Content) == 0 {
		return
	}
	var s string
	if json.Unmarshal(ln.Content, &s) != nil {
		return
	}
	t.processNotification(ln.Timestamp, s)
}

// processNotification applies a <task-notification> block: it sets the status,
// summary and end time of the task the block names, creating a kind-other task
// for an unknown id. It reports whether s was such a block, so a user line that
// only carried one adds no chat item. A notification that says the agent is
// waiting on its own background work leaves the task running.
func (t *Transcript) processNotification(at, s string) bool {
	block, ok := extractTag(s, "task-notification")
	if !ok {
		return false
	}
	id, _ := extractTag(block, "task-id")
	status, _ := extractTag(block, "status")
	summary, _ := extractTag(block, "summary")
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	summary = strings.TrimSpace(summary)
	if strings.Contains(summary, "is waiting on its own background work") {
		return true
	}
	if status == "" {
		status = "completed"
	}
	if t.setTaskStatus(id, status, summary, at) {
		return true
	}
	if id != "" {
		t.addTask(Task{ID: id, Kind: "other", Title: summary, Status: status, Summary: summary, Started: at, Ended: at})
	}
	return true
}

// addTask adds or replaces a task by id and trims the list to the newest
// maxTasks by start time (REQ-WC-016).
func (t *Transcript) addTask(task Task) {
	if task.ID != "" {
		for i := range t.meta.Tasks {
			if t.meta.Tasks[i].ID == task.ID {
				t.meta.Tasks[i] = task
				t.metaChanged = true
				return
			}
		}
	}
	t.meta.Tasks = append(t.meta.Tasks, task)
	t.trimTasks()
	t.metaChanged = true
}

// setTaskStatus updates a task the id names, reporting whether it was found.
// An empty summary keeps the current one.
func (t *Transcript) setTaskStatus(id, status, summary, at string) bool {
	if id == "" {
		return false
	}
	for i := range t.meta.Tasks {
		if t.meta.Tasks[i].ID != id {
			continue
		}
		if summary != "" {
			t.meta.Tasks[i].Summary = summary
		}
		t.meta.Tasks[i].Status = status
		t.meta.Tasks[i].Ended = at
		t.metaChanged = true
		return true
	}
	return false
}

// trimTasks drops the oldest tasks (by start time) until at most maxTasks
// remain.
func (t *Transcript) trimTasks() {
	for len(t.meta.Tasks) > maxTasks {
		oldest := 0
		oldestAt := parseTime(t.meta.Tasks[0].Started)
		for i := 1; i < len(t.meta.Tasks); i++ {
			if at := parseTime(t.meta.Tasks[i].Started); at.Before(oldestAt) {
				oldest, oldestAt = i, at
			}
		}
		t.meta.Tasks = append(t.meta.Tasks[:oldest], t.meta.Tasks[oldest+1:]...)
	}
}

// addItem stores a new item or replaces an item with the same id in place, and
// records the position as changed.
func (t *Transcript) addItem(it Item) {
	if idx, ok := t.index[it.ID]; ok {
		t.items[idx] = it
		t.changed[idx] = true
		return
	}
	t.changed[len(t.items)] = true
	t.index[it.ID] = len(t.items)
	t.items = append(t.items, it)
}

// addUserItem adds a user-side item. A prompt (user or command) closes the
// previous turn with a turn_end item and starts a new one (REQ-WC-014).
func (t *Transcript) addUserItem(it Item, prompt bool) {
	if prompt {
		t.finalizeTurn(it.ID)
		t.beginTurn(it.At)
	}
	t.addItem(it)
}

func (t *Transcript) touchedTurn(ln rawLine) {
	if t.turn == nil {
		return
	}
	t.turn.hasAssistant = true
	if ts := parseTime(ln.Timestamp); !ts.IsZero() {
		t.turn.lastAt = ts
	}
	if u := ln.Message.Usage; u != nil {
		id := ln.Message.ID
		if id == "" {
			id = ln.UUID
		}
		if !t.turn.seenIDs[id] {
			t.turn.seenIDs[id] = true
			t.turn.outputTokens += u.OutputTokens
		}
	}
}

func (t *Transcript) beginTurn(at string) {
	t.turn = &turnState{startedAt: parseTime(at), seenIDs: map[string]bool{}}
}

// finalizeTurn emits the turn_end item for the turn that just ended. nextID is
// the id of the line that starts the next turn; the turn_end id is
// "turn:<nextID>". A turn without assistant lines emits nothing.
func (t *Transcript) finalizeTurn(nextID string) {
	turn := t.turn
	t.turn = nil
	if turn == nil || !turn.hasAssistant {
		return
	}
	var dur int64
	if !turn.startedAt.IsZero() && !turn.lastAt.IsZero() {
		dur = turn.lastAt.Sub(turn.startedAt).Milliseconds()
	}
	t.addItem(Item{ID: "turn:" + nextID, Kind: "turn_end", DurationMS: dur, OutputTokens: turn.outputTokens})
}

func (t *Transcript) bumpUnknown() {
	t.meta.UnknownLines++
	t.metaChanged = true
}

// applyToolSummary fills a tool item's summary (and diff) from its input.
func applyToolSummary(it *Item, name string, input json.RawMessage, cwd string) {
	var in struct {
		FilePath    string `json:"file_path"`
		Command     string `json:"command"`
		Pattern     string `json:"pattern"`
		Path        string `json:"path"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
		OldString   string `json:"old_string"`
		NewString   string `json:"new_string"`
		Content     string `json:"content"`
		Edits       []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	_ = json.Unmarshal(input, &in)

	summary := &ToolSummary{Verb: name}
	switch name {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		summary.Target = relativePath(in.FilePath, cwd)
	case "Bash":
		summary.Target = firstLine(in.Command, 200)
	case "Grep":
		summary.Target = in.Pattern
		if in.Path != "" {
			summary.Target += " in " + relativePath(in.Path, cwd)
		}
	case "Glob":
		summary.Target = in.Pattern
	case "WebFetch":
		summary.Target = in.URL
	case "WebSearch":
		summary.Target = in.Query
	case "Task", "Agent":
		summary.Target = in.Description
	default:
		if server, tool, ok := splitMCP(name); ok {
			summary.Target = server + " · " + tool
		} else {
			summary.Target = name
		}
	}

	switch name {
	case "Edit":
		summary.Meta = diffMeta(in.NewString, in.OldString)
		it.Diff = &Diff{Old: cutLines(in.OldString, maxDiffLines), New: cutLines(in.NewString, maxDiffLines)}
	case "Write":
		summary.Meta = fmt.Sprintf("+%d", countLines(in.Content))
		it.Diff = &Diff{Old: "", New: cutLines(in.Content, maxDiffLines)}
	case "MultiEdit":
		olds := make([]string, 0, len(in.Edits))
		news := make([]string, 0, len(in.Edits))
		for _, e := range in.Edits {
			olds = append(olds, e.OldString)
			news = append(news, e.NewString)
		}
		oldJoined := strings.Join(olds, "\n…\n")
		newJoined := strings.Join(news, "\n…\n")
		summary.Meta = diffMeta(newJoined, oldJoined)
		it.Diff = &Diff{Old: cutLines(oldJoined, maxDiffLines), New: cutLines(newJoined, maxDiffLines)}
	}
	it.Summary = summary
}

// parseQuestions reads an AskUserQuestion input's "questions" array.
func parseQuestions(input json.RawMessage) []Question {
	var q struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(input, &q); err != nil {
		return nil
	}
	out := make([]Question, 0, len(q.Questions))
	for _, item := range q.Questions {
		question := Question{Question: item.Question, Header: item.Header, MultiSelect: item.MultiSelect}
		for _, o := range item.Options {
			question.Options = append(question.Options, QuestionOption{Label: o.Label, Description: o.Description})
		}
		out = append(out, question)
	}
	return out
}

// planText reads an ExitPlanMode input's "plan" markdown.
func planText(input json.RawMessage) string {
	var p struct {
		Plan string `json:"plan"`
	}
	_ = json.Unmarshal(input, &p)
	return p.Plan
}

// parseResult flattens a tool_result's content and cuts it to maxResultBytes /
// maxResultLines. It returns the cut text, the full line count and whether the
// text was cut.
func parseResult(content json.RawMessage) (string, int, bool) {
	text := resultText(content)
	lines := countLines(text)
	out := text
	truncated := false
	if countLines(out) > maxResultLines {
		out = firstLines(out, maxResultLines)
		truncated = true
	}
	if len(out) > maxResultBytes {
		out = cutBytes(out, maxResultBytes)
		truncated = true
	}
	return out, lines, truncated
}

// resultText turns a tool_result content (a string or an array of blocks) into
// plain text, joining text blocks and ignoring the rest.
func resultText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	switch firstNonSpace(content) {
	case '"':
		var s string
		if json.Unmarshal(content, &s) == nil {
			return s
		}
	case '[':
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &blocks) == nil {
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

// cutInput cuts every string value in a tool input over maxInputString bytes.
func cutInput(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := marshalJSON(cutInputValue(v))
	if err != nil {
		return raw
	}
	return json.RawMessage(b)
}

func cutInputValue(v any) any {
	switch x := v.(type) {
	case string:
		if len(x) > maxInputString {
			return cutBytes(x, maxInputString) + "…"
		}
		return x
	case []any:
		for i := range x {
			x[i] = cutInputValue(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = cutInputValue(x[k])
		}
		return x
	default:
		return v
	}
}

func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func contextWindow(model string) int64 {
	if strings.Contains(model, "[1m]") {
		return longWindow
	}
	return defaultWindow
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts
	}
	return time.Time{}
}

// extractTag returns the text between <tag> and </tag>.
func extractTag(s, tag string) (string, bool) {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(s, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	end := strings.Index(s[start:], close)
	if end < 0 {
		return "", false
	}
	return s[start : start+end], true
}

// relativePath returns p relative to cwd when p is an absolute path inside cwd,
// else p unchanged.
func relativePath(p, cwd string) string {
	if p == "" || cwd == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if max > 0 {
		if r := []rune(s); len(r) > max {
			s = string(r[:max])
		}
	}
	return s
}

func cutLines(s string, max int) string {
	if max <= 0 {
		return s
	}
	parts := strings.Split(s, "\n")
	if len(parts) <= max {
		return s
	}
	return strings.Join(parts[:max], "\n")
}

func firstLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	parts := strings.Split(s, "\n")
	if len(parts) <= n {
		return s
	}
	return strings.Join(parts[:n], "\n")
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// cutBytes cuts s to at most max bytes without splitting a rune.
func cutBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	b := s[:max]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b
}

func diffMeta(newText, oldText string) string {
	return fmt.Sprintf("+%d −%d", countLines(newText), countLines(oldText))
}

// splitMCP splits an "mcp__server__tool" name into its server and tool.
func splitMCP(name string) (string, string, bool) {
	parts := strings.SplitN(name, "__", 3)
	if len(parts) != 3 || parts[0] != "mcp" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func firstNonSpace(b []byte) byte {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return c
		}
	}
	return 0
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

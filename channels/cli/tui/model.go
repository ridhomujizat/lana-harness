package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// Options is the input to Run.
type Options struct {
	Backend   Backend
	Approver  *Approver
	Agent     string
	Model     string
	Policy    string
	SessionID string
	Cwd       string
	Version   string
	Home      string
	Notice    string
	Verbose   bool
	Restore   []core.SessionEvent
	In        io.Reader
	Out       io.Writer
}

type liveFrameMsg struct{ frame core.LiveFrame }

type turnOutcome struct {
	result TurnResult
	err    error
}

type turnDoneMsg struct{ outcome turnOutcome }

type approvalMsg struct {
	req   core.ApprovalRequest
	reply chan core.ApprovalOutcome
}

type approvalExpiredMsg struct{ callID string }

type approvalState struct {
	req     core.ApprovalRequest
	reply   chan core.ApprovalOutcome
	choices *menu
}

func newApprovalChoices() *menu {
	return &menu{items: []menuItem{
		{value: string(core.OutcomeAllowedOnce), label: "Yes, once", hint: "y"},
		{value: string(core.OutcomeAllowedSession), label: "Yes, always for this session", hint: "a"},
		{value: string(core.OutcomeRejected), label: "No, reject", hint: "n / esc"},
	}}
}

type compactDoneMsg struct {
	info CompactionInfo
	err  error
}

// Model is the state of the TUI chat.
type Model struct {
	backend  Backend
	approver *Approver
	program  *tea.Program
	ctx      context.Context

	agent     string
	model     string
	policy    string
	sessionID string
	cwd       string
	version   string
	notice    string
	restore   []core.SessionEvent

	input   textarea.Model
	spinner spinner.Model
	history *History

	width  int
	height int

	running   bool
	cancel    context.CancelFunc
	turnStart time.Time
	usageIn   int64
	usageOut  int64

	stream           strings.Builder
	assistantStarted bool
	tools            []*ToolRecord
	lastTool         *ToolRecord
	verbose          bool

	approval   *approvalState
	picker     *menu
	compacting bool

	lastCtrlC time.Time

	queue       []string
	searching   bool
	searchQuery string
	searchMatch string
	searchIdx   int

	suggestIdx int
	suggestOff bool

	histIdx   int
	histDraft string
}

// NewModel creates the TUI model.
func NewModel(ctx context.Context, opts Options) *Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Ask anything…  (/help)"
	input.CharLimit = 0
	input.ShowLineNumbers = false
	input.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j", "shift+enter")
	input.SetWidth(78)
	input.SetHeight(1)
	input.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: []string{"✢", "✳", "✶", "✻"}, FPS: 120 * time.Millisecond}

	if ctx == nil {
		ctx = context.Background()
	}
	policy := opts.Policy
	if policy == "" {
		policy = string(core.PolicyAsk)
	}
	return &Model{
		backend:   opts.Backend,
		approver:  opts.Approver,
		ctx:       ctx,
		agent:     opts.Agent,
		model:     opts.Model,
		policy:    policy,
		sessionID: opts.SessionID,
		cwd:       opts.Cwd,
		version:   opts.Version,
		notice:    opts.Notice,
		restore:   opts.Restore,
		input:     input,
		spinner:   sp,
		history:   LoadHistory(historyPath(opts.Home), 1000),
		verbose:   opts.Verbose,
		histIdx:   -1,
	}
}

func historyPath(home string) string {
	if home == "" {
		return ""
	}
	return home + string(os.PathSeparator) + "history"
}

// Run runs the TUI until the user exits.
func Run(ctx context.Context, opts Options) error {
	model := NewModel(ctx, opts)
	programOpts := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.In != nil {
		programOpts = append(programOpts, tea.WithInput(opts.In))
	}
	if opts.Out != nil {
		programOpts = append(programOpts, tea.WithOutput(opts.Out))
	}
	program := tea.NewProgram(model, programOpts...)
	model.program = program
	if opts.Approver != nil {
		opts.Approver.SetProgram(program)
	}
	if _, err := program.Run(); err != nil {
		return err
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	_, _ = fmt.Fprintln(out)
	return nil
}

// Init starts the program.
func (m *Model) Init() tea.Cmd { return m.startBanner() }

func (m *Model) startBanner() tea.Cmd {
	cmds := []tea.Cmd{tea.Println(strings.Join(m.banner(), "\n"))}
	if m.notice != "" {
		cmds = append(cmds, tea.Println(styleMuted.Render("· "+m.notice)))
	}
	if restore := m.restoreLines(); len(restore) > 0 {
		cmds = append(cmds, tea.Println(strings.Join(restore, "\n")))
	}
	return tea.Batch(cmds...)
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case liveFrameMsg:
		return m, m.handleFrame(msg.frame)
	case turnDoneMsg:
		return m, m.finishTurn(msg.outcome)
	case approvalMsg:
		m.approval = &approvalState{req: msg.req, reply: msg.reply, choices: newApprovalChoices()}
		return m, tea.Tick(10*time.Minute, func(time.Time) tea.Msg {
			return approvalExpiredMsg{callID: msg.req.CallID}
		})
	case approvalExpiredMsg:
		if m.approval != nil && m.approval.req.CallID == msg.callID {
			m.approval = nil
			return m, tea.Println(styleMuted.Render("· approval expired"))
		}
		return m, nil
	case compactDoneMsg:
		m.compacting = false
		if msg.err != nil {
			return m, tea.Println(styleErr.Render("✗ compaction: " + msg.err.Error()))
		}
		return m, tea.Println(styleMuted.Render(fmt.Sprintf("· compaction %d → %d tokens", msg.info.TokensBefore, msg.info.TokensAfter)))
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) resize() {
	width := m.contentWidth() - 2
	m.input.SetWidth(width)
	m.resizeInput()
}

func (m *Model) resizeInput() {
	height := m.input.LineCount()
	if height < 1 {
		height = 1
	}
	if height > 8 {
		height = 8
	}
	m.input.SetHeight(height)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.picker != nil {
		return m.handlePickerKey(msg)
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	if m.approval != nil {
		return m.handleApprovalKey(msg)
	}
	if suggestions := m.suggestions(); len(suggestions) > 0 {
		if model, cmd, handled := m.handleSuggestKey(msg, suggestions); handled {
			return model, cmd
		}
	}
	switch msg.String() {
	case "ctrl+c":
		return m.handleCtrlC()
	case "ctrl+d":
		if strings.TrimSpace(m.input.Value()) == "" {
			return m.quit()
		}
		return m, nil
	case "ctrl+o":
		m.verbose = !m.verbose
		cmds := []tea.Cmd{tea.Println(styleMuted.Render("· verbose " + onOff(m.verbose)))}
		if m.verbose && m.lastTool != nil {
			cmds = append(cmds, tea.Println(strings.Join(m.lastTool.Block(true, m.contentWidth()), "\n")))
		}
		return m, tea.Batch(cmds...)
	case "ctrl+r":
		m.searching = true
		m.searchQuery = ""
		m.searchIdx = m.history.Len()
		m.searchMatch = ""
		return m, nil
	case "esc":
		if m.running {
			m.cancelTurn()
		}
		return m, nil
	case "enter":
		return m, m.submit()
	case "up":
		if m.input.Line() == 0 && m.history.Len() > 0 {
			return m, m.historyPrev()
		}
	case "down":
		if m.input.Line() >= m.input.LineCount()-1 && m.histIdx >= 0 {
			return m, m.historyNext()
		}
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeInput()
	if m.input.Value() != before {
		m.suggestIdx = 0
		m.suggestOff = false
	}
	return m, cmd
}

// suggestions returns the slash commands that match the current input.
func (m *Model) suggestions() []slashCommand {
	if m.suggestOff {
		return nil
	}
	value := m.input.Value()
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, " \t\n") {
		return nil
	}
	return matchCommands(value)
}

func (m *Model) handleSuggestKey(msg tea.KeyMsg, suggestions []slashCommand) (tea.Model, tea.Cmd, bool) {
	if m.suggestIdx >= len(suggestions) {
		m.suggestIdx = 0
	}
	selected := suggestions[m.suggestIdx]
	switch msg.String() {
	case "up", "shift+tab":
		m.suggestIdx = (m.suggestIdx - 1 + len(suggestions)) % len(suggestions)
		return m, nil, true
	case "down":
		m.suggestIdx = (m.suggestIdx + 1) % len(suggestions)
		return m, nil, true
	case "tab":
		if selected.args != "" {
			m.setInput(selected.name + " ")
		} else {
			m.setInput(selected.name)
		}
		return m, nil, true
	case "esc":
		m.suggestOff = true
		return m, nil, true
	case "enter":
		if !isSlashCommand(strings.TrimSpace(m.input.Value())) {
			m.setInput(selected.name)
		}
		return m, m.submit(), true
	}
	return m, nil, false
}

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		return m, m.answerApproval(core.OutcomeAllowedOnce)
	case "a":
		return m, m.answerApproval(core.OutcomeAllowedSession)
	case "n", "esc":
		return m, m.answerApproval(core.OutcomeRejected)
	case "ctrl+c":
		cmd := m.answerApproval(core.OutcomeCancelled)
		m.cancelTurn()
		return m, cmd
	}
	if picked, _ := m.approval.choices.key(msg); picked != nil {
		return m, m.answerApproval(core.ApprovalOutcome(picked.value))
	}
	return m, nil
}

func (m *Model) answerApproval(outcome core.ApprovalOutcome) tea.Cmd {
	m.approval.reply <- outcome
	m.approval = nil
	switch outcome {
	case core.OutcomeAllowedOnce:
		return tea.Println(styleOK.Render("  ✓ allowed once"))
	case core.OutcomeAllowedSession:
		return tea.Println(styleOK.Render("  ✓ allowed for session"))
	case core.OutcomeRejected:
		return tea.Println(styleErr.Render("  ⊘ rejected"))
	}
	return tea.Println(styleErr.Render("  ⊘ cancelled"))
}

func (m *Model) handleCtrlC() (tea.Model, tea.Cmd) {
	if m.running {
		m.cancelTurn()
		return m, nil
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.resizeInput()
		return m, nil
	}
	if !m.lastCtrlC.IsZero() && time.Since(m.lastCtrlC) < 2*time.Second {
		return m.quit()
	}
	m.lastCtrlC = time.Now()
	return m, tea.Println(styleMuted.Render("· press ctrl+c again to exit"))
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.cancelTurn()
	return m, tea.Quit
}

func (m *Model) cancelTurn() {
	if m.cancel != nil {
		m.cancel()
	}
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (m *Model) submit() tea.Cmd {
	raw := m.input.Value()
	if strings.HasSuffix(raw, "\\") {
		m.setInput(strings.TrimSuffix(raw, "\\") + "\n")
		return nil
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "//") {
		value = strings.TrimPrefix(value, "/")
	} else if strings.HasPrefix(value, "/") {
		m.input.Reset()
		m.resizeInput()
		m.histIdx = -1
		return m.slash(value)
	}
	m.input.Reset()
	m.resizeInput()
	m.histIdx = -1
	m.history.Add(value)

	if m.running {
		m.queue = append(m.queue, value)
		return tea.Println(strings.Join(append(renderUser(value), styleMuted.Render("  (queued)")), "\n"))
	}
	return tea.Batch(m.startTurn(value), tea.Println(strings.Join(renderUser(value), "\n")+"\n"))
}

func (m *Model) startTurn(prompt string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.running = true
	m.turnStart = time.Now()
	m.assistantStarted = false
	m.stream.Reset()
	m.tools = nil

	sessionID := m.sessionID
	sub, err := m.backend.SubscribeLive(ctx, sessionID)
	if err != nil {
		m.running = false
		cancel()
		return tea.Println(styleErr.Render("✗ error: " + err.Error()))
	}
	finish := make(chan turnOutcome, 1)
	go func() {
		result, runErr := m.backend.RunTurn(ctx, sessionID, prompt)
		finish <- turnOutcome{result: result, err: runErr}
	}()
	go m.pump(ctx, sub, finish)
	return m.spinner.Tick
}

func (m *Model) pump(ctx context.Context, sub *core.Subscription[core.LiveFrame], finish <-chan turnOutcome) {
	if m.program == nil {
		return
	}
	for {
		select {
		case out := <-finish:
			for sub.Len() > 0 {
				recvCtx, cancel := context.WithTimeout(ctx, time.Second)
				received, err := sub.Recv(recvCtx)
				cancel()
				if err != nil {
					break
				}
				if received.Lagged > 0 {
					continue
				}
				m.program.Send(liveFrameMsg{frame: received.Value})
			}
			m.program.Send(turnDoneMsg{outcome: out})
			return
		default:
		}
		if ctx.Err() != nil {
			return
		}
		recvCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
		received, err := sub.Recv(recvCtx)
		cancel()
		if err != nil {
			continue
		}
		if received.Lagged > 0 {
			continue
		}
		m.program.Send(liveFrameMsg{frame: received.Value})
	}
}

func (m *Model) handleFrame(frame core.LiveFrame) tea.Cmd {
	if frame.SessionID != m.sessionID {
		return nil
	}
	switch frame.Kind {
	case "text_delta":
		return m.appendDelta(frame.Text)
	case "tool_call":
		cmds := make([]tea.Cmd, 0, 2)
		if text := m.stream.String(); strings.TrimSpace(text) != "" {
			cmds = append(cmds, tea.Println(strings.Join(m.renderAssistant(text), "\n")+"\n"))
			m.stream.Reset()
		}
		record := &ToolRecord{CallID: frame.CallID, Name: frame.Name, Input: frame.Input, Started: time.Now()}
		m.tools = append(m.tools, record)
		return tea.Batch(cmds...)
	case "tool_result":
		for i, record := range m.tools {
			if record.CallID != frame.CallID {
				continue
			}
			record.Status = core.ToolStatus(frame.Status)
			record.Output = frame.Output
			record.Done = time.Now()
			m.tools = append(m.tools[:i], m.tools[i+1:]...)
			m.lastTool = record
			m.assistantStarted = false
			return tea.Println(strings.Join(record.Block(m.verbose, m.contentWidth()), "\n") + "\n")
		}
	}
	return nil
}

func (m *Model) appendDelta(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.stream.WriteString(text)
	blocks, rest := splitCompleteBlocks(m.stream.String())
	m.stream.Reset()
	m.stream.WriteString(rest)
	if len(blocks) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(blocks))
	for _, block := range blocks {
		cmds = append(cmds, tea.Println(strings.Join(m.renderAssistant(block), "\n")+"\n"))
	}
	return tea.Batch(cmds...)
}

func (m *Model) finishTurn(outcome turnOutcome) tea.Cmd {
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	cmds := []tea.Cmd{}
	if rest := m.stream.String(); strings.TrimSpace(rest) != "" {
		cmds = append(cmds, tea.Println(strings.Join(m.renderAssistant(rest), "\n")+"\n"))
	}
	m.stream.Reset()
	m.usageIn += outcome.result.InputTokens
	m.usageOut += outcome.result.OutputTokens
	m.approval = nil
	switch {
	case outcome.err != nil && (errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, context.DeadlineExceeded)):
		cmds = append(cmds, tea.Println(styleMuted.Render("· turn cancelled")))
	case outcome.err != nil:
		cmds = append(cmds, tea.Println(styleErr.Render("✗ error: "+outcome.err.Error())))
	case outcome.result.StopReason == "cancelled":
		cmds = append(cmds, tea.Println(styleMuted.Render("· turn cancelled")))
	case outcome.result.StopReason == "max_steps":
		cmds = append(cmds, tea.Println(styleWarn.Render("⚠ turn stopped due to max_steps")))
	}
	if len(m.queue) > 0 {
		next := m.queue[0]
		m.queue = m.queue[1:]
		cmds = append(cmds, m.startTurn(next))
	}
	return tea.Batch(cmds...)
}

func (m *Model) renderAssistant(block string) []string {
	lines := renderMarkdownLines(block, m.contentWidth()-2)
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 && !m.assistantStarted {
			out = append(out, styleAccent.Render("⏺ ")+line)
			continue
		}
		out = append(out, "  "+line)
	}
	m.assistantStarted = true
	return out
}

func (m *Model) historyPrev() tea.Cmd {
	if m.histIdx == -1 {
		m.histDraft = m.input.Value()
		m.histIdx = m.history.Len() - 1
	} else if m.histIdx > 0 {
		m.histIdx--
	}
	m.setInput(m.history.At(m.histIdx))
	return nil
}

func (m *Model) historyNext() tea.Cmd {
	if m.histIdx == -1 {
		return nil
	}
	m.histIdx++
	if m.histIdx >= m.history.Len() {
		m.histIdx = -1
		m.setInput(m.histDraft)
		return nil
	}
	m.setInput(m.history.At(m.histIdx))
	return nil
}

func (m *Model) setInput(value string) {
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.resizeInput()
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.searching = false
		m.searchQuery = ""
		m.searchMatch = ""
		return m, nil
	case "enter", "tab":
		m.searching = false
		if m.searchMatch != "" {
			m.setInput(m.searchMatch)
		}
		m.searchQuery = ""
		m.searchMatch = ""
		return m, nil
	case "ctrl+r":
		if m.searchMatch != "" {
			m.searchStep(m.searchIdx)
		}
		return m, nil
	case "backspace":
		if m.searchQuery != "" {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(runes[:len(runes)-1])
		}
		m.searchStep(m.history.Len())
		return m, nil
	}
	if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
		m.searchQuery += string(msg.Runes)
		m.searchStep(m.history.Len())
	}
	return m, nil
}

// searchStep finds a match for the query before index before. If there is
// none, the previous match stays visible.
func (m *Model) searchStep(before int) {
	if match, index, ok := m.history.SearchBefore(m.searchQuery, before); ok {
		m.searchMatch = match
		m.searchIdx = index
		return
	}
	if before >= m.history.Len() {
		m.searchMatch = ""
	}
}

func (m *Model) slash(value string) tea.Cmd {
	fields := strings.Fields(value)
	command := fields[0]
	args := fields[1:]
	switch command {
	case "/help":
		return tea.Println(strings.Join(helpLines(), "\n"))
	case "/exit", "/quit":
		return tea.Quit
	case "/clear":
		return tea.Batch(tea.ClearScreen, tea.Println(strings.Join(m.banner(), "\n")))
	case "/new":
		info, err := m.backend.CreateSession(m.ctx, m.agent)
		if err != nil {
			return tea.Println(styleErr.Render("✗ error: " + err.Error()))
		}
		m.sessionID = info.ID
		m.usageIn, m.usageOut = 0, 0
		m.assistantStarted = false
		m.queue = nil
		m.picker = nil
		return tea.Println(styleMuted.Render("· new session " + info.ID))
	case "/session":
		return tea.Println(styleMuted.Render("· session " + m.sessionID + " · " + m.cwd))
	case "/sessions":
		return m.switchOrPick(args)
	case "/fork":
		return m.forkSession()
	case "/compact":
		return m.compactSession()
	case "/agent":
		return m.agentCommand(args)
	case "/model":
		return m.modelCommand(args)
	case "/memory":
		return m.memoryCommand()
	case "/verbose":
		m.verbose = !m.verbose
		return tea.Println(styleMuted.Render("· verbose " + onOff(m.verbose)))
	case "/yolo":
		return m.setPolicy(string(core.PolicyTrust))
	case "/permission":
		if len(args) == 0 {
			m.picker = m.policyMenu()
			return nil
		}
		return m.setPolicy(strings.ToLower(args[0]))
	}
	return tea.Println(styleMuted.Render("· unknown command: " + command))
}

func (m *Model) memoryCommand() tea.Cmd {
	user, memory, err := m.backend.Memory(m.agent)
	if err != nil {
		return tea.Println(styleErr.Render("✗ memory: " + err.Error()))
	}
	lines := memoryLines("user", user, agenthome.UserLimit)
	lines = append(lines, memoryLines("memory", memory, agenthome.MemoryLimit)...)
	return tea.Println(strings.Join(lines, "\n"))
}

func memoryLines(target string, entries []string, limit int) []string {
	lines := []string{styleMuted.Render(fmt.Sprintf("· %s %d/%d characters, %d entries", target, agenthome.Size(entries), limit, len(entries)))}
	for i, entry := range entries {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, strings.ReplaceAll(entry, "\n", "\n     ")))
	}
	return lines
}

func (m *Model) setPolicy(value string) tea.Cmd {
	var policy core.Policy
	switch value {
	case string(core.PolicyAsk), string(core.PolicyTrust), string(core.PolicyNever):
		policy = core.Policy(value)
	default:
		return tea.Println(styleErr.Render("✗ unknown policy: " + value))
	}
	if err := m.backend.SetPolicy(m.ctx, m.sessionID, policy); err != nil {
		return tea.Println(styleErr.Render("✗ error: " + err.Error()))
	}
	m.policy = value
	lines := []string{styleMuted.Render("· policy " + value)}
	if policy == core.PolicyTrust {
		lines = append(lines, styleWarn.Render("⚠ YOLO: all tools allowed without approval (deny rules still apply)"))
	}
	return tea.Println(strings.Join(lines, "\n"))
}

func (m *Model) switchOrPick(args []string) tea.Cmd {
	if m.running || m.compacting {
		return tea.Println(styleMuted.Render("· wait for the turn to finish"))
	}
	if len(args) > 0 {
		if _, err := m.backend.SessionInfo(m.ctx, args[0]); err != nil {
			return tea.Println(styleErr.Render("✗ session not found: " + args[0]))
		}
		return m.switchSession(args[0])
	}
	sessions, err := m.backend.ListSessions(m.ctx, 20)
	if err != nil {
		return tea.Println(styleErr.Render("✗ error: " + err.Error()))
	}
	if len(sessions) == 0 {
		return tea.Println(styleMuted.Render("· no other sessions yet"))
	}
	items := make([]menuItem, 0, len(sessions))
	for _, session := range sessions {
		hint := session.Agent + " · " + fmt.Sprintf("%d turns", session.TurnCount) + " · " + time.UnixMilli(session.UpdatedAt).Format("15:04")
		if session.ID == m.sessionID {
			hint += " · active"
		}
		items = append(items, menuItem{value: session.ID, label: shortSessionID(session.ID), hint: hint})
	}
	m.picker = &menu{title: "pick session", items: items, onPick: m.switchSession}
	return nil
}

func (m *Model) policyMenu() *menu {
	picker := &menu{
		title: "pick policy",
		items: []menuItem{
			{value: string(core.PolicyAsk), label: "ask", hint: "ask approval for risky tools"},
			{value: string(core.PolicyTrust), label: "trust", hint: "YOLO: allow all tools"},
			{value: string(core.PolicyNever), label: "never", hint: "reject all tools that need approval"},
		},
		onPick: m.setPolicy,
	}
	picker.selectValue(m.policy)
	return picker
}

func (m *Model) switchSession(id string) tea.Cmd {
	m.sessionID = id
	m.usageIn, m.usageOut = 0, 0
	m.assistantStarted = false
	m.queue = nil
	m.picker = nil
	if info, err := m.backend.SessionInfo(m.ctx, id); err == nil {
		m.agent = info.Agent
		if model := m.backend.AgentModel(info.Agent); model != "" {
			m.model = model
		}
	}
	if policy, err := m.backend.Policy(m.ctx, id); err == nil {
		m.policy = string(policy)
	}
	return tea.Println(styleMuted.Render("· switched to session " + id))
}

func (m *Model) forkSession() tea.Cmd {
	if m.running || m.compacting {
		return tea.Println(styleMuted.Render("· wait for the turn to finish"))
	}
	info, err := m.backend.ForkSession(m.ctx, m.sessionID)
	if err != nil {
		return tea.Println(styleErr.Render("✗ fork: " + err.Error()))
	}
	return tea.Batch(tea.Println(styleMuted.Render("· fork "+info.ID)), m.switchSession(info.ID))
}

func (m *Model) compactSession() tea.Cmd {
	if m.running || m.compacting {
		return tea.Println(styleMuted.Render("· wait for the turn to finish"))
	}
	m.compacting = true
	sessionID := m.sessionID
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
		defer cancel()
		info, err := m.backend.Compact(ctx, sessionID)
		if m.program != nil {
			m.program.Send(compactDoneMsg{info: info, err: err})
		}
	}()
	return tea.Println(styleMuted.Render("· compacting…"))
}

func (m *Model) agentCommand(args []string) tea.Cmd {
	if len(args) == 0 {
		items := []menuItem{}
		for _, name := range m.backend.AgentNames() {
			items = append(items, menuItem{value: name, label: name, hint: shortModel(m.backend.AgentModel(name)) + " · " + m.backend.AgentPolicy(name)})
		}
		if len(items) == 0 {
			return tea.Println(styleMuted.Render("· agent " + m.agent + " · " + m.model + " · " + m.policy))
		}
		m.picker = &menu{title: "pick agent", items: items, onPick: func(name string) tea.Cmd {
			return m.agentCommand([]string{name})
		}}
		m.picker.selectValue(m.agent)
		return nil
	}
	name := args[0]
	if err := m.backend.SetAgent(m.ctx, m.sessionID, name); err != nil {
		return tea.Println(styleErr.Render("✗ agent: " + err.Error()))
	}
	m.agent = name
	if model := m.backend.AgentModel(name); model != "" {
		m.model = model
	}
	return tea.Println(styleMuted.Render("· agent: " + name))
}

func (m *Model) modelCommand(args []string) tea.Cmd {
	if len(args) == 0 {
		items := []menuItem{}
		seen := map[string]bool{}
		add := func(model, hint string) {
			if model == "" || seen[model] {
				return
			}
			seen[model] = true
			items = append(items, menuItem{value: model, label: model, hint: hint})
		}
		add(m.model, "active")
		for _, name := range m.backend.AgentNames() {
			add(m.backend.AgentModel(name), "agent "+name)
		}
		m.picker = &menu{title: "pick model", items: items, onPick: func(model string) tea.Cmd {
			return m.modelCommand([]string{model})
		}}
		return tea.Println(styleMuted.Render("· other models: /model provider:model"))
	}
	value := args[0]
	if !strings.Contains(value, ":") {
		return tea.Println(styleErr.Render("✗ model must be provider:model, e.g. deepseek:deepseek-chat"))
	}
	if err := m.backend.SetModel(m.ctx, m.sessionID, value); err != nil {
		return tea.Println(styleErr.Render("✗ model: " + err.Error()))
	}
	m.model = value
	return tea.Println(styleMuted.Render("· model: " + value))
}

func (m *Model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.picker
	picked, cancel := picker.key(msg)
	if cancel {
		m.picker = nil
		return m, nil
	}
	if picked == nil {
		return m, nil
	}
	m.picker = nil
	return m, picker.onPick(picked.value)
}

func shortSessionID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

func (m *Model) contentWidth() int {
	width := m.width
	if width <= 0 {
		width = 80
	}
	if width < 40 {
		width = 40
	}
	return width
}

// View renders the dynamic area below the scrollback.
func (m *Model) View() string {
	if m.program == nil {
		return ""
	}
	sections := make([]string, 0, 8)
	if stream := m.stream.String(); strings.TrimSpace(stream) != "" {
		sections = append(sections, m.renderStream(stream))
	}
	if len(m.tools) > 0 {
		sections = append(sections, m.renderTools()...)
	}
	if m.running && strings.TrimSpace(m.stream.String()) == "" && len(m.tools) == 0 {
		sections = append(sections, styleAccent.Render(m.spinner.View())+" "+styleMuted.Render("Thinking… "+formatDuration(time.Since(m.turnStart))+" · esc cancel"))
	}
	if len(m.queue) > 0 {
		sections = append(sections, styleMuted.Render(fmt.Sprintf("  ⧗ %d messages queued", len(m.queue))))
	}
	if m.approval != nil {
		sections = append(sections, m.renderApproval())
	}
	if m.searching {
		sections = append(sections, m.renderSearch())
	}
	if m.picker != nil {
		sections = append(sections, m.picker.render(m.contentWidth()))
	}
	if m.policy == string(core.PolicyTrust) {
		sections = append(sections, styleWarn.Render("⚠ YOLO: all tools allowed without approval (deny rules still apply)"))
	}
	width := m.contentWidth()
	sections = append(sections, styleDim.Render(strings.Repeat("─", width)))
	sections = append(sections, m.renderInput())
	if suggestions := m.suggestions(); len(suggestions) > 0 && m.picker == nil && m.approval == nil {
		sections = append(sections, m.renderSuggestions(suggestions))
	}
	sections = append(sections, m.renderStatus())
	return strings.Join(sections, "\n")
}

func (m *Model) renderSuggestions(suggestions []slashCommand) string {
	items := make([]menuItem, 0, len(suggestions))
	for _, command := range suggestions {
		items = append(items, menuItem{label: padRight(strings.TrimSpace(command.name+" "+command.args), 18), hint: command.desc})
	}
	index := m.suggestIdx
	if index >= len(items) {
		index = 0
	}
	return strings.Join(renderList(items, index, m.contentWidth(), false), "\n")
}

func (m *Model) renderSearch() string {
	line := styleAccent.Render("search history: ") + m.searchQuery + styleAccent.Render("▏")
	switch {
	case m.searchMatch != "":
		match := strings.ReplaceAll(m.searchMatch, "\n", " ⏎ ")
		line += "\n  " + truncateLine(styleBold.Render(match), m.contentWidth()-2)
		line += "\n  " + styleMuted.Render("enter use · ctrl+r older · esc cancel")
	case m.searchQuery != "":
		line += "\n  " + styleMuted.Render("no matches")
	}
	return line
}

func (m *Model) renderStream(stream string) string {
	width := m.contentWidth() - 2
	lines := strings.Split(strings.TrimRight(stream, "\n"), "\n")
	out := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		for _, wrapped := range wrap(line, width) {
			out = append(out, "  "+wrapped)
		}
	}
	return strings.Join(out, "\n")
}

func (m *Model) renderTools() []string {
	out := make([]string, 0, len(m.tools)*2)
	for _, record := range m.tools {
		out = append(out, styleAccent.Render("⏺ ")+record.Summary())
		out = append(out, styleMuted.Render("  "+m.spinner.View()+" running · "+formatDuration(record.Duration())))
	}
	return out
}

func (m *Model) renderApproval() string {
	approval := m.approval
	label := approval.req.ToolName
	if record := m.findTool(approval.req.CallID); record != nil {
		label = record.Name
		if arg := primaryArg(record.Name, record.Input); arg != "" {
			label += ": " + arg
		}
	}
	lines := []string{styleAccent.Render("? ") + "Allow " + label}
	if m.verbose {
		if record := m.findTool(approval.req.CallID); record != nil {
			lines = append(lines, record.verboseLines(m.contentWidth())...)
		}
	}
	lines = append(lines, renderList(approval.choices.items, approval.choices.index, m.contentWidth(), true)...)
	return strings.Join(lines, "\n")
}

func (m *Model) findTool(callID string) *ToolRecord {
	for _, record := range m.tools {
		if record.CallID == callID {
			return record
		}
	}
	if m.lastTool != nil && m.lastTool.CallID == callID {
		return m.lastTool
	}
	return nil
}

func (m *Model) renderInput() string {
	lines := strings.Split(strings.TrimRight(m.input.View(), "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	var builder strings.Builder
	builder.WriteString(styleUser.Render("> "))
	builder.WriteString(strings.TrimRight(lines[0], " "))
	for _, line := range lines[1:] {
		builder.WriteString("\n  ")
		builder.WriteString(strings.TrimRight(line, " "))
	}
	return builder.String()
}

func (m *Model) renderStatus() string {
	parts := []string{m.agent, "embedded", m.policy, shortModel(m.model), formatTokens(m.usageIn + m.usageOut)}
	if m.verbose {
		parts = append(parts, "verbose")
	}
	left := strings.Join(parts, " · ")
	right := "/ commands · ctrl+r history"
	if m.approval != nil {
		right = "↑/↓ enter · y/a/n"
	}
	if m.compacting {
		right = "compacting…"
	}
	if m.running {
		right = "turn running · ctrl+c cancel"
	}
	return statusLine(left, right, m.contentWidth())
}

func statusLine(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return styleMuted.Render(truncateLine(left, width))
	}
	return styleMuted.Render(left) + strings.Repeat(" ", gap) + styleDim.Render(right)
}

func (m *Model) banner() []string {
	width := m.contentWidth()
	inner := width - 4
	if inner > 68 {
		inner = 68
	}
	if inner < 24 {
		inner = 24
	}
	rows := []string{
		styleBold.Render("lana") + styleMuted.Render(" "+m.version) + "  ·  " + m.agent,
		"embedded · " + m.policy + " · " + m.model,
		styleMuted.Render(shortCwd(m.cwd)),
	}
	lines := []string{"  " + styleDim.Render("╭"+strings.Repeat("─", inner+2)+"╮")}
	for _, row := range rows {
		lines = append(lines, "  "+styleDim.Render("│ ")+padRight(row, inner)+" "+styleDim.Render("│"))
	}
	lines = append(lines, "  "+styleDim.Render("╰"+strings.Repeat("─", inner+2)+"╯"))
	lines = append(lines, "  "+styleMuted.Render("/help for commands · press ctrl+c twice to exit"))
	return lines
}

func (m *Model) restoreLines() []string {
	if len(m.restore) == 0 {
		return nil
	}
	out := []string{styleMuted.Render("· continuing session " + m.sessionID)}
	for _, event := range m.restore {
		switch event.Kind {
		case core.KindUserMessage:
			var payload core.UserMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			if text := textOf(payload.Content); text != "" {
				out = append(out, "")
				out = append(out, renderUser(text)...)
			}
		case core.KindAssistantMessage:
			var payload core.AssistantMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			if text := textOf(payload.Content); text != "" {
				m.assistantStarted = false
				out = append(out, m.renderAssistant(text)...)
			}
		}
	}
	return out
}

func textOf(blocks []core.ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Kind == core.BlockText && block.Text != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

func padRight(s string, width int) string {
	gap := width - ansi.StringWidth(s)
	if gap <= 0 {
		return truncateLine(s, width)
	}
	return s + strings.Repeat(" ", gap)
}

func shortCwd(cwd string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(cwd, home) {
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return cwd
}

func shortModel(model string) string {
	if index := strings.LastIndex(model, ":"); index >= 0 {
		return model[index+1:]
	}
	return model
}

func formatTokens(total int64) string {
	switch {
	case total >= 1_000_000:
		return fmt.Sprintf("%.1fm tok", float64(total)/1_000_000)
	case total >= 1_000:
		return fmt.Sprintf("%.1fk tok", float64(total)/1_000)
	default:
		return fmt.Sprintf("%d tok", total)
	}
}

func renderUser(value string) []string {
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, styleUser.Render("> ")+line)
			continue
		}
		out = append(out, "  "+line)
	}
	return out
}

// splitCompleteBlocks splits the streaming buffer into completed Markdown
// blocks (delimited by blank lines outside code fences).
func splitCompleteBlocks(s string) ([]string, string) {
	var blocks []string
	inFence := false
	start := 0
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "\x60\x60\x60") && (i == 0 || s[i-1] == '\n') {
			inFence = !inFence
			i += 3
			if inFence {
				continue
			}
			lineEnd := strings.IndexByte(s[i:], '\n')
			if lineEnd < 0 {
				break
			}
			i += lineEnd + 1
			block := strings.TrimRight(s[start:i], "\n")
			if strings.TrimSpace(block) != "" {
				blocks = append(blocks, block)
			}
			start = i
			continue
		}
		if !inFence && strings.HasPrefix(s[i:], "\n\n") {
			block := strings.TrimRight(s[start:i], "\n")
			if strings.TrimSpace(block) != "" {
				blocks = append(blocks, block)
			}
			i += 2
			start = i
			continue
		}
		i++
	}
	return blocks, s[start:]
}

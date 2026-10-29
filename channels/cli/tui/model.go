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
)

// Options adalah input Run.
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
	req   core.ApprovalRequest
	reply chan core.ApprovalOutcome
}

// Model adalah state TUI chat.
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

	approval *approvalState

	lastCtrlC time.Time

	queue       []string
	searching   bool
	searchQuery string

	histIdx   int
	histDraft string
}

// NewModel membuat model TUI.
func NewModel(ctx context.Context, opts Options) *Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Tanya apa saja…  (/help)"
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

// Run menjalankan TUI sampai user keluar.
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

// Init memulai program.
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

// Update menangani pesan.
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
		m.approval = &approvalState{req: msg.req, reply: msg.reply}
		return m, tea.Tick(10*time.Minute, func(time.Time) tea.Msg {
			return approvalExpiredMsg{callID: msg.req.CallID}
		})
	case approvalExpiredMsg:
		if m.approval != nil && m.approval.req.CallID == msg.callID {
			m.approval = nil
			return m, tea.Println(styleMuted.Render("· approval kedaluwarsa"))
		}
		return m, nil
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
	if m.searching {
		return m.handleSearchKey(msg)
	}
	if m.approval != nil {
		return m.handleApprovalKey(msg)
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
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeInput()
	return m, cmd
}

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	approval := m.approval
	switch strings.ToLower(msg.String()) {
	case "y":
		approval.reply <- core.OutcomeAllowedOnce
		m.approval = nil
		return m, tea.Println(styleOK.Render("  ✓ diizinkan sekali"))
	case "a":
		approval.reply <- core.OutcomeAllowedSession
		m.approval = nil
		return m, tea.Println(styleOK.Render("  ✓ diizinkan untuk session"))
	case "n":
		approval.reply <- core.OutcomeRejected
		m.approval = nil
		return m, tea.Println(styleErr.Render("  ⊘ ditolak"))
	case "ctrl+c":
		approval.reply <- core.OutcomeCancelled
		m.approval = nil
		m.cancelTurn()
		return m, tea.Println(styleErr.Render("  ⊘ dibatalkan"))
	}
	return m, nil
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
	return m, tea.Println(styleMuted.Render("· tekan ctrl+c lagi untuk keluar"))
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
		return "menyala"
	}
	return "mati"
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
		return tea.Println(strings.Join(append(renderUser(value), styleMuted.Render("  (antre)")), "\n"))
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
		cmds = append(cmds, tea.Println(styleMuted.Render("· turn dibatalkan")))
	case outcome.err != nil:
		cmds = append(cmds, tea.Println(styleErr.Render("✗ error: "+outcome.err.Error())))
	case outcome.result.StopReason == "cancelled":
		cmds = append(cmds, tea.Println(styleMuted.Render("· turn dibatalkan")))
	case outcome.result.StopReason == "max_steps":
		cmds = append(cmds, tea.Println(styleWarn.Render("⚠ turn berhenti karena max_steps")))
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
		return m, nil
	case "enter":
		m.searching = false
		if match, ok := m.history.Search(m.searchQuery); ok {
			m.setInput(match)
		}
		m.searchQuery = ""
		return m, nil
	case "backspace":
		if m.searchQuery != "" {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(runes[:len(runes)-1])
		}
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		m.searchQuery += string(msg.Runes)
	}
	return m, nil
}

func (m *Model) slash(value string) tea.Cmd {
	fields := strings.Fields(value)
	command := fields[0]
	args := fields[1:]
	switch command {
	case "/help":
		return tea.Println(strings.Join([]string{
			styleMuted.Render("· perintah:"),
			"  /help            bantuan ini",
			"  /new             session baru",
			"  /clear           bersihkan layar",
			"  /session         id session dan cwd",
			"  /agent           agent aktif dan daftar agent",
			"  /model           model aktif",
			"  /permission [p]  tampilkan atau ubah policy (ask|trust|never)",
			"  /yolo            sama dengan /permission trust",
			"  /verbose         nyalakan atau matikan mode verbose",
			"  /exit            keluar",
		}, "\n"))
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
		return tea.Println(styleMuted.Render("· session baru " + info.ID))
	case "/session":
		return tea.Println(styleMuted.Render("· session " + m.sessionID + " · " + m.cwd))
	case "/agent":
		lines := []string{styleMuted.Render("· agent " + m.agent + " · " + m.model + " · " + m.policy)}
		lines = append(lines, styleMuted.Render("· tersedia: "+strings.Join(m.backend.AgentNames(), ", ")))
		return tea.Println(strings.Join(lines, "\n"))
	case "/model":
		return tea.Println(styleMuted.Render("· model " + m.model))
	case "/verbose":
		m.verbose = !m.verbose
		return tea.Println(styleMuted.Render("· verbose " + onOff(m.verbose)))
	case "/yolo":
		return m.setPolicy(string(core.PolicyTrust))
	case "/permission":
		if len(args) == 0 {
			return tea.Println(styleMuted.Render("· policy " + m.policy))
		}
		return m.setPolicy(strings.ToLower(args[0]))
	}
	return tea.Println(styleMuted.Render("· perintah tidak dikenal: " + command))
}

func (m *Model) setPolicy(value string) tea.Cmd {
	var policy core.Policy
	switch value {
	case string(core.PolicyAsk), string(core.PolicyTrust), string(core.PolicyNever):
		policy = core.Policy(value)
	default:
		return tea.Println(styleErr.Render("✗ policy tidak dikenal: " + value))
	}
	if err := m.backend.SetPolicy(m.ctx, m.sessionID, policy); err != nil {
		return tea.Println(styleErr.Render("✗ error: " + err.Error()))
	}
	m.policy = value
	lines := []string{styleMuted.Render("· policy " + value)}
	if policy == core.PolicyTrust {
		lines = append(lines, styleWarn.Render("⚠ YOLO: semua tool diizinkan tanpa approval (rule deny tetap berlaku)"))
	}
	return tea.Println(strings.Join(lines, "\n"))
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

// View merender area dinamis di bawah scrollback.
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
		sections = append(sections, m.spinner.View()+" "+styleMuted.Render("Berpikir…"))
	}
	if m.approval != nil {
		sections = append(sections, m.renderApproval())
	}
	if m.searching {
		sections = append(sections, styleAccent.Render("cari riwayat: ")+m.searchQuery)
	}
	if m.policy == string(core.PolicyTrust) {
		sections = append(sections, styleWarn.Render("⚠ YOLO: semua tool diizinkan tanpa approval (rule deny tetap berlaku)"))
	}
	width := m.contentWidth()
	sections = append(sections, styleDim.Render(strings.Repeat("─", width)))
	sections = append(sections, m.renderInput())
	sections = append(sections, m.renderStatus())
	return strings.Join(sections, "\n")
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
		out = append(out, styleMuted.Render("  "+m.spinner.View()+" berjalan · "+formatDuration(record.Duration())))
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
	lines := []string{styleAccent.Render("? ") + "Izinkan " + label}
	if m.verbose {
		if record := m.findTool(approval.req.CallID); record != nil {
			lines = append(lines, record.verboseLines(m.contentWidth())...)
		}
	}
	lines = append(lines, "  "+styleOK.Render("[y]")+" ya sekali   "+
		styleOK.Render("[a]")+" selalu untuk session ini   "+
		styleErr.Render("[n]")+" tolak")
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
	right := "/help"
	if m.running {
		right = "turn berjalan · ctrl+c batal"
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
	lines = append(lines, "  "+styleMuted.Render("/help untuk perintah · ctrl+c dua kali untuk keluar"))
	return lines
}

func (m *Model) restoreLines() []string {
	if len(m.restore) == 0 {
		return nil
	}
	out := []string{styleMuted.Render("· melanjutkan session " + m.sessionID)}
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

// splitCompleteBlocks memotong buffer streaming menjadi blok Markdown yang
// sudah selesai (dibatasi baris kosong di luar code fence).
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

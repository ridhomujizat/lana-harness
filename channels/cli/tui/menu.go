package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// menuVisible is the number of menu rows shown at once.
const menuVisible = 8

type menuItem struct {
	value string
	label string
	hint  string
}

// menu is a list of choices navigated with arrows, numbers, and enter.
type menu struct {
	title  string
	items  []menuItem
	index  int
	onPick func(value string) tea.Cmd
}

func (m *menu) move(delta int) {
	if len(m.items) == 0 {
		return
	}
	m.index = (m.index + delta + len(m.items)) % len(m.items)
}

func (m *menu) selectValue(value string) {
	for i, item := range m.items {
		if item.value == value {
			m.index = i
			return
		}
	}
}

// key handles navigation keys. picked is set on enter or digits 1-9;
// cancel is true on esc or ctrl+c.
func (m *menu) key(msg tea.KeyMsg) (picked *menuItem, cancel bool) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return nil, true
	case "up", "k", "shift+tab", "left":
		m.move(-1)
	case "down", "j", "tab", "right":
		m.move(1)
	case "home", "g":
		m.index = 0
	case "end", "G":
		m.index = len(m.items) - 1
	case "enter":
		if len(m.items) > 0 {
			return &m.items[m.index], false
		}
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
			if n := int(msg.Runes[0] - '0'); n >= 1 && n <= 9 && n <= len(m.items) {
				m.index = n - 1
				return &m.items[m.index], false
			}
		}
	}
	return nil, false
}

func (m *menu) render(width int) string {
	lines := []string{}
	if m.title != "" {
		lines = append(lines, styleAccent.Render("? ")+m.title+styleMuted.Render("  (↑/↓, 1-9, enter, esc)"))
	}
	lines = append(lines, renderList(m.items, m.index, width, true)...)
	return strings.Join(lines, "\n")
}

// renderList renders a window of items around the selected index.
func renderList(items []menuItem, index, width int, numbered bool) []string {
	start := 0
	if index >= menuVisible {
		start = index - menuVisible + 1
	}
	end := start + menuVisible
	if end > len(items) {
		end = len(items)
	}
	lines := make([]string, 0, end-start+2)
	if start > 0 {
		lines = append(lines, styleDim.Render(fmt.Sprintf("    ↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		item := items[i]
		label := item.label
		if numbered && i < 9 {
			label = fmt.Sprintf("%d. %s", i+1, label)
		}
		row := "    " + label
		if i == index {
			row = styleAccent.Render("  ❯ ") + styleBold.Render(label)
		}
		if item.hint != "" {
			row += "  " + styleMuted.Render(item.hint)
		}
		lines = append(lines, truncateLine(row, width))
	}
	if rest := len(items) - end; rest > 0 {
		lines = append(lines, styleDim.Render(fmt.Sprintf("    ↓ %d more", rest)))
	}
	return lines
}

type slashCommand struct {
	name string
	args string
	desc string
}

var slashCommands = []slashCommand{
	{"/help", "", "this help"},
	{"/new", "", "new session"},
	{"/sessions", "[id]", "list / switch sessions"},
	{"/fork", "", "fork session at the last turn"},
	{"/compact", "", "compact session history"},
	{"/clear", "", "clear screen"},
	{"/session", "", "session id and cwd"},
	{"/agent", "[name]", "pick or change agent"},
	{"/model", "[p:m]", "pick or change model"},
	{"/permission", "[p]", "pick or change policy (ask|trust|never)"},
	{"/yolo", "", "same as /permission trust"},
	{"/memory", "", "show this agent's memory"},
	{"/verbose", "", "turn verbose mode on or off"},
	{"/exit", "", "exit"},
}

// matchCommands returns the commands that start with prefix.
func matchCommands(prefix string) []slashCommand {
	out := []slashCommand{}
	for _, command := range slashCommands {
		if strings.HasPrefix(command.name, prefix) {
			out = append(out, command)
		}
	}
	return out
}

func isSlashCommand(name string) bool {
	if name == "/quit" {
		return true
	}
	for _, command := range slashCommands {
		if command.name == name {
			return true
		}
	}
	return false
}

func helpLines() []string {
	lines := []string{styleMuted.Render("· commands:")}
	for _, command := range slashCommands {
		lines = append(lines, "  "+padRight(strings.TrimSpace(command.name+" "+command.args), 18)+" "+command.desc)
	}
	lines = append(lines,
		styleMuted.Render("· keys:"),
		"  tab / ↑↓           complete slash command",
		"  ctrl+r             search history (repeat for older)",
		"  ctrl+o             tool verbose",
		"  esc / ctrl+c       cancel turn",
		"  shift+enter        new line",
	)
	return lines
}

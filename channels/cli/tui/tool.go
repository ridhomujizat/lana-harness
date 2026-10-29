package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/ridhomujizat/lana-harness/core"
)

// ToolRecord adalah satu tool call beserta hasilnya.
type ToolRecord struct {
	CallID  string
	Name    string
	Input   json.RawMessage
	Status  core.ToolStatus
	Output  string
	Started time.Time
	Done    time.Time
}

// Running melaporkan apakah tool belum selesai.
func (t *ToolRecord) Running() bool {
	return t.Status == ""
}

// Summary adalah baris ringkas "nama  argumen utama".
func (t *ToolRecord) Summary() string {
	arg := primaryArg(t.Name, t.Input)
	if arg == "" {
		return t.Name
	}
	return t.Name + "  " + arg
}

// Icon adalah ikon status.
func (t *ToolRecord) Icon() string {
	switch t.Status {
	case core.ToolStatusSuccess:
		return "✓"
	case core.ToolStatusError:
		return "✗"
	case core.ToolStatusRejected:
		return "⊘"
	default:
		return "…"
	}
}

// Label adalah teks status.
func (t *ToolRecord) Label() string {
	switch t.Status {
	case core.ToolStatusSuccess:
		return "sukses"
	case core.ToolStatusError:
		return "error"
	case core.ToolStatusRejected:
		return "ditolak"
	default:
		return "berjalan"
	}
}

// Duration adalah durasi tool.
func (t *ToolRecord) Duration() time.Duration {
	if t.Done.IsZero() {
		return time.Since(t.Started)
	}
	return t.Done.Sub(t.Started)
}

// StatusLine adalah baris status di bawah tool call.
func (t *ToolRecord) StatusLine() string {
	return "  " + t.Icon() + " " + t.Label() + " · " + formatDuration(t.Duration())
}

// Block merender tool call ringkas atau verbose menjadi baris terminal.
func (t *ToolRecord) Block(verbose bool, width int) []string {
	lines := []string{styleTool.Render("⏺ " + t.Summary())}
	if verbose {
		lines = append(lines, t.verboseLines(width)...)
	}
	lines = append(lines, styleMuted.Render(t.StatusLine()))
	return lines
}

func (t *ToolRecord) verboseLines(width int) []string {
	out := []string{styleMuted.Render("  input:")}
	pretty := t.Input
	var buf bytes.Buffer
	if len(pretty) > 0 && json.Indent(&buf, pretty, "", "  ") == nil {
		pretty = json.RawMessage(buf.String())
	}
	for _, line := range strings.Split(string(pretty), "\n") {
		out = append(out, styleMuted.Render("    "+truncateLine(line, width-4)))
	}
	out = append(out, styleMuted.Render("  output:"))
	rows := strings.Split(strings.TrimRight(t.Output, "\n"), "\n")
	limit := 40
	for i, line := range rows {
		if i == limit {
			out = append(out, styleMuted.Render(fmt.Sprintf("    … (+%d baris)", len(rows)-limit)))
			break
		}
		out = append(out, styleMuted.Render("    "+truncateLine(line, width-4)))
	}
	return out
}

// primaryArg mengambil argumen utama tool sesuai API-CLI-003.
func primaryArg(name string, input json.RawMessage) string {
	var value map[string]any
	_ = json.Unmarshal(input, &value)
	get := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := value[key]; ok {
				if text, ok := raw.(string); ok && text != "" {
					return text
				}
			}
		}
		return ""
	}
	var raw string
	switch name {
	case "bash":
		raw = get("command")
	case "read_file", "write_file", "edit_file":
		raw = get("path", "file_path")
	case "glob", "grep":
		raw = get("pattern")
	case "web_fetch":
		raw = get("url")
	case "task":
		agent, title := get("agent"), get("title")
		raw = strings.TrimSpace(agent + " · " + title)
	default:
		if len(input) > 0 {
			raw = string(input)
		}
	}
	raw = strings.Join(strings.Fields(raw), " ")
	if raw == "" {
		return ""
	}
	return truncateLine(raw, 88)
}

func truncateLine(s string, width int) string {
	if width < 8 {
		width = 8
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

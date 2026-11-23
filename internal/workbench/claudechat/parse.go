package claudechat

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DialogOption is one choice of a dialog parsed from the screen. Key is the
// digit Claude Code prints in front of the option, Description is the deeper
// indented line under it, and Checked is a multi-select mark (spec 71
// REQ-WC-021).
type DialogOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Selected    bool   `json:"selected"`
	Description string `json:"description,omitempty"`
	Checked     bool   `json:"checked,omitempty"`
}

// DialogTab is one chip of a question dialog's tab row. Done marks the chips
// whose step is finished ("✔"/"☒"); the active chip renders the same as the
// others on the visible screen (spec 71 REQ-WC-021).
type DialogTab struct {
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

// Dialog is an open Claude Code dialog read from the screen: a permission
// prompt, a question, a plan approval or another numbered list. Preview is the
// text of the box Claude Code draws beside a question's options for the option
// under the cursor (spec 71 REQ-WC-021).
type Dialog struct {
	Kind    string         `json:"kind"`
	Title   string         `json:"title"`
	Lines   []string       `json:"lines"`
	Tabs    []DialogTab    `json:"tabs,omitempty"`
	Options []DialogOption `json:"options"`
	Preview string         `json:"preview,omitempty"`
}

// Screen is what a mirror's visible rows say about the pane: whether Claude is
// working, its live status line, the permission mode, the effort, the chat
// input and any open dialog (spec 71 REQ-WC-021/022).
type Screen struct {
	Working    bool    // footer has "esc to interrupt"
	Status     string  // spinner line, e.g. "✶ Flowing…"
	Mode       string  // default, acceptEdits, plan, auto, bypassPermissions, "" when no footer seen
	Effort     string  // from "◐ medium · /effort" (low|medium|high|xhigh|max), "" if absent
	InputEmpty bool    // the input box holds no typed text
	Input      string  // typed input text ("" when empty/placeholder)
	Dialog     *Dialog // open dialog, or nil
}

// footerLines is how many of the last non-empty screen lines the mode and the
// working state are read from; a dialog can cover everything above the footer.
const footerLines = 6

// dialogScope is how many of the bottom screen lines the dialog parser scans.
const dialogScope = 30

// maxDialogLines caps a dialog's "lines" block.
const maxDialogLines = 12

// optionWrapIndent is the indentation at or beyond which a line under an option
// is a wrapped label continuation rather than the option's description.
const optionWrapIndent = 6

// modeWords maps a footer phrase to the permission mode it reports. The manual
// phrase reports the default mode.
var modeWords = []struct {
	phrase string
	mode   string
}{
	{"manual mode on", "default"},
	{"accept edits on", "acceptEdits"},
	{"plan mode on", "plan"},
	{"auto mode on", "auto"},
	{"bypass permissions on", "bypassPermissions"},
}

// effortWords are the effort levels reported next to "/effort".
var effortWords = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

var (
	// optionRE matches a numbered dialog option. The ❯ marker (when present)
	// marks the selected one.
	optionRE = regexp.MustCompile(`^\s*(❯\s*)?([0-9]+)\.\s+(.+)$`)
	// statusRE matches a live spinner line: a leading activity glyph, a space
	// and some text. ⏺ (a tool/message bullet) is deliberately not a match.
	statusRE = regexp.MustCompile(`^[·✢✳✶✻✽*]\s+\S`)
)

// ParseScreen reads the visible screen rows a mirror renders into the live
// state the chat shows. It never panics: a nil or empty slice is accepted and
// short or odd lines are ignored.
func ParseScreen(lines []string) Screen {
	var s Screen

	recent := lastNonEmpty(lines, footerLines)
	s.Mode = parseMode(recent)
	s.Working = containsAny(recent, "esc to interrupt")
	s.Effort = parseEffort(lines)

	boxTop, boxMid, hasBox := findInputBox(lines)
	if hasBox {
		s.Input, s.InputEmpty = parseInput(lines[boxMid])
	} else {
		s.Input, s.InputEmpty = "", false
	}

	// A dialog replaces the input box; while one is open there is no input.
	s.Dialog = parseDialog(lines)
	if s.Dialog != nil {
		s.Input, s.InputEmpty = "", false
	}

	if s.Working {
		limit := len(lines)
		if hasBox {
			limit = boxTop
		}
		s.Status = parseStatus(lines[:limit])
	}
	return s
}

// lastNonEmpty returns up to n of the last non-empty lines, bottom-first.
func lastNonEmpty(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			out = append(out, lines[i])
		}
	}
	return out
}

func containsAny(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// parseMode reads the permission mode from the footer, newest line first. No
// known phrase means the footer is hidden, so the mode is unknown ("") rather
// than default.
func parseMode(recent []string) string {
	for _, l := range recent {
		for _, mw := range modeWords {
			if strings.Contains(l, mw.phrase) {
				return mw.mode
			}
		}
	}
	return ""
}

// parseEffort reads the effort word from a "◐ medium · /effort" footer.
func parseEffort(lines []string) string {
	for _, line := range lines {
		i := strings.Index(line, "/effort")
		if i < 0 {
			continue
		}
		pre := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[:i]), "·"))
		fields := strings.Fields(pre)
		if len(fields) == 0 {
			continue
		}
		if last := fields[len(fields)-1]; effortWords[last] {
			return last
		}
	}
	return ""
}

// parseStatus returns the last live spinner line at or above the given rows.
func parseStatus(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); statusRE.MatchString(t) {
			return t
		}
	}
	return ""
}

// findInputBox locates the chat input box: a "─" rule, a line whose first rune
// is ❯, then another "─" rule. It returns the box's top rule index and the
// input line index. The last match wins.
func findInputBox(lines []string) (top, mid int, ok bool) {
	top, mid = -1, -1
	for i := 0; i+2 < len(lines); i++ {
		if !isInputRule(lines[i]) || !isInputRule(lines[i+2]) {
			continue
		}
		if !startsPrompt(lines[i+1]) {
			continue
		}
		top, mid, ok = i, i+1, true
	}
	return top, mid, ok
}

// parseInput reads the typed text of an input line. The text starts after the
// ❯ marker and its single following space. It is empty when there is nothing
// after the marker, when it holds the "Try …" placeholder, or when it is a dim
// suggestion (the marker is followed by another space).
func parseInput(line string) (text string, empty bool) {
	t := strings.TrimLeftFunc(line, unicode.IsSpace)
	if !strings.HasPrefix(t, "❯") {
		return "", true
	}
	rest := t[len("❯"):]
	if rest != "" {
		if r, size := utf8.DecodeRuneInString(rest); unicode.IsSpace(r) {
			rest = rest[size:]
		}
	}
	if rest == "" || strings.HasPrefix(rest, `Try "`) {
		return "", true
	}
	if r, _ := utf8.DecodeRuneInString(rest); unicode.IsSpace(r) {
		return "", true
	}
	return strings.TrimRightFunc(rest, unicode.IsSpace), false
}

// rawOption is a numbered option line before labels are finalized.
type rawOption struct {
	idx      int
	selected bool
	key      string
	label    string
	indent   int
}

func matchOption(line string) (rawOption, bool) {
	m := optionRE.FindStringSubmatch(line)
	if m == nil {
		return rawOption{}, false
	}
	return rawOption{
		selected: m[1] != "",
		key:      m[2],
		label:    strings.TrimRightFunc(m[3], unicode.IsSpace),
		indent:   leadingIndent(line),
	}, true
}

// parseDialog finds the dialog shown near the bottom of the screen, if any. It
// collects every candidate numbered block, drops the ones that sit above an
// input box (a dialog replaces the input box), then prefers the block that
// carries the ❯ cursor. With no marked block it takes the last one, but only
// when a hint line or a permission title marks it as a real dialog; otherwise
// it falls back to an unnumbered list (the trust prompt).
func parseDialog(lines []string) *Dialog {
	if len(lines) == 0 {
		return nil
	}
	start := len(lines) - dialogScope
	if start < 0 {
		start = 0
	}
	var opts []rawOption
	for i := start; i < len(lines); i++ {
		if o, ok := matchOption(lines[i]); ok {
			o.idx = i
			opts = append(opts, o)
		}
	}
	if len(opts) == 0 {
		return parseUnnumbered(lines, start)
	}
	boxTop, _, hasBox := findInputBox(lines)
	blocks := candidateDialogs(groupOptions(lines, opts), boxTop, hasBox)
	if len(blocks) == 0 {
		return parseUnnumbered(lines, start)
	}
	for b := len(blocks) - 1; b >= 0; b-- {
		if blockHasCursor(blocks[b]) {
			return buildDialog(lines, blocks[b])
		}
	}
	last := blocks[len(blocks)-1]
	_, title := findTitle(lines, last[0].idx)
	if dialogHint(lines, last) || strings.HasPrefix(title, "Do you want") {
		return buildDialog(lines, last)
	}
	return parseUnnumbered(lines, start)
}

// candidateDialogs keeps the option runs that could be a dialog: two or more
// options with no input box below the run. A numbered run above an input box is
// part of a reply, not a dialog, because a dialog replaces the input box.
func candidateDialogs(groups [][]rawOption, boxTop int, hasBox bool) [][]rawOption {
	blocks := make([][]rawOption, 0, len(groups))
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		if hasBox && boxTop > g[len(g)-1].idx {
			continue
		}
		blocks = append(blocks, g)
	}
	return blocks
}

// blockHasCursor reports whether any option of a run carries the ❯ marker.
func blockHasCursor(group []rawOption) bool {
	for _, o := range group {
		if o.selected {
			return true
		}
	}
	return false
}

// dialogHint reports whether a run is followed by a dialog hint: one of the
// four lines after it, or one of the screen's last three rows.
func dialogHint(lines []string, group []rawOption) bool {
	end := group[len(group)-1].idx
	for i := end + 1; i <= end+4 && i < len(lines); i++ {
		if isHintLine(lines[i]) {
			return true
		}
	}
	for i := len(lines) - 3; i < len(lines); i++ {
		if i >= 0 && isHintLine(lines[i]) {
			return true
		}
	}
	return false
}

// isHintLine reports whether a line is a dialog hint that names a key.
func isHintLine(line string) bool {
	return strings.Contains(line, "Esc to cancel") ||
		strings.Contains(line, "Enter to select") ||
		strings.Contains(line, "Enter to confirm")
}

// groupOptions splits option lines into runs. Two options belong to the same
// run when the lines between them are rule lines or are indented deeper than
// the run's shallowest option (wrapped labels and descriptions); a blank or
// shallower line starts a new run.
func groupOptions(lines []string, opts []rawOption) [][]rawOption {
	var groups [][]rawOption
	for i := 0; i < len(opts); {
		g := []rawOption{opts[i]}
		j := i + 1
		for j < len(opts) && optionsConnected(lines, g, opts[j].idx) {
			g = append(g, opts[j])
			j++
		}
		groups = append(groups, g)
		i = j
	}
	return groups
}

func optionsConnected(lines []string, group []rawOption, nextIdx int) bool {
	from := group[len(group)-1].idx
	base := group[0].indent
	for _, o := range group {
		if o.indent < base {
			base = o.indent
		}
	}
	for i := from + 1; i < nextIdx && i < len(lines); i++ {
		if isRule(lines[i]) {
			continue
		}
		if strings.TrimSpace(lines[i]) == "" {
			return false
		}
		if leadingIndent(lines[i]) <= base {
			return false
		}
	}
	return true
}

func buildDialog(lines []string, group []rawOption) *Dialog {
	firstOpt := group[0].idx
	// A question can carry a preview box beside its options. Cut every line
	// that runs into the box at its left edge, and keep the box's inner text.
	var preview string
	if col, inner, ok := findPreviewBox(lines, firstOpt); ok {
		lines = cutDialogColumns(lines, col, firstOpt)
		group = rematchOptions(lines, group)
		firstOpt = group[0].idx
		preview = inner
	}
	titleIdx, title := findTitle(lines, firstOpt)
	var tabs []DialogTab
	if chipsIdx, t := findChips(lines, firstOpt); chipsIdx >= 0 {
		tabs = t
		// A chips line names a question dialog: its title is the first line
		// under the chips, not the nearest "?" line.
		if ti, tt := titleBelowChips(lines, chipsIdx, firstOpt); ti >= 0 {
			titleIdx, title = ti, tt
		}
	}
	opts := buildOptions(lines, group, title)
	if len(opts) < 2 {
		return nil
	}
	kind := dialogKind(lines, title, titleIdx, firstOpt)
	if len(tabs) > 0 {
		kind = "question"
	}
	return &Dialog{
		Kind:    kind,
		Title:   title,
		Lines:   dialogLines(lines, titleIdx, firstOpt),
		Tabs:    tabs,
		Options: opts,
		Preview: preview,
	}
}

// findChips finds the chips line of a question dialog: a line above the first
// option, inside the dialog box (before its top rule), that carries ☐/☒/✔
// chips. It returns the line index and the tabs it holds, or -1.
func findChips(lines []string, firstOpt int) (int, []DialogTab) {
	for i := firstOpt - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			return -1, nil
		}
		if tabs, ok := parseChipsLine(lines[i]); ok {
			return i, tabs
		}
	}
	return -1, nil
}

// titleBelowChips returns the first non-empty line between a chips line and the
// first option: the title of a question dialog. The index is -1 when there is
// none.
func titleBelowChips(lines []string, chipsIdx, firstOpt int) (int, string) {
	for i := chipsIdx + 1; i < firstOpt && i < len(lines); i++ {
		if isRule(lines[i]) {
			continue
		}
		if t := strings.TrimSpace(lines[i]); t != "" {
			return i, t
		}
	}
	return -1, ""
}

// parseChipsLine reads a question dialog's chips row into its tabs: each chip
// is a ☐/☒/✔ marker followed by its label, and the arrows "←"/"→" that may wrap
// the row are not labels. ok is false when the line carries no chip.
func parseChipsLine(line string) ([]DialogTab, bool) {
	if !strings.ContainsAny(line, "☐☒✔") {
		return nil, false
	}
	var tabs []DialogTab
	rest := line
	for {
		i := strings.IndexAny(rest, "☐☒✔")
		if i < 0 {
			break
		}
		marker, size := utf8.DecodeRuneInString(rest[i:])
		after := rest[i+size:]
		end := strings.IndexAny(after, "☐☒✔←→")
		seg := after
		if end >= 0 {
			seg = after[:end]
		}
		tabs = append(tabs, DialogTab{
			Label: strings.TrimSpace(seg),
			Done:  marker == '✔' || marker == '☒',
		})
		if end < 0 {
			break
		}
		rest = after[end:]
	}
	return tabs, true
}

// parseOptionLabel strips a leading multi-select marker from an option label. A
// checked marker ("[✔]", "[x]", "[X]", "☒", "✔ ") marks the option checked and
// is removed; an unchecked marker ("[ ]", "☐ ") is removed and leaves it
// unchecked.
func parseOptionLabel(label string) (string, bool) {
	for _, m := range []string{"[✔]", "[x]", "[X]", "☒", "✔ "} {
		if rest, ok := strings.CutPrefix(label, m); ok {
			return strings.TrimSpace(rest), true
		}
	}
	for _, m := range []string{"[ ]", "☐ "} {
		if rest, ok := strings.CutPrefix(label, m); ok {
			return strings.TrimSpace(rest), false
		}
	}
	return label, false
}

// findTitle returns the nearest line above the first option that ends with "?",
// else the nearest non-empty line, joined with the lines its text wraps onto.
// The index is -1 when there is none.
func findTitle(lines []string, firstOpt int) (int, string) {
	idx := -1
	for i := firstOpt - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); strings.HasSuffix(t, "?") {
			idx = i
			break
		}
	}
	if idx < 0 {
		for i := firstOpt - 1; i >= 0; i-- {
			if t := strings.TrimSpace(lines[i]); t != "" {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return -1, ""
	}
	return joinWrappedTitle(lines, idx)
}

// joinWrappedTitle joins the lines a wrapped title spans. Starting at the title
// line it takes up to three lines directly above that share the title's
// indentation, are non-empty, are not rule lines and do not end a sentence, and
// prepends them with a space.
func joinWrappedTitle(lines []string, idx int) (int, string) {
	title := strings.TrimSpace(lines[idx])
	indent := leadingIndent(lines[idx])
	top := idx
	for n := 0; n < 3 && top-1 >= 0; n++ {
		prev := lines[top-1]
		t := strings.TrimSpace(prev)
		if t == "" || isRule(prev) || leadingIndent(prev) != indent {
			break
		}
		if strings.HasSuffix(t, ".") || strings.HasSuffix(t, "?") ||
			strings.HasSuffix(t, ":") || strings.HasSuffix(t, "!") {
			break
		}
		title = t + " " + title
		top--
	}
	return top, title
}

// buildOptions finalizes a run's options: it strips any multi-select marker,
// joins wrapped label continuations and deeper-indented descriptions, marks a
// selection, and repairs the permission prompt whose "1. Yes" was overwritten
// by a render glitch.
func buildOptions(lines []string, group []rawOption, title string) []DialogOption {
	extra, extraIdx := unnumberedOptions(lines, group)
	opts := make([]DialogOption, 0, len(group))
	for gi, o := range group {
		label, checked := parseOptionLabel(o.label)
		opt := DialogOption{Key: o.key, Label: label, Selected: o.selected, Checked: checked}
		hasNext := gi+1 < len(group)
		end := len(lines)
		if hasNext {
			end = group[gi+1].idx
		}
		labelDone := false
		for i := o.idx + 1; i < end && i < len(lines); i++ {
			if extraIdx[i] {
				break
			}
			if isRule(lines[i]) {
				continue
			}
			t := strings.TrimSpace(lines[i])
			if t == "" {
				continue
			}
			indent := leadingIndent(lines[i])
			if !labelDone && hasNext && indent >= optionWrapIndent {
				opt.Label = strings.TrimSpace(opt.Label + " " + t)
				continue
			}
			if !labelDone && indent > o.indent {
				opt.Description = strings.TrimSpace(opt.Description + " " + t)
				continue
			}
			labelDone = true
		}
		opts = append(opts, opt)
	}
	if len(opts) == 0 {
		return opts
	}
	anySelected := false
	for _, o := range opts {
		if o.Selected {
			anySelected = true
			break
		}
	}
	// A glitch in the permission prompt can overwrite the "1. Yes" line and
	// leave only options 2, 3, 4. Put the missing option back; it is selected
	// when the visible options are not.
	if opts[0].Key == "2" && strings.HasPrefix(title, "Do you want") {
		opts = append([]DialogOption{{Key: "1", Label: "Yes", Selected: !anySelected}}, opts...)
		anySelected = anySelected || opts[0].Selected
	}
	// With no marker anywhere, the first option is the selection.
	if !anySelected {
		opts[0].Selected = true
	}
	opts = append(opts, extra...)
	return opts
}

// unnumberedOptions returns the keyless option lines that follow the last
// numbered option of a dialog after a rule, such as "Chat about this". They sit
// at the option text column and are not hints. The set of their line indices is
// returned so the description scan can stop there.
func unnumberedOptions(lines []string, group []rawOption) ([]DialogOption, map[int]bool) {
	if len(group) == 0 {
		return nil, nil
	}
	col := optionContentColumn(group)
	sawRule := false
	var out []DialogOption
	idx := make(map[int]bool)
	for i := group[len(group)-1].idx + 1; i < len(lines); i++ {
		if isHintLine(lines[i]) {
			break
		}
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if isRule(lines[i]) {
			sawRule = true
			continue
		}
		if !sawRule || leadingIndent(lines[i]) != col {
			continue
		}
		out = append(out, DialogOption{Key: "", Label: t})
		idx[i] = true
	}
	return out, idx
}

// optionContentColumn is the column an option's text starts at, counting the ❯
// cursor as the two spaces it replaces.
func optionContentColumn(group []rawOption) int {
	col := -1
	for _, o := range group {
		c := o.indent
		if o.selected {
			c += 2
		}
		if col < 0 || c < col {
			col = c
		}
	}
	return col
}

// rematchOptions re-reads option lines after they were cut at the preview
// column, so the labels no longer carry the box characters.
func rematchOptions(lines []string, group []rawOption) []rawOption {
	out := make([]rawOption, 0, len(group))
	for _, o := range group {
		if o.idx >= 0 && o.idx < len(lines) {
			if m, ok := matchOption(lines[o.idx]); ok {
				m.idx = o.idx
				out = append(out, m)
				continue
			}
		}
		out = append(out, o)
	}
	return out
}

// findPreviewBox locates the preview column of a question dialog: a box whose
// top-left "┌" and bottom-left "└" sit in the same column C (C > 10) with "│"
// at C on the lines between. It returns C and the box's inner text, or ok=false.
func findPreviewBox(lines []string, start int) (int, string, bool) {
	if start < 0 {
		start = 0
	}
	for t := start; t < len(lines); t++ {
		for c, ch := range []rune(lines[t]) {
			if ch != '┌' || c <= 10 {
				continue
			}
			bottom, right, ok := boxFrame(lines, t, c)
			if !ok {
				continue
			}
			return c, boxInner(lines, t, bottom, c, right), true
		}
	}
	return 0, "", false
}

// boxFrame finds the bottom-left "└" line of a box whose top-left "┌" sits at
// (top, col), and the column of the top border's "┐". Every line between must
// carry "│" at col.
func boxFrame(lines []string, top, col int) (bottom, right int, ok bool) {
	topRunes := []rune(lines[top])
	right = -1
	for c := col + 1; c < len(topRunes); c++ {
		if topRunes[c] == '┐' {
			right = c
			break
		}
	}
	if right < 0 {
		return 0, 0, false
	}
	for i := top + 1; i < len(lines); i++ {
		r := []rune(lines[i])
		if col >= len(r) {
			return 0, 0, false
		}
		switch r[col] {
		case '│':
		case '└':
			return i, right, true
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// boxInner reads the lines between a box's top and bottom borders, dropping the
// outer "│" at col and at the box's right edge, right-trimming, and removing the
// indentation the rows share.
func boxInner(lines []string, top, bottom, col, right int) string {
	rows := make([]string, 0, bottom-top-1)
	for i := top + 1; i < bottom; i++ {
		r := []rune(lines[i])
		if col >= len(r) {
			rows = append(rows, "")
			continue
		}
		rest := r[col+1:]
		if cut := right - col - 1; cut >= 0 && cut <= len(rest) {
			rest = rest[:cut]
		}
		rows = append(rows, strings.TrimRightFunc(string(rest), unicode.IsSpace))
	}
	return trimCommonIndent(rows)
}

// trimCommonIndent removes the leading whitespace every row shares and joins the
// rows with "\n".
func trimCommonIndent(rows []string) string {
	common := -1
	for _, row := range rows {
		n := 0
		for _, ch := range row {
			if ch != ' ' && ch != '\t' {
				break
			}
			n++
		}
		if common < 0 || n < common {
			common = n
		}
	}
	if common > 0 {
		for i, row := range rows {
			r := []rune(row)
			if len(r) >= common {
				rows[i] = string(r[common:])
			} else {
				rows[i] = ""
			}
		}
	}
	return strings.Join(rows, "\n")
}

// cutDialogColumns returns the lines with every dialog line at or below start
// truncated at the preview column col (a no-op for shorter lines). It stops at
// the hint line so the footer is left whole.
func cutDialogColumns(lines []string, col, start int) []string {
	out := make([]string, len(lines))
	copy(out, lines)
	if col <= 0 {
		return out
	}
	for i := start; i < len(lines); i++ {
		if isHintLine(lines[i]) {
			break
		}
		r := []rune(out[i])
		if len(r) > col {
			r = r[:col]
		}
		out[i] = strings.TrimRightFunc(string(r), unicode.IsSpace)
	}
	return out
}

// dialogLines returns the non-empty, non-rule, non-Tip text between the block's
// top rule and the title, in screen order, capped at maxDialogLines.
func dialogLines(lines []string, titleIdx, firstOpt int) []string {
	out := []string{}
	if titleIdx <= 0 {
		return out
	}
	start := 0
	if top := topRuleAbove(lines, titleIdx); top >= 0 {
		start = top + 1
	}
	end := titleIdx
	if end > firstOpt {
		end = firstOpt
	}
	for i := start; i < end && i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || isRule(lines[i]) || strings.HasPrefix(t, "Tip:") {
			continue
		}
		out = append(out, t)
		if len(out) >= maxDialogLines {
			break
		}
	}
	return out
}

// topRuleAbove finds the top border of the box the title sits in: the topmost
// rule of the run of non-blank lines directly above the title. When the title
// is separated from its box by a blank, the nearest rule above is used.
func topRuleAbove(lines []string, titleIdx int) int {
	if titleIdx <= 0 {
		return -1
	}
	blockTop := titleIdx
	for i := titleIdx - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		blockTop = i
	}
	for i := blockTop; i < titleIdx; i++ {
		if isRule(lines[i]) {
			return i
		}
	}
	for i := titleIdx - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			return i
		}
	}
	return -1
}

// dialogKind classifies a dialog from its title and surroundings (spec 71
// REQ-WC-021).
func dialogKind(lines []string, title string, titleIdx, firstOpt int) string {
	if strings.HasPrefix(title, "Do you want") {
		return "permission"
	}
	if strings.Contains(title, "Would you like to proceed") {
		return "plan"
	}
	for i := 0; i < firstOpt && i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "Ready to code?" {
			return "plan"
		}
	}
	for i := titleIdx - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if strings.ContainsAny(t, "☐☒✔") {
			return "question"
		}
		break
	}
	if containsAny(lines, "Enter to select") {
		return "question"
	}
	return "other"
}

// parseUnnumbered handles a dialog with no numbered options, such as the trust
// prompt: a hint "Enter to confirm"/"Enter to select" and a ❯-marked line whose
// siblings are the other options.
func parseUnnumbered(lines []string, start int) *Dialog {
	if start > len(lines) {
		start = len(lines)
	}
	if !containsAny(lines[start:], "Enter to confirm") && !containsAny(lines[start:], "Enter to select") {
		return nil
	}
	_, boxMid, hasBox := findInputBox(lines)
	marker := -1
	for i := start; i < len(lines); i++ {
		if hasBox && i == boxMid {
			continue
		}
		if startsPrompt(lines[i]) {
			marker = i
			break
		}
	}
	if marker < 0 {
		return nil
	}
	mi := leadingIndent(lines[marker])
	group := []int{marker}
	for i := marker - 1; i >= start; i-- {
		if !siblingOption(lines[i], mi) {
			break
		}
		group = append([]int{i}, group...)
	}
	for i := marker + 1; i < len(lines); i++ {
		if !siblingOption(lines[i], mi) {
			break
		}
		group = append(group, i)
	}
	if len(group) < 2 {
		return nil
	}
	opts := make([]DialogOption, 0, len(group))
	for n, i := range group {
		label := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeftFunc(lines[i], unicode.IsSpace), "❯"))
		opts = append(opts, DialogOption{Key: strconv.Itoa(n + 1), Label: label, Selected: i == marker})
	}
	titleIdx, title := findTitle(lines, group[0])
	return &Dialog{
		Kind:    dialogKind(lines, title, titleIdx, group[0]),
		Title:   title,
		Lines:   dialogLines(lines, titleIdx, group[0]),
		Options: opts,
	}
}

// siblingOption reports whether a line is a sibling option of an unnumbered
// list: a non-blank, non-rule line indented to the label column of a ❯ marker
// (the three cells the "❯ " marker occupies, or two).
func siblingOption(line string, markerIndent int) bool {
	if strings.TrimSpace(line) == "" || isRule(line) {
		return false
	}
	d := leadingIndent(line)
	return d == markerIndent+2 || d == markerIndent+3
}

// isRule reports whether a line is made only of horizontal rule characters.
func isRule(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	for _, r := range t {
		switch r {
		case '─', '╌', '▔', '━':
		default:
			return false
		}
	}
	return true
}

// isInputRule reports whether a line is an input-box border (only "─").
func isInputRule(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	for _, r := range t {
		if r != '─' {
			return false
		}
	}
	return true
}

// startsPrompt reports whether a line begins with the ❯ prompt marker.
func startsPrompt(line string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "❯")
}

// leadingIndent counts the leading whitespace runes of a line.
func leadingIndent(s string) int {
	return utf8.RuneCountInString(s) - utf8.RuneCountInString(strings.TrimLeftFunc(s, unicode.IsSpace))
}

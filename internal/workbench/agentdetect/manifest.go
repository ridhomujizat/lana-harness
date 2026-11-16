package agentdetect

import (
	"embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

// Agent states a manifest rule can report.
const (
	StateIdle    = "idle"
	StateWorking = "working"
	StateBlocked = "blocked"
	StateUnknown = "unknown"
)

// Input is what a detection runs against: the rendered screen plus the OSC
// window title and progress strings captured from the terminal output. Pass
// empty strings when those sequences are not available.
type Input struct {
	Screen      string
	OSCTitle    string
	OSCProgress string
}

// Detection is the screen-derived state of an agent and whether the screen is
// an agent-owned view that must not update the state.
type Detection struct {
	State string
	Skip  bool
}

//go:embed manifests/*.toml
var manifestFS embed.FS

// bundledManifests maps an agent label to its bundled manifest file.
var bundledManifests = []struct {
	label string
	file  string
}{
	{agentAmp, "amp.toml"},
	{agentAntigravity, "antigravity.toml"},
	{agentClaude, "claude.toml"},
	{agentCline, "cline.toml"},
	{agentCodex, "codex.toml"},
	{agentCursor, "cursor.toml"},
	{agentDevin, "devin.toml"},
	{agentDroid, "droid.toml"},
	{agentGemini, "gemini.toml"},
	{agentGrok, "grok.toml"},
	{agentHermes, "hermes.toml"},
	{agentKilo, "kilo.toml"},
	{agentKimi, "kimi.toml"},
	{agentKiro, "kiro.toml"},
	{agentLetta, "letta.toml"},
	{agentMaki, "maki.toml"},
	{agentMuse, "muse.toml"},
	{agentOpenCode, "opencode.toml"},
	{agentPi, "pi.toml"},
	{agentQodercli, "qodercli.toml"},
	{agentQwen, "qwen.toml"},
	{agentCopilot, "github-copilot.toml"},
}

type agentManifest struct {
	ID    string         `toml:"id"`
	Rules []manifestRule `toml:"rules"`
}

type manifestRule struct {
	ID              string         `toml:"id"`
	State           string         `toml:"state"`
	Priority        int            `toml:"priority"`
	Region          string         `toml:"region"`
	SkipStateUpdate bool           `toml:"skip_state_update"`
	All             []manifestGate `toml:"all"`
	Any             []manifestGate `toml:"any"`
	Not             []manifestGate `toml:"not"`
	Contains        []string       `toml:"contains"`
	Regex           []string       `toml:"regex"`
	LineRegex       []string       `toml:"line_regex"`
}

type manifestGate struct {
	All       []manifestGate `toml:"all"`
	Any       []manifestGate `toml:"any"`
	Not       []manifestGate `toml:"not"`
	Contains  []string       `toml:"contains"`
	Regex     []string       `toml:"regex"`
	LineRegex []string       `toml:"line_regex"`
}

type loadedRule struct {
	state    string
	priority int
	region   string
	skip     bool
	gate     compiledGate
}

type compiledGate struct {
	all       []compiledGate
	any       []compiledGate
	not       []compiledGate
	contains  []string
	regex     []*regexp.Regexp
	lineRegex []*regexp.Regexp
}

// manifests holds every bundled manifest, keyed by agent label. It is parsed
// and compiled at package init; a manifest that does not parse or whose regexes
// do not compile leaves manifestsErr set, which the tests assert is nil.
var (
	manifests    = map[string][]loadedRule{}
	manifestsErr error
)

func init() {
	manifests, manifestsErr = loadManifests()
}

func loadManifests() (map[string][]loadedRule, error) {
	loaded := make(map[string][]loadedRule, len(bundledManifests))
	for _, bundled := range bundledManifests {
		content, err := manifestFS.ReadFile("manifests/" + bundled.file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", bundled.file, err)
		}
		var manifest agentManifest
		if err := toml.Unmarshal(content, &manifest); err != nil {
			return nil, fmt.Errorf("%s: %w", bundled.file, err)
		}
		rules := make([]loadedRule, len(manifest.Rules))
		for i, rule := range manifest.Rules {
			gate, err := compileGate(manifestGate{
				All:       rule.All,
				Any:       rule.Any,
				Not:       rule.Not,
				Contains:  rule.Contains,
				Regex:     rule.Regex,
				LineRegex: rule.LineRegex,
			})
			if err != nil {
				return nil, fmt.Errorf("%s rule %s: %w", bundled.file, rule.ID, err)
			}
			rules[i] = loadedRule{
				state:    rule.State,
				priority: rule.Priority,
				region:   rule.Region,
				skip:     rule.SkipStateUpdate,
				gate:     gate,
			}
		}
		loaded[bundled.label] = rules
	}
	return loaded, nil
}

func compileGate(gate manifestGate) (compiledGate, error) {
	compiled := compiledGate{}
	for _, needle := range gate.Contains {
		compiled.contains = append(compiled.contains, strings.ToLower(needle))
	}
	for _, pattern := range gate.Regex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return compiledGate{}, err
		}
		compiled.regex = append(compiled.regex, re)
	}
	for _, pattern := range gate.LineRegex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return compiledGate{}, err
		}
		compiled.lineRegex = append(compiled.lineRegex, re)
	}
	for _, nested := range gate.All {
		child, err := compileGate(nested)
		if err != nil {
			return compiledGate{}, err
		}
		compiled.all = append(compiled.all, child)
	}
	for _, nested := range gate.Any {
		child, err := compileGate(nested)
		if err != nil {
			return compiledGate{}, err
		}
		compiled.any = append(compiled.any, child)
	}
	for _, nested := range gate.Not {
		child, err := compileGate(nested)
		if err != nil {
			return compiledGate{}, err
		}
		compiled.not = append(compiled.not, child)
	}
	return compiled, nil
}

// Detect reports the state of label on the given input. An agent with no
// bundled manifest is unknown. A known agent whose screen matches no rule is
// idle, matching herdr's known-agent fallback.
func Detect(label string, in Input) Detection {
	rules, ok := manifests[label]
	if !ok || len(rules) == 0 {
		return Detection{State: StateUnknown}
	}
	best := -1
	for i := range rules {
		if !gateMatches(&rules[i].gate, region(in, rules[i].region)) {
			continue
		}
		if best < 0 || rules[i].priority > rules[best].priority {
			best = i
		}
	}
	if best < 0 {
		return Detection{State: StateIdle}
	}
	return Detection{State: rules[best].state, Skip: rules[best].skip}
}

// region returns the slice of the input a rule matches against.
func region(in Input, spec string) string {
	trimmed := strings.TrimSpace(spec)
	switch trimmed {
	case "osc_title":
		return in.OSCTitle
	case "osc_progress":
		return in.OSCProgress
	}
	content := in.Screen
	switch trimmed {
	case "whole_recent":
		return content
	case "after_last_prompt_marker":
		return afterLastPromptMarker(content)
	case "before_current_prompt_marker":
		return beforeCurrentPromptMarker(content)
	case "whole_recent_without_current_prompt_marker":
		return wholeRecentWithoutCurrentPromptMarker(content)
	case "current_prompt_block_marker":
		return currentPromptBlockMarker(content)
	case "after_current_prompt_block_marker":
		return afterCurrentPromptBlockMarker(content)
	case "prompt_box_body":
		return promptBoxBody(content)
	case "above_prompt_box":
		return abovePromptBox(content)
	case "last_non_empty_above_prompt_box":
		return lastNonEmptyLine(abovePromptBox(content))
	case "after_last_horizontal_rule":
		return afterLastHorizontalRule(content)
	}
	if count, ok := regionCount(trimmed, "bottom_lines"); ok {
		return bottomLines(content, count)
	}
	if count, ok := regionCount(trimmed, "bottom_non_empty_lines"); ok {
		return bottomNonEmptyLines(content, count)
	}
	if count, ok := topRegionCount(trimmed); ok {
		return topNonEmptyLines(content, count)
	}
	return ""
}

func gateMatches(gate *compiledGate, text string) bool {
	return gateMatchesLower(gate, text, strings.ToLower(text))
}

func gateMatchesLower(gate *compiledGate, text, lower string) bool {
	for _, needle := range gate.contains {
		if !strings.Contains(lower, needle) {
			return false
		}
	}
	for _, re := range gate.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range gate.lineRegex {
		matched := false
		for _, line := range rustLines(text) {
			if re.MatchString(line) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for i := range gate.all {
		if !gateMatchesLower(&gate.all[i], text, lower) {
			return false
		}
	}
	if len(gate.any) > 0 {
		matched := false
		for i := range gate.any {
			if gateMatchesLower(&gate.any[i], text, lower) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for i := range gate.not {
		if gateMatchesLower(&gate.not[i], text, lower) {
			return false
		}
	}
	return true
}

// rustLines splits like Rust's str::lines: on '\n', dropping a '\r' that
// precedes a line feed and not yielding a trailing empty line.
func rustLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, part := range parts {
		parts[i] = strings.TrimSuffix(part, "\r")
	}
	return parts
}

func regionCount(spec, name string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, name)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	return parseCount(rest, false)
}

func topRegionCount(spec string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, "top_non_empty_lines")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	return parseCount(rest, true)
}

func parseCount(value string, rejectLeadingZero bool) (int, bool) {
	if value == "" {
		return 0, false
	}
	if rejectLeadingZero && value[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	count, err := strconv.Atoi(value)
	if err != nil || count > 0xFFFF {
		return 0, false
	}
	return count, true
}

func bottomLines(content string, count int) string {
	lines := rustLines(content)
	start := len(lines) - count
	if start < 0 {
		start = 0
	}
	return sliceFromLineIndex(content, lines, start)
}

func bottomNonEmptyLines(content string, count int) string {
	lines := rustLines(content)
	start := -1
	taken := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		taken++
		start = i
		if taken == count {
			break
		}
	}
	if start < 0 {
		return ""
	}
	return sliceFromLineIndex(content, lines, start)
}

func topNonEmptyLines(content string, count int) string {
	lines := rustLines(content)
	end := -1
	taken := 0
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		taken++
		end = i
		if taken == count {
			break
		}
	}
	if end < 0 {
		return ""
	}
	offset := lineStartOffset(content, lines, end+1)
	return content[:offset]
}

func afterLastPromptMarker(content string) string {
	lines := rustLines(content)
	index := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if codexPromptLine(lines[i]) {
			index = i
			break
		}
	}
	if index < 0 {
		return content
	}
	return sliceFromLineIndex(content, lines, index+1)
}

func beforeCurrentPromptMarker(content string) string {
	lines := rustLines(content)
	index, ok := currentCodexPromptIndex(lines)
	if !ok {
		return content
	}
	offset := lineStartOffset(content, lines, index)
	return content[:offset]
}

func wholeRecentWithoutCurrentPromptMarker(content string) string {
	if _, ok := currentCodexPromptIndex(rustLines(content)); ok {
		return ""
	}
	return content
}

func currentPromptBlockMarker(content string) string {
	lines := rustLines(content)
	index, ok := currentCodexPromptIndex(lines)
	if !ok {
		return ""
	}
	for i := index - 1; i >= 0; i-- {
		if codexBlockMarkerLine(lines[i]) {
			return lines[i]
		}
	}
	return ""
}

func afterCurrentPromptBlockMarker(content string) string {
	lines := rustLines(content)
	index, ok := currentCodexPromptIndex(lines)
	if !ok {
		return ""
	}
	block := -1
	for i := index - 1; i >= 0; i-- {
		if codexBlockMarkerLine(lines[i]) {
			block = i
			break
		}
	}
	if block < 0 {
		return ""
	}
	return sliceFromLineIndex(content, lines, block)
}

func currentCodexPromptIndex(lines []string) (int, bool) {
	prompt := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if codexPromptLine(lines[i]) {
			prompt = i
			break
		}
	}
	if prompt < 0 {
		return 0, false
	}
	for i := prompt + 1; i < len(lines); i++ {
		if codexBlockMarkerLine(lines[i]) {
			return 0, false
		}
	}
	return prompt, true
}

func codexPromptLine(line string) bool {
	return line == "›" || strings.HasPrefix(line, "› ")
}

func codexBlockMarkerLine(line string) bool {
	for _, marker := range []string{"•", "■", "✗", "✓"} {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return false
}

func promptBoxBody(content string) string {
	lines := rustLines(content)
	top, ok := promptBoxTopBorderIndex(lines)
	if !ok {
		return ""
	}
	start := lineStartOffset(content, lines, top+1)
	endIndex := len(lines)
	for i := top + 1; i < len(lines); i++ {
		if isHorizontalRule(lines[i]) {
			endIndex = i
			break
		}
	}
	end := lineStartOffset(content, lines, endIndex)
	if start > len(content) {
		start = len(content)
	}
	if end > len(content) {
		end = len(content)
	}
	if end < start {
		end = start
	}
	return content[start:end]
}

func abovePromptBox(content string) string {
	lines := rustLines(content)
	top, ok := promptBoxTopBorderIndex(lines)
	if !ok {
		return content
	}
	return content[:lineStartOffset(content, lines, top)]
}

func afterLastHorizontalRule(content string) string {
	lastRuleEnd := 0
	offset := 0
	for _, line := range rustLines(content) {
		next := offset + len(line) + 1
		if isHorizontalRule(line) {
			lastRuleEnd = next
			if lastRuleEnd > len(content) {
				lastRuleEnd = len(content)
			}
		}
		offset = next
	}
	return content[lastRuleEnd:]
}

func lastNonEmptyLine(content string) string {
	lines := rustLines(content)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func promptBoxTopBorderIndex(lines []string) (int, bool) {
	borderCount := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if isHorizontalRule(lines[i]) {
			borderCount++
			if borderCount == 2 {
				return i, true
			}
		}
	}
	return 0, false
}

func isHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	runes := []rune(trimmed)
	count := 0
	for count < len(runes) && runes[count] == '─' {
		count++
	}
	if count == 0 {
		return false
	}
	suffix := strings.TrimLeftFunc(string(runes[count:]), unicode.IsSpace)
	return suffix == "" || count >= 3
}

func sliceFromLineIndex(content string, lines []string, index int) string {
	offset := lineStartOffset(content, lines, index)
	return content[offset:]
}

func lineStartOffset(content string, lines []string, index int) int {
	if index > len(lines) {
		index = len(lines)
	}
	offset := 0
	for i := 0; i < index; i++ {
		offset += len(lines[i]) + 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return offset
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/mcpconfig"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

const mcpManageDescription = "Lists, adds, or removes MCP servers for you. add and remove need the owner's approval every time. Put secrets as ${NAME} references; the owner sets the values in Settings → Secrets. New tools are available from the next turn."

const (
	mcpCommandMax  = 1024
	mcpArgsMax     = 64
	mcpEntriesMax  = 32
	mcpNameMax     = 64
	mcpConfigName  = "config.toml"
	secretHintText = "is not set; ask the owner to add it in Settings → Secrets"
)

var mcpManageSpec = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","add","remove"]},"name":{"type":"string"},"command":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"url":{"type":"string"},"env":{"type":"object","additionalProperties":{"type":"string"}},"headers":{"type":"object","additionalProperties":{"type":"string"}}},"required":["action"],"additionalProperties":false}`)

var (
	mcpEnvKeyRE      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	missingSecretRE  = regexp.MustCompile(`variable \$\{([^}]+)\} not found`)
	mcpNoopCheckFunc = func(context.Context, []byte) error { return nil }
)

type mcpManageTool struct{}

// NewMCPManage creates the mcp_manage tool.
func NewMCPManage() core.Tool { return &mcpManageTool{} }

func (t *mcpManageTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "mcp_manage", Description: mcpManageDescription, InputSchema: mcpManageSpec}
}

// Risk is read for list and admin for every other input, including an input
// that cannot be parsed.
func (t *mcpManageTool) Risk(raw json.RawMessage) core.Risk {
	var in struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return core.RiskAdmin
	}
	if in.Action == "list" {
		return core.RiskRead
	}
	return core.RiskAdmin
}

type mcpManageInput struct {
	Action  string             `json:"action"`
	Name    string             `json:"name"`
	Command *string            `json:"command"`
	Args    *[]string          `json:"args"`
	URL     *string            `json:"url"`
	Env     *map[string]string `json:"env"`
	Headers *map[string]string `json:"headers"`
}

// mcpApply is the in-memory work that runs after the config write succeeded.
type mcpApply struct {
	output string

	putName   string
	putConfig core.MCPConfig
	put       bool

	removeName string
	remove     bool

	setAgent bool
	agent    string
	servers  []string
}

func (a mcpApply) run(tc core.ToolCtx) {
	if a.put {
		tc.MCPAdmin.PutServer(a.putName, a.putConfig)
	}
	if a.remove {
		tc.MCPAdmin.RemoveServer(a.removeName)
	}
	if a.setAgent {
		tc.MCPAdmin.SetAgentServers(a.agent, a.servers)
	}
}

func (t *mcpManageTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in mcpManageInput
	if err := decodeInput(raw, &in); err != nil {
		return mcpInputError(err)
	}
	if err := validateMCPManageInput(&in); err != nil {
		return errorOutput("invalid input: " + err.Error())
	}
	if tc.Home == "" {
		return errorOutput("mcp_manage is not available: no home folder")
	}
	if in.Action != "list" && tc.MCPAdmin == nil {
		return errorOutput("mcp_manage is not available in this runtime")
	}
	slog.Info("mcp_manage", "action", in.Action, "agent", tc.AgentName, "server", in.Name)

	switch in.Action {
	case "list":
		return t.list(tc)
	case "add":
		return t.add(ctx, tc, &in)
	case "remove":
		return t.remove(ctx, tc, &in)
	default:
		return errorOutput("invalid input: action must be list, add, or remove")
	}
}

func (t *mcpManageTool) list(tc core.ToolCtx) (core.ToolOutput, error) {
	raw, out, ok := readMCPConfig(tc.Home)
	if !ok {
		return out, nil
	}
	doc, err := mcpconfig.Parse(raw)
	if err != nil {
		return errorOutput("config.toml is not valid: " + err.Error())
	}
	if len(doc.MCP) == 0 {
		return successOutput("no MCP servers configured")
	}
	attached := make(map[string]bool)
	for _, name := range mcpconfig.AgentList(doc, tc.AgentName) {
		attached[name] = true
	}
	names := make([]string, 0, len(doc.MCP))
	for name := range doc.MCP {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]string, 0, len(names))
	for _, name := range names {
		def := mcpconfig.FromMap(doc.MCP[name])
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s  %s  env=%s  headers=%s",
			name,
			mcpTypeLabel(def),
			mcpTarget(def),
			mcpAttachLabel(attached[name]),
			mcpStateLabel(tc, name),
			mcpKeysLabel(def.Env),
			mcpKeysLabel(def.Headers),
		))
	}
	return successOutput(strings.Join(lines, "\n"))
}

// mcpStateLabel returns the runtime state column of one server. It is "-" when
// no status source exists, and "not loaded" when the runtime does not know the
// server (API-TFB-007).
func mcpStateLabel(tc core.ToolCtx, name string) string {
	status := tc.MCPStatus
	if status == nil && tc.MCPAdmin != nil {
		// The admin also exposes a read-only status method; keeping it as a
		// fallback covers a caller that set only the admin.
		status = tc.MCPAdmin.ServerStatus
	}
	if status == nil {
		return "-"
	}
	state, tools, reason, known := status(name)
	if !known {
		return "not loaded"
	}
	switch state {
	case "running":
		return fmt.Sprintf("running (%d tools)", tools)
	case "unavailable":
		if reason == "" {
			return "unavailable"
		}
		return "unavailable: " + reason
	case "stopped", "":
		return "stopped"
	default:
		return state
	}
}

func (t *mcpManageTool) add(ctx context.Context, tc core.ToolCtx, in *mcpManageInput) (core.ToolOutput, error) {
	webfiles.ConfigWriteMu.Lock()
	plan, out, ok := t.addLocked(ctx, tc, in)
	webfiles.ConfigWriteMu.Unlock()
	if !ok {
		return out, nil
	}
	plan.run(tc)
	return successOutput(plan.output)
}

func (t *mcpManageTool) addLocked(ctx context.Context, tc core.ToolCtx, in *mcpManageInput) (mcpApply, core.ToolOutput, bool) {
	var zero mcpApply
	raw, out, ok := readMCPConfig(tc.Home)
	if !ok {
		return zero, out, false
	}
	doc, err := mcpconfig.Parse(raw)
	if err != nil {
		return zero, mcpErrorOutput("config.toml is not valid: " + err.Error()), false
	}
	stored, exists := doc.MCP[in.Name]
	list := mcpconfig.AgentList(doc, tc.AgentName)
	attached := stringInList(list, in.Name)
	def, hasDefinition := in.definition()

	if !hasDefinition {
		if !exists {
			return zero, mcpErrorOutput(fmt.Sprintf("mcp server %s is not configured; give command or url", in.Name)), false
		}
		if attached {
			return mcpApply{output: fmt.Sprintf("mcp server %s is already attached to agent %s", in.Name, tc.AgentName)}, core.ToolOutput{}, true
		}
		storedDef := mcpconfig.FromMap(stored)
		return t.attachOnly(ctx, tc, raw, list, in.Name, storedDef.Env, storedDef.Headers)
	}

	same := exists && sameMCPDefinition(mcpconfig.FromMap(stored), def)
	if same && attached {
		return mcpApply{output: fmt.Sprintf("mcp server %s is already attached to agent %s", in.Name, tc.AgentName)}, core.ToolOutput{}, true
	}
	if exists && !same {
		if others := otherUsage(mcpconfig.Usage(doc)[in.Name], tc.AgentName); len(others) > 0 {
			return zero, mcpErrorOutput(fmt.Sprintf("conflict: mcp server %s is used by %s", in.Name, strings.Join(others, ","))), false
		}
	}

	changedDefinition := !same
	listChanged := !attached
	nextList := append([]string(nil), list...)
	if listChanged {
		nextList = append(nextList, in.Name)
	}

	candidate := raw
	var mcpChanged []string
	if changedDefinition {
		table := mcpconfig.Render(in.Name, def)
		candidate, mcpChanged, err = webfiles.RewriteTable(ctx, mcpNoopCheckFunc, candidate, "[mcp."+in.Name+"]", table)
		if err != nil {
			return zero, mcpErrorOutput("config.toml is not valid: " + err.Error()), false
		}
	}
	candidate, out, ok = setAgentList(candidate, tc.AgentName, nextList)
	if !ok {
		return zero, out, false
	}

	redact := redactMaps(def.Env, def.Headers)
	if existing, ok := doc.MCP[in.Name]; ok {
		storedDef := mcpconfig.FromMap(existing)
		redact = append(redact, storedDef.Env, storedDef.Headers)
	}
	result, out, ok := checkMCPCandidate(ctx, tc.Home, candidate, redact...)
	if !ok {
		return zero, out, false
	}

	if changedDefinition {
		tools, err := tc.MCPAdmin.TestServer(ctx, result.Config.MCP[in.Name])
		if err != nil {
			resolved := resolvedMCPValues(result.Config.MCP[in.Name])
			message := mcpconfig.RedactValues(err.Error(), append(redact, resolved)...)
			return zero, mcpErrorOutput("connection test failed: " + message), false
		}
		plan := mcpApply{
			put:       true,
			putName:   in.Name,
			putConfig: result.Config.MCP[in.Name],
			setAgent:  true,
			agent:     tc.AgentName,
			servers:   result.Config.Agents[tc.AgentName].MCP,
			output:    fmt.Sprintf("added mcp server %s for agent %s (%d tools). Its tools are available from the next turn.", in.Name, tc.AgentName, tools),
		}
		if err := writeAndAudit(tc.Home, candidate, plan, "mcp.save", in.Name, mcpChanged, list, nextList); err != nil {
			return zero, *err, false
		}
		return plan, core.ToolOutput{}, true
	}

	// The definition is unchanged: only the agent list changes.
	plan := mcpApply{
		setAgent: true,
		agent:    tc.AgentName,
		servers:  result.Config.Agents[tc.AgentName].MCP,
		output:   fmt.Sprintf("attached mcp server %s to agent %s. Its tools are available from the next turn.", in.Name, tc.AgentName),
	}
	if err := writeAndAudit(tc.Home, candidate, plan, "", "", nil, list, nextList); err != nil {
		return zero, *err, false
	}
	return plan, core.ToolOutput{}, true
}

// attachOnly attaches an already configured, identical definition. It writes
// only the agent list and never runs a connection test.
func (t *mcpManageTool) attachOnly(ctx context.Context, tc core.ToolCtx, raw []byte, list []string, name string, redact ...map[string]string) (mcpApply, core.ToolOutput, bool) {
	var zero mcpApply
	nextList := append(append([]string(nil), list...), name)
	candidate, out, ok := setAgentList(raw, tc.AgentName, nextList)
	if !ok {
		return zero, out, false
	}
	result, out, ok := checkMCPCandidate(ctx, tc.Home, candidate, redact...)
	if !ok {
		return zero, out, false
	}
	plan := mcpApply{
		setAgent: true,
		agent:    tc.AgentName,
		servers:  result.Config.Agents[tc.AgentName].MCP,
		output:   fmt.Sprintf("attached mcp server %s to agent %s. Its tools are available from the next turn.", name, tc.AgentName),
	}
	if err := writeAndAudit(tc.Home, candidate, plan, "", "", nil, list, nextList); err != nil {
		return zero, *err, false
	}
	return plan, core.ToolOutput{}, true
}

func (t *mcpManageTool) remove(ctx context.Context, tc core.ToolCtx, in *mcpManageInput) (core.ToolOutput, error) {
	webfiles.ConfigWriteMu.Lock()
	plan, out, ok := t.removeLocked(ctx, tc, in)
	webfiles.ConfigWriteMu.Unlock()
	if !ok {
		return out, nil
	}
	plan.run(tc)
	return successOutput(plan.output)
}

func (t *mcpManageTool) removeLocked(ctx context.Context, tc core.ToolCtx, in *mcpManageInput) (mcpApply, core.ToolOutput, bool) {
	var zero mcpApply
	raw, out, ok := readMCPConfig(tc.Home)
	if !ok {
		return zero, out, false
	}
	doc, err := mcpconfig.Parse(raw)
	if err != nil {
		return zero, mcpErrorOutput("config.toml is not valid: " + err.Error()), false
	}
	list := mcpconfig.AgentList(doc, tc.AgentName)
	if !stringInList(list, in.Name) {
		return zero, mcpErrorOutput(fmt.Sprintf("mcp server %s is not attached to agent %s", in.Name, tc.AgentName)), false
	}
	others := otherUsage(mcpconfig.Usage(doc)[in.Name], tc.AgentName)
	_, tableExists := doc.MCP[in.Name]
	deleteTable := tableExists && len(others) == 0

	nextList := removeFromList(list, in.Name)
	candidate := raw
	var mcpChanged []string
	if deleteTable {
		candidate, mcpChanged, err = webfiles.RewriteTable(ctx, mcpNoopCheckFunc, candidate, "[mcp."+in.Name+"]", "")
		if err != nil {
			return zero, mcpErrorOutput("config.toml is not valid: " + err.Error()), false
		}
	}
	candidate, out, ok = setAgentList(candidate, tc.AgentName, nextList)
	if !ok {
		return zero, out, false
	}

	redact := []map[string]string{}
	if stored, ok := doc.MCP[in.Name]; ok {
		storedDef := mcpconfig.FromMap(stored)
		redact = append(redact, storedDef.Env, storedDef.Headers)
	}
	result, out, ok := checkMCPCandidate(ctx, tc.Home, candidate, redact...)
	if !ok {
		return zero, out, false
	}

	output := fmt.Sprintf("removed mcp server %s from agent %s", in.Name, tc.AgentName)
	if deleteTable {
		output += fmt.Sprintf("; deleted [mcp.%s]", in.Name)
	}
	plan := mcpApply{
		output:   output,
		remove:   deleteTable,
		setAgent: true,
		agent:    tc.AgentName,
		servers:  result.Config.Agents[tc.AgentName].MCP,
	}
	auditKind, auditTarget := "", ""
	if deleteTable {
		plan.removeName = in.Name
		auditKind, auditTarget = "mcp.delete", in.Name
	}
	if err := writeAndAudit(tc.Home, candidate, plan, auditKind, auditTarget, mcpChanged, list, nextList); err != nil {
		return zero, *err, false
	}
	return plan, core.ToolOutput{}, true
}

// writeAndAudit writes candidate atomically and appends the audit lines of the
// change. Audit failures are logged and do not undo the save.
func writeAndAudit(home string, candidate []byte, plan mcpApply, mcpKind, mcpTarget string, mcpChanged, oldList, newList []string) *core.ToolOutput {
	if err := webfiles.AtomicWrite(filepath.Join(home, mcpConfigName), candidate); err != nil {
		out := core.ToolOutput{Status: core.ToolStatusError, Output: "failed to write config.toml: " + err.Error()}
		return &out
	}
	if mcpKind != "" {
		appendMCPAuditKeys(home, mcpKind, mcpTarget, mcpChanged)
	}
	if changedArray(oldList, newList) {
		appendMCPAuditKeys(home, "agent.save", plan.agent, changedListKeys("agents."+plan.agent+".mcp", oldList, newList))
	}
	return nil
}

func appendMCPAuditKeys(home, kind, target string, keys []string) {
	entry := webfiles.AuditEntry{
		Time:        time.Now().UnixMilli(),
		Kind:        kind,
		Target:      target,
		ChangedKeys: keys,
	}
	if err := webfiles.AppendAudit(webfiles.AuditPath(home), entry); err != nil {
		slog.Warn("mcp_manage: append audit line", "error", err)
	}
}

func readMCPConfig(home string) ([]byte, core.ToolOutput, bool) {
	path := filepath.Join(home, mcpConfigName)
	data, err := os.ReadFile(path)
	if err == nil {
		return data, core.ToolOutput{}, true
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.ToolOutput{Status: core.ToolStatusError, Output: "config.toml does not exist"}, false
	}
	return nil, core.ToolOutput{Status: core.ToolStatusError, Output: "failed to read config.toml: " + err.Error()}, false
}

func setAgentList(candidate []byte, agent string, list []string) ([]byte, core.ToolOutput, bool) {
	out, err := webfiles.SetTableKey(candidate, "[agents."+agent+"]", "mcp", mcpconfig.RenderList(list))
	if err == nil {
		return out, core.ToolOutput{}, true
	}
	message := "config.toml is not valid: " + err.Error()
	if errors.Is(err, webfiles.ErrTableNotFound) {
		message = fmt.Sprintf("config.toml is not valid: agent %s is not configured", agent)
	}
	return nil, core.ToolOutput{Status: core.ToolStatusError, Output: message}, false
}

func checkMCPCandidate(ctx context.Context, home string, candidate []byte, redact ...map[string]string) (config.Result, core.ToolOutput, bool) {
	result, err := config.Check(ctx, home, candidate)
	if err == nil {
		return result, core.ToolOutput{}, true
	}
	message := err.Error()
	if match := missingSecretRE.FindStringSubmatch(message); len(match) == 2 {
		return config.Result{}, core.ToolOutput{
			Status: core.ToolStatusError,
			Output: fmt.Sprintf("secret %s %s", match[1], secretHintText),
		}, false
	}
	message = mcpconfig.RedactValues(message, redact...)
	return config.Result{}, core.ToolOutput{Status: core.ToolStatusError, Output: "config.toml is not valid: " + message}, false
}

func (in *mcpManageInput) definition() (mcpconfig.Definition, bool) {
	command := ""
	if in.Command != nil {
		command = *in.Command
	}
	rawURL := ""
	if in.URL != nil {
		rawURL = *in.URL
	}
	if command == "" && rawURL == "" {
		return mcpconfig.Definition{}, false
	}
	def := mcpconfig.Definition{Type: "command"}
	if rawURL != "" {
		def.Type = "url"
		def.URL = rawURL
		if in.Headers != nil {
			def.Headers = copyValues(*in.Headers)
		}
		return def, true
	}
	def.Command = command
	if in.Args != nil {
		def.Args = append([]string(nil), (*in.Args)...)
	}
	if in.Env != nil {
		def.Env = copyValues(*in.Env)
	}
	return def, true
}

func validateMCPManageInput(in *mcpManageInput) error {
	switch in.Action {
	case "list":
		return nil
	case "add", "remove":
	default:
		return errors.New("action must be list, add, or remove")
	}
	if in.Name == "" {
		return errors.New("name is required")
	}
	if len(in.Name) > mcpNameMax || !mcpconfig.NameRE.MatchString(in.Name) {
		return fmt.Errorf("name must match [a-z0-9-]+ and be at most %d characters", mcpNameMax)
	}
	if in.Action != "add" {
		return nil
	}

	command := ""
	if in.Command != nil {
		command = *in.Command
	}
	rawURL := ""
	if in.URL != nil {
		rawURL = *in.URL
	}
	if command != "" && rawURL != "" {
		return errors.New("command and url cannot both be set")
	}
	if in.Args != nil && command == "" {
		return errors.New("args is only for a command server")
	}
	if in.Env != nil && command == "" {
		return errors.New("env is only for a command server")
	}
	if in.Headers != nil && rawURL == "" {
		return errors.New("headers is only for a url server")
	}
	if command != "" {
		if strings.ContainsAny(command, "\r\n") {
			return errors.New("command must be one line")
		}
		if len([]rune(command)) > mcpCommandMax {
			return fmt.Errorf("command is too long (max %d characters)", mcpCommandMax)
		}
	}
	if in.Args != nil {
		if len(*in.Args) > mcpArgsMax {
			return fmt.Errorf("args has too many items (max %d)", mcpArgsMax)
		}
		for _, arg := range *in.Args {
			if strings.ContainsAny(arg, "\r\n") {
				return errors.New("args must be one line")
			}
		}
	}
	if in.Env != nil {
		if len(*in.Env) > mcpEntriesMax {
			return fmt.Errorf("env has too many entries (max %d)", mcpEntriesMax)
		}
		for key, value := range *in.Env {
			if !mcpEnvKeyRE.MatchString(key) {
				return fmt.Errorf("env key %s must match [A-Za-z_][A-Za-z0-9_]*", key)
			}
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("env value for %s must be one line", key)
			}
		}
	}
	if in.Headers != nil {
		if len(*in.Headers) > mcpEntriesMax {
			return fmt.Errorf("headers has too many entries (max %d)", mcpEntriesMax)
		}
		for key, value := range *in.Headers {
			if strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("headers value for %s must be one line", key)
			}
		}
	}
	if rawURL != "" {
		if strings.ContainsAny(rawURL, "\r\n") {
			return errors.New("url must be one line")
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("url must be an http:// or https:// URL")
		}
	}
	return nil
}

func sameMCPDefinition(a, b mcpconfig.Definition) bool {
	if a.Type != b.Type || a.Command != b.Command || a.URL != b.URL {
		return false
	}
	if !sameStrings(a.Args, b.Args) {
		return false
	}
	return sameValues(a.Env, b.Env) && sameValues(a.Headers, b.Headers)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameValues(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func copyValues(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func stringInList(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

func removeFromList(list []string, name string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != name {
			out = append(out, item)
		}
	}
	return out
}

func otherUsage(usage []string, agent string) []string {
	out := make([]string, 0, len(usage))
	for _, name := range usage {
		if name != agent {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func changedArray(oldList, newList []string) bool {
	if len(oldList) != len(newList) {
		return true
	}
	for i := range oldList {
		if oldList[i] != newList[i] {
			return true
		}
	}
	return false
}

// changedListKeys mirrors the dotted key paths of the config differ.
func changedListKeys(prefix string, oldList, newList []string) []string {
	shared := len(oldList)
	if len(newList) < shared {
		shared = len(newList)
	}
	keys := make([]string, 0)
	for i := 0; i < shared; i++ {
		if oldList[i] != newList[i] {
			keys = append(keys, prefix+"."+strconv.Itoa(i))
		}
	}
	for i := shared; i < len(oldList); i++ {
		keys = append(keys, prefix+"."+strconv.Itoa(i))
	}
	for i := shared; i < len(newList); i++ {
		keys = append(keys, prefix+"."+strconv.Itoa(i))
	}
	sort.Strings(keys)
	return keys
}

// resolvedMCPValues returns the resolved env and header values for redaction.
func resolvedMCPValues(cfg core.MCPConfig) map[string]string {
	out := make(map[string]string, len(cfg.Env)+len(cfg.Headers))
	for key, value := range cfg.Env {
		out[key] = value.Reveal()
	}
	for key, value := range cfg.Headers {
		out[key] = value.Reveal()
	}
	return out
}

func redactMaps(values ...map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(values))
	for _, value := range values {
		if len(value) > 0 {
			out = append(out, value)
		}
	}
	return out
}

func mcpErrorOutput(message string) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusError, Output: message}
}

func mcpInputError(err error) (core.ToolOutput, error) {
	message := err.Error()
	if field, ok := strings.CutPrefix(message, "json: unknown field "); ok {
		return errorOutput("invalid input: unknown field " + strings.Trim(field, `"`))
	}
	return inputErrorOutput(err)
}

func mcpTypeLabel(def mcpconfig.Definition) string {
	if def.Type == "url" {
		return "http"
	}
	return "stdio"
}

func mcpTarget(def mcpconfig.Definition) string {
	if def.Type == "url" {
		return def.URL
	}
	if len(def.Args) == 0 {
		return def.Command
	}
	return def.Command + " " + strings.Join(def.Args, " ")
}

func mcpAttachLabel(attached bool) string {
	if attached {
		return "attached"
	}
	return "available"
}

func mcpKeysLabel(values map[string]string) string {
	keys := mcpconfig.SortedKeys(values)
	if len(keys) == 0 {
		return "-"
	}
	return strings.Join(keys, ",")
}

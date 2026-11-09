package workflow

import "sort"

// NodeSpec describes one node type of the catalog (REQ-WF-005): its group, its
// required parameters (the bold params of the contract), its static output
// ports, and whether it is a trigger. A switch's ports depend on the node, so
// its Ports stays nil and NodePorts derives them.
type NodeSpec struct {
	Type     string
	Group    string
	Label    string
	Required []string
	Ports    []string
	Trigger  bool
	// Doc documents optional params the assistant prompt should mention.
	Doc string
}

// Node groups.
const (
	groupTrigger = "trigger"
	groupAI      = "ai"
	groupTool    = "tool"
	groupFlow    = "flow"
	groupState   = "state"
)

// Lock tool names fixed by the catalog (REQ-WF-009). The remaining tools are
// named after the node type or by node params.
const (
	toolBrowser   = "browser"
	toolBash      = "bash"
	toolWebfetch  = "webfetch"
	toolMessage   = "telegram.send"
	toolKnowledge = "knowledge.save"
	toolStateSet  = "state.set"
)

// nodeCatalog is the static table of every node type. NodeTypes returns its
// keys sorted.
var nodeCatalog = map[string]NodeSpec{
	"trigger.manual":   {Type: "trigger.manual", Group: groupTrigger, Label: "Manual", Ports: []string{"out"}, Trigger: true},
	"trigger.schedule": {Type: "trigger.schedule", Group: groupTrigger, Label: "Schedule", Required: []string{"cron"}, Ports: []string{"out"}, Trigger: true},
	"trigger.interval": {Type: "trigger.interval", Group: groupTrigger, Label: "Interval", Required: []string{"minutes"}, Ports: []string{"out"}, Trigger: true},
	"trigger.webhook":  {Type: "trigger.webhook", Group: groupTrigger, Label: "Webhook", Ports: []string{"out"}, Trigger: true},
	"trigger.event":    {Type: "trigger.event", Group: groupTrigger, Label: "Event", Required: []string{"kind"}, Ports: []string{"out"}, Trigger: true},
	"trigger.chat":     {Type: "trigger.chat", Group: groupTrigger, Label: "Chat command", Required: []string{"command"}, Ports: []string{"out"}, Trigger: true},

	"agent":        {Type: "agent", Group: groupAI, Label: "Agent", Required: []string{"prompt"}, Ports: []string{"out"}},
	"bot":          {Type: "bot", Group: groupAI, Label: "Bot", Required: []string{"agent", "prompt"}, Ports: []string{"out"}},
	"worker":       {Type: "worker", Group: groupAI, Label: "Worker", Required: []string{"task"}, Ports: []string{"out"}},
	"script_parse": {Type: "script_parse", Group: groupAI, Label: "Script Parse", Required: []string{"input", "goal", "output_schema"}, Ports: []string{"out"}},

	"browser":  {Type: "browser", Group: groupTool, Label: "Browser", Required: []string{"url"}, Ports: []string{"out"}},
	"bash":     {Type: "bash", Group: groupTool, Label: "Bash", Required: []string{"command"}, Ports: []string{"out"}},
	"webfetch": {Type: "webfetch", Group: groupTool, Label: "Web fetch", Required: []string{"url"}, Ports: []string{"out"}},
	"tool":     {Type: "tool", Group: groupTool, Label: "Tool", Required: []string{"name"}, Ports: []string{"out"}},
	"message": {Type: "message", Group: groupTool, Label: "Message", Required: []string{"text"}, Ports: []string{"out"},
		Doc: `param "channels": list of channel ids ("web", "telegram"; default ["telegram"]); "web" posts into the agent's web chat and needs "agent"`},
	"knowledge": {Type: "knowledge", Group: groupTool, Label: "Knowledge", Required: []string{"title", "text"}, Ports: []string{"out"}},

	"if":       {Type: "if", Group: groupFlow, Label: "If", Required: []string{"left", "op"}, Ports: []string{"true", "false"}},
	"switch":   {Type: "switch", Group: groupFlow, Label: "Switch", Required: []string{"value", "cases"}},
	"loop":     {Type: "loop", Group: groupFlow, Label: "Loop", Required: []string{"items"}, Ports: []string{"body", "done", "out"}},
	"wait":     {Type: "wait", Group: groupFlow, Label: "Wait", Required: []string{"seconds"}, Ports: []string{"out"}},
	"approval": {Type: "approval", Group: groupFlow, Label: "Approval", Required: []string{"message"}, Ports: []string{"approved", "denied"}},
	"merge":    {Type: "merge", Group: groupFlow, Label: "Merge", Ports: []string{"out"}},

	"state_get": {Type: "state_get", Group: groupState, Label: "Get state", Required: []string{"key"}, Ports: []string{"out"}},
	"state_set": {Type: "state_set", Group: groupState, Label: "Set state", Required: []string{"key", "value"}, Ports: []string{"out"}},
}

// Spec returns the catalog entry for a node type; ok is false for an unknown
// type (REQ-WF-002, unknown_type).
func Spec(nodeType string) (NodeSpec, bool) {
	spec, ok := nodeCatalog[nodeType]
	return spec, ok
}

// NodeTypes returns every known node type, sorted.
func NodeTypes() []string {
	out := make([]string, 0, len(nodeCatalog))
	for t := range nodeCatalog {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// NodePorts returns the output ports of one node. A switch exposes one port per
// case plus "default"; every other known type keeps its static ports, and an
// unknown type falls back to ["out"].
func NodePorts(n Node) []string {
	if n.Type == "switch" {
		ports := append([]string(nil), caseNames(n.Params["cases"])...)
		return append(ports, "default")
	}
	if spec, ok := Spec(n.Type); ok && len(spec.Ports) > 0 {
		return spec.Ports
	}
	return []string{"out"}
}

// LockToolsOf returns the tools a node needs for the permission lock
// (REQ-WF-009). A disabled node needs none.
func LockToolsOf(n Node) []string {
	if n.Disabled {
		return nil
	}
	switch n.Type {
	case "browser":
		return []string{toolBrowser}
	case "bash":
		return []string{toolBash}
	case "webfetch":
		return []string{toolWebfetch}
	case "tool":
		if name, ok := n.Params["name"].(string); ok && name != "" {
			return []string{name}
		}
		return nil
	case "message":
		return []string{toolMessage}
	case "knowledge":
		return []string{toolKnowledge}
	case "state_set":
		return []string{toolStateSet}
	case "worker":
		return stringList(n.Params["tools"])
	case "script_parse":
		// A script_parse node runs in-process and adds no tool (REQ-SP-014).
		return nil
	}
	return nil
}

// caseNames reads a switch's cases, accepting both []string and the []any that
// JSON decoding produces.
func caseNames(v any) []string {
	return stringList(v)
}

// stringList reads a list of strings, accepting both []string and []any and
// dropping empty entries.
func stringList(v any) []string {
	var raw []string
	switch t := v.(type) {
	case []string:
		raw = t
	case []any:
		raw = make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	default:
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

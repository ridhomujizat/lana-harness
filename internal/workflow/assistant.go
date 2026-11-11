package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file holds the guardian assistant prompt, its reply parsing and the
// graph diff of REQ-WF-010 (design section "Guardian prompts").

// assistantReplyContract is the exact reply contract given to the guardian at
// the end of the assistant prompt.
const assistantReplyContract = `Reply with ONE JSON object only, no prose around it: {"reply": "<answer in the owner's language>", "graph": <the full new graph> or null when nothing changes, "summary": "<one line of what changed>", "test": true to start a test run of the saved graph now, else false}. Do not call tools.`

// BuildAssistantPrompt builds the guardian assistant prompt of REQ-WF-010: the
// workflow goal, the current graph as indented JSON, the node catalog, the
// editing rules, the last ten chat messages, the owner message and the reply
// contract.
func BuildAssistantPrompt(w Workflow, g Graph, history []ChatMessage, message string) string {
	return BuildAssistantPromptEnv(w, g, history, message, AssistEnv{}, nil)
}

// BuildAssistantPromptEnv is BuildAssistantPrompt with the environment the
// workflow can use and the newest run, so the guardian builds with what
// exists, reads test results, and does not ask the owner for known facts.
func BuildAssistantPromptEnv(w Workflow, g Graph, history []ChatMessage, message string, env AssistEnv, last *RunDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the guardian of workflow %q (%s).\n", w.Name, w.ID)
	goal := w.Description
	if goal == "" {
		goal = w.Name
	}
	fmt.Fprintf(&b, "Goal: %s\n\n", goal)

	b.WriteString("Current graph (JSON):\n")
	graphJSON, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		graphJSON = []byte("{}")
	}
	b.Write(graphJSON)
	b.WriteString("\n\n")

	b.WriteString("Node catalog:\n")
	for _, t := range NodeTypes() {
		spec, _ := Spec(t)
		fmt.Fprintf(&b, "- %s [%s]: required %s; ports %s\n", t, spec.Group, paramsList(spec.Required), portsList(t, spec))
		if spec.Doc != "" {
			fmt.Fprintf(&b, "  %s\n", spec.Doc)
		}
	}
	b.WriteString("\n")

	b.WriteString("Rules:\n")
	b.WriteString("- node ids match ^[a-z0-9][a-z0-9_-]*$ and are unique.\n")
	b.WriteString("- every non-trigger node needs an incoming edge.\n")
	b.WriteString("- from_port is \"out\" (default), \"true\"/\"false\" (if), a case name or \"default\" (switch), \"approved\"/\"denied\" (approval), \"body\"/\"done\" (loop).\n")
	b.WriteString("- loops: a body node carries \"parent\": \"<loop id>\"; the loop has an edge with from_port \"body\" to the first body node and an edge with from_port \"done\" to what runs after.\n")
	b.WriteString("- templates: {{ input.x }}, {{ nodes.<id>.output.<path> }}, {{ item }}, {{ state.key }}, {{ run.number }}.\n")
	b.WriteString("- {{ nodes.<id>.output }} inside text gives the node's main text (text, markdown, content); use {{ nodes.<id>.output.<field> }} for another field.\n")
	b.WriteString("- positions: x/y in pixels, nodes are 248x64, keep a 40 px vertical gap, keep existing positions and place new nodes below their parent node.\n")
	// Script Parse rules (spec #44, REQ-SP-020).
	b.WriteString("- prefer script_parse over agent when a step only extracts, filters or reformats data of a fixed shape (pages, API or MCP JSON).\n")
	b.WriteString("- leave the script_parse param \"script\" empty: it is learned on the first run.\n")
	b.WriteString("- always give a script_parse \"output_schema\" (and \"item_schema\" for object[] fields).\n")
	b.WriteString("- rule tokens may follow the type in output_schema and item_schema, such as \"integer >0\", \"string url\", min:N, max:N or >=0.\n")
	b.WriteString("- a script_parse \"audit_every\": N (0 = off) also runs the fallback every Nth script run to catch a wrong-but-valid value; each audit costs one fallback.\n")
	b.WriteString("\n")

	writeAssistEnv(&b, env)
	writeLastRun(&b, last)

	b.WriteString("How to answer:\n")
	b.WriteString("- Use the environment above. Never ask the owner for an agent, MCP server, URL, tool name or secret name it lists; use it.\n")
	b.WriteString("- To call an API that has an MCP server, prefer a tool node with the MCP tool name and its input: no model per run, and lana adds the server's auth.\n")
	b.WriteString("- Ask the owner only for a fact that is not above and that you cannot leave as a sensible default; then ask once, for everything at the same time.\n")
	b.WriteString("- You cannot run commands, read files or call tools in this chat, and nothing carries over to a later chat. Never promise to do something later; do it now in the graph, or say plainly what the owner must do.\n")
	b.WriteString("- When the owner asks you to try or test it, or after a change you are unsure works, save the graph and set \"test\": true; the next message shows the run result above as \"Last run\". Read it and fix what failed.\n")
	b.WriteString("- Keep the reply short: what you changed, and why if it is not what the owner asked.\n\n")

	if len(history) > 10 {
		history = history[len(history)-10:]
	}
	if len(history) > 0 {
		b.WriteString("Recent chat:\n")
		for _, m := range history {
			fmt.Fprintf(&b, "%s: %s\n", chatRole(m.Role), m.Text)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Owner: %s\n\n", message)
	b.WriteString(assistantReplyContract)
	b.WriteString("\n")
	return b.String()
}

// AssistantReply is a parsed assistant answer (REQ-WF-010): the reply text, an
// optional full new graph and a one-line summary of the change.
type AssistantReply struct {
	Reply   string
	Graph   *Graph
	Summary string
	// Test asks for a test run of the saved graph.
	Test bool
}

// ParseAssistantReply parses the guardian assistant answer (REQ-WF-010). A text
// without a JSON object is treated as a plain reply; a null or missing "graph"
// means no change.
func ParseAssistantReply(text string) (AssistantReply, error) {
	v, err := ExtractJSON(text)
	if err != nil {
		return AssistantReply{Reply: strings.TrimSpace(text)}, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return AssistantReply{Reply: strings.TrimSpace(text)}, nil
	}
	var reply AssistantReply
	if s, ok := obj["reply"].(string); ok {
		reply.Reply = s
	}
	if s, ok := obj["summary"].(string); ok {
		reply.Summary = s
	}
	if t, ok := obj["test"].(bool); ok {
		reply.Test = t
	}
	if gv, ok := obj["graph"]; ok && gv != nil {
		raw, err := json.Marshal(gv)
		if err != nil {
			return AssistantReply{}, fmt.Errorf("workflow: bad assistant graph: %w", err)
		}
		var g Graph
		if err := json.Unmarshal(raw, &g); err != nil {
			return AssistantReply{}, fmt.Errorf("workflow: bad assistant graph: %w", err)
		}
		reply.Graph = &g
	}
	return reply, nil
}

// DiffGraphs compares two graphs by node id (REQ-WF-010). Added, Removed and
// Changed hold node names, falling back to the id; Added is in b order and
// Removed in a order. A node counts as changed when its type, name, parent,
// disabled flag, params or on_failure differ; position-only moves are ignored.
// An added or removed edge counts as a change of the edge's target node unless
// that node is already added or removed. FromVersion, ToVersion and Summary are
// left for the caller.
func DiffGraphs(a, b Graph) GraphChange {
	change := GraphChange{Added: []string{}, Removed: []string{}, Changed: []string{}}

	oldNodes := make(map[string]Node, len(a.Nodes))
	for _, n := range a.Nodes {
		oldNodes[n.ID] = n
	}
	newNodes := make(map[string]Node, len(b.Nodes))
	for _, n := range b.Nodes {
		newNodes[n.ID] = n
	}

	added := map[string]bool{}
	removed := map[string]bool{}
	changed := map[string]bool{}

	for _, n := range b.Nodes {
		prev, ok := oldNodes[n.ID]
		if !ok {
			if !added[n.ID] {
				change.Added = append(change.Added, nodeName(n))
			}
			added[n.ID] = true
			continue
		}
		if nodesDiffer(prev, n) {
			changed[n.ID] = true
		}
	}
	for _, n := range a.Nodes {
		if _, ok := newNodes[n.ID]; ok {
			continue
		}
		if !removed[n.ID] {
			change.Removed = append(change.Removed, nodeName(n))
		}
		removed[n.ID] = true
	}

	oldEdges := edgeSet(a.Edges)
	newEdges := edgeSet(b.Edges)
	edgeChanged := map[string]bool{}
	for _, e := range b.Edges {
		if oldEdges[edgeKey(e)] {
			continue
		}
		if !added[e.To] && !removed[e.To] {
			edgeChanged[e.To] = true
		}
	}
	for _, e := range a.Edges {
		if newEdges[edgeKey(e)] {
			continue
		}
		if !added[e.To] && !removed[e.To] {
			edgeChanged[e.To] = true
		}
	}

	for _, n := range b.Nodes {
		if changed[n.ID] || edgeChanged[n.ID] {
			change.Changed = append(change.Changed, nodeName(n))
		}
	}
	return change
}

// nodesDiffer reports whether two nodes differ in anything but position.
func nodesDiffer(a, b Node) bool {
	if a.Type != b.Type || a.Name != b.Name || a.Parent != b.Parent || a.Disabled != b.Disabled {
		return true
	}
	if !jsonEqual(a.Params, b.Params) {
		return true
	}
	return !jsonEqual(a.OnFailure, b.OnFailure)
}

// edgeKey identifies an edge by its endpoints and output port.
func edgeKey(e Edge) string {
	return e.From + "\x00" + edgePort(e) + "\x00" + e.To
}

// edgeSet returns the edges of a graph as a set of keys.
func edgeSet(edges []Edge) map[string]bool {
	set := make(map[string]bool, len(edges))
	for _, e := range edges {
		set[edgeKey(e)] = true
	}
	return set
}

// nodeName names a node for a change list: its name, or its id when unnamed.
func nodeName(n Node) string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

// paramsList renders a node type's required params for the prompt.
func paramsList(required []string) string {
	if len(required) == 0 {
		return "(none)"
	}
	return strings.Join(required, ", ")
}

// portsList renders a node type's output ports for the prompt.
func portsList(nodeType string, spec NodeSpec) string {
	if nodeType == "switch" {
		return "one per case plus default"
	}
	if len(spec.Ports) == 0 {
		return "(none)"
	}
	return strings.Join(spec.Ports, ", ")
}

// chatRole names the speaker of one chat message.
func chatRole(role string) string {
	if role == "" {
		return "owner"
	}
	return role
}

// writeAssistEnv writes the environment section of the assistant prompt.
func writeAssistEnv(b *strings.Builder, env AssistEnv) {
	if len(env.Agents) == 0 && len(env.MCPTools) == 0 && len(env.MCPUnavailable) == 0 && len(env.Secrets) == 0 {
		return
	}
	b.WriteString("Environment (what this workflow can use):\n")
	if len(env.Agents) > 0 {
		b.WriteString("Agents:\n")
		for _, a := range env.Agents {
			line := "- " + a.Name
			if a.Model != "" {
				line += " (" + a.Model + ")"
			}
			if a.Description != "" {
				line += ": " + a.Description
			}
			if len(a.MCP) > 0 {
				line += "; MCP servers: " + strings.Join(a.MCP, ", ")
			}
			b.WriteString(line + "\n")
		}
	}
	if len(env.MCPTools) > 0 {
		b.WriteString("MCP tools (use in a tool node as \"name\", or the guardian's agent node can call them):\n")
		for _, t := range env.MCPTools {
			line := "- " + t.Name
			if t.Description != "" {
				line += ": " + truncateRunes(oneLine(t.Description), 160)
			}
			if t.Input != "" {
				line += "; input " + truncateRunes(t.Input, 300)
			}
			b.WriteString(line + "\n")
		}
	}
	if len(env.MCPUnavailable) > 0 {
		names := make([]string, 0, len(env.MCPUnavailable))
		for name := range env.MCPUnavailable {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(b, "- MCP server %s is not available: %s\n", name, oneLine(env.MCPUnavailable[name]))
		}
	}
	if len(env.Secrets) > 0 {
		fmt.Fprintf(b, "Secrets set by the owner (names only): %s.\n", strings.Join(env.Secrets, ", "))
		if env.BashSeesSecrets {
			b.WriteString("Bash nodes get them as env vars.\n")
		} else {
			b.WriteString("Bash nodes do NOT get them as env vars; MCP servers and providers use them through lana's config.\n")
		}
	}
	b.WriteString("\n")
}

// writeLastRun writes the newest run and its failed nodes.
func writeLastRun(b *strings.Builder, last *RunDetail) {
	if last == nil || last.Run.ID == "" {
		return
	}
	r := last.Run
	fmt.Fprintf(b, "Last run: #%d (%s, version %d) %s", r.Number, r.Trigger, r.Version, r.Status)
	if r.Error != "" {
		b.WriteString(": " + truncateRunes(oneLine(r.Error), 300))
	}
	b.WriteString("\n")
	// Each node's status with its error, or the start of its output, so the
	// guardian can answer what a test produced.
	for i, n := range last.Nodes {
		if i >= 12 {
			fmt.Fprintf(b, "- … %d more nodes\n", len(last.Nodes)-i)
			break
		}
		detail := oneLine(n.Error)
		if detail == "" && len(n.Output) > 0 && string(n.Output) != "null" {
			detail = "output " + oneLine(string(n.Output))
		}
		fmt.Fprintf(b, "- node %s %s", n.NodeID, n.Status)
		if detail != "" {
			b.WriteString(": " + truncateRunes(detail, 300))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file holds the guardian chat guide, its per-turn context notes and the
// graph diff of REQ-WF-010 and spec 51 (REQ-WFC-002, REQ-WFC-004).

// BuildAssistantGuide builds the first runtime note of a guardian chat session
// (spec 51 REQ-WFC-002): who the guardian is, the workflow goal, the current
// graph, the node catalog, the editing rules, the environment, the newest run,
// any open candidate and how to work with the workflow_builder tool.
func BuildAssistantGuide(w Workflow, g Graph, env AssistEnv, last *RunDetail, cand *Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the guardian of workflow %q (%s). The owner is chatting with you in the workflow editor; this note is lana's context for that chat, not an owner message.\n", w.Name, w.ID)
	goal := w.Description
	if goal == "" {
		goal = w.Name
	}
	fmt.Fprintf(&b, "Goal: %s\n\n", goal)

	writeGraph(&b, fmt.Sprintf("Current graph, version %d (JSON):\n", w.Version), g)

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
	writeCandidate(&b, cand)

	b.WriteString("How to work in this chat:\n")
	b.WriteString("- Change the workflow only with the workflow_builder tool. The canvas shows the current version; a saved candidate is not live until you publish it.\n")
	b.WriteString("- Do not use the workflow tool's edit action for this workflow: it would start a second turn in this same chat and fail as \"session is busy\".\n")
	b.WriteString("- For every change: (1) workflow_builder save with the full new graph and a one-line summary, and fix any issues it returns; (2) workflow_builder test, with an input object when the trigger needs one, and read every node's status, output and error; (3) when a node failed or the output is not what the owner wants, fix the graph, save and test again; (4) publish only after a test passed, then tell the owner what changed and what the test produced.\n")
	b.WriteString("- Never say it works unless a test in this chat passed. When you cannot test (a trigger needs real outside data you do not have), say so plainly.\n")
	b.WriteString("- Before a test that does something real outside lana that the owner may not want (messages other people, posts publicly, buys, deletes, writes to an outside system), ask with ask_user first, for example: test now / publish without testing / change it first. Messages only to the owner are fine.\n")
	b.WriteString("- When the request is ambiguous and the answer changes the graph, ask with ask_user (up to 3 questions, each with options) instead of guessing. Never ask for an agent, MCP server, URL, tool name or secret name the environment lists; use it.\n")
	b.WriteString("- When the workflow needs a credential (API key, token, password) that the environment's secrets do not list, call secret_request with the name, why it is needed and where to get it; the owner types it into a secure card. Never ask for a secret value in chat. Reuse a listed secret name instead of asking again.\n")
	b.WriteString("- You may use your other tools to explore before building, for example call an MCP tool once or fetch a page to see the real data shape, then write templates and schemas against it.\n")
	b.WriteString("- To call an API that has an MCP server, prefer a tool node with the MCP tool name and its input: no model per run, and lana adds the server's auth.\n")
	b.WriteString("- A test cannot start while another run of this workflow is running; wait for it or tell the owner.\n")
	b.WriteString("- If publish reports a version conflict, the owner changed the canvas meanwhile: get the current graph, apply your change to it, save and test again.\n")
	b.WriteString("- A question about the workflow needs no save: just answer it.\n")
	b.WriteString("- Answer in the owner's language. Keep replies short: what you changed, what the test showed, and what the owner must do, if anything.\n")
	return b.String()
}

// BuildAssistantDelta builds a later runtime note of a guardian chat session
// (spec 51 REQ-WFC-002): only what changed since the last note. g is set when
// the current version moved, last when there is a newer run. It is empty when
// nothing changed.
func BuildAssistantDelta(w Workflow, g *Graph, last *RunDetail) string {
	var b strings.Builder
	if g != nil {
		writeGraph(&b, fmt.Sprintf("Workflow update: the current version is now %d (JSON):\n", w.Version), *g)
	}
	writeLastRun(&b, last)
	return strings.TrimSpace(b.String())
}

// writeGraph writes a heading and the indented graph JSON.
func writeGraph(b *strings.Builder, heading string, g Graph) {
	b.WriteString(heading)
	graphJSON, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		graphJSON = []byte("{}")
	}
	b.Write(graphJSON)
	b.WriteString("\n\n")
}

// writeCandidate writes the open candidate, when there is one.
func writeCandidate(b *strings.Builder, c *Candidate) {
	if c == nil || c.ID == 0 {
		return
	}
	fmt.Fprintf(b, "Open candidate #%d (based on version %d, not published): %s\n", c.ID, c.BaseVersion, c.Summary)
	b.WriteString("Use workflow_builder get to read it; test, publish or discard it.\n\n")
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

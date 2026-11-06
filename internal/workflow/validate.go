package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// This file holds graph validation (REQ-WF-002) and the name-to-id slug of
// REQ-WF-001. Validate runs the rules of the design section "Catalog and
// validation"; ValidateForActivation adds the activation-only rule of
// REQ-WF-009.

// idPattern is the node id form of the contract: it must start with a letter
// or digit and may hold lowercase letters, digits, underscores and dashes, up
// to 48 characters.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// workflowIDPattern is the workflow id form of REQ-WF-001: like a node id but
// dashes only (a slug).
var workflowIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// ValidWorkflowID reports whether id is a valid workflow id (REQ-WF-001):
// `^[a-z0-9][a-z0-9-]{0,47}$`.
func ValidWorkflowID(id string) bool {
	return workflowIDPattern.MatchString(id)
}

// Slug turns a name into a workflow id (REQ-WF-001): lowercase, every run of
// characters outside [a-z0-9] becomes one "-", the result is trimmed and cut to
// 48 characters, and an empty result becomes "workflow".
func Slug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case isSlugRune(r):
			b.WriteRune(r)
			lastDash = false
		case b.Len() > 0 && !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		return "workflow"
	}
	return s
}

// isSlugRune reports whether r may stay in a slug: a lowercase letter or digit.
func isSlugRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// Validate checks a graph against every rule of REQ-WF-002 and the design
// section "Catalog and validation". agentExists, when non-nil, rejects agent
// names it does not know. The issues are sorted by node id then code.
func Validate(g Graph, agentExists func(string) bool) []Issue {
	v := &validator{agentExists: agentExists, nodeByID: make(map[string]Node, len(g.Nodes))}
	for _, n := range g.Nodes {
		v.nodeByID[n.ID] = n
	}
	v.graph = g
	v.checkNodes()
	v.checkEdges()
	v.checkCycle()
	v.checkReachability()
	return sortIssues(v.issues)
}

// ValidateForActivation checks the graph before activation (REQ-WF-009): the
// rules of Validate plus the requirement of at least one enabled trigger node.
func ValidateForActivation(g Graph, agentExists func(string) bool) []Issue {
	issues := Validate(g, agentExists)
	if !hasEnabledTrigger(g) {
		issues = append(issues, Issue{NodeID: "", Code: "no_trigger", Message: "the workflow has no enabled trigger"})
	}
	return sortIssues(issues)
}

// validator carries the graph and the issues found while checking it.
type validator struct {
	graph       Graph
	agentExists func(string) bool
	nodeByID    map[string]Node
	issues      []Issue
}

// add appends one issue.
func (v *validator) add(nodeID, code, format string, args ...any) {
	v.issues = append(v.issues, Issue{NodeID: nodeID, Code: code, Message: fmt.Sprintf(format, args...)})
}

// checkNodes runs every per-node rule.
func (v *validator) checkNodes() {
	seen := make(map[string]bool, len(v.graph.Nodes))
	for _, n := range v.graph.Nodes {
		v.checkNode(n, seen)
	}
}

// checkNode checks one node: type, id, uniqueness, required params, bounds,
// cron, parent, templates and agent name.
func (v *validator) checkNode(n Node, seen map[string]bool) {
	spec, known := Spec(n.Type)
	if !known {
		v.add(n.ID, "unknown_type", "node %s has unknown type %q", nodeLabel(n), n.Type)
	}
	if !idPattern.MatchString(n.ID) {
		v.add(n.ID, "bad_id", "node id %q must match ^[a-z0-9][a-z0-9_-]{0,47}$", n.ID)
	}
	if seen[n.ID] {
		v.add(n.ID, "duplicate_id", "node id %q is used more than once", n.ID)
	}
	seen[n.ID] = true

	if known {
		for _, p := range spec.Required {
			if isMissingParam(n.Params, p) {
				v.add(n.ID, "missing_param", "node %s needs param %q", nodeLabel(n), p)
			}
		}
	}
	v.checkBounds(n)
	v.checkCron(n)
	v.checkParent(n, spec)
	v.checkTemplates(n)
	v.checkAgent(n)
}

// checkBounds enforces the numeric range of the interval and wait nodes
// (REQ-WF-002): interval minutes ≥ 1 and wait seconds in 1..604800.
func (v *validator) checkBounds(n Node) {
	switch n.Type {
	case "trigger.interval":
		if raw, ok := n.Params["minutes"]; ok && !isMissingValue(raw) {
			if f, ok := asNumber(raw); !ok || f < 1 {
				v.add(n.ID, "missing_param", "node %s needs param %q to be a number of minutes of at least 1", nodeLabel(n), "minutes")
			}
		}
	case "wait":
		if raw, ok := n.Params["seconds"]; ok && !isMissingValue(raw) {
			if f, ok := asNumber(raw); !ok || f < 1 || f > 604800 {
				v.add(n.ID, "missing_param", "node %s needs param %q between 1 and 604800 seconds", nodeLabel(n), "seconds")
			}
		}
	}
}

// checkCron parses the schedule of a trigger.schedule node (REQ-WF-003). A
// missing or empty cron is reported by the required-param rule instead.
func (v *validator) checkCron(n Node) {
	if n.Type != "trigger.schedule" {
		return
	}
	expr, ok := n.Params["cron"].(string)
	if !ok || expr == "" {
		return
	}
	if _, err := ParseCron(expr); err != nil {
		v.add(n.ID, "bad_cron", "node %s has a bad cron schedule: %v", nodeLabel(n), err)
	}
}

// checkParent checks the loop membership of a node (REQ-WF-002): a parent must
// exist, be a loop, and not be a loop nested in a loop; a trigger cannot have a
// parent.
func (v *validator) checkParent(n Node, spec NodeSpec) {
	if n.Parent == "" {
		return
	}
	if spec.Trigger {
		v.add(n.ID, "bad_parent", "node %s is a trigger and cannot have a parent", nodeLabel(n))
		return
	}
	parent, ok := v.nodeByID[n.Parent]
	if !ok {
		v.add(n.ID, "bad_parent", "node %s has parent %q that is not in the graph", nodeLabel(n), n.Parent)
		return
	}
	if parent.Type != "loop" {
		v.add(n.ID, "bad_parent", "node %s has parent %q that is not a loop", nodeLabel(n), n.Parent)
		return
	}
	if n.Type == "loop" {
		v.add(n.ID, "bad_parent", "node %s is a loop nested in loop %q", nodeLabel(n), n.Parent)
	}
}

// checkTemplates parses every template of a node and checks that every
// nodes.<id> reference names a node of the graph (REQ-WF-002).
func (v *validator) checkTemplates(n Node) {
	if err := CheckTemplates(n.Params); err != nil {
		v.add(n.ID, "bad_template", "node %s has a bad template: %v", nodeLabel(n), err)
	}
	for _, ref := range TemplateRefs(n.Params) {
		if _, ok := v.nodeByID[ref]; !ok {
			v.add(n.ID, "bad_template", "node %s references unknown node %q", nodeLabel(n), ref)
		}
	}
}

// checkAgent rejects an unknown agent name on bot, worker and trigger.event
// nodes (REQ-WF-002). It is skipped when agentExists is nil.
func (v *validator) checkAgent(n Node) {
	if v.agentExists == nil {
		return
	}
	switch n.Type {
	case "bot", "worker", "trigger.event":
	default:
		return
	}
	name, _ := n.Params["agent"].(string)
	if name == "" {
		return
	}
	if !v.agentExists(name) {
		v.add(n.ID, "unknown_agent", "node %s uses unknown agent %q", nodeLabel(n), name)
	}
}

// checkEdges runs the per-edge rules.
func (v *validator) checkEdges() {
	for _, e := range v.graph.Edges {
		v.checkEdge(e)
	}
}

// checkEdge checks one edge (REQ-WF-002): both endpoints exist, the target is
// not a trigger, the port belongs to the source, and the edge does not cross a
// loop boundary.
func (v *validator) checkEdge(e Edge) {
	from, fromOK := v.nodeByID[e.From]
	to, toOK := v.nodeByID[e.To]
	if !fromOK || !toOK {
		missing := e.From
		if fromOK {
			missing = e.To
		}
		v.add(e.ID, "missing_node", "edge %q points to missing node %q", e.ID, missing)
		return
	}
	if spec, ok := Spec(to.Type); ok && spec.Trigger {
		v.add(e.ID, "bad_port", "edge %q points into trigger node %q", e.ID, to.ID)
		return
	}
	port := edgePort(e)
	if !containsString(NodePorts(from), port) {
		v.add(e.ID, "bad_port", "edge %q uses port %q that node %q does not have", e.ID, e.FromPort, from.ID)
		return
	}
	v.checkBoundary(e, from, to, port)
}

// checkBoundary rejects an edge that crosses a loop boundary (REQ-WF-002). The
// only allowed crossing is loop --body--> one of its own body nodes.
func (v *validator) checkBoundary(e Edge, from, to Node, port string) {
	if from.Type == "loop" && port == "body" {
		if to.Parent != from.ID {
			v.add(e.ID, "bad_edge", "edge %q from loop %q with port %q must go to one of its own body nodes", e.ID, from.ID, "body")
		}
		return
	}
	if from.Parent == to.Parent {
		return
	}
	loop := from.Parent
	if loop == "" {
		loop = to.Parent
	}
	v.add(e.ID, "bad_edge", "edge %q crosses the boundary of loop %q", e.ID, loop)
}

// checkCycle runs Kahn's algorithm over every edge. When nodes stay unprocessed
// it reports one cycle, naming the nodes of the cycle (REQ-WF-002, AC-WF-002).
func (v *validator) checkCycle() {
	adj := make(map[string][]string, len(v.graph.Nodes))
	indeg := make(map[string]int, len(v.graph.Nodes))
	for _, n := range v.graph.Nodes {
		if _, ok := indeg[n.ID]; !ok {
			indeg[n.ID] = 0
		}
	}
	for _, e := range v.graph.Edges {
		if _, ok := v.nodeByID[e.From]; !ok {
			continue
		}
		if _, ok := v.nodeByID[e.To]; !ok {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}

	queued := make(map[string]bool, len(indeg))
	var queue []string
	for _, n := range v.graph.Nodes {
		if indeg[n.ID] == 0 && !queued[n.ID] {
			queued[n.ID] = true
			queue = append(queue, n.ID)
		}
	}
	processed := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		processed++
		for _, next := range adj[cur] {
			indeg[next]--
			if indeg[next] == 0 && !queued[next] {
				queued[next] = true
				queue = append(queue, next)
			}
		}
	}
	if processed == len(indeg) {
		return
	}

	remaining := make(map[string]bool)
	var order []string
	for _, n := range v.graph.Nodes {
		if queued[n.ID] || remaining[n.ID] {
			continue
		}
		remaining[n.ID] = true
		order = append(order, n.ID)
	}
	cycle := findCycle(order, adj, remaining)
	if len(cycle) == 0 {
		return
	}
	v.add(cycle[0], "cycle", "nodes form a cycle: %s", strings.Join(cycle, " -> "))
}

// findCycle returns the node ids of one cycle inside the remaining set, with
// the first node repeated at the end, or nil when there is none.
func findCycle(order []string, adj map[string][]string, inSet map[string]bool) []string {
	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(order))
	var stack []string
	var cycle []string
	var dfs func(string) bool
	dfs = func(n string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, next := range adj[n] {
			if !inSet[next] {
				continue
			}
			switch color[next] {
			case gray:
				for i, s := range stack {
					if s == next {
						cycle = append(append([]string(nil), stack[i:]...), next)
						return true
					}
				}
			case white:
				if dfs(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	for _, n := range order {
		if inSet[n] && color[n] == white {
			if dfs(n) {
				return cycle
			}
		}
	}
	return nil
}

// checkReachability rejects a non-trigger node without an incoming edge
// (REQ-WF-002). A top-level node needs any incoming edge; a body node needs one
// from its loop (port body) or from a sibling body node.
func (v *validator) checkReachability() {
	for _, n := range v.graph.Nodes {
		if spec, ok := Spec(n.Type); ok && spec.Trigger {
			continue
		}
		if n.Parent == "" {
			if v.hasIncoming(n.ID) {
				continue
			}
			v.add(n.ID, "unreachable", "node %s has no incoming edge", nodeLabel(n))
			continue
		}
		if v.hasLoopInput(n) {
			continue
		}
		v.add(n.ID, "unreachable", "body node %s has no incoming edge from its loop or a sibling body node", nodeLabel(n))
	}
}

// hasIncoming reports whether any edge into id comes from a node of the graph.
func (v *validator) hasIncoming(id string) bool {
	for _, e := range v.graph.Edges {
		if e.To != id {
			continue
		}
		if _, ok := v.nodeByID[e.From]; ok {
			return true
		}
	}
	return false
}

// hasLoopInput reports whether a body node has an incoming edge from its loop
// (port body) or from a sibling body node.
func (v *validator) hasLoopInput(n Node) bool {
	for _, e := range v.graph.Edges {
		if e.To != n.ID {
			continue
		}
		from, ok := v.nodeByID[e.From]
		if !ok {
			continue
		}
		if from.ID == n.Parent && edgePort(e) == "body" {
			return true
		}
		if from.Parent == n.Parent {
			return true
		}
	}
	return false
}

// sortIssues orders issues by node id then code, keeping the insertion order of
// ties.
func sortIssues(issues []Issue) []Issue {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].NodeID != issues[j].NodeID {
			return issues[i].NodeID < issues[j].NodeID
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

// hasEnabledTrigger reports whether the graph has at least one enabled trigger
// node (REQ-WF-009).
func hasEnabledTrigger(g Graph) bool {
	for _, n := range g.Nodes {
		if triggerEnabled(n) {
			return true
		}
	}
	return false
}

// triggerEnabled reports whether a trigger node fires: it is a known trigger,
// not disabled, and its enabled param is not false.
func triggerEnabled(n Node) bool {
	spec, ok := Spec(n.Type)
	if !ok || !spec.Trigger || n.Disabled {
		return false
	}
	if raw, ok := n.Params["enabled"]; ok {
		if b, isBool := raw.(bool); isBool {
			return b
		}
	}
	return true
}

// edgePort is an edge's output port with "" meaning "out".
func edgePort(e Edge) string {
	if e.FromPort == "" {
		return "out"
	}
	return e.FromPort
}

// isMissingParam reports whether a required param is absent, nil or ""; an
// empty list (a switch's cases) also counts as missing.
func isMissingParam(params map[string]any, key string) bool {
	if params == nil {
		return true
	}
	raw, ok := params[key]
	if !ok {
		return true
	}
	return isMissingValue(raw)
}

// isMissingValue reports whether a param value counts as absent.
func isMissingValue(raw any) bool {
	switch t := raw.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	default:
		return false
	}
}

// nodeLabel names a node in a message: its id, or its name when the id is empty.
func nodeLabel(n Node) string {
	if n.ID == "" {
		return fmt.Sprintf("%q", n.Name)
	}
	return fmt.Sprintf("%q", n.ID)
}

// Package layout is the Workbench layout model shared with the web UI
// (spec 55 REQ-WB-031, spec 58 REQ-DW-020). It ports web/ui/src/views/
// workbench-model.ts. The helpers are pure: they return a new layout and
// never change the one they were given.
//
// Every object keeps the JSON fields it does not know (REQ-DW-021), so a
// layout written by a newer web UI keeps its extra fields when the native
// app rewrites it.
package layout

import (
	"bytes"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	mrand "math/rand"
	"regexp"
	"strings"
)

// DefaultColor is the colour of a space that has none.
const DefaultColor = "#3E9C8F"

// SpaceColors are the colours a new space takes, in turn.
var SpaceColors = []string{"#3E9C8F", "#5B7BE0", "#D9824B", "#7652C2", "#B21FB8", "#3F8F3F", "#8C8880"}

// AgentPrograms are the programs the Workbench treats as coding agents.
var AgentPrograms = []string{"claude", "codex", "jcode", "opencode", "gemini"}

// Layout is the stored Workbench document: its version, the spaces, and the
// name of the active one.
type Layout struct {
	Version     int     `json:"version"`
	Spaces      []Space `json:"spaces"`
	ActiveSpace string  `json:"activeSpace,omitempty"`
	Grouping    string  `json:"grouping,omitempty"`

	extra map[string]json.RawMessage
}

// Space is a named group of tabs, with the folder of its terminals.
type Space struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Dir       string `json:"dir"`
	Tabs      []Tab  `json:"tabs"`
	ActiveTab string `json:"activeTab,omitempty"`
	// Auto names the space after the folder of its active terminal; a name
	// the owner gave it turns it off.
	Auto bool `json:"auto"`

	extra   map[string]json.RawMessage
	hasID   bool
	hasName bool
}

// Tab is a tab of a space: a tree of panes holding its sessions.
type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Root  Node   `json:"root"`
	Focus string `json:"focus,omitempty"`
	Zoom  string `json:"zoom,omitempty"`

	extra   map[string]json.RawMessage
	hasID   bool
	hasRoot bool
}

// Node is a pane ({type:"pane", session}) or a split of nodes ({type:
// "split", dir:"row"|"col", children, sizes}). Sizes are each child's
// fraction of the room (spec 58 REQ-DW-022); a missing or invalid sizes
// means equal shares.
type Node struct {
	Type     string    `json:"type"`
	Session  string    `json:"session,omitempty"`
	Dir      string    `json:"dir,omitempty"`
	Children []Node    `json:"children,omitempty"`
	Sizes    []float64 `json:"sizes,omitempty"`

	extra       map[string]json.RawMessage
	hasSession  bool
	hasChildren bool
}

// Session is the little of a Workbench session the layout helpers need.
type Session struct {
	Program  string            `json:"program,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	State    string            `json:"state,omitempty"`
	NeedsYou bool              `json:"needs_you,omitempty"`
	ExitCode int               `json:"exit_code,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	Dir      string            `json:"dir,omitempty"`
	Agent    string            `json:"agent,omitempty"`
}

// Status is what a session is doing, as the Agents panel shows it.
type Status string

// The session statuses.
const (
	StatusBlocked Status = "blocked"
	StatusWorking Status = "working"
	StatusIdle    Status = "idle"
	StatusExited  Status = "exited"
)

// The JSON fields the model knows, at every level; anything else is kept
// as an unknown field.
var (
	layoutKeys = keySet("version", "spaces", "activeSpace", "grouping")
	spaceKeys  = keySet("id", "name", "color", "dir", "tabs", "activeTab", "auto")
	tabKeys    = keySet("id", "title", "root", "focus", "zoom")
	nodeKeys   = keySet("type", "session", "dir", "children", "sizes")
)

func keySet(keys ...string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// objectFields decodes a JSON object into its raw fields; a value that is
// not an object gives false.
func objectFields(b []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// unknownFrom keeps the fields of m that the model does not know.
func unknownFrom(m map[string]json.RawMessage, known map[string]bool) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	for k, v := range m {
		if k == "" || known[k] {
			continue
		}
		if out == nil {
			out = make(map[string]json.RawMessage)
		}
		out[k] = v
	}
	return out
}

// keepUnknown marshals a typed value and merges the unknown fields it kept
// back into the same JSON object.
func keepUnknown(v any, extra map[string]json.RawMessage) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return b, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, raw := range extra {
		if _, exists := m[k]; !exists {
			m[k] = raw
		}
	}
	return json.Marshal(m)
}

// jsonString reads a JSON string; any other value, null included, gives
// false.
func jsonString(raw json.RawMessage) (string, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// decodeList decodes a JSON array, keeping only the elements decode accepts.
// A missing, null or non-array value gives an empty list.
func decodeList[T any](raw json.RawMessage, decode func([]byte) (T, bool)) []T {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil || elems == nil {
		return []T{}
	}
	out := make([]T, 0, len(elems))
	for _, e := range elems {
		if v, ok := decode(e); ok {
			out = append(out, v)
		}
	}
	return out
}

func decodeSpace(b []byte) (Space, bool) {
	var s Space
	if err := json.Unmarshal(b, &s); err != nil {
		return Space{}, false
	}
	return s, true
}

func decodeTab(b []byte) (Tab, bool) {
	var t Tab
	if err := json.Unmarshal(b, &t); err != nil {
		return Tab{}, false
	}
	return t, true
}

// Layout, Space, Tab and Node each marshal to their typed fields plus the
// unknown fields kept on unmarshal.
type layoutJSON Layout
type spaceJSON Space
type tabJSON Tab
type nodeJSON Node

// UnmarshalJSON reads a layout, keeping the fields it does not know.
func (l *Layout) UnmarshalJSON(b []byte) error {
	m, ok := objectFields(b)
	if !ok {
		return errors.New("layout: not a JSON object")
	}
	*l = Layout{extra: unknownFrom(m, layoutKeys)}
	if raw, ok := m["version"]; ok {
		_ = json.Unmarshal(raw, &l.Version)
	}
	if v, ok := jsonString(m["activeSpace"]); ok {
		l.ActiveSpace = v
	}
	if v, ok := jsonString(m["grouping"]); ok {
		l.Grouping = v
	}
	if raw, ok := m["spaces"]; ok {
		l.Spaces = decodeList(raw, decodeSpace)
	}
	if l.Spaces == nil {
		l.Spaces = []Space{}
	}
	return nil
}

// MarshalJSON writes a layout and the fields it kept.
func (l Layout) MarshalJSON() ([]byte, error) {
	if l.Spaces == nil {
		l.Spaces = []Space{}
	}
	return keepUnknown(layoutJSON(l), l.extra)
}

// UnmarshalJSON reads a space, keeping the fields it does not know.
func (s *Space) UnmarshalJSON(b []byte) error {
	m, ok := objectFields(b)
	if !ok {
		return errors.New("space: not a JSON object")
	}
	*s = Space{Auto: true, Color: DefaultColor, extra: unknownFrom(m, spaceKeys)}
	if v, ok := jsonString(m["id"]); ok {
		s.ID = v
		s.hasID = true
	}
	if v, ok := jsonString(m["name"]); ok {
		s.Name = v
		s.hasName = true
	}
	if v, ok := jsonString(m["color"]); ok {
		s.Color = v
	}
	if v, ok := jsonString(m["dir"]); ok {
		s.Dir = v
	}
	if v, ok := jsonString(m["activeTab"]); ok {
		s.ActiveTab = v
	}
	if raw, ok := m["auto"]; ok {
		var v bool
		if json.Unmarshal(raw, &v) == nil {
			s.Auto = v
		}
	}
	if raw, ok := m["tabs"]; ok {
		s.Tabs = decodeList(raw, decodeTab)
	}
	if s.Tabs == nil {
		s.Tabs = []Tab{}
	}
	return nil
}

// MarshalJSON writes a space and the fields it kept.
func (s Space) MarshalJSON() ([]byte, error) {
	if s.Tabs == nil {
		s.Tabs = []Tab{}
	}
	return keepUnknown(spaceJSON(s), s.extra)
}

// UnmarshalJSON reads a tab, keeping the fields it does not know.
func (t *Tab) UnmarshalJSON(b []byte) error {
	m, ok := objectFields(b)
	if !ok {
		return errors.New("tab: not a JSON object")
	}
	*t = Tab{extra: unknownFrom(m, tabKeys)}
	if v, ok := jsonString(m["id"]); ok {
		t.ID = v
		t.hasID = true
	}
	if v, ok := jsonString(m["title"]); ok {
		t.Title = v
	}
	if v, ok := jsonString(m["focus"]); ok {
		t.Focus = v
	}
	if v, ok := jsonString(m["zoom"]); ok {
		t.Zoom = v
	}
	if raw, ok := m["root"]; ok {
		var n Node
		if json.Unmarshal(raw, &n) == nil {
			t.Root = n
			t.hasRoot = true
		}
	}
	return nil
}

// MarshalJSON writes a tab and the fields it kept.
func (t Tab) MarshalJSON() ([]byte, error) {
	return keepUnknown(tabJSON(t), t.extra)
}

// UnmarshalJSON reads a node, keeping the fields it does not know.
func (n *Node) UnmarshalJSON(b []byte) error {
	m, ok := objectFields(b)
	if !ok {
		return errors.New("node: not a JSON object")
	}
	*n = Node{extra: unknownFrom(m, nodeKeys)}
	if v, ok := jsonString(m["type"]); ok {
		n.Type = v
	}
	if v, ok := jsonString(m["session"]); ok {
		n.Session = v
		n.hasSession = true
	}
	if v, ok := jsonString(m["dir"]); ok {
		n.Dir = v
	}
	if raw, ok := m["sizes"]; ok {
		var sizes []float64
		if json.Unmarshal(raw, &sizes) == nil {
			n.Sizes = sizes
		}
	}
	if raw, ok := m["children"]; ok {
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err == nil && elems != nil {
			n.hasChildren = true
			n.Children = make([]Node, 0, len(elems))
			for _, e := range elems {
				var c Node
				if json.Unmarshal(e, &c) != nil {
					c = Node{} // not an object: an invalid child
				}
				n.Children = append(n.Children, c)
			}
		}
	}
	return nil
}

// MarshalJSON writes a node and the fields it kept.
func (n Node) MarshalJSON() ([]byte, error) {
	return keepUnknown(nodeJSON(n), n.extra)
}

// valid reports whether a parsed node is well formed, as isNode does in the
// web UI: a pane needs a session, a split a direction and valid children.
func (n *Node) valid() bool {
	switch n.Type {
	case "pane":
		return n.hasSession
	case "split":
		if n.Dir != "row" && n.Dir != "col" {
			return false
		}
		if !n.hasChildren {
			return false
		}
		for i := range n.Children {
			if !n.Children[i].valid() {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// NewID returns a new identifier with the given prefix, "sp_" for a space
// and "tb_" for a tab.
func NewID(prefix string) string {
	var b [6]byte
	if _, err := crand.Read(b[:]); err != nil {
		for i := range b {
			b[i] = byte(mrand.Intn(256))
		}
	}
	return prefix + hex.EncodeToString(b[:])
}

// Empty is a new, empty layout.
func Empty() Layout {
	return Layout{Version: 1, Spaces: []Space{}, Grouping: "space"}
}

// Parse reads a stored layout, dropping anything malformed and filling in
// the defaults, as parseLayout does in the web UI.
func Parse(raw []byte) Layout {
	var parsed Layout
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Empty()
	}
	l := parsed
	l.Version = 1
	if l.Grouping != "status" {
		l.Grouping = "space"
	}
	spaces := make([]Space, 0, len(l.Spaces))
	for _, s := range l.Spaces {
		if !s.hasID || !s.hasName {
			continue
		}
		tabs := make([]Tab, 0, len(s.Tabs))
		for _, t := range s.Tabs {
			if t.hasID && t.hasRoot && t.Root.valid() {
				tabs = append(tabs, t)
			}
		}
		s.Tabs = tabs
		spaces = append(spaces, s)
	}
	l.Spaces = spaces
	switch {
	case l.ActiveSpace != "" && hasSpace(l.Spaces, l.ActiveSpace):
		// kept
	case len(l.Spaces) > 0:
		l.ActiveSpace = l.Spaces[0].ID
	default:
		l.ActiveSpace = ""
	}
	return l
}

// Marshal writes a layout as JSON, keeping the fields a newer web UI added.
func Marshal(l Layout) ([]byte, error) {
	return json.Marshal(l)
}

// PaneSessions lists the sessions of a pane tree, in order.
func PaneSessions(n Node) []string {
	if n.Type == "pane" {
		return []string{n.Session}
	}
	out := []string{}
	for _, c := range n.Children {
		out = append(out, PaneSessions(c)...)
	}
	return out
}

// TabSessions lists the sessions of a tab.
func TabSessions(t Tab) []string {
	return PaneSessions(t.Root)
}

// SpaceSessions lists the sessions of every tab of a space.
func SpaceSessions(s Space) []string {
	out := []string{}
	for _, t := range s.Tabs {
		out = append(out, TabSessions(t)...)
	}
	return out
}

// Locate finds where a session sits in the layout, if anywhere.
func Locate(l Layout, sessionID string) (*Space, *Tab, bool) {
	for i := range l.Spaces {
		s := &l.Spaces[i]
		for j := range s.Tabs {
			if contains(TabSessions(s.Tabs[j]), sessionID) {
				return s, &s.Tabs[j], true
			}
		}
	}
	return nil, nil, false
}

// FolderName is the last part of a path, "~" for a home folder.
func FolderName(path string) string {
	clean := strings.TrimRight(path, "/")
	if clean == "" || homeDir.MatchString(clean+"/") {
		return "~"
	}
	if i := strings.LastIndex(clean, "/"); i >= 0 {
		if last := clean[i+1:]; last != "" {
			return last
		}
		return "/"
	}
	return clean
}

var homeDir = regexp.MustCompile(`^/(?:Users|home)/[^/]+/$`)

// SpaceCwd is the folder of a space's active terminal: its live cwd, else
// the folder it started in. Empty for a space with no terminals.
func SpaceCwd(space *Space, sessions map[string]Session) string {
	tab := ActiveTab(space)
	if tab == nil {
		return ""
	}
	id := tab.Focus
	if id == "" {
		if ss := TabSessions(*tab); len(ss) > 0 {
			id = ss[0]
		}
	}
	if id == "" {
		return ""
	}
	s, ok := sessions[id]
	if !ok {
		return ""
	}
	if s.Cwd != "" {
		return s.Cwd
	}
	return s.Dir
}

// SpaceLabel names a space: the folder of its active terminal while it is
// auto-named, else the name it was given.
func SpaceLabel(space *Space, sessions map[string]Session) string {
	if !space.Auto {
		return space.Name
	}
	if cwd := SpaceCwd(space, sessions); cwd != "" {
		return FolderName(cwd)
	}
	if space.Name != "" {
		return space.Name
	}
	return "~"
}

// IsAgent reports whether a session is a coding agent: its program is a
// known agent or Lana labelled it (REQ-WB-052).
func IsAgent(s Session) bool {
	if s.Agent != "" {
		return true
	}
	program := strings.ToLower(s.Program)
	for _, a := range AgentPrograms {
		if program == a {
			return true
		}
	}
	return s.Labels["lana.agent"] != ""
}

// StatusOf is what a session is doing, for its status dot.
func StatusOf(s *Session) Status {
	if s == nil {
		return StatusIdle
	}
	if s.NeedsYou {
		return StatusBlocked
	}
	if s.State == "exited" {
		return StatusExited
	}
	if s.State == "working" {
		return StatusWorking
	}
	return StatusIdle
}

// contains reports whether list holds v.
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// hasSpace reports whether a layout has a space with the given id.
func hasSpace(spaces []Space, id string) bool {
	for _, s := range spaces {
		if s.ID == id {
			return true
		}
	}
	return false
}

// hasTab reports whether a space has a tab with the given id.
func hasTab(tabs []Tab, id string) bool {
	for _, t := range tabs {
		if t.ID == id {
			return true
		}
	}
	return false
}

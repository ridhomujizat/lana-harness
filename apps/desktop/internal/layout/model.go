package layout

import (
	"math"
	"strings"
)

// ActiveSpace is the space the layout selects, or the first one.
func ActiveSpace(l Layout) *Space {
	for i := range l.Spaces {
		if l.Spaces[i].ID == l.ActiveSpace {
			return &l.Spaces[i]
		}
	}
	if len(l.Spaces) > 0 {
		return &l.Spaces[0]
	}
	return nil
}

// ActiveTab is the tab a space selects, or its first one.
func ActiveTab(s *Space) *Tab {
	if s == nil {
		return nil
	}
	for i := range s.Tabs {
		if s.Tabs[i].ID == s.ActiveTab {
			return &s.Tabs[i]
		}
	}
	if len(s.Tabs) > 0 {
		return &s.Tabs[0]
	}
	return nil
}

// mapSpace returns the layout with fn applied to one space.
func mapSpace(l Layout, id string, fn func(Space) Space) Layout {
	spaces := make([]Space, len(l.Spaces))
	for i := range l.Spaces {
		if l.Spaces[i].ID == id {
			spaces[i] = fn(l.Spaces[i])
		} else {
			spaces[i] = l.Spaces[i]
		}
	}
	l.Spaces = spaces
	return l
}

// mapTab returns the space with fn applied to one tab.
func mapTab(s Space, id string, fn func(Tab) Tab) Space {
	tabs := make([]Tab, len(s.Tabs))
	for i := range s.Tabs {
		if s.Tabs[i].ID == id {
			tabs[i] = fn(s.Tabs[i])
		} else {
			tabs[i] = s.Tabs[i]
		}
	}
	s.Tabs = tabs
	return s
}

// AddSpace appends a space with the next colour and makes it active.
func AddSpace(l Layout, name, dir string) Layout {
	color := DefaultColor
	if len(SpaceColors) > 0 {
		color = SpaceColors[len(l.Spaces)%len(SpaceColors)]
	}
	sp := Space{ID: NewID("sp_"), Name: name, Color: color, Dir: dir, Tabs: []Tab{}, Auto: true}
	l.Spaces = append(append([]Space{}, l.Spaces...), sp)
	l.ActiveSpace = sp.ID
	return l
}

// RemoveSpace drops a space; the first one becomes active when it was.
func RemoveSpace(l Layout, spaceID string) Layout {
	spaces := make([]Space, 0, len(l.Spaces))
	for _, s := range l.Spaces {
		if s.ID != spaceID {
			spaces = append(spaces, s)
		}
	}
	l.Spaces = spaces
	if l.ActiveSpace == spaceID {
		if len(spaces) > 0 {
			l.ActiveSpace = spaces[0].ID
		} else {
			l.ActiveSpace = ""
		}
	}
	return l
}

// RenameSpace renames a space; an empty name turns automatic folder naming
// back on.
func RenameSpace(l Layout, spaceID, name string) Layout {
	clean := strings.TrimSpace(name)
	return mapSpace(l, spaceID, func(s Space) Space {
		if clean != "" {
			s.Name = clean
			s.Auto = false
		} else {
			s.Auto = true
		}
		return s
	})
}

// SetSpaceColor gives a space one of the SpaceColors; a colour that is not
// one of them, or a space that is not there, leaves the layout as it was.
func SetSpaceColor(l Layout, spaceID, color string) Layout {
	if !contains(SpaceColors, color) {
		return l
	}
	return mapSpace(l, spaceID, func(s Space) Space {
		s.Color = color
		return s
	})
}

// RenameTab renames a tab; an empty title goes back to the program's own
// label.
func RenameTab(l Layout, spaceID, tabID, title string) Layout {
	clean := strings.TrimSpace(title)
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			t.Title = clean
			return t
		})
	})
}

// SelectSpace makes a space active.
func SelectSpace(l Layout, spaceID string) Layout {
	l.ActiveSpace = spaceID
	return l
}

// SelectTab makes a tab active.
func SelectTab(l Layout, spaceID, tabID string) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		s.ActiveTab = tabID
		return s
	})
}

// AddTab appends a tab holding one session and makes it active.
func AddTab(l Layout, spaceID, sessionID, title string) Layout {
	tab := Tab{ID: NewID("tb_"), Title: title, Root: Node{Type: "pane", Session: sessionID}, Focus: sessionID}
	l = mapSpace(l, spaceID, func(s Space) Space {
		s.Tabs = append(append([]Tab{}, s.Tabs...), tab)
		s.ActiveTab = tab.ID
		return s
	})
	l.ActiveSpace = spaceID
	return l
}

// SplitSizes are a split's children's shares: its stored fractions when
// they are valid (one positive number per child), else equal shares. Always
// sums to 1.
func SplitSizes(n Node) []float64 {
	if n.Type != "split" {
		return []float64{}
	}
	count := len(n.Children)
	if len(n.Sizes) == count && count > 0 {
		total := 0.0
		valid := true
		for _, v := range n.Sizes {
			if math.IsInf(v, 0) || math.IsNaN(v) || v <= 0 {
				valid = false
				break
			}
			total += v
		}
		if valid && total > 0 {
			out := make([]float64, count)
			for i, v := range n.Sizes {
				out[i] = v / total
			}
			return out
		}
	}
	out := make([]float64, count)
	for i := range out {
		out[i] = 1 / float64(count)
	}
	return out
}

// storedSizes are the sizes to store: nil when they are all equal, so a
// layout without dragged dividers stays as it was before spec 58.
func storedSizes(sizes []float64) []float64 {
	if len(sizes) < 2 {
		return nil
	}
	first := sizes[0]
	for _, v := range sizes {
		if math.Abs(v-first) >= 1e-6 {
			return sizes
		}
	}
	return nil
}

// withSizes returns the split node with new children and sizes, dropping
// the sizes when they are all equal.
func withSizes(n Node, children []Node, sizes []float64) Node {
	n.Children = children
	n.Sizes = storedSizes(sizes)
	return n
}

// splitNode adds a pane next to the target pane. Splitting a pane inside a
// split of the same direction adds a sibling instead of nesting, and the
// split's children become equal shares (any dragged sizes are dropped).
func splitNode(n Node, target, sessionID, dir string) Node {
	if n.Type == "pane" {
		if n.Session != target {
			return n
		}
		return Node{Type: "split", Dir: dir, Children: []Node{n, {Type: "pane", Session: sessionID}}}
	}
	index := -1
	for i, c := range n.Children {
		if c.Type == "pane" && c.Session == target {
			index = i
			break
		}
	}
	if index >= 0 && n.Dir == dir {
		children := make([]Node, 0, len(n.Children)+1)
		children = append(children, n.Children[:index+1]...)
		children = append(children, Node{Type: "pane", Session: sessionID})
		children = append(children, n.Children[index+1:]...)
		return withSizes(n, children, nil)
	}
	children := make([]Node, len(n.Children))
	for i, c := range n.Children {
		children[i] = splitNode(c, target, sessionID, dir)
	}
	n.Children = children
	return n
}

// SplitPane splits the target pane, right of it (dir "row") or below it
// (dir "col"), and gives the new pane the focus.
func SplitPane(l Layout, spaceID, tabID, target, sessionID, dir string) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			t.Root = splitNode(t.Root, target, sessionID, dir)
			t.Focus = sessionID
			t.Zoom = ""
			return t
		})
	})
}

// removeNode drops a session's pane; a split left with one child gives its
// place to it, and when two or more children remain they take equal shares
// (any dragged sizes are dropped). The bool is false when nothing is left.
func removeNode(n Node, sessionID string) (Node, bool) {
	if n.Type == "pane" {
		if n.Session == sessionID {
			return Node{}, false
		}
		return n, true
	}
	children := []Node{}
	for _, c := range n.Children {
		kept, ok := removeNode(c, sessionID)
		if !ok {
			continue
		}
		children = append(children, kept)
	}
	switch {
	case len(children) == 0:
		return Node{}, false
	case len(children) == 1:
		return children[0], true
	case len(children) == len(n.Children):
		n.Children = children
		return n, true
	}
	return withSizes(n, children, nil), true
}

// RemoveSession drops a session from every tab; a tab left empty is
// removed.
func RemoveSession(l Layout, sessionID string) Layout {
	spaces := make([]Space, len(l.Spaces))
	for i := range l.Spaces {
		s := l.Spaces[i]
		tabs := make([]Tab, 0, len(s.Tabs))
		for j := range s.Tabs {
			t := s.Tabs[j]
			root, ok := removeNode(t.Root, sessionID)
			if !ok {
				continue
			}
			sessions := PaneSessions(root)
			t.Root = root
			if t.Focus == "" || !contains(sessions, t.Focus) {
				if len(sessions) > 0 {
					t.Focus = sessions[0]
				} else {
					t.Focus = ""
				}
			}
			if t.Zoom == sessionID {
				t.Zoom = ""
			}
			tabs = append(tabs, t)
		}
		s.Tabs = tabs
		if !hasTab(tabs, s.ActiveTab) {
			if len(tabs) > 0 {
				s.ActiveTab = tabs[len(tabs)-1].ID
			} else {
				s.ActiveTab = ""
			}
		}
		spaces[i] = s
	}
	l.Spaces = spaces
	return l
}

// CloseTab drops a tab; the last one becomes active when it was.
func CloseTab(l Layout, spaceID, tabID string) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		tabs := make([]Tab, 0, len(s.Tabs))
		for _, t := range s.Tabs {
			if t.ID != tabID {
				tabs = append(tabs, t)
			}
		}
		s.Tabs = tabs
		if s.ActiveTab == tabID {
			if len(tabs) > 0 {
				s.ActiveTab = tabs[len(tabs)-1].ID
			} else {
				s.ActiveTab = ""
			}
		}
		return s
	})
}

// FocusPane gives a pane of a tab the focus.
func FocusPane(l Layout, spaceID, tabID, sessionID string) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			t.Focus = sessionID
			return t
		})
	})
}

// ToggleZoom zooms the pane, or unzooms it when it was, and gives it the
// focus.
func ToggleZoom(l Layout, spaceID, tabID, sessionID string) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			if t.Zoom == sessionID {
				t.Zoom = ""
			} else {
				t.Zoom = sessionID
			}
			t.Focus = sessionID
			return t
		})
	})
}

// SetGrouping sets how the Agents panel groups sessions: "space" or
// "status".
func SetGrouping(l Layout, grouping string) Layout {
	l.Grouping = grouping
	return l
}

// MergeRemote takes the structure of a layout another client saved but
// keeps this client's own selection: the active space, each space's active
// tab, and each tab's focus and zoom, where the pane still exists.
func MergeRemote(local, remote Layout) Layout {
	localSpaces := make(map[string]Space, len(local.Spaces))
	for _, s := range local.Spaces {
		localSpaces[s.ID] = s
	}
	spaces := make([]Space, len(remote.Spaces))
	for i := range remote.Spaces {
		space := remote.Spaces[i]
		mine, ok := localSpaces[space.ID]
		if !ok {
			spaces[i] = space
			continue
		}
		myTabs := make(map[string]Tab, len(mine.Tabs))
		for _, t := range mine.Tabs {
			myTabs[t.ID] = t
		}
		tabs := make([]Tab, len(space.Tabs))
		for j := range space.Tabs {
			tab := space.Tabs[j]
			myTab, ok := myTabs[tab.ID]
			if !ok {
				tabs[j] = tab
				continue
			}
			ids := PaneSessions(tab.Root)
			if myTab.Focus != "" && contains(ids, myTab.Focus) {
				tab.Focus = myTab.Focus
			}
			if myTab.Zoom != "" && contains(ids, myTab.Zoom) {
				tab.Zoom = myTab.Zoom
			} else {
				tab.Zoom = ""
			}
			tabs[j] = tab
		}
		if mine.ActiveTab != "" && hasTab(tabs, mine.ActiveTab) {
			space.ActiveTab = mine.ActiveTab
		}
		space.Tabs = tabs
		spaces[i] = space
	}
	remote.Spaces = spaces
	if local.ActiveSpace != "" && hasSpace(spaces, local.ActiveSpace) {
		remote.ActiveSpace = local.ActiveSpace
	}
	if local.Grouping != "" {
		remote.Grouping = local.Grouping
	}
	return remote
}

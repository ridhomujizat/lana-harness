package layout

// MoveTab moves a tab to another place among the tabs of its space
// (spec 58 REQ-DW-034), where to is the index it takes in the list. An
// index outside the list clamps to it, and a tab that is where it should
// be leaves the layout as it is.
func MoveTab(l Layout, spaceID, tabID string, to int) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		from := -1
		for i := range s.Tabs {
			if s.Tabs[i].ID == tabID {
				from = i
				break
			}
		}
		if from < 0 {
			return s
		}
		rest := make([]Tab, 0, len(s.Tabs)-1)
		rest = append(rest, s.Tabs[:from]...)
		rest = append(rest, s.Tabs[from+1:]...)
		to = min(max(to, 0), len(rest))
		if to == from {
			return s
		}
		tabs := make([]Tab, 0, len(s.Tabs))
		tabs = append(tabs, rest[:to]...)
		tabs = append(tabs, s.Tabs[from])
		tabs = append(tabs, rest[to:]...)
		s.Tabs = tabs
		return s
	})
}

// MoveTabToSpace moves a tab, and its panes, into another space, which
// becomes active (spec 58 REQ-DW-034). Moving a tab into its own space
// leaves the layout as it is.
func MoveTabToSpace(l Layout, fromSpace, tabID, toSpace string) Layout {
	if fromSpace == toSpace {
		return l
	}
	moved, found := Tab{}, false
	l = mapSpace(l, fromSpace, func(s Space) Space {
		tabs := make([]Tab, 0, len(s.Tabs))
		for i := range s.Tabs {
			if s.Tabs[i].ID == tabID && !found {
				moved, found = s.Tabs[i], true
				continue
			}
			tabs = append(tabs, s.Tabs[i])
		}
		if !found {
			return s
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
	if !found {
		return l
	}
	l = mapSpace(l, toSpace, func(s Space) Space {
		s.Tabs = append(append([]Tab{}, s.Tabs...), moved)
		s.ActiveTab = moved.ID
		return s
	})
	l.ActiveSpace = toSpace
	return l
}

package layout

import "math"

// Rect is a pane's place in its tab.
type Rect struct {
	X, Y, W, H float64
}

// PaneRect is a pane's session and its rectangle.
type PaneRect struct {
	Session string
	Rect
}

// Rects lays out the panes of a tab in the box (x, y, w, h), with gap
// pixels between neighbours, as the view does. A split divides its room
// between its children by their shares (SplitSizes).
func Rects(root Node, x, y, w, h, gap float64) []PaneRect {
	out := []PaneRect{}
	var walk func(n Node, x, y, w, h float64)
	walk = func(n Node, x, y, w, h float64) {
		if n.Type != "split" || len(n.Children) == 0 {
			out = append(out, PaneRect{Session: n.Session, Rect: Rect{x, y, w, h}})
			return
		}
		sizes := SplitSizes(n)
		space := gap * float64(len(n.Children)-1)
		if n.Dir == "col" {
			total := math.Max(h-space, 0)
			cy := y
			for i, c := range n.Children {
				ch := total * sizes[i]
				walk(c, x, cy, w, ch)
				cy += ch + gap
			}
			return
		}
		total := math.Max(w-space, 0)
		cx := x
		for i, c := range n.Children {
			cw := total * sizes[i]
			walk(c, cx, y, cw, h)
			cx += cw + gap
		}
	}
	walk(root, x, y, w, h)
	return out
}

// Neighbor returns the session of the pane in the direction (dx, dy) from
// the focused one: the nearest, then the one most in line with it, as
// GoRex's focus moves do. The bool is false when there is none.
func Neighbor(rects []PaneRect, from string, dx, dy int) (string, bool) {
	src, ok := rectOf(rects, from)
	if !ok {
		return "", false
	}
	cx, cy := src.X+src.W/2, src.Y+src.H/2
	best := ""
	bestScore := math.Inf(1)
	for _, pr := range rects {
		if pr.Session == from {
			continue
		}
		r := pr.Rect
		var dist, overlap float64
		switch {
		case dx > 0:
			dist = r.X - (src.X + src.W)
			overlap = math.Min(r.Y+r.H, src.Y+src.H) - math.Max(r.Y, src.Y)
		case dx < 0:
			dist = src.X - (r.X + r.W)
			overlap = math.Min(r.Y+r.H, src.Y+src.H) - math.Max(r.Y, src.Y)
		case dy > 0:
			dist = r.Y - (src.Y + src.H)
			overlap = math.Min(r.X+r.W, src.X+src.W) - math.Max(r.X, src.X)
		default:
			dist = src.Y - (r.Y + r.H)
			overlap = math.Min(r.X+r.W, src.X+src.W) - math.Max(r.X, src.X)
		}
		if dist < -4 || overlap <= 0 {
			continue
		}
		px, py := r.X+r.W/2, r.Y+r.H/2
		off := px - cx
		if dx != 0 {
			off = py - cy
		}
		score := dist*4 + math.Abs(off)
		if score < bestScore {
			best, bestScore = pr.Session, score
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// rectOf finds a session's rectangle.
func rectOf(rects []PaneRect, session string) (Rect, bool) {
	for _, pr := range rects {
		if pr.Session == session {
			return pr.Rect, true
		}
	}
	return Rect{}, false
}

// SetSizes stores a dragged divider (spec 58 REQ-DW-022). path holds the
// child indexes from the tab root to the split; sizes hold one fraction per
// child of that split. A path that does not reach a split, or a count that
// does not match the children, leaves the layout unchanged.
func SetSizes(l Layout, spaceID, tabID string, path []int, sizes []float64) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			if root, ok := setSizesNode(t.Root, path, sizes); ok {
				t.Root = root
			}
			return t
		})
	})
}

func setSizesNode(n Node, path []int, sizes []float64) (Node, bool) {
	if len(path) == 0 {
		if n.Type != "split" || len(n.Children) != len(sizes) {
			return n, false
		}
		return withSizes(n, n.Children, sizes), true
	}
	i := path[0]
	if n.Type != "split" || i < 0 || i >= len(n.Children) {
		return n, false
	}
	kid, ok := setSizesNode(n.Children[i], path[1:], sizes)
	if !ok {
		return n, false
	}
	children := make([]Node, len(n.Children))
	copy(children, n.Children)
	children[i] = kid
	n.Children = children
	return n, true
}

// Equalize drops the sizes of every split of a tab, so its panes go back to
// equal shares.
func Equalize(l Layout, spaceID, tabID string) Layout {
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			t.Root = dropSizes(t.Root)
			return t
		})
	})
}

func dropSizes(n Node) Node {
	if n.Type != "split" {
		return n
	}
	n.Sizes = nil
	children := make([]Node, len(n.Children))
	for i, c := range n.Children {
		children[i] = dropSizes(c)
	}
	n.Children = children
	return n
}

// NudgeDivider moves the nearest divider of the focused pane one step in
// the direction (dx, dy), keeping every share at least 0.1, as GoRex's
// divider move does.
func NudgeDivider(l Layout, spaceID, tabID, sessionID string, dx, dy int, step float64) Layout {
	if (dx == 0 && dy == 0) || step <= 0 {
		return l
	}
	axis := "row"
	positive := dx > 0
	if dy != 0 {
		axis = "col"
		positive = dy > 0
	}
	return mapSpace(l, spaceID, func(s Space) Space {
		return mapTab(s, tabID, func(t Tab) Tab {
			if root, ok := nudgeNode(t.Root, sessionID, axis, positive, step); ok {
				t.Root = root
			}
			return t
		})
	})
}

// nudgeNode adjusts the divider of the split nearest the session on the
// axis: the deepest split on the pane's path first, then its ancestors.
func nudgeNode(n Node, sessionID, axis string, positive bool, step float64) (Node, bool) {
	if n.Type != "split" {
		return n, false
	}
	index := -1
	for i, c := range n.Children {
		if contains(PaneSessions(c), sessionID) {
			index = i
			break
		}
	}
	if index < 0 {
		return n, false
	}
	if kid, ok := nudgeNode(n.Children[index], sessionID, axis, positive, step); ok {
		children := make([]Node, len(n.Children))
		copy(children, n.Children)
		children[index] = kid
		n.Children = children
		return n, true
	}
	if n.Dir == axis {
		return adjustSplit(n, index, positive, step)
	}
	return n, false
}

// adjustSplit moves the divider beside the child at index, in the direction
// asked for, by step, without letting either share drop below 0.1.
func adjustSplit(n Node, index int, positive bool, step float64) (Node, bool) {
	count := len(n.Children)
	if count < 2 {
		return n, false
	}
	j := 0
	switch {
	case positive && index+1 < count:
		j = index
	case positive:
		j = index - 1
	case index-1 >= 0:
		j = index - 1
	default:
		j = index
	}
	if j < 0 || j+1 >= count {
		return n, false
	}
	sizes := SplitSizes(n)
	a, b := sizes[j], sizes[j+1]
	delta := step
	if !positive {
		delta = -step
	}
	if delta > 0 {
		room := b - 0.1
		if room <= 0 {
			return n, false
		}
		if delta > room {
			delta = room
		}
	} else {
		room := a - 0.1
		if room <= 0 {
			return n, false
		}
		if -delta > room {
			delta = -room
		}
	}
	if delta == 0 {
		return n, false
	}
	next := make([]float64, len(sizes))
	copy(next, sizes)
	next[j], next[j+1] = a+delta, b-delta
	return withSizes(n, n.Children, next), true
}

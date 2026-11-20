package layout

import "testing"

// tabIDs lists the tabs of a space, for comparing orders.
func tabIDs(l Layout, spaceID string) []string {
	for i := range l.Spaces {
		if l.Spaces[i].ID == spaceID {
			out := []string{}
			for _, t := range l.Spaces[i].Tabs {
				out = append(out, t.ID)
			}
			return out
		}
	}
	return nil
}

func TestMoveTabReorders(t *testing.T) {
	l := AddSpace(Empty(), "home", "")
	sp := l.Spaces[0]
	for _, id := range []string{"a", "b", "c", "d"} {
		l = AddTab(l, sp.ID, id, "")
	}
	ids := tabIDs(l, sp.ID)
	l = MoveTab(l, sp.ID, ids[0], 2)
	equal(t, "after moving the first to index 2", tabIDs(l, sp.ID), []string{ids[1], ids[2], ids[0], ids[3]})
	l = MoveTab(l, sp.ID, ids[3], 0)
	equal(t, "after moving the last to the front", tabIDs(l, sp.ID), []string{ids[3], ids[1], ids[2], ids[0]})
	l = MoveTab(l, sp.ID, ids[1], 99)
	equal(t, "an index past the end clamps", tabIDs(l, sp.ID), []string{ids[3], ids[2], ids[0], ids[1]})
	l = MoveTab(l, sp.ID, "gone", 0)
	equal(t, "a tab that is not there changes nothing", tabIDs(l, sp.ID), []string{ids[3], ids[2], ids[0], ids[1]})
}

func TestMoveTabToSpace(t *testing.T) {
	l := AddSpace(Empty(), "one", "")
	one := l.Spaces[0].ID
	l = AddTab(l, one, "a", "")
	l = AddTab(l, one, "b", "")
	l = AddSpace(l, "two", "")
	two := l.Spaces[1].ID
	ids := tabIDs(l, one)
	moved, kept := ids[0], ids[1]

	l = MoveTabToSpace(l, one, moved, two)
	equal(t, "the source keeps the other tab", tabIDs(l, one), []string{kept})
	equal(t, "the target gained the tab", tabIDs(l, two), []string{moved})
	if l.ActiveSpace != two {
		t.Fatalf("activeSpace = %q, want %q", l.ActiveSpace, two)
	}
	equal(t, "the moved tab is active", l.Spaces[1].ActiveTab, moved)

	// A tab moved into its own space, or one that is not there, changes
	// nothing.
	l = MoveTabToSpace(l, one, kept, one)
	equal(t, "moving into the same space", tabIDs(l, one), []string{kept})
	l = MoveTabToSpace(l, one, "gone", two)
	equal(t, "moving a tab that is not there", tabIDs(l, two), []string{moved})
}

package layout

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// parse is Parse for a test, failing on a nil layout.
func parse(t *testing.T, raw string) Layout {
	t.Helper()
	return Parse([]byte(raw))
}

func equal[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

func closeTo(t *testing.T, what string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func TestAddSpaceTabsAndSplits(t *testing.T) {
	l := AddSpace(Empty(), "sxored-hub", "~/sxh")
	sp := l.Spaces[0]
	if l.ActiveSpace != sp.ID {
		t.Fatalf("activeSpace = %q, want %q", l.ActiveSpace, sp.ID)
	}
	l = AddTab(l, sp.ID, "a", "")
	tab := l.Spaces[0].Tabs[0]
	l = SplitPane(l, sp.ID, tab.ID, "a", "b", "row")
	l = SplitPane(l, sp.ID, tab.ID, "b", "c", "row")
	root := l.Spaces[0].Tabs[0].Root
	if root.Type != "split" {
		t.Fatalf("root type = %q, want split", root.Type)
	}
	equal(t, "tabSessions", TabSessions(l.Spaces[0].Tabs[0]), []string{"a", "b", "c"})
	for _, c := range root.Children {
		if c.Type != "pane" {
			t.Fatalf("same-direction split nested a %q instead of a sibling", c.Type)
		}
	}
	l = SplitPane(l, sp.ID, tab.ID, "c", "d", "col")
	equal(t, "tabSessions after col split", TabSessions(l.Spaces[0].Tabs[0]), []string{"a", "b", "c", "d"})
	if _, got, ok := Locate(l, "d"); !ok || got.ID != tab.ID {
		t.Fatalf("locate(d) = %v, %v, want tab %q", got, ok, tab.ID)
	}
}

func TestRemoveSessionsAndEmptyTabs(t *testing.T) {
	l := AddSpace(Empty(), "home", "")
	sp := l.Spaces[0]
	l = AddTab(l, sp.ID, "a", "")
	l = AddTab(l, sp.ID, "b", "")
	t1 := l.Spaces[0].Tabs[0]
	l = SplitPane(l, sp.ID, t1.ID, "a", "c", "col")
	l = RemoveSession(l, "a")
	equal(t, "root after removing a", l.Spaces[0].Tabs[0].Root, Node{Type: "pane", Session: "c"})
	equal(t, "focus after removing a", l.Spaces[0].Tabs[0].Focus, "c")
	l = RemoveSession(l, "b")
	if got := len(l.Spaces[0].Tabs); got != 1 {
		t.Fatalf("tabs = %d, want 1", got)
	}
	l = CloseTab(l, sp.ID, t1.ID)
	if got := len(l.Spaces[0].Tabs); got != 0 {
		t.Fatalf("tabs after close = %d, want 0", got)
	}
}

func TestMergeRemoteKeepsLocalSelection(t *testing.T) {
	local := AddSpace(Empty(), "a", "")
	sp := local.Spaces[0]
	local = AddTab(local, sp.ID, "s1", "")
	local = AddTab(local, sp.ID, "s2", "")
	firstTab := local.Spaces[0].Tabs[0].ID
	local = SelectTab(local, sp.ID, firstTab)
	remote := AddTab(local, sp.ID, "s3", "")
	merged := MergeRemote(local, remote)
	if got := len(merged.Spaces[0].Tabs); got != 3 {
		t.Fatalf("merged tabs = %d, want 3", got)
	}
	equal(t, "merged activeTab", merged.Spaces[0].ActiveTab, firstTab)
}

func TestParseDropsMalformedParts(t *testing.T) {
	l := parse(t, `{
		"version": 1,
		"grouping": "status",
		"activeSpace": "nope",
		"spaces": [
			{"id": "sp1", "name": "a", "color": "#000", "dir": "/x", "tabs": [
				{"id": "t1", "root": {"type": "pane", "session": "s"}},
				{"id": "bad", "root": {"type": "x"}}
			]},
			{"name": "missing id"}
		]
	}`)
	equal(t, "grouping", l.Grouping, "status")
	if got := len(l.Spaces); got != 1 {
		t.Fatalf("spaces = %d, want 1", got)
	}
	if got := len(l.Spaces[0].Tabs); got != 1 {
		t.Fatalf("tabs = %d, want 1", got)
	}
	equal(t, "activeSpace", l.ActiveSpace, "sp1")
	equal(t, "parse(nil).spaces", Parse(nil).Spaces, []Space{})
	equal(t, "parse(null).spaces", parse(t, "null").Spaces, []Space{})
}

func TestParseGroupingDefaultsToSpace(t *testing.T) {
	equal(t, "grouping", parse(t, `{"version":1,"spaces":[]}`).Grouping, "space")
	equal(t, "empty grouping", Empty().Grouping, "space")
	equal(t, "empty version", Empty().Version, 1)
}

// tabWith parses a layout holding one tab whose root is raw.
func tabWith(t *testing.T, root string) (Layout, Node) {
	t.Helper()
	l := parse(t, `{"version":1,"spaces":[{"id":"sp","name":"a","tabs":[{"id":"t","root":`+root+`}]}]}`)
	return l, l.Spaces[0].Tabs[0].Root
}

func TestSplitSizesStored(t *testing.T) {
	_, root := tabWith(t, `{"type":"split","dir":"row","sizes":[0.25,0.75],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]}`)
	if root.Type != "split" {
		t.Fatalf("root type = %q", root.Type)
	}
	closeTo(t, "stored sizes", root.Sizes, []float64{0.25, 0.75})
	closeTo(t, "splitSizes", SplitSizes(root), []float64{0.25, 0.75})
}

func TestSplitSizesFallBackToEqual(t *testing.T) {
	children := `[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]`
	cases := []struct {
		name string
		root string
		want []float64
	}{
		{"missing", `{"type":"split","dir":"row","children":` + children + `}`, []float64{0.5, 0.5}},
		{"wrong length", `{"type":"split","dir":"row","children":` + children + `,"sizes":[1]}`, []float64{0.5, 0.5}},
		{"zero", `{"type":"split","dir":"row","children":` + children + `,"sizes":[0,1]}`, []float64{0.5, 0.5}},
		{"proportional", `{"type":"split","dir":"row","children":` + children + `,"sizes":[1,3]}`, []float64{0.25, 0.75}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, root := tabWith(t, c.root)
			closeTo(t, "splitSizes", SplitSizes(root), c.want)
		})
	}
}

// TestSplitGivesEverySiblingAnEqualShare is spec 58, workbench: splitting a
// pane inside a row of the same direction adds a sibling, and the row's
// children become equal shares, dropping any dragged sizes.
func TestSplitGivesEverySiblingAnEqualShare(t *testing.T) {
	l, _ := tabWith(t, `{"type":"split","dir":"row","sizes":[0.7,0.3],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]}`)
	next := SplitPane(l, "sp", "t", "b", "c", "row")
	tab := next.Spaces[0].Tabs[0]
	equal(t, "tabSessions", TabSessions(tab), []string{"a", "b", "c"})
	if tab.Root.Sizes != nil {
		t.Fatalf("the split stored sizes %v, want none for equal shares", tab.Root.Sizes)
	}
	closeTo(t, "splitSizes", SplitSizes(tab.Root), []float64{1.0 / 3, 1.0 / 3, 1.0 / 3})
}

func TestEqualSplitStaysWithoutStoredSizes(t *testing.T) {
	l := AddSpace(Empty(), "a", "")
	sp := l.Spaces[0]
	l = AddTab(l, sp.ID, "a", "")
	t1 := l.Spaces[0].Tabs[0]
	l = SplitPane(l, sp.ID, t1.ID, "a", "b", "row")
	l = SplitPane(l, sp.ID, t1.ID, "a", "c", "row")
	root := l.Spaces[0].Tabs[0].Root
	// The three siblings share the room equally, so nothing is stored.
	if root.Sizes != nil {
		t.Fatalf("stored sizes = %v, want none", root.Sizes)
	}
	closeTo(t, "splitSizes", SplitSizes(root), []float64{1.0 / 3, 1.0 / 3, 1.0 / 3})

	before, _ := tabWith(t, `{"type":"split","dir":"row","children":[{"type":"pane","session":"x"},{"type":"pane","session":"y"}]}`)
	after := RemoveSession(SplitPane(before, "sp", "t", "x", "z", "col"), "z")
	equal(t, "root after split then remove", after.Spaces[0].Tabs[0].Root, before.Spaces[0].Tabs[0].Root)
}

// TestClosedPaneRoomSharedEqually is spec 58, workbench: closing one of three
// panes leaves the others equal shares, dropping the dragged sizes.
func TestClosedPaneRoomSharedEqually(t *testing.T) {
	l, _ := tabWith(t, `{"type":"split","dir":"row","sizes":[0.5,0.25,0.25],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"},{"type":"pane","session":"c"}]}`)
	root := RemoveSession(l, "c").Spaces[0].Tabs[0].Root
	if root.Sizes != nil {
		t.Fatalf("the split stored sizes %v, want none for equal shares", root.Sizes)
	}
	closeTo(t, "splitSizes", SplitSizes(root), []float64{0.5, 0.5})
}

func TestRenameSpaceAndTab(t *testing.T) {
	l := AddSpace(Empty(), "~", "")
	sp := l.Spaces[0]
	l = AddTab(l, sp.ID, "s1", "")
	tab := l.Spaces[0].Tabs[0]

	l = RenameSpace(l, sp.ID, " infra ")
	equal(t, "renamed space", l.Spaces[0].Name, "infra")
	equal(t, "renamed space auto", l.Spaces[0].Auto, false)
	l = RenameSpace(l, sp.ID, "")
	equal(t, "space auto after empty rename", l.Spaces[0].Auto, true)
	l = RenameTab(l, sp.ID, tab.ID, "dev server")
	equal(t, "renamed tab", l.Spaces[0].Tabs[0].Title, "dev server")
	l = RenameTab(l, sp.ID, tab.ID, "  ")
	equal(t, "tab title after empty rename", l.Spaces[0].Tabs[0].Title, "")
}

// TestSetSpaceColor is spec 58, spaces: the rename dialog's colour row
// stores one of the SpaceColors on its space, and leaves a colour that is
// not one of them, or a space that is not there, alone.
func TestSetSpaceColor(t *testing.T) {
	l := AddSpace(Empty(), "work", "/tmp/work")
	sp := l.Spaces[0]
	if sp.Color != SpaceColors[0] {
		t.Fatalf("a new space's colour = %q, want %q", sp.Color, SpaceColors[0])
	}

	const pick = "#5B7BE0"
	l = SetSpaceColor(l, sp.ID, pick)
	equal(t, "picked colour", l.Spaces[0].Color, pick)

	// The helper is pure: the layout it was given keeps its colour.
	before := AddSpace(Empty(), "work", "")
	before = SetSpaceColor(before, before.Spaces[0].ID, pick)
	kept := before
	same := SetSpaceColor(before, before.Spaces[0].ID, "#123456")
	equal(t, "an unknown colour changes nothing", same.Spaces[0].Color, kept.Spaces[0].Color)
	equal(t, "an unknown space changes nothing", SetSpaceColor(l, "sp_missing", pick).Spaces[0].Color, pick)
}

func TestFolderName(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/Users/me", "~"},
		{"/home/me/", "~"},
		{"/Users/me/coding/lana-harness", "lana-harness"},
		{"", "~"},
		{"/", "~"},
		{"foo", "foo"},
	}
	for _, c := range cases {
		equal(t, "folderName("+c.path+")", FolderName(c.path), c.want)
	}
}

func TestHerdrStyleSpaceNames(t *testing.T) {
	l := AddSpace(Empty(), "~", "")
	sp := l.Spaces[0]
	equal(t, "empty space label", SpaceLabel(&sp, nil), "~")

	l = AddTab(l, sp.ID, "s1", "")
	sessions := map[string]Session{"s1": {Dir: "/Users/me", Cwd: "/Users/me/sxh/sxored"}}
	equal(t, "auto label from cwd", SpaceLabel(&l.Spaces[0], sessions), "sxored")

	named := l.Spaces[0]
	named.Auto = false
	named.Name = "infra"
	equal(t, "named space label", SpaceLabel(&named, sessions), "infra")
}

func TestIsAgentAndStatusOf(t *testing.T) {
	if !IsAgent(Session{Program: "claude"}) {
		t.Fatal("claude should be an agent")
	}
	if !IsAgent(Session{Program: "zsh", Labels: map[string]string{"lana.agent": "scored-hub"}}) {
		t.Fatal("a labelled session should be an agent")
	}
	if IsAgent(Session{Program: "lazygit"}) {
		t.Fatal("lazygit should not be an agent")
	}

	equal(t, "needs you is blocked", StatusOf(&Session{NeedsYou: true, State: "working"}), StatusBlocked)
	equal(t, "exited", StatusOf(&Session{State: "exited"}), StatusExited)
	equal(t, "working", StatusOf(&Session{State: "working"}), StatusWorking)
	equal(t, "nil is idle", StatusOf(nil), StatusIdle)
}

func TestNewID(t *testing.T) {
	id := NewID("sp_")
	if !strings.HasPrefix(id, "sp_") {
		t.Fatalf("id = %q, want the sp_ prefix", id)
	}
	if got := len(id); got != 3+12 {
		t.Fatalf("id length = %d, want 15", got)
	}
	if NewID("sp_") == NewID("sp_") {
		t.Fatal("two ids are the same")
	}
}

func TestParseKeepsUnknownFields(t *testing.T) {
	raw := `{
		"version": 1,
		"grouping": "space",
		"activeSpace": "sp",
		"topUnknown": {"a": 1},
		"spaces": [{
			"id": "sp", "name": "a", "color": "#000", "dir": "/x", "auto": false,
			"spaceUnknown": [1, 2],
			"tabs": [{
				"id": "t", "title": "T",
				"tabUnknown": true,
				"root": {
					"type": "split", "dir": "row", "sizes": [0.3, 0.7],
					"splitUnknown": "keep me",
					"children": [
						{"type": "pane", "session": "a", "paneUnknown": 7},
						{"type": "pane", "session": "b"}
					]
				}
			}]
		}]
	}`
	out, err := Marshal(parse(t, raw))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if _, ok := top["topUnknown"]; !ok {
		t.Fatal("top-level unknown field was dropped")
	}
	space := element(t, top["spaces"], 0)
	if _, ok := space["spaceUnknown"]; !ok {
		t.Fatal("space unknown field was dropped")
	}
	tab := element(t, space["tabs"], 0)
	if _, ok := tab["tabUnknown"]; !ok {
		t.Fatal("tab unknown field was dropped")
	}
	root := object(t, tab["root"])
	if _, ok := root["splitUnknown"]; !ok {
		t.Fatal("split unknown field was dropped")
	}
	child := element(t, root["children"], 0)
	if _, ok := child["paneUnknown"]; !ok {
		t.Fatal("pane unknown field was dropped")
	}
	// The known fields survive too.
	closeTo(t, "sizes round-trip", decodeSizes(t, root["sizes"]), []float64{0.3, 0.7})
}

func TestParseKeepsUnknownFieldsOnEmptyObjects(t *testing.T) {
	out, err := Marshal(parse(t, `{"version":1,"spaces":[],"note":"hi"}`))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"note":"hi"`) {
		t.Fatalf("marshal dropped the unknown field: %s", out)
	}
	if !strings.Contains(string(out), `"spaces":[]`) {
		t.Fatalf("marshal of empty spaces = %s, want []", out)
	}
}

// --- helpers for reading the marshalled output ---

func object(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %v", err)
	}
	return m
}

func element(t *testing.T, raw json.RawMessage, i int) map[string]json.RawMessage {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("not an array: %v", err)
	}
	if i >= len(list) {
		t.Fatalf("array has %d elements, want index %d", len(list), i)
	}
	return object(t, list[i])
}

func decodeSizes(t *testing.T, raw json.RawMessage) []float64 {
	t.Helper()
	var sizes []float64
	if err := json.Unmarshal(raw, &sizes); err != nil {
		t.Fatalf("sizes: %v", err)
	}
	return sizes
}

// TestHelpersDoNotMutateInput checks that a helper leaves its input layout
// byte-for-byte the same, whether it changed anything or not.
func TestHelpersDoNotMutateInput(t *testing.T) {
	src := `{"version":1,"grouping":"space","activeSpace":"sp","spaces":[{"id":"sp","name":"a","color":"#000","dir":"/x","auto":false,"tabs":[{"id":"t","title":"T","focus":"a","root":{"type":"split","dir":"row","sizes":[0.3,0.7],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]}}]}]}`

	run := func(name string, fn func(Layout) Layout) {
		t.Run(name, func(t *testing.T) {
			l := parse(t, src)
			before, err := Marshal(l)
			if err != nil {
				t.Fatalf("marshal before: %v", err)
			}
			_ = fn(l)
			after, err := Marshal(l)
			if err != nil {
				t.Fatalf("marshal after: %v", err)
			}
			equal(t, "input after "+name, string(after), string(before))
		})
	}

	run("AddSpace", func(l Layout) Layout { return AddSpace(l, "b", "") })
	run("RemoveSpace", func(l Layout) Layout { return RemoveSpace(l, "sp") })
	run("RenameSpace", func(l Layout) Layout { return RenameSpace(l, "sp", "renamed") })
	run("RenameTab", func(l Layout) Layout { return RenameTab(l, "sp", "t", "renamed") })
	run("SelectTab", func(l Layout) Layout { return SelectTab(l, "sp", "other") })
	run("AddTab", func(l Layout) Layout { return AddTab(l, "sp", "c", "") })
	run("SplitPane", func(l Layout) Layout { return SplitPane(l, "sp", "t", "a", "c", "row") })
	run("RemoveSession", func(l Layout) Layout { return RemoveSession(l, "a") })
	run("CloseTab", func(l Layout) Layout { return CloseTab(l, "sp", "t") })
	run("FocusPane", func(l Layout) Layout { return FocusPane(l, "sp", "t", "b") })
	run("ToggleZoom", func(l Layout) Layout { return ToggleZoom(l, "sp", "t", "a") })
	run("SetGrouping", func(l Layout) Layout { return SetGrouping(l, "status") })
	run("MergeRemote", func(l Layout) Layout { return MergeRemote(l, l) })
	run("SetSizes", func(l Layout) Layout { return SetSizes(l, "sp", "t", nil, []float64{0.5, 0.5}) })
	run("Equalize", func(l Layout) Layout { return Equalize(l, "sp", "t") })
	run("NudgeDivider", func(l Layout) Layout { return NudgeDivider(l, "sp", "t", "a", 1, 0, 0.05) })
}

// TestSetSizesChangesOnlyTheTargetSplit checks SetSizes stores a dragged
// divider at the split the path names.
func TestSetSizesChangesOnlyTheTargetSplit(t *testing.T) {
	l := parse(t, `{"version":1,"spaces":[{"id":"sp","name":"a","tabs":[{"id":"t","root":{"type":"split","dir":"row","sizes":[0.5,0.5],"children":[{"type":"split","dir":"col","sizes":[0.5,0.5],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]},{"type":"pane","session":"c"}]}}]}]}`)

	// The root split (path nil).
	next := SetSizes(l, "sp", "t", nil, []float64{0.25, 0.75})
	closeTo(t, "root sizes", SplitSizes(next.Spaces[0].Tabs[0].Root), []float64{0.25, 0.75})

	// The nested col split (path [0]).
	next = SetSizes(l, "sp", "t", []int{0}, []float64{0.2, 0.8})
	inner := next.Spaces[0].Tabs[0].Root.Children[0]
	closeTo(t, "inner sizes", SplitSizes(inner), []float64{0.2, 0.8})
	closeTo(t, "outer sizes untouched", SplitSizes(next.Spaces[0].Tabs[0].Root), []float64{0.5, 0.5})

	// A bad path or count leaves the layout alone.
	equal(t, "bad path", SetSizes(l, "sp", "t", []int{5}, []float64{0.5, 0.5}), l)
	equal(t, "bad count", SetSizes(l, "sp", "t", []int{0}, []float64{0.5}), l)
}

// TestEqualizeDropsSizes checks Equalize clears the sizes of every split.
func TestEqualizeDropsSizes(t *testing.T) {
	l := parse(t, `{"version":1,"spaces":[{"id":"sp","name":"a","tabs":[{"id":"t","root":{"type":"split","dir":"row","sizes":[0.25,0.75],"children":[{"type":"split","dir":"col","sizes":[0.3,0.7],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]},{"type":"pane","session":"c"}]}}]}]}`)
	got := Equalize(l, "sp", "t").Spaces[0].Tabs[0].Root
	if got.Sizes != nil {
		t.Fatalf("root sizes = %v, want none", got.Sizes)
	}
	if got.Children[0].Sizes != nil {
		t.Fatalf("inner sizes = %v, want none", got.Children[0].Sizes)
	}
	closeTo(t, "root splitSizes", SplitSizes(got), []float64{0.5, 0.5})
	closeTo(t, "inner splitSizes", SplitSizes(got.Children[0]), []float64{0.5, 0.5})
}

func TestRects(t *testing.T) {
	root := Node{Type: "split", Dir: "row", Children: []Node{
		{Type: "pane", Session: "a"},
		{Type: "pane", Session: "b"},
	}}
	got := Rects(root, 0, 0, 100, 40, 0)
	equal(t, "rects without gap", got, []PaneRect{
		{Session: "a", Rect: Rect{X: 0, Y: 0, W: 50, H: 40}},
		{Session: "b", Rect: Rect{X: 50, Y: 0, W: 50, H: 40}},
	})
	got = Rects(root, 0, 0, 100, 40, 8)
	equal(t, "rects with a gap", got, []PaneRect{
		{Session: "a", Rect: Rect{X: 0, Y: 0, W: 46, H: 40}},
		{Session: "b", Rect: Rect{X: 54, Y: 0, W: 46, H: 40}},
	})

	col := Node{Type: "split", Dir: "col", Sizes: []float64{0.25, 0.75}, Children: []Node{
		{Type: "pane", Session: "a"},
		{Type: "pane", Session: "b"},
	}}
	got = Rects(col, 0, 0, 40, 100, 0)
	equal(t, "col rects with sizes", got, []PaneRect{
		{Session: "a", Rect: Rect{X: 0, Y: 0, W: 40, H: 25}},
		{Session: "b", Rect: Rect{X: 0, Y: 25, W: 40, H: 75}},
	})
}

func TestNeighbor(t *testing.T) {
	rects := Rects(Node{Type: "split", Dir: "row", Children: []Node{
		{Type: "pane", Session: "a"},
		{Type: "pane", Session: "b"},
	}}, 0, 0, 100, 40, 8)

	if got, ok := Neighbor(rects, "a", 1, 0); !ok || got != "b" {
		t.Fatalf("right of a = %q, %v, want b", got, ok)
	}
	if got, ok := Neighbor(rects, "b", -1, 0); !ok || got != "a" {
		t.Fatalf("left of b = %q, %v, want a", got, ok)
	}
	if _, ok := Neighbor(rects, "a", -1, 0); ok {
		t.Fatal("there is no pane left of a")
	}
	if _, ok := Neighbor(rects, "a", 0, 1); ok {
		t.Fatal("there is no pane below a")
	}
}

func TestNeighborPicksThePaneInLine(t *testing.T) {
	// A column of two panes, then a third beside the top one: from the
	// bottom pane, moving right reaches the third, not the top.
	rects := []PaneRect{
		{Session: "top", Rect: Rect{X: 0, Y: 0, W: 100, H: 50}},
		{Session: "bottom", Rect: Rect{X: 0, Y: 50, W: 100, H: 50}},
		{Session: "side", Rect: Rect{X: 100, Y: 0, W: 100, H: 100}},
	}
	if got, ok := Neighbor(rects, "bottom", 1, 0); !ok || got != "side" {
		t.Fatalf("right of bottom = %q, %v, want side", got, ok)
	}
	if got, ok := Neighbor(rects, "top", 0, 1); !ok || got != "bottom" {
		t.Fatalf("below top = %q, %v, want bottom", got, ok)
	}
}

func TestNudgeDivider(t *testing.T) {
	l := parse(t, `{"version":1,"spaces":[{"id":"sp","name":"a","tabs":[{"id":"t","root":{"type":"split","dir":"row","sizes":[0.5,0.5],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]}}]}]}`)

	// From a, to the right: a grows.
	got := NudgeDivider(l, "sp", "t", "a", 1, 0, 0.1)
	closeTo(t, "nudge right from a", SplitSizes(got.Spaces[0].Tabs[0].Root), []float64{0.6, 0.4})

	// From b, to the right: the divider still moves right, so b shrinks.
	got = NudgeDivider(l, "sp", "t", "b", 1, 0, 0.1)
	closeTo(t, "nudge right from b", SplitSizes(got.Spaces[0].Tabs[0].Root), []float64{0.6, 0.4})

	// From b, to the left: b grows.
	got = NudgeDivider(l, "sp", "t", "b", -1, 0, 0.1)
	closeTo(t, "nudge left from b", SplitSizes(got.Spaces[0].Tabs[0].Root), []float64{0.4, 0.6})
}

func TestNudgeDividerKeepsSharesAtLeastTenth(t *testing.T) {
	l := parse(t, `{"version":1,"spaces":[{"id":"sp","name":"a","tabs":[{"id":"t","root":{"type":"split","dir":"row","sizes":[0.15,0.85],"children":[{"type":"pane","session":"a"},{"type":"pane","session":"b"}]}}]}]}`)
	got := NudgeDivider(l, "sp", "t", "a", -1, 0, 0.1)
	closeTo(t, "clamped shares", SplitSizes(got.Spaces[0].Tabs[0].Root), []float64{0.1, 0.9})
}

func TestNudgeDividerUsesTheNearestSplit(t *testing.T) {
	// The col split around b is nearer than the row root, so a col nudge
	// changes the inner one.
	l := parse(t, `{"version":1,"spaces":[{"id":"sp","name":"a","tabs":[{"id":"t","root":{"type":"split","dir":"row","sizes":[0.5,0.5],"children":[{"type":"pane","session":"a"},{"type":"split","dir":"col","sizes":[0.5,0.5],"children":[{"type":"pane","session":"b"},{"type":"pane","session":"c"}]}]}}]}]}`)
	got := NudgeDivider(l, "sp", "t", "b", 0, 1, 0.1)
	root := got.Spaces[0].Tabs[0].Root
	closeTo(t, "inner col sizes", SplitSizes(root.Children[1]), []float64{0.6, 0.4})
	closeTo(t, "outer row sizes untouched", SplitSizes(root), []float64{0.5, 0.5})
}

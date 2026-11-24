package workbench

import (
	"slices"
	"testing"
)

// paneSelf is the fixed pid the walk starts from in tests.
const paneSelf = 100

// fakeProc is one process in the fake table a test's procTTY reads.
type fakeProc struct {
	dev    uint64
	hasTTY bool
	ppid   int
	ok     bool
}

// noTTYChain builds a fake parent chain: every process up to ttyAt has no
// controlling terminal, and the process at that level is on ttyDev. Levels
// count the process itself as 0, its parent as 1, and so on.
func noTTYChain(ttyAt int, ttyDev uint64) map[int]fakeProc {
	procs := make(map[int]fakeProc, ttyAt+1)
	for level := 0; level <= ttyAt; level++ {
		pid := paneSelf + level
		if level == ttyAt {
			procs[pid] = fakeProc{dev: ttyDev, hasTTY: true, ppid: 1, ok: true}
			continue
		}
		procs[pid] = fakeProc{ppid: pid + 1, ok: true}
	}
	return procs
}

// chainPids is the pids of the first levels+1 processes of a chain: paneSelf
// and its ancestors up to that level.
func chainPids(levels int) []int {
	pids := make([]int, 0, levels+1)
	for level := 0; level <= levels; level++ {
		pids = append(pids, paneSelf+level)
	}
	return pids
}

func TestInPane(t *testing.T) {
	paneEnv := map[string]string{
		"LANA_WORKBENCH_SESSION": "ws-12",
		"LANA_WORKBENCH_TTY":     "/dev/ttys012",
	}
	tests := []struct {
		name string
		env  map[string]string
		// procs is the fake process table procTTY reads, keyed by pid.
		procs map[int]fakeProc
		// dev is the device statRdev reports for the path in the marker.
		dev   uint64
		devOK bool
		// wantCalls is the exact pid sequence procTTY must be asked about.
		wantCalls []int
		wantID    string
		want      bool
	}{
		{
			name:      "in a pane",
			env:       paneEnv,
			procs:     map[int]fakeProc{paneSelf: {dev: 7, hasTTY: true, ppid: 1, ok: true}},
			dev:       7,
			devOK:     true,
			wantCalls: []int{paneSelf},
			wantID:    "ws-12",
			want:      true,
		},
		{
			name: "no session marker",
			env:  map[string]string{"LANA_WORKBENCH_TTY": "/dev/ttys012"},
			dev:  7, devOK: true,
		},
		{
			name: "no tty marker",
			env:  map[string]string{"LANA_WORKBENCH_SESSION": "ws-12"},
			dev:  7, devOK: true,
		},
		{
			// A herdr or tmux inside a pane opens another terminal, so the
			// process has its own controlling tty and it differs from the
			// marker (REQ-WA-002). The walk stops there: the parent is on the
			// pane tty, but it is not consulted.
			name: "a nested terminal has another tty",
			env:  paneEnv,
			procs: map[int]fakeProc{
				paneSelf:     {dev: 9, hasTTY: true, ppid: paneSelf + 1, ok: true},
				paneSelf + 1: {dev: 7, hasTTY: true, ppid: 1, ok: true},
			},
			dev:       7,
			devOK:     true,
			wantCalls: []int{paneSelf},
		},
		{
			// Claude Code runs its hooks and its Bash tool without a
			// controlling tty, so the walk goes up to the pane's shell.
			name: "no controlling terminal falls back to the parent",
			env:  paneEnv,
			procs: map[int]fakeProc{
				paneSelf:     {ppid: paneSelf + 1, ok: true},
				paneSelf + 1: {dev: 7, hasTTY: true, ppid: 1, ok: true},
			},
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(1),
			wantID:    "ws-12",
			want:      true,
		},
		{
			name: "a ttyless parent falls back to the grandparent",
			env:  paneEnv,
			procs: map[int]fakeProc{
				paneSelf:     {ppid: paneSelf + 1, ok: true},
				paneSelf + 1: {ppid: paneSelf + 2, ok: true},
				paneSelf + 2: {dev: 7, hasTTY: true, ppid: 1, ok: true},
			},
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(2),
			wantID:    "ws-12",
			want:      true,
		},
		{
			name: "the first tty found is on another pty",
			env:  paneEnv,
			procs: map[int]fakeProc{
				paneSelf:     {ppid: paneSelf + 1, ok: true},
				paneSelf + 1: {dev: 9, hasTTY: true, ppid: 1, ok: true},
			},
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(1),
		},
		{
			name: "a ttyless chain stops at pid 1",
			env:  paneEnv,
			procs: map[int]fakeProc{
				paneSelf:     {ppid: paneSelf + 1, ok: true},
				paneSelf + 1: {ppid: 1, ok: true},
			},
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(1),
		},
		{
			name:      "a lookup error on the process itself",
			env:       paneEnv,
			procs:     map[int]fakeProc{paneSelf: {ok: false}},
			dev:       7,
			devOK:     true,
			wantCalls: []int{paneSelf},
		},
		{
			name: "a lookup error on an ancestor",
			env:  paneEnv,
			procs: map[int]fakeProc{
				paneSelf:     {ppid: paneSelf + 1, ok: true},
				paneSelf + 1: {ok: false},
			},
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(1),
		},
		{
			// The cap is the process itself plus up to maxPaneAncestors
			// ancestors, so levels 0..16 are looked at and a tty at level 17
			// is out of reach.
			name:      "the pane tty at level 17 is out of reach",
			env:       paneEnv,
			procs:     noTTYChain(maxPaneAncestors+1, 7),
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(maxPaneAncestors),
		},
		{
			name:      "the pane tty at the 16th ancestor is reached",
			env:       paneEnv,
			procs:     noTTYChain(maxPaneAncestors, 7),
			dev:       7,
			devOK:     true,
			wantCalls: chainPids(maxPaneAncestors),
			wantID:    "ws-12",
			want:      true,
		},
		{
			name: "the tty is gone",
			env:  paneEnv,
			dev:  7, devOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldEnv, oldProc, oldStat := envLookup, procTTY, statRdev
			t.Cleanup(func() { envLookup, procTTY, statRdev = oldEnv, oldProc, oldStat })
			envLookup = func(key string) (string, bool) { v, ok := tt.env[key]; return v, ok }
			statRdev = func(string) (uint64, bool) { return tt.dev, tt.devOK }
			var calls []int
			procTTY = func(pid int) (uint64, bool, int, bool) {
				calls = append(calls, pid)
				p, ok := tt.procs[pid]
				if !ok {
					return 0, false, 0, false
				}
				return p.dev, p.hasTTY, p.ppid, p.ok
			}

			id, ok := inPaneFrom(paneSelf)
			if id != tt.wantID || ok != tt.want {
				t.Errorf("inPaneFrom(%d) = (%q, %v), want (%q, %v)", paneSelf, id, ok, tt.wantID, tt.want)
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("procTTY calls = %v, want %v", calls, tt.wantCalls)
			}
		})
	}
}

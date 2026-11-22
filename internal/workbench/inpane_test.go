package workbench

import "testing"

func TestInPane(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		self   uint64
		selfOK bool
		dev    uint64
		devOK  bool
		wantID string
		want   bool
	}{
		{
			name: "in a pane",
			env:  map[string]string{"LANA_WORKBENCH_SESSION": "ws-12", "LANA_WORKBENCH_TTY": "/dev/ttys012"},
			self: 7, selfOK: true, dev: 7, devOK: true,
			wantID: "ws-12", want: true,
		},
		{
			name: "no session marker",
			env:  map[string]string{"LANA_WORKBENCH_TTY": "/dev/ttys012"},
			self: 7, selfOK: true, dev: 7, devOK: true,
		},
		{
			name: "no tty marker",
			env:  map[string]string{"LANA_WORKBENCH_SESSION": "ws-12"},
			self: 7, selfOK: true, dev: 7, devOK: true,
		},
		{
			// A herdr or tmux inside a pane opens another terminal, so the
			// controlling tty differs from the marker (REQ-WA-002).
			name: "a nested terminal has another tty",
			env:  map[string]string{"LANA_WORKBENCH_SESSION": "ws-12", "LANA_WORKBENCH_TTY": "/dev/ttys012"},
			self: 9, selfOK: true, dev: 7, devOK: true,
		},
		{
			name:   "no controlling terminal",
			env:    map[string]string{"LANA_WORKBENCH_SESSION": "ws-12", "LANA_WORKBENCH_TTY": "/dev/ttys012"},
			selfOK: false, dev: 7, devOK: true,
		},
		{
			name: "the tty is gone",
			env:  map[string]string{"LANA_WORKBENCH_SESSION": "ws-12", "LANA_WORKBENCH_TTY": "/dev/ttys012"},
			self: 7, selfOK: true, devOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldEnv, oldSelf, oldStat := envLookup, selfTTYDev, statRdev
			t.Cleanup(func() { envLookup, selfTTYDev, statRdev = oldEnv, oldSelf, oldStat })
			envLookup = func(key string) (string, bool) { v, ok := tt.env[key]; return v, ok }
			selfTTYDev = func() (uint64, bool) { return tt.self, tt.selfOK }
			statRdev = func(string) (uint64, bool) { return tt.dev, tt.devOK }

			id, ok := InPane()
			if id != tt.wantID || ok != tt.want {
				t.Errorf("InPane() = (%q, %v), want (%q, %v)", id, ok, tt.wantID, tt.want)
			}
		})
	}
}

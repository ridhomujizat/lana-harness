//go:build darwin || linux

package workbench

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/claudeshare"
)

const agyTestID = "7f3c2a10-1b2c-4d5e-8f90-a1b2c3d4e5f6"

// REQ-WAG-001: the agy adapter reads the pane's record when it is newer than
// the agy process; the owner's HOME is the built-in account.
func TestAgyLookup(t *testing.T) {
	lana, owner, account := t.TempDir(), t.TempDir(), t.TempDir()
	started := time.Now().Add(-time.Minute)
	q := agentQuery{Home: owner, SessionID: "abc123", LanaHome: lana, Started: started}
	if _, ok := agyLookup(q); ok {
		t.Fatal("lookup without a record found one")
	}
	write := func(rec AgentRecord) {
		t.Helper()
		if err := WriteAgentRecord(lana, "abc123", rec); err != nil {
			t.Fatal(err)
		}
	}
	write(AgentRecord{Agent: AgentAgy, ConversationID: agyTestID, Home: account, At: time.Now()})
	if got, ok := agyLookup(q); !ok || got.ID != agyTestID || got.ConfigDir != account {
		t.Fatalf("named = %+v, %v", got, ok)
	}
	write(AgentRecord{Agent: AgentAgy, ConversationID: agyTestID, Home: owner, At: time.Now()})
	if got, ok := agyLookup(q); !ok || got.ConfigDir != "" {
		t.Fatalf("built-in = %+v, %v", got, ok)
	}
	// A record from before this agy started belongs to an earlier one.
	write(AgentRecord{Agent: AgentAgy, ConversationID: agyTestID, Home: account, At: started.Add(-time.Second)})
	if _, ok := agyLookup(q); ok {
		t.Fatal("an older record matched")
	}
	// Bad records are never written.
	for _, rec := range []AgentRecord{
		{Agent: AgentAgy, ConversationID: "x; rm -rf ~"},
		{Agent: "claude", ConversationID: agyTestID},
	} {
		if err := WriteAgentRecord(lana, "abc123", rec); err == nil {
			t.Fatalf("wrote %+v", rec)
		}
	}
	if err := WriteAgentRecord(lana, "../x", AgentRecord{Agent: AgentAgy, ConversationID: agyTestID}); err == nil {
		t.Fatal("wrote a record outside the folder")
	}
	removeAgentRecord(lana, "abc123")
	if _, err := os.Stat(agentRecordPath(lana, "abc123")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record not removed: %v", err)
	}
}

// AC-WAG-002: the restore line of an agy pane.
func TestAgyResumeLine(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		id, dir, want string
	}{
		{agyTestID, home, "env HOME='" + home + "' SSH_CONNECTION='127.0.0.1 0 127.0.0.1 0' agy --conversation " + agyTestID},
		{"", home, "env HOME='" + home + "' SSH_CONNECTION='127.0.0.1 0 127.0.0.1 0' agy --continue"},
		{agyTestID, "", "agy --conversation " + agyTestID},
		{"bad id;", "", "agy --continue"},
		// A home that is gone falls back to the plain form.
		{agyTestID, filepath.Join(home, "gone"), "agy --conversation " + agyTestID},
	}
	for _, c := range cases {
		if got, ok := resumeLine(AgentAgy, c.id, c.dir, "", nil); !ok || got != c.want {
			t.Errorf("resumeLine(agy, %q, %q) = %q, %v; want %q", c.id, c.dir, got, ok, c.want)
		}
	}
}

// REQ-WAG-004: a restored named-account agy pane is relinked before it
// resumes; the built-in account and other homes are not.
func TestRelinkRestoredAgy(t *testing.T) {
	lana := t.TempDir()
	var calls []string
	old := relinkAgyMCP
	relinkAgyMCP = func(account, owner string, _ time.Time) (claudeshare.Item, error) {
		calls = append(calls, account+"|"+owner)
		return claudeshare.Item{}, nil
	}
	t.Cleanup(func() { relinkAgyMCP = old })
	named := filepath.Join(lana, "antigravity", "work")
	if !relinkRestoredAgy(AgentAgy, named, lana, "/owner") {
		t.Fatal("named account not relinked")
	}
	for _, c := range []struct{ agent, dir string }{{AgentAgy, ""}, {AgentAgy, "/elsewhere/home"}, {"claude", named}} {
		if relinkRestoredAgy(c.agent, c.dir, lana, "/owner") {
			t.Fatalf("%+v relinked", c)
		}
	}
	if len(calls) != 1 || calls[0] != named+"|/owner" {
		t.Fatalf("calls = %v", calls)
	}
}

// fakeAgyBin writes a fake "fakeagy" foreground program that exits on the
// first Ctrl+C, and the "agy" the typed line starts, which only prints what it
// got. Never a real agy.
func fakeAgyBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	fake := "#!/bin/sh\ntrap 'echo fake-agy-exit; exit 0' INT\necho fake-agy-\"\"ready\n" +
		"i=0\nwhile [ $i -lt 400 ]; do sleep 0.05; i=$((i+1)); done\n"
	resumed := "#!/bin/sh\necho \"agy-\"\"resumed home=[$HOME] ssh=[$SSH_CONNECTION] args=[$*]\"\n"
	for name, body := range map[string]string{"fakeagy": fake, "agy": resumed} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// AC-WAG-003: an agy pane switches to a named account with --conversation;
// the recorded home is the new one and the pane's old record is dropped; a
// Claude Code switch of the agy pane is no_conversation.
func TestSwitchAccountAgy(t *testing.T) {
	lana := shortHome(t)
	setCoreHome(lana)
	t.Cleanup(func() { setCoreHome("") })
	bin := fakeAgyBin(t)
	ss := switchSession(t, bin, []string{"/bin/sh"})
	waitOutput(t, ss, "$ ")
	if _, err := ss.p.write([]byte("fakeagy\r")); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, ss, "fake-agy-ready")
	if err := WriteAgentRecord(lana, ss.id, AgentRecord{Agent: AgentAgy, ConversationID: agyTestID, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ss.mu.Lock()
	ss.rAgent, ss.agentSession, ss.agentSeen = AgentAgy, agyTestID, time.Now()
	ss.mu.Unlock()
	if info := ss.info(); info.AccountSwitch != SwitchReady || info.ConversationAgent != AgentAgy || info.ConversationID != agyTestID {
		t.Fatalf("before switch: %+v", info)
	}

	err := ss.switchAccount(SwitchOptions{})
	if !errors.Is(err, ErrNoConversation) || err.Error() != "No Claude conversation in this pane" {
		t.Fatalf("claude switch of an agy pane = %v", err)
	}

	account := t.TempDir()
	if err := ss.switchAccount(SwitchOptions{Agent: AgentAgy, ConfigDir: account}); err != nil {
		t.Fatalf("switch: %v\n%s", err, ss.output())
	}
	waitOutput(t, ss, "agy-resumed home=["+account+"] ssh=[127.0.0.1 0 127.0.0.1 0] args=[--conversation "+agyTestID+"]")
	out := ss.output()
	if !strings.Contains(out, "fake-agy-exit") {
		t.Fatalf("agy did not get Ctrl+C:\n%s", out)
	}
	if info := ss.info(); info.AgentConfigDir != account {
		t.Fatalf("after switch: %+v", info)
	}
	if _, ok := readAgentRecord(lana, ss.id); ok {
		t.Fatal("the old agy record is still there")
	}
}

// An agy pane with no conversation names Antigravity in its refusal.
func TestSwitchAccountAgyNoConversation(t *testing.T) {
	ss := switchSession(t, fakeAgyBin(t), []string{"/bin/sh"})
	waitOutput(t, ss, "$ ")
	err := ss.switchAccount(SwitchOptions{Agent: AgentAgy})
	if !errors.Is(err, ErrNoConversation) || err.Error() != "No Antigravity conversation in this pane" {
		t.Fatalf("switch = %v", err)
	}
	if !errors.Is(stillRunningError(AgentAgy), ErrStillRunning) ||
		stillRunningError(AgentAgy).Error() != "Antigravity did not exit. Exit it in the pane and try again." {
		t.Fatalf("still running = %v", stillRunningError(AgentAgy))
	}
}

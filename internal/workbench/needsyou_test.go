package workbench

import (
	"context"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agentdetect"
)

// TestAgentNeedsYouFollowsScreenState is the direct seam: it fakes the detected
// agent and its screen state and checks the reported "needs you" is live, not
// sticky. A blocked agent needs you, and it stops the moment the screen is idle
// again, with no control attach (spec 55 A6).
func TestAgentNeedsYouFollowsScreenState(t *testing.T) {
	s := &session{agent: "claude", agentState: agentdetect.StateBlocked}
	if !s.needsYouLocked() {
		t.Fatal("a blocked agent does not need you")
	}
	s.agentState = agentdetect.StateIdle
	if s.needsYouLocked() {
		t.Fatal("an idle agent still needs you with no control attach")
	}
}

// TestAgentBellIsIgnored checks end to end that an agent printing a terminal
// bell does not report "needs you": an agent's state comes from its screen, not
// its bell (spec 55 A6).
func TestAgentBellIsIgnored(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	shell := shellWithExecA(t)
	// `exec -a claude <shell> -c 'printf "\a"; sleep 3; :'` names the session's
	// process claude, like agent_test.go, and rings the bell right away.
	script := "exec -a claude " + shellQuote(shell) + ` -c 'printf "\a"; sleep 3; :'`
	s, err := c.Create(ctx, CreateOptions{Command: []string{shell, "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "agent claude", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Agent == "claude"
	})
	// Poll so a bell that arrives late, after the agent is detected, is caught.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g, err := c.Get(ctx, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if g.NeedsYou {
			t.Fatal("an agent's bell was reported as needs you")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAgentExitNeedsYouUntilControlAttach checks that an agent which exits with
// no control client attached is real attention, unlike its bell, and that a
// control attach clears it (spec 55 A6).
func TestAgentExitNeedsYouUntilControlAttach(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	shell := shellWithExecA(t)
	script := "exec -a claude " + shellQuote(shell) + ` -c 'sleep 2; :'`
	s, err := c.Create(ctx, CreateOptions{Command: []string{shell, "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "agent claude", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Agent == "claude"
	})
	waitFor(t, "exit", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.State == "exited"
	})
	if g, err := c.Get(ctx, s.ID); err != nil || !g.NeedsYou {
		t.Fatalf("an unattended agent exit = %+v, %v; want needs you", g, err)
	}
	stream, err := c.Attach(ctx, s.ID, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	waitFor(t, "needs you cleared", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && !g.NeedsYou
	})
}

// TestShellBellClearedByControlAttach checks the unchanged behaviour for a
// plain program: a bell needs you until a control client attaches
// (REQ-WB-012).
func TestShellBellClearedByControlAttach(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", `printf "\a"; sleep 5`}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "bell", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.NeedsYou
	})
	stream, err := c.Attach(ctx, s.ID, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	waitFor(t, "needs you cleared", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && !g.NeedsYou
	})
}

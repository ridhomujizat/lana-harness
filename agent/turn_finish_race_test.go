package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestStartAfterWaitNeverBusy runs the Start -> Wait -> Start pattern many
// times: a Wait that returned means the turn finished and the session is idle,
// so the next Start must never see ErrSessionBusy. Before the runChain fix,
// finishTurn closed the done channel before clearing runner.running, so this
// pattern could get ErrSessionBusy from the fast path in Registry.start.
func TestStartAfterWaitNeverBusy(t *testing.T) {
	const turns = 300
	responses := make([]mock.Response, 0, turns)
	for i := 0; i < turns; i++ {
		responses = append(responses, textResponse(fmt.Sprintf("reply %d", i)))
	}
	h := newHarness(t, mock.Script{Responses: responses}, nil)

	ctx := context.Background()
	for i := 0; i < turns; i++ {
		turnID, err := h.reg.Start(ctx, h.info.ID, fmt.Sprintf("message %d", i))
		if err != nil {
			t.Fatalf("iteration %d: Start returned %v, want nil", i, err)
		}
		if _, err := h.reg.Wait(ctx, turnID); err != nil {
			t.Fatalf("iteration %d: Wait returned %v, want nil", i, err)
		}
	}
}

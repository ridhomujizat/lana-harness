package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// busyTurnEndStore fails the first failures turn/end writes with the error the
// SQLite store returns for SQLITE_BUSY_SNAPSHOT.
type busyTurnEndStore struct {
	*memstore.Store
	mu       sync.Mutex
	failures int
	attempts int
}

func (s *busyTurnEndStore) Append(ctx context.Context, sessionID string, drafts []core.EventDraft) ([]core.SessionEvent, error) {
	for _, draft := range drafts {
		if draft.Kind != core.KindTurnEnd {
			continue
		}
		s.mu.Lock()
		s.attempts++
		fail := s.attempts <= s.failures
		s.mu.Unlock()
		if fail {
			return nil, errors.New("database is locked (517)")
		}
	}
	return s.Store.Append(ctx, sessionID, drafts)
}

func (s *busyTurnEndStore) turnEndAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// TestTurnEndRetriedOnBusyStore covers a turn/end write that meets a busy
// store: it is retried, so the session does not stay "running". Past the
// in-line retries the write continues in the background.
func TestTurnEndRetriedOnBusyStore(t *testing.T) {
	cases := []struct {
		name     string
		failures int
	}{
		{name: "in-line retry", failures: 2},
		{name: "background retry", failures: len(turnEndDelays) + 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saved := turnEndWait
			turnEndWait = func(time.Duration) {}
			t.Cleanup(func() { turnEndWait = saved })

			h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("done")}}, nil)
			busy := &busyTurnEndStore{Store: h.store, failures: tc.failures}
			cfg := core.Config{
				Providers: map[string]core.ProviderConfig{"mock": {Kind: "openai_compat"}},
				Agents:    map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}},
			}
			h.reg = NewRegistry(busy, h.llm, h.tools, h.hooks, cfg, nil, h.home, h.ws)
			if result := h.run(t, "hi"); result.StopReason != "end_turn" {
				t.Fatalf("result=%+v", result)
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				info, err := h.store.Info(context.Background(), h.info.ID)
				if err != nil {
					t.Fatal(err)
				}
				if info.Status == "idle" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("session status=%q after %d turn/end attempts, want idle", info.Status, busy.turnEndAttempts())
				}
				time.Sleep(5 * time.Millisecond)
			}
			if got, want := busy.turnEndAttempts(), tc.failures+1; got != want {
				t.Fatalf("turn/end attempts=%d, want %d", got, want)
			}
		})
	}
}

func TestIsStoreBusy(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("database is locked (5)"), true},
		{errors.New("database is locked (517)"), true},
		{errors.New("database table is locked (6)"), true},
		{errors.New("session not found"), false},
	} {
		if got := isStoreBusy(tc.err); got != tc.want {
			t.Errorf("isStoreBusy(%v)=%v, want %v", tc.err, got, tc.want)
		}
	}
}

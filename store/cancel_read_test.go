package store

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestCancelledReadsDoNotBreakWrites covers readers whose context is cancelled
// while their query runs, as the web SSE pollers are when a client drops the
// stream. A cancelled read must not leave a statement open on a pooled
// connection: the open read snapshot made a later BEGIN IMMEDIATE on that
// connection fail at once with SQLITE_BUSY_SNAPSHOT (517), so a turn could not
// write its turn/end.
func TestCancelledReadsDoNotBreakWrites(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	for i := 0; i < 50; i++ {
		appendTestMessage(t, s, info.ID)
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(rand.IntN(200))*time.Microsecond)
				for _, err := range s.Events(ctx, info.ID, 1) {
					if err != nil {
						break
					}
				}
				_, _ = s.Info(ctx, info.ID)
				cancel()
			}
		}()
	}

	deadline := time.Now().Add(time.Second)
	var writeErr error
	for writeErr == nil && time.Now().Before(deadline) {
		_, writeErr = s.Append(context.Background(), info.ID, []core.EventDraft{testMessageDraft()})
	}
	close(stop)
	readers.Wait()
	if writeErr != nil {
		t.Fatalf("append after cancelled reads: %v", writeErr)
	}
}

func testMessageDraft() core.EventDraft {
	return core.EventDraft{Kind: core.KindUserMessage, Payload: core.UserMessage{V: core.PayloadVersion, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}}
}

func appendTestMessage(t *testing.T, s *Store, sessionID string) {
	t.Helper()
	if _, err := s.Append(context.Background(), sessionID, []core.EventDraft{testMessageDraft()}); err != nil {
		t.Fatal(err)
	}
}

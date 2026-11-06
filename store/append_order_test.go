package store

import (
	"context"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestAppendPublishesInSeqOrder checks that concurrent Appends publish their
// events in commit order. Because the write lock is held across COMMIT and the
// publish callback, a subscriber sees strictly increasing seqs even when
// several writers race.
func TestAppendPublishesInSeqOrder(t *testing.T) {
	s, _ := openTest(t)
	s.Hooks = core.NewHooks()
	info := createSession(t, s)
	sub := s.Hooks.SessionEvent.Subscribe()
	defer sub.Close()

	const writers = 8
	const perWriter = 10
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
					{Kind: core.KindUserMessage, Payload: core.UserMessage{V: 1}},
				}); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	want := writers * perWriter
	if got := sub.Len(); got != want {
		t.Fatalf("buffered=%d want %d", got, want)
	}
	last := int64(0)
	for i := 0; i < want; i++ {
		recv, err := sub.Recv(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if recv.Lagged != 0 {
			t.Fatalf("unexpected lagged %d", recv.Lagged)
		}
		if recv.Value.Seq <= last {
			t.Fatalf("seq %d did not increase after %d", recv.Value.Seq, last)
		}
		last = recv.Value.Seq
	}
}

// TestAppendFailedCommitDoesNotPublish checks that a transaction that never
// commits does not notify subscribers.
func TestAppendFailedCommitDoesNotPublish(t *testing.T) {
	s, _ := openTest(t)
	s.Hooks = core.NewHooks()
	info := createSession(t, s)
	sub := s.Hooks.SessionEvent.Subscribe()
	defer sub.Close()

	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: "unknown/kind", Payload: core.UserMessage{V: 1}},
	}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if got := sub.Len(); got != 0 {
		t.Fatalf("published %d events for a failed append", got)
	}
}

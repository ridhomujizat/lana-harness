package lana

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestSubscribeParsesEvents(t *testing.T) {
	d := newFakeDaemon(t)
	d.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		// A heartbeat comment and a blank line must not become an event.
		fmt.Fprint(w, ": ping\n\n")
		fmt.Fprint(w, "event: session.created\ndata: {\"type\":\"session.created\",\"session\":{\"id\":\"a\",\"state\":\"running\"}}\n\n")
		// The data of one event may span lines, joined with a newline.
		fmt.Fprint(w, "event: layout.changed\ndata: {\"type\":\"layout.changed\",\ndata: \"layout\":{\"version\":1}}\n\n")
		flusher.Flush()
	}
	conn := d.connect(t)

	var got []Event
	err := conn.Subscribe(context.Background(), func(event Event) { got = append(got, event) })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("subscribe error = %v, want io.ErrUnexpectedEOF", err)
	}
	if len(got) != 2 {
		t.Fatalf("events = %+v, want 2", got)
	}
	if got[0].Type != EventSessionCreated || got[0].Session == nil || got[0].Session.ID != "a" {
		t.Fatalf("first event = %+v", got[0])
	}
	if got[1].Type != EventLayoutChanged || string(got[1].Layout) != `{"version":1}` {
		t.Fatalf("second event = %+v, layout %q", got[1], got[1].Layout)
	}
}

func TestSubscribeEndsWithContext(t *testing.T) {
	d := newFakeDaemon(t)
	entered := make(chan struct{})
	d.events = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}
	conn := d.connect(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- conn.Subscribe(ctx, nil) }()
	<-entered
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("subscribe after cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscribe did not return after the context ended")
	}
}

func TestBackoff(t *testing.T) {
	var backoff Backoff
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for i, expected := range want {
		if got := backoff.Next(); got != expected {
			t.Fatalf("Next #%d = %v, want %v", i+1, got, expected)
		}
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("Next after Reset = %v, want 1s", got)
	}
}

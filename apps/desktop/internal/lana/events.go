package lana

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Backoff spaces reconnect attempts (REQ-DW-013): 1 s, doubled each time up to
// 30 s, and Reset back to 1 s after a call succeeds. The zero value is ready.
type Backoff struct {
	next time.Duration
}

const (
	backoffFirst = time.Second
	backoffMax   = 30 * time.Second
)

// Next returns the delay before the next attempt and advances the backoff.
func (b *Backoff) Next() time.Duration {
	switch {
	case b.next < backoffFirst:
		b.next = backoffFirst
	case b.next < backoffMax:
		b.next *= 2
		if b.next > backoffMax {
			b.next = backoffMax
		}
	}
	return b.next
}

// Reset returns the backoff to its first delay.
func (b *Backoff) Reset() { b.next = 0 }

// Subscribe opens the event stream (GET /v1/workbench/events) and calls
// onEvent for each event until ctx ends or the stream fails. It returns nil
// when ctx ends, and the stream error otherwise so the caller can wait for
// Backoff and subscribe again (REQ-DW-013). onEvent may be nil.
func (c *Local) Subscribe(ctx context.Context, onEvent func(Event)) error {
	err := c.subscribe(ctx, onEvent)
	if ctx.Err() != nil {
		// The caller ended the subscription: not an error to reconnect on.
		return nil
	}
	return err
}

// subscribe does the work of Subscribe and returns the raw error.
func (c *Local) subscribe(ctx context.Context, onEvent func(Event)) error {
	resp, err := c.send(ctx, http.MethodGet, EventsPath, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := c.checkResponse(resp); err != nil {
		return err
	}
	return parseEvents(resp.Body, onEvent)
}

// parseEvents reads an SSE stream: `event:` names the event type, `data:`
// lines are joined with a newline, and comments (the heartbeat) and the blank
// line between events are ignored. A stream that ends on its own is an error,
// so the caller reconnects.
func parseEvents(r io.Reader, onEvent func(Event)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var (
		eventType string
		data      []string
	)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				if err := dispatch(onEvent, eventType, data); err != nil {
					return err
				}
			}
			eventType = ""
			data = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		switch field {
		case "event":
			eventType = strings.TrimPrefix(value, " ")
		case "data":
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

// dispatch parses one event and hands it to onEvent. The event line's type is
// the fallback when the data itself does not carry one.
func dispatch(onEvent func(Event), eventType string, data []string) error {
	var event Event
	if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &event); err != nil {
		return fmt.Errorf("lana: event stream: %w", err)
	}
	if event.Type == "" {
		event.Type = eventType
	}
	if onEvent != nil {
		onEvent(event)
	}
	return nil
}

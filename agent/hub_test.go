package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// recvHubFrame reads one frame from a hub subscription.
func recvHubFrame(t *testing.T, sub *core.Subscription[core.LiveFrame]) core.LiveFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, err := sub.Recv(ctx)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if received.Lagged > 0 {
		t.Fatalf("unexpected lag: %d", received.Lagged)
	}
	return received.Value
}

func TestHubLinkIdempotent(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	sub := hub.Subscribe("parent")
	defer sub.Close()

	hub.Link("parent", "child")
	hub.Link("parent", "child")
	hub.Publish("child", core.LiveFrame{SessionID: "child", Kind: "text_delta", Text: "hi"})

	frame := recvHubFrame(t, sub)
	if frame.SessionID != "child" || frame.Text != "hi" {
		t.Fatalf("frame=%+v", frame)
	}
	if got := sub.Len(); got != 0 {
		t.Fatalf("duplicate frames=%d", got)
	}
}

func TestHubChildFrameReachesParent(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	parentSub := hub.Subscribe("parent")
	defer parentSub.Close()
	childSub := hub.Subscribe("child")
	defer childSub.Close()

	hub.Link("parent", "child")
	hub.Publish("child", core.LiveFrame{SessionID: "child", Kind: "text_delta", Text: "from child"})

	for name, sub := range map[string]*core.Subscription[core.LiveFrame]{"parent": parentSub, "child": childSub} {
		frame := recvHubFrame(t, sub)
		if frame.SessionID != "child" || frame.Text != "from child" {
			t.Fatalf("%s frame=%+v", name, frame)
		}
		if got := sub.Len(); got != 0 {
			t.Fatalf("%s duplicate frames=%d", name, got)
		}
	}
}

func TestHubGrandchildFrameReachesAncestors(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	rootSub := hub.Subscribe("root")
	defer rootSub.Close()
	middleSub := hub.Subscribe("middle")
	defer middleSub.Close()
	leafSub := hub.Subscribe("leaf")
	defer leafSub.Close()

	hub.Link("root", "middle")
	hub.Link("middle", "leaf")
	hub.Publish("leaf", core.LiveFrame{SessionID: "leaf", Kind: "text_delta", Text: "from leaf"})

	for name, sub := range map[string]*core.Subscription[core.LiveFrame]{"root": rootSub, "middle": middleSub, "leaf": leafSub} {
		frame := recvHubFrame(t, sub)
		if frame.SessionID != "leaf" || frame.Text != "from leaf" {
			t.Fatalf("%s frame=%+v", name, frame)
		}
		if got := sub.Len(); got != 0 {
			t.Fatalf("%s duplicate frames=%d", name, got)
		}
	}
}

func TestHubUnlinkedSessionDoesNotFanOut(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	ownSub := hub.Subscribe("own")
	defer ownSub.Close()
	otherSub := hub.Subscribe("other")
	defer otherSub.Close()

	hub.Publish("own", core.LiveFrame{SessionID: "own", Kind: "text_delta", Text: "only own"})

	frame := recvHubFrame(t, ownSub)
	if frame.SessionID != "own" || frame.Text != "only own" {
		t.Fatalf("frame=%+v", frame)
	}
	if got := otherSub.Len(); got != 0 {
		t.Fatalf("unrelated subscriber got %d frames", got)
	}
}

func TestHubLinkCycleDoesNotDuplicate(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	firstSub := hub.Subscribe("first")
	defer firstSub.Close()
	secondSub := hub.Subscribe("second")
	defer secondSub.Close()

	hub.Link("first", "second")
	hub.Link("second", "first")
	hub.Publish("first", core.LiveFrame{SessionID: "first", Kind: "text_delta", Text: "cycle"})

	for name, sub := range map[string]*core.Subscription[core.LiveFrame]{"first": firstSub, "second": secondSub} {
		frame := recvHubFrame(t, sub)
		if frame.SessionID != "first" || frame.Text != "cycle" {
			t.Fatalf("%s frame=%+v", name, frame)
		}
		if got := sub.Len(); got != 0 {
			t.Fatalf("%s duplicate frames=%d", name, got)
		}
	}
}

func TestHubCloseClosesSubscribers(t *testing.T) {
	hub := NewHub()
	sub := hub.Subscribe("session")
	hub.Close()

	if _, err := sub.Recv(context.Background()); !errors.Is(err, core.ErrClosed) {
		t.Fatalf("recv error=%v want %v", err, core.ErrClosed)
	}
}

func TestHubParentSlowSubscriberWarns(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	hub := NewHub()
	defer hub.Close()
	sub := hub.Subscribe("parent")
	defer sub.Close()

	hub.Link("parent", "child")
	for i := 0; i < 1500; i++ {
		hub.Publish("child", core.LiveFrame{SessionID: "child", Kind: "text_delta", Text: "x"})
	}
	if !strings.Contains(buf.String(), "live frame dropped") {
		t.Fatalf("warning not found: %s", buf.String())
	}
}

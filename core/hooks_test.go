package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaterfallOrder(t *testing.T) {
	var got []string
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		got = append(got, "A")
		return next(ctx, in)
	})
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		got = append(got, "B")
		return next(ctx, in)
	})
	w.SetDefault(func(ctx context.Context, in string) (string, error) {
		got = append(got, "default")
		return in, nil
	})
	if _, err := w.Call(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	want := []string{"A", "B", "default"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestWaterfallStopsWithoutNext(t *testing.T) {
	var got []string
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		got = append(got, "A")
		return "A-result", nil
	})
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		got = append(got, "B")
		return next(ctx, in)
	})
	w.SetDefault(func(ctx context.Context, in string) (string, error) {
		got = append(got, "default")
		return in, nil
	})
	out, err := w.Call(context.Background(), "x")
	if err != nil || out != "A-result" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("got %v", got)
	}
}

func TestWaterfallInputChanged(t *testing.T) {
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		return next(ctx, in+"A")
	})
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		return next(ctx, in+"B")
	})
	w.SetDefault(func(ctx context.Context, in string) (string, error) { return in, nil })
	out, err := w.Call(context.Background(), "")
	if err != nil || out != "AB" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestWaterfallPrepend(t *testing.T) {
	var got []string
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		got = append(got, "A")
		return next(ctx, in)
	})
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		got = append(got, "C")
		return next(ctx, in)
	}, WithPrepend())
	w.SetDefault(func(ctx context.Context, in string) (string, error) {
		got = append(got, "default")
		return in, nil
	})
	_, _ = w.Call(context.Background(), "x")
	if fmt.Sprint(got) != fmt.Sprint([]string{"C", "A", "default"}) {
		t.Fatalf("got %v", got)
	}
}

func TestWaterfallErrorStops(t *testing.T) {
	wantErr := errors.New("boom")
	w := NewWaterfall[string, string]()
	called := false
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		return "", wantErr
	})
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		called = true
		return next(ctx, in)
	})
	w.SetDefault(func(ctx context.Context, in string) (string, error) { return in, nil })
	_, err := w.Call(context.Background(), "x")
	if !errors.Is(err, wantErr) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestWaterfallNoDefault(t *testing.T) {
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		return next(ctx, in)
	})
	if _, err := w.Call(context.Background(), "x"); !errors.Is(err, ErrNoDefault) {
		t.Fatalf("err=%v", err)
	}
}

func TestSerialResultsInOrder(t *testing.T) {
	s := NewSerial[string, string]()
	var mu sync.Mutex
	var order []string
	s.Use(func(ctx context.Context, in string) (string, error) {
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		order = append(order, "A")
		mu.Unlock()
		return "A", nil
	})
	s.Use(func(ctx context.Context, in string) (string, error) {
		mu.Lock()
		order = append(order, "B")
		mu.Unlock()
		return "B", nil
	})
	out, err := s.Call(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(out) != fmt.Sprint([]string{"A", "B"}) || fmt.Sprint(order) != fmt.Sprint([]string{"A", "B"}) {
		t.Fatalf("out=%v order=%v", out, order)
	}
}

func TestReleaseHandle(t *testing.T) {
	var count atomic.Int32
	w := NewWaterfall[string, string]()
	release := w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		count.Add(1)
		return next(ctx, in)
	})
	w.SetDefault(func(ctx context.Context, in string) (string, error) { return in, nil })
	_, _ = w.Call(context.Background(), "x")
	release()
	release()
	_, _ = w.Call(context.Background(), "x")
	if count.Load() != 1 {
		t.Fatalf("count=%d", count.Load())
	}
}

func TestObserveNonBlocking(t *testing.T) {
	o := NewObserve[int]()
	sub := o.Subscribe()
	defer sub.Close()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			o.Publish(i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked")
	}
}

func TestObserveLagged(t *testing.T) {
	o := NewObserve[int]()
	sub := o.Subscribe()
	defer sub.Close()
	for i := 0; i < 1500; i++ {
		o.Publish(i)
	}
	first, err := sub.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Lagged != 476 {
		t.Fatalf("lagged=%d", first.Lagged)
	}
	for i := 0; i < 1024; i++ {
		got, err := sub.Recv(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got.Lagged != 0 {
			t.Fatalf("unexpected lagged %d", got.Lagged)
		}
		if got.Value != 476+i {
			t.Fatalf("value=%d want %d", got.Value, 476+i)
		}
	}
}

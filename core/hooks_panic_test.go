package core

import (
	"context"
	"strings"
	"testing"
)

func TestWaterfallListenerPanicRecovered(t *testing.T) {
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		panic("boom")
	})
	_, err := w.Call(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "hook listener panicked") {
		t.Fatalf("err=%v", err)
	}
}

func TestWaterfallDefaultPanicRecovered(t *testing.T) {
	w := NewWaterfall[string, string]()
	w.Use(func(ctx context.Context, in string, next Next[string, string]) (string, error) {
		return next(ctx, in)
	})
	w.SetDefault(func(ctx context.Context, in string) (string, error) { panic("boom") })
	_, err := w.Call(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "hook listener panicked") {
		t.Fatalf("err=%v", err)
	}
}

func TestSerialListenerPanicRecovered(t *testing.T) {
	s := NewSerial[string, string]()
	s.Use(func(ctx context.Context, in string) (string, error) {
		panic("boom")
	})
	_, err := s.Call(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "hook listener panicked") {
		t.Fatalf("err=%v", err)
	}
}

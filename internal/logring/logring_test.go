package logring

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// recordSink is a slog.Handler that keeps every record it receives, so a test
// can prove the tee did not drop anything on the way to the wrapped handler.
type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *recordSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *recordSink) Handle(_ context.Context, record slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record.Clone())
	return nil
}

func (s *recordSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *recordSink) WithGroup(string) slog.Handler      { return s }

func (s *recordSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// TestBufferKeepsNewestWarnAndError covers AC-DSH-003: the buffer keeps the
// newest 500 WARN+ records, drops INFO, and the tee still passes every record
// to the wrapped handler.
func TestBufferKeepsNewestWarnAndError(t *testing.T) {
	sink := &recordSink{}
	buffer := New(500)
	logger := slog.New(Handler(sink, buffer))

	for i := 0; i < 600; i++ {
		logger.Warn("warn", "i", i)
	}
	for i := 0; i < 20; i++ {
		logger.Info("info", "i", i)
	}

	records := buffer.Since(0)
	if len(records) != 500 {
		t.Fatalf("buffered records = %d, want 500", len(records))
	}
	if records[0].Attrs != "i=100" || records[len(records)-1].Attrs != "i=599" {
		t.Fatalf("buffer window = %q..%q, want i=100..i=599", records[0].Attrs, records[len(records)-1].Attrs)
	}
	for _, record := range records {
		if record.Level < slog.LevelWarn {
			t.Fatalf("buffered level = %v, want >= WARN", record.Level)
		}
	}
	if sink.count() != 620 {
		t.Fatalf("wrapped handler saw %d records, want 620", sink.count())
	}
}

// TestBufferSinceFiltersByTime covers the range filter of Since.
func TestBufferSinceFiltersByTime(t *testing.T) {
	buffer := New(10)
	buffer.Add(Record{At: 100, Level: slog.LevelWarn, Message: "old"})
	buffer.Add(Record{At: 200, Level: slog.LevelError, Message: "new"})
	got := buffer.Since(150)
	if len(got) != 1 || got[0].Message != "new" {
		t.Fatalf("Since(150) = %+v", got)
	}
}

// TestHandlerCarriesAttrs covers the WithAttrs/WithGroup rendering: captured
// attributes and the record's own attributes land in Record.Attrs as
// "key=value", with groups joined as "group.key".
func TestHandlerCarriesAttrs(t *testing.T) {
	sink := &recordSink{}
	buffer := New(10)
	logger := slog.New(Handler(sink, buffer)).With("k", "v").WithGroup("g")
	logger.Warn("m", "a", 1)

	records := buffer.Since(0)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Attrs != "k=v g.a=1" {
		t.Fatalf("attrs = %q, want %q", records[0].Attrs, "k=v g.a=1")
	}
	if records[0].Message != "m" || records[0].Level != slog.LevelWarn {
		t.Fatalf("record = %+v", records[0])
	}
}

// TestHandlerWithAttrsInsideGroup covers a group opened before WithAttrs.
func TestHandlerWithAttrsInsideGroup(t *testing.T) {
	sink := &recordSink{}
	buffer := New(10)
	logger := slog.New(Handler(sink, buffer)).WithGroup("g").With("k", "v")
	logger.Error("m")
	records := buffer.Since(0)
	if len(records) != 1 || records[0].Attrs != "g.k=v" {
		t.Fatalf("records = %+v", records)
	}
}

// TestHandlerUsesRecordTime covers a record with an explicit time.
func TestHandlerUsesRecordTime(t *testing.T) {
	sink := &recordSink{}
	buffer := New(10)
	handler := Handler(sink, buffer)
	earlier := time.UnixMilli(1700000000000)
	record := slog.NewRecord(earlier, slog.LevelWarn, "boom", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	records := buffer.Since(0)
	if len(records) != 1 || records[0].At != earlier.UnixMilli() {
		t.Fatalf("records = %+v, want at %d", records, earlier.UnixMilli())
	}
}

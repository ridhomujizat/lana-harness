// Package logring keeps the daemon's most recent warning and error log records
// in memory while forwarding every record to the wrapped handler. It backs the
// error log section of the monitoring dashboard (design.md §4, REQ-DSH-009).
package logring

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Record is one buffered log record. At is Unix milliseconds; Attrs is the
// record's attributes rendered as "key=value" pairs joined by spaces.
type Record struct {
	At      int64
	Level   slog.Level
	Message string
	Attrs   string
}

// Buffer is a fixed-size ring of the newest records, safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	records []Record
	next    int
	count   int
}

// New creates a Buffer that keeps the newest size records. A size below 1 is
// clamped to 1.
func New(size int) *Buffer {
	if size < 1 {
		size = 1
	}
	return &Buffer{records: make([]Record, size)}
}

// Add appends one record, overwriting the oldest record when the buffer is
// full. It is safe to call from several goroutines.
func (b *Buffer) Add(record Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records[b.next] = record
	b.next = (b.next + 1) % len(b.records)
	if b.count < len(b.records) {
		b.count++
	}
}

// Since returns the records with At >= ms, oldest first. The result is a copy.
func (b *Buffer) Since(ms int64) []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Record, 0, b.count)
	start := 0
	if b.count == len(b.records) {
		start = b.next
	}
	for i := 0; i < b.count; i++ {
		record := b.records[(start+i)%len(b.records)]
		if record.At >= ms {
			out = append(out, record)
		}
	}
	return out
}

// Handler returns a slog.Handler that passes every record to next and, for
// records at WARN or above, also stores it in b. Attributes added with
// WithAttrs and groups opened with WithGroup are kept and rendered into
// Record.Attrs as "key=value" pairs joined by spaces, groups as "group.key"
// (design.md §4, REQ-DSH-009).
func Handler(next slog.Handler, b *Buffer) slog.Handler {
	return &teeHandler{next: next, buffer: b}
}

// teeHandler forwards every record to the wrapped handler and mirrors the
// warnings and errors into a Buffer.
type teeHandler struct {
	next   slog.Handler
	buffer *Buffer
	// attrs holds attributes captured with WithAttrs, pre-rendered with the
	// groups that were open at the time of the call.
	attrs []string
	// groups is the prefix of the group stack open now.
	groups []string
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		at := record.Time
		if at.IsZero() {
			at = time.Now()
		}
		h.buffer.Add(Record{
			At:      at.UnixMilli(),
			Level:   record.Level,
			Message: record.Message,
			Attrs:   h.attrsString(record),
		})
	}
	return h.next.Handle(ctx, record)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append([]string(nil), h.attrs...)
	for _, attr := range attrs {
		if rendered := renderAttr(h.groups, attr); rendered != "" {
			clone.attrs = append(clone.attrs, rendered)
		}
	}
	return &clone
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

// attrsString renders the WithAttrs attributes captured so far plus the
// record's own attributes under the groups open now.
func (h *teeHandler) attrsString(record slog.Record) string {
	parts := append([]string(nil), h.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		if rendered := renderAttr(h.groups, attr); rendered != "" {
			parts = append(parts, rendered)
		}
		return true
	})
	return strings.Join(parts, " ")
}

// renderAttr renders one attribute as "key=value". A group value flattens its
// children under the group name; an inline group (empty key) adds no segment.
func renderAttr(groups []string, attr slog.Attr) string {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return ""
	}
	if attr.Value.Kind() == slog.KindGroup {
		inner := groups
		if attr.Key != "" {
			inner = append(append([]string(nil), groups...), attr.Key)
		}
		parts := make([]string, 0, len(attr.Value.Group()))
		for _, child := range attr.Value.Group() {
			if rendered := renderAttr(inner, child); rendered != "" {
				parts = append(parts, rendered)
			}
		}
		return strings.Join(parts, " ")
	}
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(append(append([]string(nil), groups...), key), ".")
	}
	return key + "=" + attr.Value.String()
}

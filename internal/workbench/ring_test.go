package workbench

import (
	"bytes"
	"testing"
)

// TestRingSnapshotMarksInOrder checks that a replay carries the size marker
// before the bytes it applies to, in buffer order.
func TestRingSnapshotMarksInOrder(t *testing.T) {
	r := newRing(1024)
	r.mark(80, 24)
	r.write([]byte("hello"))
	r.mark(100, 40)
	r.write([]byte("world"))

	want := string(sizeMarker(80, 24)) + "hello" + string(sizeMarker(100, 40)) + "world" + replayEndMarker
	if got := string(r.snapshot()); got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

// TestRingBaseAfterTrim checks that a mark trimmed off the front becomes the
// base size, replayed before the retained bytes, and that the marks left shift
// down by what was trimmed.
func TestRingBaseAfterTrim(t *testing.T) {
	r := newRing(5)
	r.mark(80, 24)
	r.write([]byte("ab"))
	r.mark(100, 40)
	r.write([]byte("cdef"))

	// "abcdef" overflows to "bcdef": the first mark fell off and is now the
	// base, and the second mark moved from offset 2 to offset 1.
	want := string(sizeMarker(80, 24)) + "b" + string(sizeMarker(100, 40)) + "cdef" + replayEndMarker
	if got := string(r.snapshot()); got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

// TestRingCollapseSameOffset checks that two marks at the same offset collapse
// into the last one.
func TestRingCollapseSameOffset(t *testing.T) {
	r := newRing(64)
	r.write([]byte("x"))
	r.mark(80, 24)
	r.mark(100, 40)

	want := "x" + string(sizeMarker(100, 40)) + replayEndMarker
	if got := string(r.snapshot()); got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

// TestRingSameSizeMarkIsNoop checks that a mark equal to the size already in
// effect, the base here, adds nothing.
func TestRingSameSizeMarkIsNoop(t *testing.T) {
	r := newRing(2)
	r.mark(80, 24)
	r.write([]byte("abc"))
	r.mark(80, 24)

	// The trim moved the first mark's size to the base, and the later mark of
	// the same size is a no-op.
	want := string(sizeMarker(80, 24)) + "bc" + replayEndMarker
	if got := string(r.snapshot()); got != want {
		t.Fatalf("snapshot = %q, want %q", got, want)
	}
}

// TestRingReplayEndLast checks that the replay-end marker is the very last
// bytes of a replay.
func TestRingReplayEndLast(t *testing.T) {
	r := newRing(1024)
	r.mark(80, 24)
	r.write([]byte("data"))

	if got := r.snapshot(); !bytes.HasSuffix(got, []byte(replayEndMarker)) {
		t.Fatalf("snapshot does not end with the replay-end marker: %q", got)
	}
}

// TestRingEmptySnapshot checks that a ring with no size and no bytes replays
// only the replay-end marker.
func TestRingEmptySnapshot(t *testing.T) {
	r := newRing(1024)
	if got := string(r.snapshot()); got != replayEndMarker {
		t.Fatalf("empty snapshot = %q, want %q", got, replayEndMarker)
	}
}

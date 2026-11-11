package roommemory

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRoomMemoryLifecycle(t *testing.T) {
	s := New(t.TempDir())
	tick := time.UnixMilli(1000)
	s.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	old, err := s.Add("career", KindFact, "Target nego: Rp 30 jt.", "dana", "d_1", "")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.Add("career", KindFact, "Target nego: Rp 31 jt.", "dana", "d_2", "target nego: rp 30 jt.")
	if err != nil {
		t.Fatal(err)
	}
	notes, _ := s.List("career")
	if len(notes) != 2 || notes[0].ID != newer.ID || notes[1].ReplacedBy != newer.ID || notes[1].ValidUntil == 0 {
		t.Fatalf("notes=%+v", notes)
	}
	if p := s.Prompt("career", 4000); !strings.Contains(p, "31 jt") || strings.Contains(p, "30 jt") {
		t.Fatalf("prompt=%q", p)
	}
	kind := KindDecision
	if _, err := s.Update("career", old.ID, &kind, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("career", old.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("career", old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err=%v", err)
	}
	if _, err := s.Add("career", "wish", "x", "", "owner", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad kind err=%v", err)
	}
	if _, err := s.Add("career", KindFact, strings.Repeat("a", MaxText+1), "", "owner", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long text err=%v", err)
	}
	if _, err := s.List("../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad team err=%v", err)
	}
	if notes, _ := New(t.TempDir()).List("empty"); len(notes) != 0 {
		t.Fatalf("empty room=%+v", notes)
	}
}

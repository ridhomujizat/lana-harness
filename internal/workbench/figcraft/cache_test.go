package figcraft

import (
	"testing"
)

func TestCacheLRUEvicts(t *testing.T) {
	c := newCache()
	c.max = 10
	c.put("a", infoData{Pages: []Page{{ID: 1}}}, 6)
	c.put("b", infoData{Pages: []Page{{ID: 2}}}, 6)

	if _, ok := c.get("a"); ok {
		t.Error("a survived, want it evicted as the least recently used")
	}
	if _, ok := c.get("b"); !ok {
		t.Error("b was evicted, want it kept")
	}
}

func TestCacheGetMovesToFront(t *testing.T) {
	c := newCache()
	c.max = 12
	c.put("a", infoData{Pages: []Page{{ID: 1}}}, 6)
	c.put("b", infoData{Pages: []Page{{ID: 2}}}, 6)
	if _, ok := c.get("a"); !ok {
		t.Fatal("a missing")
	}
	// Touching a makes b the least recently used, so the new entry evicts b.
	c.put("c", infoData{Pages: []Page{{ID: 3}}}, 6)

	if _, ok := c.get("a"); !ok {
		t.Error("a was evicted, want it kept")
	}
	if _, ok := c.get("b"); ok {
		t.Error("b survived, want it evicted")
	}
	if _, ok := c.get("c"); !ok {
		t.Error("c missing")
	}
}

func TestCacheReplacesExistingKey(t *testing.T) {
	c := newCache()
	c.max = 10
	c.put("k", infoData{Pages: []Page{{ID: 1}}}, 6)
	c.put("k", infoData{Pages: []Page{{ID: 2}}}, 6)

	if c.total != 6 {
		t.Errorf("total = %d, want 6 after a replace", c.total)
	}
	v, ok := c.get("k")
	if !ok {
		t.Fatal("k missing")
	}
	if got := v.(infoData).Pages[0].ID; got != 2 {
		t.Errorf("value = %d, want the replacement 2", got)
	}
}

func TestCacheMisses(t *testing.T) {
	c := newCache()
	if _, ok := c.get("nope"); ok {
		t.Error("empty cache returned a hit")
	}
}

func TestMeasure(t *testing.T) {
	if got := measure(Render{PNG: []byte{1, 2, 3}}); got != 3 {
		t.Errorf("render bytes = %d, want the PNG length", got)
	}
	if got := measure(infoData{Pages: []Page{{ID: 1, Name: "x"}}}); got <= 0 {
		t.Errorf("info bytes = %d, want a positive JSON size", got)
	}
}

func TestStaleStore(t *testing.T) {
	s := newStaleStore()
	if _, ok := s.get("k"); ok {
		t.Error("empty store returned a hit")
	}
	s.put("k", infoData{Pages: []Page{{ID: 7}}})
	v, ok := s.get("k")
	if !ok {
		t.Fatal("k missing")
	}
	if got := v.(infoData).Pages[0].ID; got != 7 {
		t.Errorf("value = %d, want 7", got)
	}
}

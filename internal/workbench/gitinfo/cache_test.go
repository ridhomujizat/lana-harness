package gitinfo

import (
	"testing"
	"time"
)

func TestListCache(t *testing.T) {
	var c listCache
	now := time.Now()
	if _, ok := c.get("k", now); ok {
		t.Fatal("empty cache returned a hit")
	}
	c.put("k", []string{"a"}, now)
	if v, ok := c.get("k", now.Add(time.Second)); !ok || len(v) != 1 || v[0] != "a" {
		t.Fatalf("get = %v/%v, want [a]/true", v, ok)
	}
	if _, ok := c.get("k", now.Add(lsFilesTTL)); ok {
		t.Error("entry still cached at the window boundary")
	}
}

func TestWorktreesCacheInvalidate(t *testing.T) {
	var c worktreesCache
	now := time.Now()
	c.put("k1", "repo-a", WorktreeList{Repo: "repo-a"}, now)
	c.put("k2", "repo-b", WorktreeList{Repo: "repo-b"}, now)

	c.invalidate("repo-a")
	if _, ok := c.get("k1", now); ok {
		t.Error("repo-a entry survived invalidate")
	}
	if _, ok := c.get("k2", now); !ok {
		t.Error("repo-b entry was dropped")
	}
	if _, ok := c.get("k2", now.Add(worktreesTTL)); ok {
		t.Error("repo-b entry still cached at the window boundary")
	}
	c.invalidate("unknown")
}

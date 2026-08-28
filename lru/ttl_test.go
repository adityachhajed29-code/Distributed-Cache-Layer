package lru

import (
	"testing"
	"time"
)

func TestTTLExpiryAndOverwrite(t *testing.T) {
	c := NewLruCache(2)
	c.PutTTL("expired", "a", time.Hour)
	c.cache["expired"].expiresAt = time.Now().Add(-time.Second)
	if _, err := c.Get("expired"); err == nil || c.size != 0 {
		t.Fatal("expired record remained visible")
	}
	c.PutTTL("renewed", "old", time.Hour)
	c.Put("renewed", "new")
	if removed := c.Sweep(time.Now().Add(2 * time.Hour)); removed != 0 {
		t.Fatal("unlimited overwrite retained old expiration")
	}
	c.PutTTL("temporary", "v", time.Hour)
	if removed := c.Sweep(time.Now().Add(2 * time.Hour)); removed != 1 {
		t.Fatalf("sweep removed %d entries", removed)
	}
	c.PutTTL("renewed", "v", -time.Second)
	if c.size != 0 {
		t.Fatal("negative TTL did not evict")
	}
	c.Put("after", "v")
	c.Delete("after")
	c.Delete("absent")
	if c.head.next != c.tail || c.tail.prev != c.head {
		t.Fatal("broken list")
	}
}

func TestZeroCapacity(t *testing.T) {
	c := NewLruCache(0)
	c.Put("key", "value")
	if _, err := c.Get("key"); err == nil {
		t.Fatal("zero capacity retained value")
	}
}

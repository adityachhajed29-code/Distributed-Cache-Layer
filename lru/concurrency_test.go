package lru

import (
	"strconv"
	"sync"
	"testing"
)

// Concurrent hits mutate recency links and must serialize with other hits.
func TestConcurrentHitsPreserveList(t *testing.T) {
	cache := NewLruCache(32)
	for i := 0; i < 32; i++ {
		cache.Put(strconv.Itoa(i), "value")
	}
	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Add(1)
		go func(offset int) {
			defer workers.Done()
			for i := 0; i < 1000; i++ {
				if _, err := cache.Get(strconv.Itoa((i + offset) % 32)); err != nil {
					t.Error(err)
				}
			}
		}(worker)
	}
	workers.Wait()
	seen := make(map[*LLNode]bool)
	previous := cache.head
	for current := cache.head.next; current != cache.tail; current = current.next {
		if current == nil || seen[current] || current.prev != previous {
			t.Fatal("corrupt recency list")
		}
		seen[current] = true
		previous = current
	}
	if len(seen) != 32 || cache.tail.prev != previous {
		t.Fatal("recency list lost entries")
	}
}

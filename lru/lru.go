// LRU Cache implementation which uses a hashmap and doubly-linked list
// to achieve O(1) get/put operations and O(1) eviction.
package lru

import (
	"context"
	"errors"
	"sync"
	"time"
)

// doubly-linked list node
type LLNode struct {
	prev      *LLNode
	next      *LLNode
	key       string
	val       string
	expiresAt time.Time
}

// Lru cache which uses a hashmap and doubly-linked list to acheive O(1) time for get/put/evict operations
type LruCache struct {
	cache    map[string]*LLNode
	head     *LLNode
	tail     *LLNode
	capacity int
	size     int
	mutex    sync.RWMutex
}

// initialize and return new lru cache of specified capacity
func NewLruCache(capacity int) LruCache {
	if capacity < 0 {
		panic("cache capacity must be nonnegative")
	}
	lruCache := LruCache{
		cache:    make(map[string]*LLNode, capacity),
		head:     &LLNode{prev: nil, next: nil, key: "", val: ""},
		tail:     &LLNode{prev: nil, next: nil, key: "", val: ""},
		capacity: capacity,
		size:     0,
	}
	lruCache.head.next = lruCache.tail
	lruCache.tail.prev = lruCache.head
	return lruCache
}

func (lru *LruCache) Get(key string) (string, error) {
	// Reads update recency and require an exclusive lock.
	lru.mutex.Lock()
	defer lru.mutex.Unlock()

	// case 1: key in cache, move to head of list and return
	if node, ok := lru.cache[key]; ok {
		if !node.expiresAt.IsZero() && !time.Now().Before(node.expiresAt) {
			lru.remove(node)
			return "", errors.New("element expired")
		}
		lru.moveNodeToHead(node)
		return node.val, nil
	}
	// case 2: key does not exist
	return "", errors.New("element does not exist in cache")
}

func (lru *LruCache) Put(key string, value string) {
	lru.PutTTL(key, value, 0)
}

// PutTTL uses zero for unlimited lifetime and negative TTL for immediate eviction.
func (lru *LruCache) PutTTL(key string, value string, ttl time.Duration) {
	// write lock
	lru.mutex.Lock()
	defer lru.mutex.Unlock()
	if ttl < 0 {
		if node, ok := lru.cache[key]; ok {
			lru.remove(node)
		}
		return
	}
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// case 1: in cache already
	if node, ok := lru.cache[key]; ok {
		// update value and move to head
		node.val = value
		node.expiresAt = expiresAt
		lru.moveNodeToHead(node)
		return
	}

	// case 2: create node and add to head
	node := LLNode{prev: lru.head, next: lru.head.next, key: key, val: value, expiresAt: expiresAt}

	// map from key to node ptr
	lru.cache[key] = &node

	// next node links
	node.next = lru.head.next
	lru.head.next.prev = &node

	// head links
	lru.head.next = &node
	node.prev = lru.head

	// increment size
	lru.size += 1

	// evict if over capacity
	if lru.size > lru.capacity {
		lru.evict()
		lru.size -= 1
	}
}

func (lru *LruCache) remove(node *LLNode) {
	node.prev.next = node.next
	node.next.prev = node.prev
	delete(lru.cache, node.key)
	lru.size--
}

func (lru *LruCache) Delete(key string) {
	lru.mutex.Lock()
	defer lru.mutex.Unlock()
	if node, ok := lru.cache[key]; ok {
		lru.remove(node)
	}
}

func (lru *LruCache) Sweep(now time.Time) int {
	lru.mutex.Lock()
	defer lru.mutex.Unlock()
	removed := 0
	for _, node := range lru.cache {
		if !node.expiresAt.IsZero() && !now.Before(node.expiresAt) {
			lru.remove(node)
			removed++
		}
	}
	return removed
}

// RunSweeper blocks until cancellation. The caller owns its goroutine lifecycle.
func (lru *LruCache) RunSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			lru.Sweep(now)
		}
	}
}

func (lru *LruCache) moveNodeToHead(node *LLNode) {
	// remove from middle
	prev := node.prev
	next := node.next
	if prev != nil {
		prev.next = next
	}
	if next != nil {
		next.prev = prev
	}

	// add to front
	node.next = lru.head.next
	lru.head.next.prev = node
	node.prev = lru.head
	lru.head.next = node
}

func (lru *LruCache) evict() {
	// delete node from tail
	node := lru.tail.prev
	prev := lru.tail.prev.prev
	prev.next = lru.tail
	lru.tail.prev = prev

	// delete entry from map
	delete(lru.cache, node.key)
}

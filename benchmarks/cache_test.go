package benchmarks

import (
	"github.com/malwaredllc/minicache/lru"
	"testing"
)

func BenchmarkCacheHit(b *testing.B) {
	cache := lru.NewLruCache(1000)
	cache.Put("hot", "value")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := cache.Get("hot"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

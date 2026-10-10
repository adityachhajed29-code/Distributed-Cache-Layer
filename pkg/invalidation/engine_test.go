package invalidation

import (
	"context"
	"database/sql"
	"github.com/malwaredllc/minicache/pkg/storage"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu    sync.Mutex
	data  map[string]storage.Record
	reads int
}

func (m *memoryStore) Get(ctx context.Context, key string) (storage.Record, error) {
	time.Sleep(10 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	r, ok := m.data[key]
	if !ok || r.Deleted {
		return r, sql.ErrNoRows
	}
	return r, nil
}
func (m *memoryStore) Write(ctx context.Context, r storage.Record) (storage.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Version = m.data[r.Key].Version + 1
	m.data[r.Key] = r
	return r, nil
}
func TestSingleflightAndPolicies(t *testing.T) {
	for _, policy := range []string{"cache-aside", "write-through", "write-behind"} {
		t.Run(policy, func(t *testing.T) {
			store := &memoryStore{data: map[string]storage.Record{"hot": {Key: "hot", Val: []byte("value"), Version: 1}}}
			engine, err := New(store, 10, time.Minute, policy, "test")
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					g, err := engine.Get(context.Background(), "hot")
					if err != nil || string(g.Val) != "value" {
						t.Errorf("bad read %v", err)
					}
				}()
			}
			wg.Wait()
			store.mu.Lock()
			reads := store.reads
			store.mu.Unlock()
			if reads != 1 {
				t.Fatalf("database reads = %d", reads)
			}
			for _, value := range []string{"a", "b", "a"} {
				if _, err := engine.Write(context.Background(), storage.Record{Key: "hot", Val: []byte(value)}, 0); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := engine.Close(ctx); err != nil {
				t.Fatal(err)
			}
			r, _ := store.Get(ctx, "hot")
			if string(r.Val) != "a" || r.Version != 4 {
				t.Fatalf("unexpected final record %+v", r)
			}
		})
	}
}

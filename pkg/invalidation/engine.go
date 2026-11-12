package invalidation

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/malwaredllc/minicache/lru"
	"github.com/malwaredllc/minicache/pkg/stampede"
	"github.com/malwaredllc/minicache/pkg/storage"
	"golang.org/x/sync/singleflight"
)

var ErrQueueFull = errors.New("write queue full")
var ErrClosed = errors.New("cache is closing")

type Granule struct {
	storage.Record
	sequence    uint64
	TTLMS       int64   `json:"ttl_ms"`
	DBLatencyMS float64 `json:"db_latency_ms"`
	Provenance  struct {
		NodeID   string `json:"node_id"`
		Checksum string `json:"checksum"`
	} `json:"provenance"`
	ExpiresAt time.Time `json:"expires_at"`
}

type mutation struct {
	record   storage.Record
	ttl      time.Duration
	sequence uint64
}

type Engine struct {
	store        storage.Store
	cache        *lru.LruCache
	policy, node string
	ttl          time.Duration
	locks        [256]sync.Mutex
	group        singleflight.Group
	refresh      chan struct{}
	queue        chan mutation
	pending      sync.Map
	lifecycle    sync.RWMutex
	closed       bool
	done         chan struct{}
	cancel       context.CancelFunc
	Reads        atomic.Int64
	sequence     atomic.Uint64
}

func New(store storage.Store, capacity int, ttl time.Duration, policy, node string) (*Engine, error) {
	if ttl <= 0 || capacity <= 0 {
		return nil, errors.New("positive TTL and capacity required")
	}
	if policy != "cache-aside" && policy != "write-through" && policy != "write-behind" {
		return nil, errors.New("unknown write policy")
	}
	cache := lru.NewLruCache(capacity)
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{store: store, cache: &cache, ttl: ttl, policy: policy, node: node, refresh: make(chan struct{}, 16), queue: make(chan mutation, 1024), done: make(chan struct{}), cancel: cancel}
	go cache.RunSweeper(ctx, time.Second)
	go e.flush()
	return e, nil
}

func (e *Engine) lock(key string) *sync.Mutex { return &e.locks[crc32.ChecksumIEEE([]byte(key))%256] }

func (e *Engine) granule(r storage.Record, ttl, latency time.Duration) Granule {
	g := Granule{Record: r, TTLMS: ttl.Milliseconds(), DBLatencyMS: float64(latency) / float64(time.Millisecond), ExpiresAt: time.Now().Add(ttl)}
	g.Provenance.NodeID = e.node
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d:%x", len(r.Key), r.Key, r.Version, r.Val)))
	g.Provenance.Checksum = hex.EncodeToString(sum[:])
	return g
}

func (e *Engine) cached(key string) (Granule, bool) {
	if value, ok := e.pending.Load(key); ok {
		return value.(Granule), true
	}
	raw, err := e.cache.Get(key)
	var g Granule
	if err != nil || json.Unmarshal([]byte(raw), &g) != nil {
		return g, false
	}
	return g, true
}

func (e *Engine) save(g Granule) {
	raw, _ := json.Marshal(g)
	e.cache.PutTTL(g.Key, string(raw), time.Until(g.ExpiresAt))
}

func (e *Engine) Get(ctx context.Context, key string) (Granule, error) {
	normalized, err := NormalizeKey(key)
	if err != nil {
		return Granule{}, err
	}
	key = normalized
	if g, ok := e.cached(key); ok {
		if g.Deleted {
			return Granule{}, sql.ErrNoRows
		}
		if stampede.Refresh(time.Until(g.ExpiresAt), time.Duration(g.DBLatencyMS*float64(time.Millisecond)), rand.Float64()) {
			select {
			case e.refresh <- struct{}{}:
				go func() {
					defer func() { <-e.refresh }()
					c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					e.load(c, key, true)
				}()
			default:
			}
		}
		return g, nil
	}
	return e.load(ctx, key, false)
}

func (e *Engine) load(ctx context.Context, key string, force bool) (Granule, error) {
	result := e.group.DoChan(key, func() (interface{}, error) {
		lock := e.lock(key)
		lock.Lock()
		defer lock.Unlock()
		if g, ok := e.cached(key); ok {
			_, pending := e.pending.Load(key)
			if !force || pending {
				if g.Deleted {
					return Granule{}, sql.ErrNoRows
				}
				return g, nil
			}
		}
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		start := time.Now()
		e.Reads.Add(1)
		r, err := e.store.Get(c, key)
		if err != nil {
			return Granule{}, err
		}
		g := e.granule(r, e.ttl, time.Since(start))
		e.save(g)
		return g, nil
	})
	select {
	case <-ctx.Done():
		return Granule{}, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return Granule{}, result.Err
		}
		return result.Val.(Granule), nil
	}
}

func (e *Engine) Write(ctx context.Context, r storage.Record, ttl time.Duration) (Granule, error) {
	key, err := NormalizeKey(r.Key)
	if err != nil {
		return Granule{}, err
	}
	r.Key = key
	if len(r.Val) > 512*1024 {
		return Granule{}, errors.New("payload exceeds 512 KiB")
	}
	if ttl > 365*24*time.Hour {
		return Granule{}, errors.New("TTL exceeds 365 days")
	}
	e.lifecycle.RLock()
	defer e.lifecycle.RUnlock()
	if e.closed {
		return Granule{}, ErrClosed
	}
	if ttl == 0 {
		ttl = e.ttl
	}
	if ttl < 0 {
		return Granule{}, errors.New("TTL must be positive")
	}
	r.Val = append([]byte(nil), r.Val...)
	var operation [16]byte
	if _, err := cryptorand.Read(operation[:]); err != nil {
		return Granule{}, err
	}
	r.OperationID = hex.EncodeToString(operation[:])
	lock := e.lock(r.Key)
	lock.Lock()
	defer lock.Unlock()
	if e.policy == "write-behind" {
		// The worker takes the same key lock before consuming pending state.
		sequence := e.sequence.Add(1)
		select {
		case e.queue <- mutation{r, ttl, sequence}:
			g := e.granule(r, ttl, 0)
			g.sequence = sequence
			e.pending.Store(r.Key, g)
			e.cache.Delete(r.Key)
			return g, nil
		default:
			return Granule{}, ErrQueueFull
		}
	}
	start := time.Now()
	saved, err := e.store.Write(ctx, r)
	if err != nil {
		return Granule{}, err
	}
	g := e.granule(saved, ttl, time.Since(start))
	e.cache.Delete(r.Key)
	if e.policy == "write-through" && !r.Deleted {
		e.save(g)
	}
	return g, nil
}

func (e *Engine) flush() {
	defer close(e.done)
	for first := range e.queue {
		batch := []mutation{first}
		timer := time.NewTimer(20 * time.Millisecond)
	collect:
		for len(batch) < 64 {
			select {
			case next, ok := <-e.queue:
				if !ok {
					break collect
				}
				batch = append(batch, next)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		records := make([]storage.Record, len(batch))
		for i, m := range batch {
			records[i] = m.record
		}
		delay := 50 * time.Millisecond
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			saved, err := e.writeBatch(ctx, records)
			cancel()
			if err == nil {
				for i, m := range batch {
					lock := e.lock(m.record.Key)
					lock.Lock()
					if v, ok := e.pending.Load(m.record.Key); ok && v.(Granule).sequence == m.sequence {
						e.pending.Delete(m.record.Key)
						if !saved[i].Deleted {
							e.save(e.granule(saved[i], m.ttl, 0))
						}
					}
					lock.Unlock()
				}
				break
			}
			time.Sleep(delay)
			if delay < time.Second {
				delay *= 2
			}
		}
	}
}

func (e *Engine) writeBatch(ctx context.Context, records []storage.Record) ([]storage.Record, error) {
	if batcher, ok := e.store.(interface {
		WriteBatch(context.Context, []storage.Record) ([]storage.Record, error)
	}); ok {
		return batcher.WriteBatch(ctx, records)
	}
	result := make([]storage.Record, 0, len(records))
	for _, r := range records {
		saved, err := e.store.Write(ctx, r)
		if err != nil {
			return nil, err
		}
		result = append(result, saved)
	}
	return result, nil
}

// Close stops admission and waits for accepted writes to drain up to the deadline.
func (e *Engine) Close(ctx context.Context) error {
	e.lifecycle.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
		e.cancel()
	}
	e.lifecycle.Unlock()
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

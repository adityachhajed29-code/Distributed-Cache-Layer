package main

import (
	"context"
	"github.com/malwaredllc/minicache/ring"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The gateway maintains a health-filtered snapshot of configured shard endpoints.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	peers := strings.Split(os.Getenv("SHARDS"), ",")
	if len(peers) == 0 || peers[0] == "" {
		log.Fatal("SHARDS is required")
	}
	proxies := map[string]*httputil.ReverseProxy{}
	for _, peer := range peers {
		u, err := url.Parse(peer)
		if err != nil || u.Host == "" {
			log.Fatal("invalid shard URL")
		}
		proxies[peer] = httputil.NewSingleHostReverseProxy(u)
	}
	var mu sync.RWMutex
	var active *ring.Ring
	count := 0
	client := &http.Client{Timeout: 500 * time.Millisecond}
	refresh := func() {
		next := ring.NewVirtualRing()
		n := 0
		for _, peer := range peers {
			req, _ := http.NewRequestWithContext(ctx, "GET", peer+"/health", nil)
			response, err := client.Do(req)
			if err != nil {
				continue
			}
			response.Body.Close()
			if response.StatusCode == 200 {
				next.AddNode(peer, peer, 0, 0)
				n++
			}
		}
		mu.Lock()
		active = next
		count = n
		mu.Unlock()
	}
	refresh()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		n := count
		mu.RUnlock()
		if n == 0 {
			w.WriteHeader(503)
		}
	})
	mux.HandleFunc("/v1/cache/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.ToLower(strings.TrimPrefix(r.URL.Path, "/v1/cache/"))
		mu.RLock()
		if count == 0 {
			mu.RUnlock()
			http.Error(w, "no healthy shards", 503)
			return
		}
		owner := active.Get(key)
		mu.RUnlock()
		proxies[owner].ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

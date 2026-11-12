package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/malwaredllc/minicache/ring"
)

type gateway struct {
	mu       sync.RWMutex
	known    map[string]bool
	active   *ring.Ring
	proxies  map[string]*httputil.ReverseProxy
	client   *http.Client
	discover bool
}

func newGateway(peers []string, discover bool) (*gateway, error) {
	g := &gateway{known: map[string]bool{}, proxies: map[string]*httputil.ReverseProxy{}, client: &http.Client{Timeout: 500 * time.Millisecond}, discover: discover}
	for _, peer := range peers {
		if !validEndpoint(peer) {
			return nil, errors.New("invalid peer URL")
		}
		g.known[peer] = true
	}
	if len(g.known) == 0 {
		return nil, errors.New("at least one peer required")
	}
	return g, nil
}

func validEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == ""
}

// refresh serially publishes a complete health-filtered membership snapshot.
func (g *gateway) refresh(ctx context.Context) {
	g.mu.RLock()
	known := make(map[string]bool, len(g.known))
	for peer := range g.known {
		known[peer] = true
	}
	g.mu.RUnlock()
	if g.discover {
		for peer := range known {
			req, _ := http.NewRequestWithContext(ctx, "GET", peer+"/members", nil)
			response, err := g.client.Do(req)
			if err != nil {
				continue
			}
			var members []string
			if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&members) == nil {
				for _, member := range members {
					if validEndpoint(member) {
						known[member] = true
					}
				}
			}
			response.Body.Close()
		}
	}
	endpoints := make([]string, 0, len(known))
	for peer := range known {
		endpoints = append(endpoints, peer)
	}
	sort.Strings(endpoints)
	next := ring.NewVirtualRing()
	proxies := map[string]*httputil.ReverseProxy{}
	for _, peer := range endpoints {
		req, _ := http.NewRequestWithContext(ctx, "GET", peer+"/health", nil)
		response, err := g.client.Do(req)
		if err != nil {
			continue
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			continue
		}
		target, _ := url.Parse(peer)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = &http.Transport{ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second}
		proxies[peer] = proxy
		next.AddNode(peer, peer, 0, 0)
	}
	g.mu.Lock()
	old := g.proxies
	g.known = known
	g.active = next
	g.proxies = proxies
	g.mu.Unlock()
	for _, proxy := range old {
		proxy.Transport.(*http.Transport).CloseIdleConnections()
	}
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	if len(g.proxies) == 0 {
		g.mu.RUnlock()
		http.Error(w, "no healthy peers", 503)
		return
	}
	if r.URL.Path == "/health" {
		g.mu.RUnlock()
		w.WriteHeader(200)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/cache/") {
		g.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	key := strings.ToLower(strings.TrimPrefix(r.URL.Path, "/v1/cache/"))
	proxy := g.proxies[g.active.Get(key)]
	g.mu.RUnlock()
	proxy.ServeHTTP(w, r)
}

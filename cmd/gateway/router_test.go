package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDiscoveryAndFailureRecovery(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	discovered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer discovered.Close()
	seed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/members" {
			json.NewEncoder(w).Encode([]string{discovered.URL})
			return
		}
		w.WriteHeader(200)
	}))
	defer seed.Close()
	g, err := newGateway([]string{seed.URL}, true)
	if err != nil {
		t.Fatal(err)
	}
	g.refresh(context.Background())
	if len(g.proxies) != 2 {
		t.Fatal("new member not discovered")
	}
	healthy.Store(false)
	g.refresh(context.Background())
	if len(g.proxies) != 1 {
		t.Fatal("unhealthy peer retained")
	}
	result := httptest.NewRecorder()
	g.ServeHTTP(result, httptest.NewRequest("GET", "/v1/cache/key", nil))
	if result.Code != 200 {
		t.Fatalf("failover returned %d", result.Code)
	}
	healthy.Store(true)
	g.refresh(context.Background())
	if len(g.proxies) != 2 {
		t.Fatal("recovered peer not restored")
	}
}

func TestCoordinatorFailover(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer backup.Close()
	g, _ := newGateway([]string{primary.URL, backup.URL}, false)
	g.refresh(context.Background())
	primary.Close()
	g.refresh(context.Background())
	result := httptest.NewRecorder()
	g.ServeHTTP(result, httptest.NewRequest("GET", "/v1/cache/key", nil))
	if result.Code != 200 || len(g.proxies) != 1 {
		t.Fatal("coordinator failover failed")
	}
	backup.Close()
	g.refresh(context.Background())
	result = httptest.NewRecorder()
	g.ServeHTTP(result, httptest.NewRequest("GET", "/health", nil))
	if result.Code != 503 {
		t.Fatal("empty cluster reported healthy")
	}
}

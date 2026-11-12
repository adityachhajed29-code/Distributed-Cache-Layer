package server

import (
	"context"
	"encoding/json"
	"github.com/malwaredllc/minicache/node"
	"google.golang.org/grpc"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBullyElectionAndLeaderFailure(t *testing.T) {
	directory := t.TempDir()
	config := node.NodesConfig{Nodes: map[string]*node.Node{}, ServerLogfile: "stdout", ServerErrfile: "stderr"}
	listeners := make([]net.Listener, 3)
	ids := []string{"a", "b", "c"}
	for i, id := range ids {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners[i] = listener
		config.Nodes[id] = node.NewNode(id, "127.0.0.1", 0, int32(listener.Addr().(*net.TCPAddr).Port))
	}
	raw, _ := json.Marshal(config)
	path := filepath.Join(directory, "nodes.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	servers := make([]*CacheServer, 3)
	transports := make([]*grpc.Server, 3)
	for i, id := range ids {
		transports[i], servers[i] = NewCacheServer(10, path, false, id, false, false)
		go transports[i].Serve(listeners[i])
	}
	defer func() {
		for i, server := range servers {
			transports[i].Stop()
			server.Close(context.Background())
			listeners[i].Close()
		}
	}()
	waitLeader := func(expected string, count int) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			all := true
			for _, server := range servers[:count] {
				if server.currentLeader() != expected {
					all = false
				}
			}
			if all {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		for _, server := range servers[:count] {
			t.Log(server.nodeID, server.currentLeader())
		}
		t.Fatal("election did not converge")
	}
	go servers[0].RunElection()
	waitLeader("c", 3)
	transports[2].Stop()
	servers[2].Close(context.Background())
	go servers[0].StartLeaderHeartbeatMonitor()
	go servers[1].StartLeaderHeartbeatMonitor()
	waitLeader("b", 2)
}

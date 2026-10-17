package server

import "github.com/malwaredllc/minicache/node"

func (s *CacheServer) nodeSnapshot() map[string]*node.Node {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	result := make(map[string]*node.Node, len(s.nodesConfig.Nodes))
	for id, n := range s.nodesConfig.Nodes {
		result[id] = n
	}
	return result
}
func (s *CacheServer) removeMember(id string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	delete(s.nodesConfig.Nodes, id)
}
func (s *CacheServer) currentLeader() string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.leaderID
}
func (s *CacheServer) setLeader(id string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.leaderID = id
}

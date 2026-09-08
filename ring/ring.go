package ring

import (
	"errors"
	"fmt"
	"github.com/malwaredllc/minicache/node"
	"sort"
	"sync"
)

var ErrNodeNotFound = errors.New("node not found")

type Ring struct {
	Nodes    node.Nodes
	points   []point
	replicas int
	sync.Mutex
}

type point struct {
	hash  uint32
	owner string
}

func NewVirtualRing() *Ring { return &Ring{replicas: 50} }

func (r *Ring) rebuild() {
	r.points = nil
	for _, n := range r.Nodes {
		for i := 0; i < r.replicas; i++ {
			r.points = append(r.points, point{node.HashId(fmt.Sprintf("%s#%d", n.Id, i)), n.Id})
		}
	}
	sort.Slice(r.points, func(i, j int) bool {
		if r.points[i].hash == r.points[j].hash {
			return r.points[i].owner < r.points[j].owner
		}
		return r.points[i].hash < r.points[j].hash
	})
}

func NewRing() *Ring {
	return &Ring{Nodes: node.Nodes{}}
}

func (r *Ring) AddNode(id string, host string, restPort int32, grpcPort int32) {
	r.Lock()
	defer r.Unlock()
	for _, existing := range r.Nodes {
		if existing.Id == id {
			return
		}
	}

	node := node.NewNode(id, host, restPort, grpcPort)
	r.Nodes = append(r.Nodes, node)

	sort.Sort(r.Nodes)
	r.rebuild()
}

func (r *Ring) RemoveNode(id string) error {
	r.Lock()
	defer r.Unlock()

	i := -1
	for index, n := range r.Nodes {
		if n.Id == id {
			i = index
			break
		}
	}
	if i < 0 {
		return ErrNodeNotFound
	}

	r.Nodes = append(r.Nodes[:i], r.Nodes[i+1:]...)
	r.rebuild()

	return nil
}

func (r *Ring) Get(id string) string {
	r.Lock()
	defer r.Unlock()
	// handle empty ring
	if len(r.Nodes) == 0 {
		panic("CONSISTENT HASHING RING IS EMPTY")
	}
	if len(r.points) > 0 {
		hash := node.HashId(id)
		i := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash >= hash })
		if i == len(r.points) {
			i = 0
		}
		return r.points[i].owner
	}
	i := r.search(id)
	if i >= r.Nodes.Len() {
		i = 0
	}

	return r.Nodes[i].Id
}

func (r *Ring) search(id string) int {
	searchfn := func(i int) bool {
		return r.Nodes[i].HashId >= node.HashId(id)
	}

	return sort.Search(r.Nodes.Len(), searchfn)
}

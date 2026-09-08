package ring

import (
	"fmt"
	"testing"
)

func TestVirtualNodeRemapping(t *testing.T) {
	r := NewVirtualRing()
	for _, id := range []string{"a", "b", "c"} {
		r.AddNode(id, id, 8080, 5005)
	}
	before := make([]string, 10000)
	for i := range before {
		before[i] = r.Get(fmt.Sprint(i))
	}
	r.AddNode("d", "d", 8080, 5005)
	if len(r.points) != 200 {
		t.Fatal("expected 50 virtual nodes per shard")
	}
	changed := 0
	for i, owner := range before {
		after := r.Get(fmt.Sprint(i))
		if owner != after {
			changed++
			if after != "d" {
				t.Fatal("key moved between existing nodes")
			}
		}
	}
	if changed == 0 || changed == len(before) {
		t.Fatal("degenerate distribution")
	}
	if err := r.RemoveNode("d"); err != nil {
		t.Fatal(err)
	}
	for i, owner := range before {
		if r.Get(fmt.Sprint(i)) != owner {
			t.Fatal("removal failed to restore owner")
		}
	}
}

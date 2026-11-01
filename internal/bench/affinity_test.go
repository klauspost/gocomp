package bench

import (
	"slices"
	"testing"

	"github.com/klauspost/cpuid/v2/topology"
)

func TestCoreOrder(t *testing.T) {
	core := func(typ topology.CoreType, ids ...int) topology.Core {
		c := topology.Core{Type: typ}
		for _, id := range ids {
			c.Threads = append(c.Threads, topology.Thread{ID: id})
		}
		return c
	}
	p := topology.Performance
	tests := []struct {
		name   string
		groups [][]topology.Core
		want   []int
	}{
		// Two CCDs of two SMT cores, numbered like Windows and Linux on Zen.
		{"2ccd", [][]topology.Core{{core(p, 0, 1), core(p, 2, 3)}, {core(p, 4, 5), core(p, 6, 7)}}, []int{0, 4, 2, 6, 1, 5, 3, 7}},
		// Hybrid: SMT P-cores and E-cores in one group. E-cores before the P-core siblings.
		{"hybrid", [][]topology.Core{{core(topology.Efficiency, 4), core(p, 0, 1), core(topology.Efficiency, 5), core(p, 2, 3)}}, []int{0, 2, 4, 5, 1, 3}},
		// Uneven groups.
		{"uneven", [][]topology.Core{{core(p, 0)}, {core(p, 1), core(p, 2), core(p, 3)}}, []int{0, 1, 2, 3}},
	}
	for _, tt := range tests {
		var g []topology.Group
		for _, cores := range tt.groups {
			g = append(g, topology.Group{Cores: cores})
		}
		got := coreOrder(&topology.System{Packages: []topology.Package{{Groups: g}}})
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestPinOrder logs the order on this machine; it is nil where the topology is not known exactly.
func TestPinOrder(t *testing.T) {
	order := pinOrder()
	t.Logf("pin order: %v", order)
	if order == nil {
		return
	}
	got := slices.Sorted(slices.Values(order))
	for i, c := range got {
		if c != i {
			t.Fatalf("not a permutation of 0..%d: %v", len(order)-1, order)
		}
	}
	defer setAffinity(order)
	n := max(len(order)/2, 1)
	if err := setAffinity(order[:n]); err != nil {
		t.Fatal(err)
	}
	if got, err := allowedCPUs(); err != nil || !slices.Equal(got, slices.Sorted(slices.Values(order[:n]))) {
		t.Fatalf("pinned to %v, got %v, %v", order[:n], got, err)
	}
}

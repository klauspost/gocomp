package bench

import (
	"cmp"
	"runtime"
	"slices"
	"sync"

	"github.com/klauspost/cpuid/v2"
	"github.com/klauspost/cpuid/v2/topology"
)

// pinOrder is the order logical CPUs are given to a thread count; a measurement
// at n threads runs on the first n. Left to itself the OS may stack threads on
// SMT siblings or on one CCD while other cores idle, which moved 16-thread
// results by up to 2x between runs.
//
// It is nil when the topology is not known exactly, and the affinity is then
// left alone.
var pinOrder = sync.OnceValue(func() []int {
	allowed, err := allowedCPUs()
	if err != nil {
		return nil
	}
	sys, err := cpuid.ScanTopology()
	if err != nil || !sys.Scanned || sys.Online != len(allowed) {
		return nil
	}
	// Thread.ID numbers the scanned CPUs in order, so it is the OS CPU number
	// only when the scan covered CPUs 0 to n-1.
	for i, c := range allowed {
		if c != i {
			return nil
		}
	}
	return coreOrder(sys)
})

// coreOrder lists one thread of every core, spread round-robin over the
// last-level cache groups with the faster core types first, then the
// remaining SMT siblings in the same order.
func coreOrder(sys *topology.System) []int {
	var groups [][]topology.Core
	total := 0
	for _, p := range sys.Packages {
		for _, g := range p.Groups {
			groups = append(groups, g.Cores)
			total += len(g.Cores)
		}
	}
	cores := make([]topology.Core, 0, total)
	for i := 0; len(cores) < total; i++ {
		for _, g := range groups {
			if i < len(g) {
				cores = append(cores, g[i])
			}
		}
	}
	slices.SortStableFunc(cores, func(a, b topology.Core) int { return cmp.Compare(typeRank(a.Type), typeRank(b.Type)) })
	var order []int
	for s := 0; ; s++ {
		n := len(order)
		for _, c := range cores {
			if s < len(c.Threads) {
				order = append(order, c.Threads[s].ID)
			}
		}
		if len(order) == n {
			return order
		}
	}
}

func typeRank(t topology.CoreType) int {
	switch t {
	case topology.Efficiency, topology.Dense:
		return 1
	case topology.LPEfficiency:
		return 2
	}
	return 0
}

// setThreads sets GOMAXPROCS and, when pinning, the CPUs the process runs on.
func (e *Engine) setThreads(t int) {
	runtime.GOMAXPROCS(t)
	e.pin = 0
	if e.pinned == nil {
		return
	}
	if err := setAffinity(e.pinned[:min(t, len(e.pinned))]); err != nil {
		e.emit(Event{Kind: "warn", Msg: "affinity: " + err.Error()})
		return
	}
	e.pin = 1
}

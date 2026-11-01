package bench

import (
	"errors"
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// allowedCPUs returns the CPUs the calling thread may run on.
func allowedCPUs() ([]int, error) {
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		return nil, err
	}
	var cpus []int
	for i := range int(unsafe.Sizeof(set)) * 8 {
		if set.IsSet(i) {
			cpus = append(cpus, i)
		}
	}
	return cpus, nil
}

// setAffinity restricts every thread of the process to cpus. Linux sets it per
// thread and a new thread inherits it from its creator, so the threads are
// walked until no new one shows up.
func setAffinity(cpus []int) error {
	var set unix.CPUSet
	for _, c := range cpus {
		set.Set(c)
	}
	done := map[int]bool{}
	for {
		ents, err := os.ReadDir("/proc/self/task")
		if err != nil {
			return err
		}
		n := len(done)
		for _, e := range ents {
			tid, err := strconv.Atoi(e.Name())
			if err != nil || done[tid] {
				continue
			}
			// The thread may have exited since the listing.
			if err := unix.SchedSetaffinity(tid, &set); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
			done[tid] = true
		}
		if len(done) == n {
			return nil
		}
	}
}

package bench

import (
	"errors"
	"math/bits"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessAffinityMask = kernel32.NewProc("GetProcessAffinityMask")
	procSetProcessAffinityMask = kernel32.NewProc("SetProcessAffinityMask")
)

// allowedCPUs returns the CPUs of the process affinity mask, which covers one
// processor group.
func allowedCPUs() ([]int, error) {
	var proc, sys uintptr
	if r, _, err := procGetProcessAffinityMask.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&proc)), uintptr(unsafe.Pointer(&sys))); r == 0 {
		return nil, err
	}
	var cpus []int
	for m := proc; m != 0; m &= m - 1 {
		cpus = append(cpus, bits.TrailingZeros(uint(m)))
	}
	return cpus, nil
}

// setAffinity restricts every thread of the process to cpus.
func setAffinity(cpus []int) error {
	var mask uintptr
	for _, c := range cpus {
		if c >= bits.UintSize {
			return errors.New("CPU outside the processor group")
		}
		mask |= 1 << c
	}
	if r, _, err := procSetProcessAffinityMask.Call(uintptr(windows.CurrentProcess()), mask); r == 0 {
		return err
	}
	return nil
}

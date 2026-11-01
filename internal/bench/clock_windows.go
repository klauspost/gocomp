package bench

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// time.Now on Windows reads the interrupt time, which only advances on a timer
// tick of 0.5ms to 15.6ms: a fast pass was quantized, and the best of many such
// passes is biased fast by up to a tick. QPC has 100ns resolution.
var (
	qpc     = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
	qpcFreq = func() int64 {
		var f int64
		windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryPerformanceFrequency").Call(uintptr(unsafe.Pointer(&f)))
		return f
	}()
)

// now is a monotonic reading, only meaningful as a difference.
func now() time.Duration {
	var c int64
	qpc.Call(uintptr(unsafe.Pointer(&c)))
	return time.Duration(c/qpcFreq*1e9 + c%qpcFreq*1e9/qpcFreq)
}

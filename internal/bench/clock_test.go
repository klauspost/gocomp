package bench

import (
	"testing"
	"time"
)

func TestClockResolution(t *testing.T) {
	// The smallest of many steps, so a preemption between two readings cannot
	// fail it.
	step := time.Hour
	for range 100 {
		a := now()
		b := now()
		for b == a {
			b = now()
		}
		step = min(step, b-a)
	}
	if step > 10*time.Microsecond {
		t.Errorf("clock steps by %v; a pass of a few ms needs far finer", step)
	}

	w, c := time.Now(), now()
	time.Sleep(300 * time.Millisecond)
	// Generous enough for time.Now's own tick, tight enough to catch a wrong
	// frequency.
	if d := (now() - c) - time.Since(w); d.Abs() > 20*time.Millisecond {
		t.Errorf("clock drifts %v from time.Now over 300ms", d)
	}
}

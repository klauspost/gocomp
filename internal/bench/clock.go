//go:build !windows

package bench

import "time"

var epoch = time.Now()

// now is a monotonic reading, only meaningful as a difference.
func now() time.Duration { return time.Since(epoch) }

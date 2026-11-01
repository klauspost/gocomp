//go:build !windows && !linux

package bench

import "errors"

// macOS has no API to bind a thread to a core, so nothing is pinned.

func allowedCPUs() ([]int, error) { return nil, errors.ErrUnsupported }

func setAffinity([]int) error { return errors.ErrUnsupported }

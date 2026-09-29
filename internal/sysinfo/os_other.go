//go:build !windows && !linux && !darwin

package sysinfo

import "runtime"

func osInfo() OS { return OS{Name: runtime.GOOS} }

package sysinfo

import "golang.org/x/sys/unix"

func osInfo() OS {
	o := OS{Name: "macOS"}
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		o.Version = v
	}
	if v, err := unix.Sysctl("kern.osrelease"); err == nil {
		o.Kernel = v
	}
	return o
}

package sysinfo

import (
	"bufio"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// osInfo reads the distribution identity and kernel release. Uname's Nodename
// is deliberately not read: it is the hostname.
func osInfo() OS {
	o := OS{Name: "linux"}
	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"`)
			switch k {
			case "ID":
				o.Name = v
			case "VERSION_ID":
				o.Version = v
			}
		}
	}
	var u unix.Utsname
	if unix.Uname(&u) == nil {
		o.Kernel = strings.TrimRight(string(u.Release[:]), "\x00")
	}
	return o
}

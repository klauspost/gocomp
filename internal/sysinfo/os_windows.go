package sysinfo

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func osInfo() OS {
	v := windows.RtlGetVersion()
	return OS{
		Name:    "Windows",
		Version: fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber),
	}
}

//go:build windows

package api

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func diskSpace(path string) (uint64, uint64, bool) {
	if strings.TrimSpace(path) == "" {
		return 0, 0, false
	}
	_ = os.MkdirAll(path, 0755)
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	var freeBytesAvailable uint64
	var totalNumberOfBytes uint64
	var totalNumberOfFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeBytesAvailable, &totalNumberOfBytes, &totalNumberOfFreeBytes); err != nil {
		return 0, 0, false
	}
	return totalNumberOfFreeBytes, totalNumberOfBytes, true
}

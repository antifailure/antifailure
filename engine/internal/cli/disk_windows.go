//go:build windows

package cli

import (
	"golang.org/x/sys/windows"
)

// FreeDiskBytes reports free bytes on the volume holding a path.
func (p systemProber) FreeDiskBytes(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeToCaller, &total, &totalFree); err != nil {
		return 0, err
	}
	return freeToCaller, nil
}

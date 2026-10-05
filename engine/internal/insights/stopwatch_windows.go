//go:build windows

package insights

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procFrequency = kernel32.NewProc("QueryPerformanceFrequency")

	// The counter is chosen once, with both of its calls checked, so ticks
	// and tickHz always describe the same clock. Microsoft documents that
	// neither call fails on any version of Windows still supported; the coarse
	// clock is the answer for the impossible case, not a wrong number.
	chooseOnce sync.Once
	counterHz  int64
	epoch      = time.Now()
)

func choose() {
	chooseOnce.Do(func() {
		var f, c int64
		if ok, _, _ := procFrequency.Call(uintptr(unsafe.Pointer(&f))); ok == 0 || f <= 0 {
			return
		}
		if ok, _, _ := procCounter.Call(uintptr(unsafe.Pointer(&c))); ok == 0 {
			return
		}
		counterHz = f
	})
}

func tickHz() int64 {
	choose()
	if counterHz > 0 {
		return counterHz
	}
	return int64(time.Second)
}

func ticks() int64 {
	choose()
	if counterHz > 0 {
		var c int64
		// Checked at choose(); documented never to fail after that, and the
		// return carries no information the read itself does not.
		_, _, _ = procCounter.Call(uintptr(unsafe.Pointer(&c)))
		return c
	}
	return int64(time.Since(epoch))
}

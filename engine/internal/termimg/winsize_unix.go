//go:build !windows

package termimg

import "golang.org/x/sys/unix"

// windowSize asks the kernel how big the window is, in cells and in pixels.
//
// The pixel fields are the whole reason this exists: dividing them by the cell
// counts is one of the two ways to learn how many pixels a character cell
// covers, which is what a sixel has to be sized against. Plenty of terminals
// leave them zero, which is not an error and is why the caller treats a zero
// cell size as "I could not find out" rather than as a number.
func windowSize(fd uintptr) Winsize {
	ws, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil || ws == nil {
		return Winsize{}
	}
	return Winsize{
		Cols: int(ws.Col), Rows: int(ws.Row),
		XPixel: int(ws.Xpixel), YPixel: int(ws.Ypixel),
	}
}

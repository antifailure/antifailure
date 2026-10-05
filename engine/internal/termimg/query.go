//go:build !windows

package termimg

import (
	"os"
	"time"
)

// The three questions, written as one burst and answered in order.
//
//	kittyQuery  asks kitty whether it speaks the graphics protocol. It transmits
//	            a one pixel image by direct payload and asks for the answer, so
//	            only a terminal that implements the protocol replies; anything
//	            else ignores an APC sequence it does not know.
//	cellQuery   is CSI 16 t, "report the character cell size in pixels".
//	bgQuery     is OSC 11, "what colour are you painted". A palette tuned for a
//	            dark terminal is the palette that disappears on a light one, and
//	            an ANSI colour is absolute rather than relative to the page, so
//	            this is the only way to know which one to use.
//	daQuery     is the primary device attributes request. It is LAST on purpose:
//	            every terminal in existence answers it, so its answer is the
//	            marker that says the earlier answers have either arrived or are
//	            never coming. Without it the read would have to wait out the
//	            whole deadline on every terminal that draws no images, which is
//	            most of them, and that delay would be paid at every startup.
const (
	kittyQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	cellQuery  = "\x1b[16t"
	bgQuery    = "\x1b]11;?\a"
	daQuery    = "\x1b[c"
)

// queryDeadline bounds the whole round trip, and pollInterval is how often the
// reply is looked for inside it.
//
// A quarter of a second is far more than a local terminal needs and is short
// enough that a terminal at the far end of a slow link costs a visible pause
// rather than a hang. A terminal that answers nothing at all costs the full
// deadline once, at startup, and never again.
const (
	queryDeadline = 250 * time.Millisecond
	pollInterval  = 2 * time.Millisecond
)

// readWithDeadline reads until the device attributes answer is complete or the
// deadline passes, using the deadline the runtime poller provides.
//
// It is what a unix falls back to when the terminal cannot be made
// non-blocking. It was also what Windows used outright, on the belief that
// Windows takes a read deadline where macOS does not. It does not, for a
// console or for a pipe, so every query there failed after it had already been
// written, and this whole file is now unix only. See query_windows.go.
func readWithDeadline(in *os.File, deadline time.Time) (string, error) {
	if err := in.SetReadDeadline(deadline); err != nil {
		return "", err
	}
	defer func() { _ = in.SetReadDeadline(time.Time{}) }()

	var reply []byte
	buf := make([]byte, 1024)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			reply = append(reply, buf[:n]...)
			if daComplete(reply) {
				return string(reply), nil
			}
		}
		if err != nil {
			if os.IsTimeout(err) {
				// The ordinary ending for a terminal that answers nothing, and
				// what did arrive is still worth interpreting.
				return string(reply), nil
			}
			return string(reply), err
		}
	}
}

// daComplete reports whether a primary device attributes reply has been read in
// full: ESC [ ? parameters c.
func daComplete(reply []byte) bool {
	return len(csiParams(string(reply), '?', 'c')) > 0
}

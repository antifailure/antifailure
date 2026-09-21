package termimg

import (
	"errors"
	"os"
	"time"

	"golang.org/x/term"
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

// Query writes the capability questions to out and reads whatever comes back
// on in.
//
// The terminal is put in raw mode for the duration and restored afterwards,
// because a cooked terminal echoes the reply onto the screen and hands it over
// only at a newline that a device attributes reply does not contain.
//
// A short or empty answer is not an error. Every terminal answers the device
// attributes request, so reading nothing means the reply went somewhere else,
// and Interpret reports that as an unknown capability rather than as a terminal
// that draws nothing.
func Query(in, out *os.File) (string, error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("the input is not a terminal")
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	// Restored on every path. A command that left the terminal raw would leave
	// the shell after it with no echo and no line editing, which reads to
	// whoever is sitting there as a hung machine.
	defer func() { _ = term.Restore(fd, state) }()

	if _, err := out.WriteString(kittyQuery + cellQuery + bgQuery + daQuery); err != nil {
		return "", err
	}
	return readReply(in, fd, time.Now().Add(queryDeadline))
}

// readWithDeadline reads until the device attributes answer is complete or the
// deadline passes, using the deadline the runtime poller provides.
//
// This is what Windows uses outright and what a unix falls back to when the
// terminal cannot be made non-blocking. Shared rather than duplicated per
// platform, so the arm that only Windows runs is exercised by every test run on
// every machine.
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

// Package termimg draws real images in a terminal, and says plainly when it
// cannot.
//
// Three terminals in wide use will show a picture and they do not agree on how:
// iTerm2 takes an OSC 1337 payload and sizes it in character cells, kitty takes
// an APC payload and wants PNG, and everything descended from the VT340 takes a
// DCS sixel bitmap and wants to be told the size in pixels. Nothing else shows
// anything, and a terminal handed an escape it does not know prints the escape,
// which fills the screen with punctuation and is worse than never having tried.
//
// So the first question this package answers is not "how do I draw" but "may
// I". It ASKS THE TERMINAL rather than reading TERM. TERM is set by the shell,
// inherited through ssh, rewritten by tmux and routinely lies in both
// directions: TERM=xterm-256color covers a terminal that draws images and one
// that does not, and a multiplexer that swallows the payload leaves TERM
// untouched. The query below costs one round trip at startup and is answered by
// the program actually holding the file descriptor, which is the only thing
// that knows. The one protocol with no query is iTerm2's, and that is the only
// place an environment variable is read at all.
//
// A Capability always carries Why. "This terminal draws no images" and "I could
// not find out" are different facts and a view that shows a text fallback
// should be able to say which one it is looking at.
//
// TESTED ON, by running the real view and photographing the window: kitty
// 0.44.1 (kitty graphics protocol), WezTerm 20240203 (both the iTerm2 protocol
// and sixel), and Terminal.app 2.14 and tmux 3.5a (neither, which is the
// fallback arm). The escape sequences themselves are pinned by tests that
// decode them back: the sixel encoder has a decoder in its test, and the kitty
// and iTerm2 payloads are unpacked and re-parsed as images.
package termimg

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Protocol is which inline image escape a terminal speaks.
type Protocol int

// The protocols, in the order Interpret prefers them when a terminal answers
// yes to more than one.
const (
	// NoImages is a terminal that draws no images, or one that could not be
	// asked. Read Capability.Why for which.
	NoImages Protocol = iota
	// ITerm2 is the OSC 1337 inline image, sized in character cells. Preferred
	// where it exists because it takes the runner's JPEG bytes unchanged: no
	// decode, no re-encode, no resample on the path from the browser to the
	// screen.
	ITerm2
	// Kitty is the APC graphics protocol. Sized in cells like iTerm2, but it
	// takes only PNG or raw pixels, so a JPEG frame is transcoded.
	Kitty
	// Sixel is the DCS bitmap every VT340 descendant understands. Sized in
	// PIXELS, which is why a terminal that speaks sixel but will not report its
	// cell size is treated as having no images at all.
	Sixel
)

// String names the protocol for a status line.
func (p Protocol) String() string {
	switch p {
	case ITerm2:
		return "iterm2"
	case Kitty:
		return "kitty"
	case Sixel:
		return "sixel"
	}
	return "none"
}

// Background is what colour the terminal is painted, which decides which
// palette is legible on it.
//
// It is here, in the package that already holds a terminal open and asks it
// questions, because the answer comes from the same round trip and a second one
// would cost another quarter of a second and another flip into raw mode. What
// is done with the answer belongs to whoever draws.
type Background int

// The backgrounds, and the honest third answer.
const (
	// BackgroundUnknown is a terminal that did not answer, which is not the
	// same as a dark one and must not be treated as one: the palette that is
	// tuned for dark is the palette that disappears on light.
	BackgroundUnknown Background = iota
	BackgroundDark
	BackgroundLight
)

// String names the background for a status line.
func (b Background) String() string {
	switch b {
	case BackgroundDark:
		return "dark"
	case BackgroundLight:
		return "light"
	}
	return "unknown"
}

// Capability is what this terminal will draw and how big its cells are.
//
// CellWidth and CellHeight are pixels per character cell. They are zero when
// the terminal would not say, which only matters for sixel: the cell-sized
// protocols are told a box in cells and do their own arithmetic.
type Capability struct {
	Protocol   Protocol
	CellWidth  int
	CellHeight int
	// Why is one sentence explaining the verdict, for a view that has to tell
	// somebody why there is no picture. Always set, including on success.
	Why string
	// Background is the terminal's own colour, asked for in the same round
	// trip. Unknown when it would not say, which a caller reads as "keep the
	// default" rather than as "dark".
	Background Background
}

// Winsize is what the kernel says about the window, in cells and in pixels.
// Pixel fields are zero on a terminal that does not fill them in, which is
// common and not an error.
type Winsize struct {
	Cols, Rows     int
	XPixel, YPixel int
}

// cellPixels divides a window's pixel size by its cell count. Zero when either
// number is missing, because a guessed cell size draws a sixel of the wrong
// height and that is a corrupted frame rather than a slightly wrong one.
func (w Winsize) cellPixels() (int, int) {
	if w.Cols <= 0 || w.Rows <= 0 || w.XPixel <= 0 || w.YPixel <= 0 {
		return 0, 0
	}
	return w.XPixel / w.Cols, w.YPixel / w.Rows
}

// Interpret turns a terminal's answers into a Capability. Pure, so the whole
// decision is testable without a terminal: reply is what the terminal wrote
// back to Query, getenv reads the environment, and ws is what the kernel said
// about the window.
//
// The order of the checks is the order of preference, and each one is a
// different KIND of evidence:
//
//  1. kitty answered its own graphics query. Only a terminal that implements
//     the protocol replies to it at all.
//  2. the environment names iTerm2 or WezTerm. This is the one guess, and it is
//     here because the iTerm2 protocol has no query to ask; the variables read
//     are set by the terminal emulator itself rather than by a shell profile.
//  3. the primary device attributes list 4, which is the VT340's way of saying
//     it has sixel graphics.
//
// Anything else is NoImages, and the reason distinguishes a terminal that said
// no from one that was never asked.
func Interpret(reply string, getenv func(string) string, ws Winsize) Capability {
	bg := backgroundReply(reply)
	cw, ch := ws.cellPixels()
	if pw, ph, ok := cellSizeReply(reply); ok {
		// The terminal answered the cell size query directly, which is better
		// evidence than dividing the window, because a window whose pixel size
		// includes padding divides to a cell a pixel or two short.
		cw, ch = pw, ph
	}

	if strings.Contains(reply, "_Gi=31;OK") {
		return Capability{
			Protocol: Kitty, CellWidth: cw, CellHeight: ch, Background: bg,
			Why: "this terminal answered the kitty graphics query",
		}
	}

	if term := iTerm2Like(getenv); term != "" {
		return Capability{
			Protocol: ITerm2, CellWidth: cw, CellHeight: ch, Background: bg,
			Why: "the environment says this terminal is " + term + ", which draws inline images",
		}
	}

	if hasSixel(reply) {
		if cw <= 0 || ch <= 0 {
			// Sixel is sized in pixels and nothing else here is. Drawing one
			// against a guessed cell size puts a bitmap of the wrong height on
			// the screen, which scrolls the frame and corrupts every pane under
			// it, so a terminal that will not say is treated as one that cannot.
			return Capability{
				Background: bg,
				Why: "this terminal reports sixel graphics but would not report its cell size, " +
					"and a sixel has to be sized in pixels",
			}
		}
		return Capability{
			Protocol: Sixel, CellWidth: cw, CellHeight: ch, Background: bg,
			Why: "this terminal reports sixel graphics in its device attributes",
		}
	}

	if reply == "" {
		return Capability{
			CellWidth: cw, CellHeight: ch, Background: bg,
			Why: "this terminal did not answer the graphics query, so its capability is unknown",
		}
	}
	return Capability{
		CellWidth: cw, CellHeight: ch, Background: bg,
		Why: "this terminal answered the graphics query and reported no image protocol",
	}
}

// backgroundReply reads the answer to OSC 11, the terminal's own background
// colour, which comes back as rgb:RRRR/GGGG/BBBB in sixteen bits a channel.
//
// The verdict is luminance rather than a threshold on one channel, because a
// warm paper background and a cold one are both light and differ in every
// channel. Half way is the split: anything else would be picking a side for the
// terminals in the middle, and there are none in practice.
func backgroundReply(reply string) Background {
	i := strings.Index(reply, "]11;rgb:")
	if i < 0 {
		return BackgroundUnknown
	}
	rest := reply[i+len("]11;rgb:"):]
	end := strings.IndexAny(rest, "\a\x1b")
	if end >= 0 {
		rest = rest[:end]
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return BackgroundUnknown
	}
	var channel [3]float64
	for n, p := range parts {
		// Terminals answer in one, two, three or four hex digits a channel, and
		// the digit count is the scale. Reading four digits as if they were two
		// makes every background look black.
		v, err := strconv.ParseUint(p, 16, 64)
		if err != nil || len(p) == 0 || len(p) > 4 {
			return BackgroundUnknown
		}
		channel[n] = float64(v) / float64(uint64(1)<<(4*len(p))-1)
	}
	if 0.2126*channel[0]+0.7152*channel[1]+0.0722*channel[2] >= 0.5 {
		return BackgroundLight
	}
	return BackgroundDark
}

// iTerm2Like names the terminal when the environment identifies one that speaks
// the iTerm2 inline image protocol, and is empty otherwise.
//
// Only variables the terminal emulator sets itself are read. TERM is not one of
// them: it comes from the shell, survives ssh into a machine with a different
// terminal on the other end, and is rewritten by every multiplexer.
func iTerm2Like(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	// LC_TERMINAL is the one that survives ssh, and iTerm2 sets it for exactly
	// that reason.
	switch getenv("LC_TERMINAL") {
	case "iTerm2":
		return "iTerm2"
	case "WezTerm":
		return "WezTerm"
	}
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return "iTerm2"
	case "WezTerm":
		return "WezTerm"
	}
	return ""
}

// hasSixel reads a primary device attributes reply and reports whether 4, the
// sixel graphics extension, is among the attributes.
//
// The reply looks like ESC [ ? 62 ; 4 ; 6 c. The parameters are parsed rather
// than searched for a substring, because "4" appears inside 64 and 14 and a
// contains check turns a terminal with attribute 64 into one that draws
// pictures it cannot draw.
func hasSixel(reply string) bool {
	for _, params := range csiParams(reply, '?', 'c') {
		for _, p := range params {
			if p == "4" {
				return true
			}
		}
	}
	return false
}

// cellSizeReply reads the answer to CSI 16 t, which is CSI 6 ; height ; width t
// with both in pixels. Returns false when the terminal did not answer it.
func cellSizeReply(reply string) (int, int, bool) {
	for _, params := range csiParams(reply, 0, 't') {
		if len(params) != 3 || params[0] != "6" {
			continue
		}
		h, herr := strconv.Atoi(params[1])
		w, werr := strconv.Atoi(params[2])
		if herr != nil || werr != nil || w <= 0 || h <= 0 {
			continue
		}
		return w, h, true
	}
	return 0, 0, false
}

// csiParams finds every CSI sequence in a reply that carries the given private
// marker and final byte, and returns its semicolon separated parameters. A
// marker of zero means the sequence has no private marker.
//
// Written as a scan rather than a regular expression because the reply is
// several concatenated answers with no separator between them, and because a
// half-read reply must yield the sequences that did arrive rather than nothing.
func csiParams(reply string, marker byte, final byte) [][]string {
	var out [][]string
	for i := 0; i+1 < len(reply); i++ {
		if reply[i] != 0x1b || reply[i+1] != '[' {
			continue
		}
		j := i + 2
		if marker != 0 {
			if j >= len(reply) || reply[j] != marker {
				continue
			}
			j++
		} else if j < len(reply) && (reply[j] == '?' || reply[j] == '>') {
			// A private sequence when a plain one was asked for.
			continue
		}
		start := j
		for j < len(reply) && (reply[j] == ';' || (reply[j] >= '0' && reply[j] <= '9')) {
			j++
		}
		if j < len(reply) && reply[j] == final {
			out = append(out, strings.Split(reply[start:j], ";"))
			i = j
		}
	}
	return out
}

// Detect asks the terminal what it can draw.
//
// in and out must be the same terminal: the query is written to out and the
// answer arrives on in. Either not being a terminal, or the query failing, is
// answered with NoImages and a Why that says so. Nothing here ever panics and
// nothing blocks for longer than the query's own deadline, because a dashboard
// that hangs at startup on a terminal that will not answer is worse than one
// that draws no pictures.
func Detect(in, out *os.File, getenv func(string) string) Capability {
	if in == nil || out == nil {
		return Capability{Why: "there is no terminal here, so no image query was made"}
	}
	ws := windowSize(out.Fd())
	reply, err := Query(in, out)
	if err != nil {
		return Capability{
			CellWidth: firstOf(ws.cellPixels()), CellHeight: secondOf(ws.cellPixels()),
			Why: fmt.Sprintf("the terminal could not be asked what it draws: %v", err),
		}
	}
	return Interpret(reply, getenv, ws)
}

// firstOf and secondOf let Detect use both halves of cellPixels in a struct
// literal without a temporary. Small, and they keep the failure branch from
// having to restate the zero rules cellPixels already holds.
func firstOf(a, _ int) int  { return a }
func secondOf(_, b int) int { return b }

// Override applies a person's explicit choice over what the terminal answered.
//
// Detection is a question asked of the program on the other end of a file
// descriptor, and there are real situations where that question does not reach
// it: a multiplexer that swallows the query and answers on the terminal's
// behalf, a terminal reached over a connection that rewrites escapes, a new
// emulator that draws pictures and has not been taught to answer. An escape
// hatch costs one environment variable and turns each of those from "this
// product does not work here" into "tell it what you have".
//
// The measured cell size is kept, because a forced protocol still has to be
// sized against the real terminal. Sixel forced onto a terminal that never
// reported a cell size is still refused: the number is not a matter of opinion.
//
// A value this build does not know leaves the terminal's own answer in place
// and says so in Why, which the view shows. Quietly ignoring it would leave
// somebody staring at a text pane believing they had asked for pictures.
func Override(c Capability, want string) Capability {
	switch strings.ToLower(strings.TrimSpace(want)) {
	case "", "auto":
		return c
	case "off", "none":
		return Capability{
			CellWidth: c.CellWidth, CellHeight: c.CellHeight, Background: c.Background,
			Why: "pictures are switched off by AF_IMAGES",
		}
	case "iterm2":
		return forced(c, ITerm2)
	case "kitty":
		return forced(c, Kitty)
	case "sixel":
		if c.CellWidth <= 0 || c.CellHeight <= 0 {
			return Capability{
				Background: c.Background,
				Why: "AF_IMAGES asked for sixel and this terminal reported no cell size, " +
					"and a sixel has to be sized in pixels",
			}
		}
		return forced(c, Sixel)
	}
	c.Why = fmt.Sprintf("AF_IMAGES=%s is not a protocol this build draws, so the terminal's own answer stands: %s", want, c.Why)
	return c
}

// forced keeps everything measured and replaces only the verdict.
func forced(c Capability, p Protocol) Capability {
	return Capability{
		Protocol: p, CellWidth: c.CellWidth, CellHeight: c.CellHeight, Background: c.Background,
		Why: "AF_IMAGES asked for " + p.String(),
	}
}

package termimg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"strings"
)

// chunkSize is the largest payload kitty accepts in one APC sequence. The
// protocol requires a long image be split, with every chunk but the last
// flagged m=1.
const chunkSize = 4096

// maxPixels bounds the image actually sent when the terminal never said how big
// a character cell is.
//
// Only kitty reaches this: it is told a box in cells and scales into it itself,
// so the pixels are about bandwidth rather than about layout. A browser frame
// is 1280 by 800 and lands in a pane a few centimetres across, so sending it at
// full size spends a megabyte a second on detail nobody can see.
const maxPixelsW, maxPixelsH = 640, 400

// ErrNoProtocol is returned when a draw is asked of a terminal that draws
// nothing. Callers use it to choose the text fallback, so it is a value to
// compare against rather than a string to match.
var ErrNoProtocol = errors.New("this terminal draws no inline images")

// Draw returns the escape sequence that puts one frame inside a box of cols by
// rows character cells.
//
// b64 is base64 JPEG exactly as the runner sent it. id is a stable number for
// the pane the frame belongs to, which only kitty uses: giving every pane its
// own image id means a new frame REPLACES that pane's previous one rather than
// piling a fresh image into the terminal's store once a second until its quota
// evicts something somebody was looking at.
//
// The cursor is left where the terminal put it. Callers that need it back where
// it started use Place.
func Draw(c Capability, id int, b64 string, cols, rows int) (string, error) {
	if c.Protocol == NoImages {
		return "", ErrNoProtocol
	}
	if cols <= 0 || rows <= 0 {
		return "", fmt.Errorf("a frame cannot be drawn in a box of %d by %d cells", cols, rows)
	}
	if !validB64(b64) {
		// A payload with anything outside the base64 alphabet in it would end
		// the escape sequence early and print the rest of the image as text.
		return "", errors.New("the frame is not base64")
	}

	switch c.Protocol {
	case ITerm2:
		return drawITerm2(b64, cols, rows), nil
	case Kitty:
		return drawKitty(c, id, b64, cols, rows)
	case Sixel:
		return drawSixel(c, b64, cols, rows)
	}
	return "", ErrNoProtocol
}

// Place draws the frame with its top left corner at row and col, both one
// based, and puts the cursor back exactly where it found it.
//
// The save and restore are what let an image live inside a text layout. Every
// one of these protocols moves the cursor somewhere of its own choosing, and
// two images drawn one after another without this would stack down the screen
// instead of sitting side by side.
func Place(c Capability, id int, b64 string, row, col, cols, rows int) (string, error) {
	body, err := Draw(c, id, b64, cols, rows)
	if err != nil {
		return "", err
	}
	// DECSC and DECRC rather than the CSI s and CSI u pair: the DEC forms are
	// the ones every terminal that implements any of these image protocols
	// supports, and CSI s collides with the left right margin sequence.
	return "\x1b7" + fmt.Sprintf("\x1b[%d;%dH", row, col) + body + "\x1b8", nil
}

// drawITerm2 is the OSC 1337 inline image.
//
// The JPEG goes out exactly as it arrived. iTerm2 reads the format from the
// bytes, sizes the picture in character cells when width and height are bare
// integers, and preserveAspectRatio keeps a 1280 by 800 browser frame from
// being stretched to the pane's shape. Nothing is decoded on this path, which
// is why it is the preferred protocol where it exists.
func drawITerm2(b64 string, cols, rows int) string {
	return fmt.Sprintf(
		"\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1;doNotMoveCursor=1:%s\a",
		cols, rows, b64,
	)
}

// drawKitty is the APC graphics protocol.
//
// Three details are load bearing and each one was a visible bug without it:
//
//	q=2  suppresses the terminal's OK response. Without it kitty writes a reply
//	     on every frame, the reply arrives on the program's own standard input,
//	     and the key reader upstream reads it as somebody typing.
//	C=1  tells kitty not to move the cursor, so the image composes with the text
//	     layout drawn around it.
//	i,p  the image id and the placement id. Reusing a pane's pair replaces what
//	     was there; omitting them makes a new image every second.
func drawKitty(c Capability, id int, b64 string, cols, rows int) (string, error) {
	img, err := decodeJPEG(b64)
	if err != nil {
		return "", err
	}
	w, h := maxPixelsW, maxPixelsH
	if c.CellWidth > 0 && c.CellHeight > 0 {
		w, h = cols*c.CellWidth, rows*c.CellHeight
	}
	img = fit(img, w, h)

	// PNG because the kitty protocol's only compressed format is PNG. Best
	// speed rather than best compression: this is thrown away in a second and
	// the terminal is on the same machine.
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return "", err
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())

	var b strings.Builder
	first := true
	for len(payload) > 0 {
		chunk := payload
		if len(chunk) > chunkSize {
			chunk = chunk[:chunkSize]
		}
		payload = payload[len(chunk):]
		more := 0
		if len(payload) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,i=%d,p=1,c=%d,r=%d,C=1,q=2,m=%d;%s\x1b\\",
				id, cols, rows, more, chunk)
			first = false
			continue
		}
		fmt.Fprintf(&b, "\x1b_Gm=%d,q=2;%s\x1b\\", more, chunk)
	}
	return b.String(), nil
}

// drawSixel is the DCS bitmap.
//
// Sixel is sized in pixels and knows nothing about character cells, so the
// image is scaled here to exactly the box the layout allotted. A sixel drawn
// taller than its box scrolls the screen under it, which moves every pane below
// it by however many rows it overflowed, so the fit is the correctness property
// rather than a nicety.
func drawSixel(c Capability, b64 string, cols, rows int) (string, error) {
	if c.CellWidth <= 0 || c.CellHeight <= 0 {
		return "", errors.New("a sixel needs the terminal's cell size in pixels and it reported none")
	}
	img, err := decodeJPEG(b64)
	if err != nil {
		return "", err
	}
	return encodeSixel(fit(img, cols*c.CellWidth, rows*c.CellHeight)), nil
}

// validB64 reports whether every byte is in the base64 alphabet.
//
// A scan rather than a decode: the payload is tens of kilobytes arriving every
// second per pane, and the only thing that matters is that it cannot contain a
// byte that terminates the escape sequence it is about to be wrapped in.
func validB64(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
		case ch == '+' || ch == '/' || ch == '=':
		default:
			return false
		}
	}
	return true
}

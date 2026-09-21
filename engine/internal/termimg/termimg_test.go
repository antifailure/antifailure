package termimg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The replies a real terminal sends, kept here so every test reads the same
// strings a terminal would actually write.
const (
	kittyReply = "\x1b_Gi=31;OK\x1b\\\x1b[?62;4;22c"
	// xterm with sixel compiled in: attribute 4 among the others.
	sixelReply = "\x1b[?62;1;2;4;6;9;15;22c"
	// A terminal with no graphics at all. 64 contains a 4 and must not be read
	// as one.
	plainReply = "\x1b[?64;1;2;6;9;15;22c"
	cellReply  = "\x1b[6;34;15t"
)

func envOf(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestInterpretReadsWhatTheTerminalProved(t *testing.T) {
	none := envOf(nil)
	full := Winsize{Cols: 100, Rows: 30, XPixel: 800, YPixel: 600}

	if got := Interpret(kittyReply, none, full); got.Protocol != Kitty {
		t.Fatalf("a kitty graphics reply read as %v: %s", got.Protocol, got.Why)
	}
	if got := Interpret(sixelReply, none, full); got.Protocol != Sixel {
		t.Fatalf("a sixel device attributes reply read as %v: %s", got.Protocol, got.Why)
	}
	if got := Interpret(plainReply, none, full); got.Protocol != NoImages {
		t.Fatalf("a plain terminal read as %v: %s", got.Protocol, got.Why)
	}

	// The one protocol with no query. Only a variable the terminal emulator
	// sets itself counts.
	iterm := envOf(map[string]string{"TERM_PROGRAM": "iTerm.app"})
	if got := Interpret(plainReply, iterm, full); got.Protocol != ITerm2 {
		t.Fatalf("iTerm2 in the environment read as %v: %s", got.Protocol, got.Why)
	}
	wez := envOf(map[string]string{"LC_TERMINAL": "WezTerm"})
	if got := Interpret(plainReply, wez, full); got.Protocol != ITerm2 {
		t.Fatalf("WezTerm in the environment read as %v: %s", got.Protocol, got.Why)
	}
	// TERM is not evidence. It is set by a shell, survives ssh, and is rewritten
	// by every multiplexer, so a terminal that names itself in TERM alone gets
	// no picture.
	lying := envOf(map[string]string{"TERM": "xterm-kitty"})
	if got := Interpret(plainReply, lying, full); got.Protocol != NoImages {
		t.Fatalf("TERM alone was believed: %v, %s", got.Protocol, got.Why)
	}
}

func TestInterpretSaysWhichKindOfNoItMeans(t *testing.T) {
	// A terminal that answered and said no, and one that never answered, are
	// different facts. A view that shows a text fallback has to be able to tell
	// somebody which it is looking at, so the two reasons must differ.
	answered := Interpret(plainReply, envOf(nil), Winsize{})
	silent := Interpret("", envOf(nil), Winsize{})
	if answered.Protocol != NoImages || silent.Protocol != NoImages {
		t.Fatal("both of these terminals draw nothing")
	}
	if answered.Why == silent.Why {
		t.Fatalf("a terminal that refused and one that never answered gave the same reason: %q", answered.Why)
	}
	if !strings.Contains(silent.Why, "did not answer") {
		t.Fatalf("the silent terminal's reason does not say it was silent: %q", silent.Why)
	}
}

func TestInterpretRefusesSixelWithNoCellSize(t *testing.T) {
	// Sixel is the one protocol sized in pixels. Guessing the cell size draws a
	// bitmap of the wrong height, which scrolls every pane below it.
	got := Interpret(sixelReply, envOf(nil), Winsize{Cols: 100, Rows: 30})
	if got.Protocol != NoImages {
		t.Fatalf("sixel was chosen with no cell size: %v", got.Protocol)
	}
	if !strings.Contains(got.Why, "cell size") {
		t.Fatalf("the reason does not name the missing cell size: %q", got.Why)
	}
}

func TestCellSizeComesFromTheQueryBeforeTheWindowDivision(t *testing.T) {
	// Dividing the window by its cell count is an estimate: a window whose pixel
	// size includes padding divides to a cell a pixel or two short. The direct
	// answer wins where there is one.
	ws := Winsize{Cols: 100, Rows: 30, XPixel: 800, YPixel: 600}
	divided := Interpret(kittyReply, envOf(nil), ws)
	if divided.CellWidth != 8 || divided.CellHeight != 20 {
		t.Fatalf("window division gave %dx%d, want 8x20", divided.CellWidth, divided.CellHeight)
	}
	direct := Interpret(cellReply+kittyReply, envOf(nil), ws)
	if direct.CellWidth != 15 || direct.CellHeight != 34 {
		t.Fatalf("the cell query gave %dx%d, want 15x34", direct.CellWidth, direct.CellHeight)
	}
}

func TestHasSixelParsesAttributesRatherThanSearching(t *testing.T) {
	if hasSixel(plainReply) {
		t.Fatal("attribute 64 was read as attribute 4")
	}
	if !hasSixel(sixelReply) {
		t.Fatal("attribute 4 was not found")
	}
	if hasSixel("") {
		t.Fatal("an empty reply reported sixel")
	}
}

// sampleFrame is a small JPEG in the base64 shape the runner sends, with four
// clearly different quadrants so a decoder can tell top from bottom and left
// from right.
func sampleFrame(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{A: 0xff}
			switch {
			case x < w/2 && y < h/2:
				c.R = 0xff
			case x >= w/2 && y < h/2:
				c.G = 0xff
			case x < w/2:
				c.B = 0xff
			default:
				c.R, c.G, c.B = 0xff, 0xff, 0xff
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatalf("encode the sample frame: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDrawITerm2SendsTheRunnersBytesUnchanged(t *testing.T) {
	frame := sampleFrame(t, 64, 40)
	out, err := Draw(Capability{Protocol: ITerm2}, 1, frame, 20, 10)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	if !strings.HasPrefix(out, "\x1b]1337;File=") || !strings.HasSuffix(out, "\a") {
		t.Fatalf("not an OSC 1337 sequence: %.40q", out)
	}
	// The whole point of preferring this protocol: no decode and no re-encode.
	// The payload after the colon is the runner's own base64, byte for byte.
	payload := out[strings.Index(out, ":")+1 : len(out)-1]
	if payload != frame {
		t.Fatal("the JPEG was not passed through unchanged")
	}
	if !strings.Contains(out, "width=20;height=10") {
		t.Fatalf("the box was not sized in cells: %.80q", out)
	}
	if !strings.Contains(out, "preserveAspectRatio=1") {
		t.Fatal("a frame would be stretched to the pane's shape")
	}
}

// busyFrame is a JPEG with no flat areas, so the PNG kitty is sent cannot be
// compressed down to a single chunk. A flat test image would pass the chunking
// assertions by never reaching the limit, which is a check that cannot say no.
func busyFrame(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(12345)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			seed = seed*1664525 + 1013904223
			img.Set(x, y, color.RGBA{
				R: uint8(seed >> 24), G: uint8(seed >> 16), B: uint8(seed >> 8), A: 0xff,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatalf("encode the busy frame: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDrawKittyChunksAndKeepsItsRepliesOffTheKeyboard(t *testing.T) {
	// A frame with no flat areas, so the payload is certain to need more than
	// one chunk.
	frame := busyFrame(t, 900, 600)
	out, err := Draw(Capability{Protocol: Kitty, CellWidth: 10, CellHeight: 20}, 7, frame, 40, 12)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	// Checked per chunk below rather than with a Contains over the whole
	// stream. A substring search passes as long as ANY chunk carries q=2, and
	// the continuation chunks all do, so dropping it from the first one, which
	// is the one that transmits the image and answers, went unnoticed.
	if !strings.Contains(out, "C=1") {
		t.Fatal("the cursor would move, so the text drawn around the image would land in the wrong place")
	}
	if !strings.Contains(out, "i=7,p=1") {
		t.Fatalf("no stable image and placement id, so every frame would be a new image: %.90q", out)
	}
	if !strings.Contains(out, "c=40,r=12") {
		t.Fatalf("the box was not sized in cells: %.90q", out)
	}

	chunks := splitAPC(t, out)
	if len(chunks) < 2 {
		t.Fatalf("a large frame was sent in %d chunk(s); kitty takes at most %d bytes each", len(chunks), chunkSize)
	}
	var payload strings.Builder
	for i, c := range chunks {
		if len(c.data) > chunkSize {
			t.Fatalf("chunk %d is %d bytes, over the %d limit", i, len(c.data), chunkSize)
		}
		wantMore := "1"
		if i == len(chunks)-1 {
			wantMore = "0"
		}
		if c.keys["m"] != wantMore {
			t.Fatalf("chunk %d of %d has m=%q, want m=%s", i, len(chunks), c.keys["m"], wantMore)
		}
		if c.keys["q"] != "2" {
			t.Fatalf("chunk %d of %d does not suppress kitty's reply (q=%q); the reply would "+
				"arrive on standard input and be read as somebody typing", i, len(chunks), c.keys["q"])
		}
		payload.WriteString(c.data)
	}

	raw, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil {
		t.Fatalf("the reassembled payload is not base64: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("kitty was sent something that is not the PNG it was told to expect: %v", err)
	}
	// Scaled into the box it was given rather than sent at full size.
	if got := img.Bounds().Dx(); got > 40*10 {
		t.Fatalf("the image is %d pixels wide, wider than the %d pixel box", got, 40*10)
	}
}

type apcChunk struct {
	keys map[string]string
	data string
}

// splitAPC pulls the kitty graphics sequences out of an escape stream.
func splitAPC(t *testing.T, s string) []apcChunk {
	t.Helper()
	var out []apcChunk
	for _, part := range strings.Split(s, "\x1b_G")[1:] {
		end := strings.Index(part, "\x1b\\")
		if end < 0 {
			t.Fatalf("an APC sequence was never terminated: %.40q", part)
		}
		body := part[:end]
		keys := map[string]string{}
		data := ""
		if semi := strings.Index(body, ";"); semi >= 0 {
			data = body[semi+1:]
			body = body[:semi]
		}
		for _, kv := range strings.Split(body, ",") {
			if eq := strings.Index(kv, "="); eq > 0 {
				keys[kv[:eq]] = kv[eq+1:]
			}
		}
		out = append(out, apcChunk{keys: keys, data: data})
	}
	return out
}

func TestDrawSixelDecodesBackToThePictureItWasGiven(t *testing.T) {
	// The strongest check available without a terminal: encode a known image,
	// decode the bytes back with an independent reader, and compare. A sixel
	// encoder that is merely plausible passes a "contains an escape" assertion
	// and draws noise.
	frame := sampleFrame(t, 120, 80)
	cap := Capability{Protocol: Sixel, CellWidth: 10, CellHeight: 20}
	out, err := Draw(cap, 1, frame, 12, 4)
	if err != nil {
		t.Fatalf("draw: %v", err)
	}
	got := decodeSixel(t, out)
	w, h := got.Bounds().Dx(), got.Bounds().Dy()
	// The very first pixel, before anything else. A sixel is written six rows
	// at a time and the band separator goes BETWEEN bands; one written before
	// the first band shifts the whole picture down six pixels and clips the
	// bottom off. Sampling the middle of each quadrant cannot see a six pixel
	// shift, so this samples the corner, where it is the difference between the
	// image and nothing at all.
	if r, g, b, _ := got.At(0, 0).RGBA(); diff(int(r>>8), 0xff) > 40 || g>>8 > 60 || b>>8 > 60 {
		t.Fatalf("the top left pixel is %d,%d,%d, want the red the picture starts with; "+
			"the sixel is shifted or the first band was not written", r>>8, g>>8, b>>8)
	}
	if w > 12*10 || h > 4*20 {
		t.Fatalf("the sixel is %dx%d pixels, over the %dx%d box it was given", w, h, 12*10, 4*20)
	}
	if w < 8 || h < 8 {
		t.Fatalf("the sixel decoded to %dx%d, which is not a picture", w, h)
	}

	// The four quadrants of the sample, read back out of the decoded bitmap.
	want := map[string]color.RGBA{
		"top left":     {R: 0xff, A: 0xff},
		"top right":    {G: 0xff, A: 0xff},
		"bottom left":  {B: 0xff, A: 0xff},
		"bottom right": {R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	}
	at := map[string]image.Point{
		"top left":     {X: w / 4, Y: h / 4},
		"top right":    {X: 3 * w / 4, Y: h / 4},
		"bottom left":  {X: w / 4, Y: 3 * h / 4},
		"bottom right": {X: 3 * w / 4, Y: 3 * h / 4},
	}
	for name, p := range at {
		r, g, b, _ := got.At(p.X, p.Y).RGBA()
		wr, wg, wb, _ := want[name].RGBA()
		// A fixed palette quantises, so the comparison is a tolerance rather
		// than an equality. Half a channel apart would be a different colour.
		if diff(int(r>>8), int(wr>>8)) > 40 || diff(int(g>>8), int(wg>>8)) > 40 || diff(int(b>>8), int(wb>>8)) > 40 {
			t.Fatalf("the %s quadrant decoded as %d,%d,%d, want about %d,%d,%d",
				name, r>>8, g>>8, b>>8, wr>>8, wg>>8, wb>>8)
		}
	}
}

func diff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// decodeSixel reads a DCS sixel bitmap back into an image. Written for the test
// on purpose: an encoder checked only by its own encoder proves nothing.
func decodeSixel(t *testing.T, s string) image.Image {
	t.Helper()
	start := strings.Index(s, "\x1bP")
	end := strings.LastIndex(s, "\x1b\\")
	if start < 0 || end < 0 {
		t.Fatalf("not a DCS sequence: %.40q", s)
	}
	body := s[start+2 : end]
	// Past the P1;P2;P3 q introducer.
	q := strings.Index(body, "q")
	if q < 0 {
		t.Fatal("no sixel introducer")
	}
	body = body[q+1:]

	var w, h int
	palette := map[int]color.RGBA{}
	var img *image.RGBA
	x, band, cur := 0, 0, 0

	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == '"':
			// The raster attributes: aspect numerator, denominator, width,
			// height.
			j := i + 1
			for j < len(body) && (body[j] == ';' || (body[j] >= '0' && body[j] <= '9')) {
				j++
			}
			parts := strings.Split(body[i+1:j], ";")
			if len(parts) == 4 {
				w, _ = strconv.Atoi(parts[2])
				h, _ = strconv.Atoi(parts[3])
				img = image.NewRGBA(image.Rect(0, 0, w, h))
			}
			i = j

		case c == '#':
			j := i + 1
			for j < len(body) && (body[j] == ';' || (body[j] >= '0' && body[j] <= '9')) {
				j++
			}
			parts := strings.Split(body[i+1:j], ";")
			n, _ := strconv.Atoi(parts[0])
			if len(parts) == 5 {
				// A definition: index ; 2 ; r ; g ; b as percentages.
				r, _ := strconv.Atoi(parts[2])
				g, _ := strconv.Atoi(parts[3])
				b, _ := strconv.Atoi(parts[4])
				palette[n] = color.RGBA{
					R: uint8(r * 255 / 100), G: uint8(g * 255 / 100), B: uint8(b * 255 / 100), A: 0xff,
				}
			}
			cur = n
			x = 0
			i = j

		case c == '$':
			x = 0
			i++

		case c == '-':
			band += 6
			x = 0
			i++

		case c == '!':
			j := i + 1
			for j < len(body) && body[j] >= '0' && body[j] <= '9' {
				j++
			}
			n, _ := strconv.Atoi(body[i+1 : j])
			if j >= len(body) {
				t.Fatal("a run length with nothing to repeat")
			}
			for k := 0; k < n; k++ {
				putSixel(img, palette[cur], x, band, body[j])
				x++
			}
			i = j + 1

		case c >= 0x3f && c <= 0x7e:
			putSixel(img, palette[cur], x, band, c)
			x++
			i++

		default:
			i++
		}
	}
	if img == nil {
		t.Fatal("the sixel carried no raster attributes, so its size is unknown")
	}
	return img
}

func putSixel(img *image.RGBA, c color.RGBA, x, band int, ch byte) {
	if img == nil {
		return
	}
	bits := ch - 0x3f
	for y := 0; y < 6; y++ {
		if bits&(1<<uint(y)) != 0 {
			img.SetRGBA(x, band+y, c)
		}
	}
}

func TestDrawRefusesWhatItCannotDraw(t *testing.T) {
	frame := sampleFrame(t, 32, 32)
	// errors.Is rather than a bare comparison: the sentinel is returned plain
	// today, and a caller that wrapped it with context would silently stop
	// matching under ==, which is the one way this assertion could go quiet
	// without anybody changing it.
	if _, err := Draw(Capability{}, 1, frame, 10, 5); !errors.Is(err, ErrNoProtocol) {
		t.Fatalf("a terminal that draws nothing returned %v, want ErrNoProtocol", err)
	}
	// A payload with a byte outside the base64 alphabet would end the escape
	// sequence early and print the rest of the image as text.
	if _, err := Draw(Capability{Protocol: ITerm2}, 1, "not base64!\a", 10, 5); err == nil {
		t.Fatal("a payload that is not base64 was accepted")
	}
	if _, err := Draw(Capability{Protocol: ITerm2}, 1, frame, 0, 5); err == nil {
		t.Fatal("a box with no width was accepted")
	}
	if _, err := Draw(Capability{Protocol: Kitty}, 1, "Zm9vYmFy", 10, 5); err == nil {
		t.Fatal("bytes that are not a JPEG were accepted")
	}
}

func TestPlacePutsTheCursorBackWhereItFoundIt(t *testing.T) {
	frame := sampleFrame(t, 32, 32)
	out, err := Place(Capability{Protocol: ITerm2}, 1, frame, 7, 13, 10, 5)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if !strings.HasPrefix(out, "\x1b7") {
		t.Fatal("the cursor was not saved, so the text drawn after this would land under the image")
	}
	if !strings.HasSuffix(out, "\x1b8") {
		t.Fatal("the cursor was not restored")
	}
	if !strings.Contains(out, "\x1b[7;13H") {
		t.Fatalf("the image was not positioned at row 7 column 13: %.60q", out)
	}
}

func TestFitShrinksToTheBoxAndKeepsTheShape(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	got := fit(src, 400, 400)
	if got.Bounds().Dx() != 400 || got.Bounds().Dy() != 250 {
		t.Fatalf("a 1280x800 frame fitted to 400x400 became %v, want 400x250", got.Bounds())
	}
	// Up is not scaling, it is spending bandwidth to add nothing, and it hands
	// the terminal a bitmap bigger than the box it was given.
	small := image.NewRGBA(image.Rect(0, 0, 20, 10))
	if got := fit(small, 400, 400); got.Bounds().Dx() != 20 {
		t.Fatalf("a small image was enlarged to %v", got.Bounds())
	}
}

func TestFitAveragesRatherThanSampling(t *testing.T) {
	// A one pixel wide black line on white, reduced ten to one. Point sampling
	// drops the line entirely nine times out of ten; averaging keeps it as grey,
	// and grey in the right place is what makes a screenshot of text legible.
	src := image.NewRGBA(image.Rect(0, 0, 100, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 100; x++ {
			src.Set(x, y, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
		}
		src.Set(45, y, color.RGBA{A: 0xff})
	}
	got := fit(src, 10, 1)
	r, _, _, _ := got.At(4, 0).RGBA()
	if r>>8 > 250 {
		t.Fatalf("the line was sampled away: the cell it fell in read %d, still white", r>>8)
	}
	r, _, _, _ = got.At(0, 0).RGBA()
	if r>>8 < 250 {
		t.Fatalf("a cell with no line in it is not white: %d", r>>8)
	}
}

func TestOverrideLetsSomebodyTellItWhatTheyHave(t *testing.T) {
	// Detection is a question asked down a file descriptor, and a multiplexer or
	// a forwarded connection can stop it reaching the program that would answer.
	detected := Capability{CellWidth: 10, CellHeight: 20, Why: "answered no"}

	if got := Override(detected, ""); got != detected {
		t.Fatalf("an empty override changed the answer: %+v", got)
	}
	if got := Override(detected, "auto"); got != detected {
		t.Fatalf("auto changed the answer: %+v", got)
	}
	for name, want := range map[string]Protocol{"kitty": Kitty, "iterm2": ITerm2, "sixel": Sixel} {
		got := Override(detected, name)
		if got.Protocol != want {
			t.Fatalf("AF_IMAGES=%s gave %v", name, got.Protocol)
		}
		// The measured cell size survives, because a forced protocol still has
		// to be sized against the real terminal.
		if got.CellWidth != 10 || got.CellHeight != 20 {
			t.Fatalf("AF_IMAGES=%s lost the measured cell size: %+v", name, got)
		}
	}
	if got := Override(detected, "OFF"); got.Protocol != NoImages || !strings.Contains(got.Why, "AF_IMAGES") {
		t.Fatalf("off did not switch pictures off: %+v", got)
	}

	// Sixel forced onto a terminal that never reported a cell size is still
	// refused. The number is not a matter of opinion.
	noCells := Capability{Why: "answered no"}
	if got := Override(noCells, "sixel"); got.Protocol != NoImages {
		t.Fatalf("sixel was forced with no cell size: %+v", got)
	}

	// A value this build does not know leaves the terminal's answer in place
	// AND says so. Quietly ignoring it would leave somebody staring at text
	// believing they had asked for pictures.
	unknown := Override(detected, "pixelfudge")
	if unknown.Protocol != NoImages {
		t.Fatalf("an unknown protocol was drawn anyway: %+v", unknown)
	}
	if !strings.Contains(unknown.Why, "pixelfudge") {
		t.Fatalf("the unknown value is not reported: %q", unknown.Why)
	}
}

func TestBackgroundIsReadFromTheTerminalRatherThanAssumed(t *testing.T) {
	// A palette tuned for a dark terminal is the palette that disappears on a
	// light one, and an ANSI colour is absolute, so this answer is the only
	// thing standing between the launch film's warm paper terminal and a live
	// mark nobody can read.
	light := "\x1b]11;rgb:f7f7/f4f4/eeee\x07" + plainReply
	if got := Interpret(light, envOf(nil), Winsize{}); got.Background != BackgroundLight {
		t.Fatalf("warm paper read as %v", got.Background)
	}
	dark := "\x1b]11;rgb:1414/1717/1a1a\x07" + plainReply
	if got := Interpret(dark, envOf(nil), Winsize{}); got.Background != BackgroundDark {
		t.Fatalf("a dark terminal read as %v", got.Background)
	}

	// A terminal that would not say is UNKNOWN, not dark. The two are different
	// facts and the caller keeps its default rather than being told one.
	if got := Interpret(plainReply, envOf(nil), Winsize{}); got.Background != BackgroundUnknown {
		t.Fatalf("a terminal that said nothing read as %v", got.Background)
	}

	// Terminals answer in one to four hex digits a channel, and the digit count
	// is the scale. Reading four digits as if they were two makes every
	// background black, which is the bug this arm exists to catch.
	short := "\x1b]11;rgb:f/f/e\x07" + plainReply
	if got := Interpret(short, envOf(nil), Winsize{}); got.Background != BackgroundLight {
		t.Fatalf("a single digit white read as %v", got.Background)
	}
	long := "\x1b]11;rgb:ffffff/ffffff/ffffff\x07" + plainReply
	if got := Interpret(long, envOf(nil), Winsize{}); got.Background != BackgroundUnknown {
		t.Fatalf("six digits a channel is not a scale this reads, so it should refuse, got %v", got.Background)
	}

	// The background survives being overridden, because it is a property of the
	// terminal rather than of what somebody asked for.
	detected := Interpret(dark, envOf(nil), Winsize{})
	if got := Override(detected, "off"); got.Background != BackgroundDark {
		t.Fatalf("switching pictures off lost the background: %v", got.Background)
	}
}

// TestReadWithDeadlineIsTheArmWindowsRuns exercises the read path that Windows
// uses outright, on a machine that is not Windows.
//
// It can be tested here precisely because of the asymmetry that made the split
// necessary: a PIPE takes a read deadline on any platform, and it is a macOS
// TERMINAL that the runtime poller refuses. So the arm that only one platform
// runs in production is still measured by every test run on every machine,
// which is the reason readWithDeadline is shared rather than written twice.
func TestReadWithDeadlineIsTheArmWindowsRuns(t *testing.T) {
	// A terminal that answers: the read returns as soon as the device
	// attributes reply is complete, without waiting the deadline out.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	if _, err := w.WriteString(kittyReply); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()

	started := time.Now()
	got, err := readWithDeadline(r, started.Add(5*time.Second))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != kittyReply {
		t.Fatalf("read %q, want the terminal's reply", got)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Fatalf("waited %s for an answer that had already arrived; the reply "+
			"is not being recognised as complete", waited)
	}
	// And the answer is one Interpret can actually use, so this is the whole
	// path rather than a string comparison.
	if cap := Interpret(got, envOf(nil), Winsize{}); cap.Protocol != Kitty {
		t.Fatalf("the reply read back as %v", cap.Protocol)
	}

	// A terminal that answers nothing costs the deadline once and is reported
	// as unknown rather than as a terminal that draws nothing.
	silent, sw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = silent.Close() }()
	defer func() { _ = sw.Close() }()

	quiet, err := readWithDeadline(silent, time.Now().Add(150*time.Millisecond))
	if err != nil {
		t.Fatalf("a silent terminal returned an error rather than nothing: %v", err)
	}
	if quiet != "" {
		t.Fatalf("a silent terminal returned %q", quiet)
	}
	if cap := Interpret(quiet, envOf(nil), Winsize{}); !strings.Contains(cap.Why, "did not answer") {
		t.Fatalf("a silent terminal was not reported as unasked: %q", cap.Why)
	}
}

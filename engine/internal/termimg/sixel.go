package termimg

import (
	"fmt"
	"image"
	"strings"
)

// The palette: the 216 colour cube every 256 colour terminal already uses, and
// a 24 step grey ramp beside it.
//
// A fixed palette rather than one computed per image, and that is a decision
// worth naming. A median cut over each frame would look better, and it would
// also mean a different palette every second, which on a terminal that shares
// one set of colour registers between everything on screen makes the other
// panes' pictures flicker as each new frame redefines the registers underneath
// them. A fixed palette is defined once, is the same for every pane, and makes
// a screenshot of a mostly flat user interface look like itself. The grey ramp
// is what keeps text legible: the cube's grey steps are 51 apart, and antialiased
// black on white lands between them.
const (
	cubeColors    = 216
	greyColors    = 24
	paletteColors = cubeColors + greyColors
)

// cubeLevels are the six values each channel of the colour cube takes.
var cubeLevels = [6]int{0, 51, 102, 153, 204, 255}

// paletteRGB is the colour of one palette entry, in 8 bit channels.
func paletteRGB(i int) (int, int, int) {
	if i < cubeColors {
		return cubeLevels[i/36], cubeLevels[(i/6)%6], cubeLevels[i%6]
	}
	v := 8 + (i-cubeColors)*10
	return v, v, v
}

// nearest maps one colour to the closest palette entry.
//
// The cube and the ramp are each searched analytically rather than by scanning
// 240 entries: the cube's levels are known, so the nearest level per channel is
// a lookup, and the ramp is evenly spaced, so the nearest step is a division.
// Two candidates are then compared. Scanning would cost 240 distance
// calculations for every pixel of every frame of every pane.
func nearest(r, g, b int) uint8 {
	ci := cubeIndex(r)*36 + cubeIndex(g)*6 + cubeIndex(b)
	cr, cg, cb := paletteRGB(ci)
	cube := dist(r, g, b, cr, cg, cb)

	// The grey ramp runs 8 to 238 in steps of 10.
	step := (r + g + b) / 3
	k := (step - 8 + 5) / 10
	if k < 0 {
		k = 0
	}
	if k >= greyColors {
		k = greyColors - 1
	}
	gi := cubeColors + k
	gr, gg, gb := paletteRGB(gi)
	if dist(r, g, b, gr, gg, gb) < cube {
		return uint8(gi)
	}
	return uint8(ci)
}

// cubeIndex is the nearest of the six cube levels to one channel.
func cubeIndex(v int) int {
	best, bestD := 0, 1<<30
	for i, level := range cubeLevels {
		d := level - v
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func dist(r1, g1, b1, r2, g2, b2 int) int {
	dr, dg, db := r1-r2, g1-g2, b1-b2
	return dr*dr + dg*dg + db*db
}

// encodeSixel turns an image into the DCS bitmap a VT340 and its descendants
// draw.
//
// The format is six pixel rows at a time. Within one band a character carries
// one column of six pixels as the low six bits of a printable byte, and the
// band is drawn once per colour present in it: write the colour, write its
// columns, carriage return, write the next colour. So a band with forty colours
// in it is forty passes over the same strip, which is why the encoder skips a
// colour's trailing empty columns and run length encodes the rest.
func encodeSixel(img *image.RGBA) string {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return ""
	}

	// Every pixel's palette index, and which entries the image actually uses.
	idx := make([]uint8, w*h)
	used := make([]bool, paletteColors)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(bounds.Min.X+x, bounds.Min.Y+y)
			p := nearest(int(img.Pix[o]), int(img.Pix[o+1]), int(img.Pix[o+2]))
			idx[y*w+x] = p
			used[p] = true
		}
	}

	var b strings.Builder
	// P1 = 0 (the default pixel aspect ratio), P2 = 1 (a pixel left unset keeps
	// whatever was under it), P3 = 0 (unused). Every pixel is written below, so
	// P2 only decides what happens to a band the image does not reach.
	b.WriteString("\x1bP0;1;0q")
	fmt.Fprintf(&b, "\"1;1;%d;%d", w, h)
	for i, on := range used {
		if !on {
			continue
		}
		r, g, bl := paletteRGB(i)
		// Sixel colour registers are percentages, not bytes.
		fmt.Fprintf(&b, "#%d;2;%d;%d;%d", i, pct(r), pct(g), pct(bl))
	}

	row := make([]byte, w)
	for top := 0; top < h; top += 6 {
		if top > 0 {
			b.WriteByte('-')
		}
		depth := h - top
		if depth > 6 {
			depth = 6
		}

		// Which colours appear anywhere in this band, in palette order so the
		// output is deterministic for the same image.
		inBand := make([]bool, paletteColors)
		for y := 0; y < depth; y++ {
			base := (top + y) * w
			for x := 0; x < w; x++ {
				inBand[idx[base+x]] = true
			}
		}

		firstColor := true
		for c := 0; c < paletteColors; c++ {
			if !inBand[c] {
				continue
			}
			// The bits for this colour across the strip, and how far right it
			// actually reaches. Everything past that is empty and is simply not
			// written, which is most of the output for most colours.
			last := -1
			for x := 0; x < w; x++ {
				var bits byte
				for y := 0; y < depth; y++ {
					if idx[(top+y)*w+x] == uint8(c) {
						bits |= 1 << uint(y)
					}
				}
				row[x] = 0x3f + bits
				if bits != 0 {
					last = x
				}
			}
			if last < 0 {
				continue
			}
			if !firstColor {
				// Carriage return: back to the left of the same band for the
				// next colour's pass.
				b.WriteByte('$')
			}
			firstColor = false
			fmt.Fprintf(&b, "#%d", c)
			writeRuns(&b, row[:last+1])
		}
	}
	b.WriteString("\x1b\\")
	return b.String()
}

// writeRuns writes a band's bytes with run length encoding.
//
// The threshold is four. A run is written as an exclamation mark, the count and
// the byte, so three characters plus the digits; below four repeats that is
// longer than writing the bytes out.
func writeRuns(b *strings.Builder, row []byte) {
	for i := 0; i < len(row); {
		j := i + 1
		for j < len(row) && row[j] == row[i] {
			j++
		}
		n := j - i
		if n >= 4 {
			fmt.Fprintf(b, "!%d%c", n, row[i])
		} else {
			for k := 0; k < n; k++ {
				b.WriteByte(row[i])
			}
		}
		i = j
	}
}

// pct converts an 8 bit channel to the 0 to 100 scale sixel colour registers
// use, rounding rather than truncating so white stays white.
func pct(v int) int { return (v*100 + 127) / 255 }

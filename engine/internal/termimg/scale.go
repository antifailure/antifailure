package termimg

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
)

// decodeJPEG turns the runner's base64 frame into pixels.
//
// Only JPEG, because that is the one format the live channel carries and
// accepting more would mean accepting an image whose decoder has different
// failure modes on a path that runs once a second per pane.
func decodeJPEG(b64 string) (image.Image, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(raw))
}

// fit scales an image down to sit inside w by h pixels, keeping its shape.
//
// Down only. A browser frame is always larger than the pane it is shown in, and
// enlarging one would spend bandwidth to add no detail and then hand the
// terminal a bitmap bigger than the box it was given.
//
// A box average rather than nearest neighbour. Nearest neighbour on a ten to
// one reduction samples one pixel in a hundred, so a line of text becomes a row
// of unrelated dots and a screenshot of a form reads as noise: the thing being
// looked at is mostly one pixel wide strokes, which is exactly what point
// sampling destroys. Averaging the source pixels that fall in each destination
// pixel keeps the strokes as grey, and grey in the right place is legible.
func fit(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 || w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	dw, dh := sw, sh
	if sw > w || sh > h {
		// The smaller of the two ratios is the one that makes both fit.
		if sw*h > sh*w {
			dw, dh = w, sh*w/sw
		} else {
			dw, dh = sw*h/sh, h
		}
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		y0 := b.Min.Y + dy*sh/dh
		y1 := b.Min.Y + (dy+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			x0 := b.Min.X + dx*sw/dw
			x1 := b.Min.X + (dx+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint32
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					cr, cg, cb, _ := src.At(x, y).RGBA()
					r += cr
					g += cg
					bl += cb
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			i := dst.PixOffset(dx, dy)
			// RGBA() returns 16 bit channels, so the average is shifted back to
			// 8 bits rather than truncated, which would darken the whole image
			// by a factor of 257.
			dst.Pix[i+0] = uint8(r / n >> 8)
			dst.Pix[i+1] = uint8(g / n >> 8)
			dst.Pix[i+2] = uint8(bl / n >> 8)
			dst.Pix[i+3] = 0xff
		}
	}
	return dst
}

package fstools

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // (GIF pictures are decoded to be shrunk.)
	"image/jpeg"
	"image/png"
)

// This file shrinks a picture for Read, as Claude Code's Read resizes and
// recompresses a large image to fit the model's limits (its tools
// reference, "Read tool behavior"). A PNG, JPEG or GIF longer than
// imageEdge on a side, or larger than Read hands on, is scaled down to fit
// imageEdge: a model is shown it at about that size anyway, and a local
// model's count of tokens for a picture grows with its pixels. It stays a
// PNG where that is small enough, and becomes a JPEG otherwise. WebP, which
// Go does not decode, goes on as it is, or is refused when too large.

const (
	// imageEdge is the longest side of a picture Read hands on.
	imageEdge = 1568
	// imageReadMost is the largest picture file Read takes in to shrink.
	imageReadMost = 64 << 20
	// jpegQuality is the quality of a picture kept as JPEG.
	jpegQuality = 85
)

// imagePixelsMost is the most pixels Read decodes to shrink a picture: a
// larger one is refused rather than held. Decoded, 25 million pixels take
// about 100 MB (8 bits a channel) to 200 MB (16), and a progressive JPEG
// more while it is decoded; one picture is shrunk at a time (shrinking).
// A variable, for tests.
var imagePixelsMost = 25_000_000

// shrinking lets one picture at a time be decoded to be shrunk, however
// many Reads run at once.
var shrinking = make(chan struct{}, 1)

// shrunk is a picture made smaller.
type shrunk struct {
	data          []byte
	media         string
	width, height int  // its size now
	fromW, fromH  int  // its size before, upright
	turned        bool // turned upright, as its EXIF orientation says
}

// shrinkImage scales a PNG, JPEG or GIF down to fit imageEdge and limit
// bytes, and turns a JPEG upright when its EXIF orientation says the
// camera held it turned (a model is shown the pixels as they are stored,
// and Go's encoder writes no EXIF). ok is false when it need not be
// shrunk, as it fits; err says why it cannot be. Of a GIF only the first
// frame is kept.
func shrinkImage(ctx context.Context, data []byte, media string, limit int64) (s shrunk, ok bool, err error) {
	if media == "image/webp" {
		return s, false, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return s, false, fmt.Errorf("it cannot be decoded (%v)", err)
	}
	turn := 1
	if media == "image/jpeg" {
		turn = jpegOrientation(data)
	}
	if max(cfg.Width, cfg.Height) <= imageEdge && int64(len(data)) <= limit && turn == 1 {
		return s, false, nil
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > int64(imagePixelsMost) {
		return s, false, fmt.Errorf("it is %dx%d, more pixels than are shrunk here", cfg.Width, cfg.Height)
	}
	select {
	case shrinking <- struct{}{}:
		defer func() { <-shrinking }()
	case <-ctx.Done():
		return s, false, context.Cause(ctx)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return s, false, fmt.Errorf("it cannot be decoded (%v)", err)
	}
	// (The size decoded, not the one the header gave: a GIF's first frame
	// may be smaller than its canvas.)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	w, h := sw, sh
	if long := max(sw, sh); long > imageEdge {
		w, h = max(1, w*imageEdge/long), max(1, h*imageEdge/long)
	}
	small := upright(boxScale(src, w, h), turn)
	s = shrunk{width: small.Rect.Dx(), height: small.Rect.Dy(), fromW: sw, fromH: sh, turned: turn != 1}
	if turn >= 5 {
		s.fromW, s.fromH = sh, sw
	}
	var b bytes.Buffer
	if media != "image/jpeg" {
		s.media = "image/png"
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, small)
	}
	if media == "image/jpeg" || (err == nil && int64(b.Len()) > limit) {
		// (JPEG keeps no transparency: what shows through is white.)
		b.Reset()
		s.media = "image/jpeg"
		err = jpeg.Encode(&b, onWhite(small), &jpeg.Options{Quality: jpegQuality})
	}
	if err != nil {
		return s, false, err
	}
	if int64(b.Len()) > limit {
		return s, false, errors.New("shrunk, it is still too large")
	}
	s.data = b.Bytes()
	return s, true, nil
}

// boxScale scales src to w by h, each pixel the average of the pixels it
// covers, weighted by how opaque they are (so that a clear pixel lends no
// colour to its neighbour's). A picture held other than as NRGBA is made
// NRGBA one band of rows at a time, the rows one row of the result
// covers, so that it is never held twice.
func boxScale(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	whole, isNRGBA := src.(*image.NRGBA)
	var band *image.NRGBA
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	// (The products are taken in 64 bits: on a 32-bit machine dx*sw
	// overflows for a picture 25 million pixels wide.)
	scaled := func(i, of, by int) int { return int(int64(i) * int64(of) / int64(by)) }
	for dy := 0; dy < h; dy++ {
		y0 := scaled(dy, sh, h)
		y1 := max(y0+1, scaled(dy+1, sh, h))
		n, top := whole, b.Min.Y+y0 // the rows y0 to y1, and where they start in n
		if !isNRGBA {
			if band == nil || band.Rect.Dy() < y1-y0 {
				band = image.NewNRGBA(image.Rect(0, 0, sw, y1-y0))
			}
			draw.Draw(band, image.Rect(0, 0, sw, y1-y0), src, image.Pt(b.Min.X, b.Min.Y+y0), draw.Src)
			n, top = band, 0
		}
		left := n.Rect.Min.X
		for dx := 0; dx < w; dx++ {
			x0 := scaled(dx, sw, w)
			x1 := max(x0+1, scaled(dx+1, sw, w))
			var r, g, bl, a uint64
			for y := y0; y < y1; y++ {
				off := n.PixOffset(left+x0, top+y-y0)
				for x := x0; x < x1; x++ {
					pa := uint64(n.Pix[off+3])
					r += uint64(n.Pix[off]) * pa
					g += uint64(n.Pix[off+1]) * pa
					bl += uint64(n.Pix[off+2]) * pa
					a += pa
					off += 4
				}
			}
			o := dst.PixOffset(dx, dy)
			if a > 0 {
				dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2] = uint8(r/a), uint8(g/a), uint8(bl/a)
			}
			dst.Pix[o+3] = uint8(a / uint64((y1-y0)*(x1-x0)))
		}
	}
	return dst
}

// onWhite is img over a white ground.
func onWhite(img *image.NRGBA) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, image.White, image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

// jpegOrientation is the orientation a JPEG's EXIF records, 1 to 8 as in
// the EXIF standard (1 is upright), or 1 when it records none. The EXIF
// is an APP1 segment, before the first scan.
func jpegOrientation(data []byte) int {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		switch m := data[i+1]; {
		case m == 0xFF: // a fill byte
			i++
			continue
		case m == 0x01 || m >= 0xD0 && m <= 0xD8: // a marker with no length
			i += 2
			continue
		case m == 0xDA || m == 0xD9: // the scan, or the end: no EXIF came
			return 1
		}
		n := int(data[i+2])<<8 | int(data[i+3])
		if n < 2 || i+2+n > len(data) {
			return 1
		}
		if seg := data[i+4 : i+2+n]; data[i+1] == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return exifOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

// exifOrientation reads the Orientation tag (0x0112) of an EXIF block's
// first directory: t is the block from its TIFF header on.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(t[2:]) != 42 {
		return 1
	}
	at := uint64(order.Uint32(t[4:]))
	if at < 8 || at+2 > uint64(len(t)) {
		return 1
	}
	count := uint64(order.Uint16(t[at:]))
	for k := range count {
		e := at + 2 + 12*k
		if e+12 > uint64(len(t)) {
			return 1
		}
		if order.Uint16(t[e:]) != 0x0112 {
			continue
		}
		// A SHORT (type 3), held in the first two bytes of the value.
		if v := int(order.Uint16(t[e+8:])); order.Uint16(t[e+2:]) == 3 && v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

// upright turns img as EXIF orientation turn says it must be turned to
// be seen as the camera saw it: 2 mirrors it, 3 turns it half round, 4
// flips it, 5 to 8 turn it a quarter (mirrored for 5 and 7), which swaps
// its width and height.
func upright(img *image.NRGBA, turn int) *image.NRGBA {
	if turn < 2 || turn > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if turn >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var nx, ny int
			switch turn {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			si, di := img.PixOffset(b.Min.X+x, b.Min.Y+y), dst.PixOffset(nx, ny)
			copy(dst.Pix[di:di+4], img.Pix[si:si+4])
		}
	}
	return dst
}

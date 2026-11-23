package fstools

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// picture makes a w by h picture: a smooth wash of colour, or noise that
// does not compress; half of it is clear when clear is set.
func picture(w, h int, clear, noisy bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rnd := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if clear && x < w/2 {
				a = 0
			}
			c := color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, a}
			if noisy {
				v := rnd.Uint32()
				c = color.NRGBA{uint8(v), uint8(v >> 8), uint8(v >> 16), a}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func encoded(t *testing.T, img image.Image, kind string) []byte {
	t.Helper()
	var b bytes.Buffer
	var err error
	if kind == "jpeg" {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 95})
	} else {
		err = png.Encode(&b, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A picture longer than a model is shown, or larger than Read hands on,
// comes back shrunk to fit, as Claude Code's Read shrinks one; a small one
// comes back as it is; one of more pixels than are decoded here, that fits
// in bytes, goes as it is too.
func TestReadShrinksLargePictures(t *testing.T) {
	f := newFixture(t, Options{})
	big := f.file("big.png", string(encoded(t, picture(3000, 2000, true, false), "png")))
	res := f.run(f.read, in{"file_path": big})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	mustContain(t, res.Content[0].Text, "3000x2000, shown shrunk to 1568x1045 (PNG")
	got, _, err := image.Decode(bytes.NewReader(res.Content[1].Data))
	if err != nil || got.Bounds().Dx() != 1568 || got.Bounds().Dy() != 1045 {
		t.Fatalf("the picture sent: %v, %v", got.Bounds(), err)
	}
	if _, _, _, a := got.At(10, 10).RGBA(); a != 0 {
		t.Errorf("the clear half is no longer clear: alpha %d", a)
	}
	if res.Content[1].MediaType != "image/png" {
		t.Errorf("sent as %s", res.Content[1].MediaType)
	}

	photo := f.file("photo.jpg", string(encoded(t, picture(2000, 2000, false, false), "jpeg")))
	res = f.run(f.read, in{"file_path": photo})
	mustContain(t, core.TextOf(res.Content), "shown shrunk to 1568x1568 (JPEG")
	if res.Content[1].MediaType != "image/jpeg" {
		t.Errorf("a JPEG went as %s", res.Content[1].MediaType)
	}

	small := encoded(t, picture(100, 50, false, false), "png")
	p := f.file("small.png", string(small))
	res = f.run(f.read, in{"file_path": p})
	if !bytes.Equal(res.Content[1].Data, small) {
		t.Error("a small picture was changed")
	}

	defer func(n int) { imagePixelsMost = n }(imagePixelsMost)
	imagePixelsMost = 1000
	wide := encoded(t, picture(2000, 10, false, false), "png")
	q := filepath.Join(f.dir, "wide.png")
	if err := os.WriteFile(q, wide, 0o644); err != nil {
		t.Fatal(err)
	}
	res = f.run(f.read, in{"file_path": q})
	if res.IsError || !bytes.Equal(res.Content[1].Data, wide) {
		t.Errorf("a picture of too many pixels that fits was not sent as it is: %s", core.TextOf(res.Content))
	}
}

// Larger than Read hands on, and more pixels than are decoded here: it is
// refused, and says why.
func TestReadRefusesAPictureItCannotShrink(t *testing.T) {
	defer func(n int) { imagePixelsMost = n }(imagePixelsMost)
	imagePixelsMost = 1000
	f := newFixture(t, Options{ReadMaxImageBytes: 2000})
	p := f.file("wide.png", string(encoded(t, picture(3000, 40, false, true), "png")))
	f.fails(f.read, in{"file_path": p}, "could not be shrunk", "more pixels than are shrunk here", "smaller copy")
}

// halves is a w by h picture, red on the left and blue on the right.
func halves(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// withOrientation puts into a JPEG the EXIF block a camera writes to say
// how it was held: orientation o, in byte order "MM" or "II".
func withOrientation(jpg []byte, o uint16, order string) []byte {
	var bo binary.AppendByteOrder = binary.BigEndian
	if order == "II" {
		bo = binary.LittleEndian
	}
	t := []byte(order)
	t = bo.AppendUint16(t, 42)
	t = bo.AppendUint32(t, 8)
	t = bo.AppendUint16(t, 1)      // one entry
	t = bo.AppendUint16(t, 0x0112) // Orientation
	t = bo.AppendUint16(t, 3)      // a SHORT
	t = bo.AppendUint32(t, 1)      // one of it
	t = bo.AppendUint16(t, o)
	t = append(t, 0, 0)
	t = bo.AppendUint32(t, 0) // no next directory
	seg := append([]byte("Exif\x00\x00"), t...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, 0xFF, 0xE1, byte((len(seg)+2)>>8), byte(len(seg)+2))
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// colourAt says whether the picture is red or blue at x, y.
func colourAt(img image.Image, x, y int) string {
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	switch {
	case c.R > 180 && c.B < 80:
		return "red"
	case c.B > 180 && c.R < 80:
		return "blue"
	}
	return "neither"
}

// A photo whose EXIF says the camera was held turned comes back upright,
// shrunk or not: Go's decoder leaves EXIF alone and its encoder writes
// none, so a phone's photo reached the model on its side (the review of
// 2026-11-20). A photo with no EXIF that fits goes as it is.
func TestReadTurnsPhotosUpright(t *testing.T) {
	f := newFixture(t, Options{})
	// Orientation 6: turned a quarter clockwise to be seen, so the left
	// of what is stored is the top of what is seen.
	p := f.file("tall.jpg", string(withOrientation(encoded(t, halves(2000, 1000), "jpeg"), 6, "MM")))
	res := f.run(f.read, in{"file_path": p})
	mustContain(t, core.TextOf(res.Content), "1000x2000, shown turned upright, as its EXIF orientation says, and shrunk to 784x1568 (JPEG")
	got, _, err := image.Decode(bytes.NewReader(res.Content[1].Data))
	if err != nil || got.Bounds().Dx() != 784 || got.Bounds().Dy() != 1568 {
		t.Fatalf("the picture sent: %v, %v", got.Bounds(), err)
	}
	if top, bottom := colourAt(got, 392, 100), colourAt(got, 392, 1468); top != "red" || bottom != "blue" {
		t.Errorf("top %s, bottom %s; want red over blue", top, bottom)
	}
	// Orientation 8, little-endian, small enough to go as it is but for
	// the turn: the left goes to the bottom.
	q := f.file("small.jpg", string(withOrientation(encoded(t, halves(100, 50), "jpeg"), 8, "II")))
	res = f.run(f.read, in{"file_path": q})
	mustContain(t, core.TextOf(res.Content), "50x100, shown turned upright, as its EXIF orientation says, at 50x100 (JPEG")
	got, _, err = image.Decode(bytes.NewReader(res.Content[1].Data))
	if err != nil {
		t.Fatal(err)
	}
	if top, bottom := colourAt(got, 25, 10), colourAt(got, 25, 90); top != "blue" || bottom != "red" {
		t.Errorf("top %s, bottom %s; want blue over red", top, bottom)
	}
	plain := encoded(t, halves(100, 50), "jpeg")
	r := f.file("plain.jpg", string(plain))
	if res = f.run(f.read, in{"file_path": r}); !bytes.Equal(res.Content[1].Data, plain) {
		t.Error("a small photo with no EXIF was changed")
	}
	for _, c := range []struct {
		data []byte
		want int
	}{
		{plain, 1},
		{withOrientation(plain, 3, "II"), 3},
		{withOrientation(plain, 9, "MM"), 1},
		{withOrientation(plain, 6, "XX"), 1},
		{withOrientation(plain, 6, "MM")[:30], 1},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, 1},
		{nil, 1},
	} {
		if got := jpegOrientation(c.data); got != c.want {
			t.Errorf("orientation of %x... = %d, want %d", c.data[:min(len(c.data), 12)], got, c.want)
		}
	}
}

// A GIF is shrunk by its first frame, at that frame's own size: one
// smaller than the GIF's canvas is not stretched to the canvas's shape.
// It says only the first frame is shown.
func TestReadShrinksAGIFByItsFirstFrame(t *testing.T) {
	f := newFixture(t, Options{})
	frame := image.NewPaletted(image.Rect(0, 0, 2000, 500), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10},
		Config: image.Config{Width: 3000, Height: 2000, ColorModel: frame.Palette}})
	if err != nil {
		t.Fatal(err)
	}
	p := f.file("moving.gif", b.String())
	res := f.run(f.read, in{"file_path": p})
	mustContain(t, core.TextOf(res.Content), "2000x500, shown shrunk to 1568x392 (PNG", "of a GIF, the first frame only")
}

// Pictures are shrunk one at a time; one that waits its turn stops
// waiting when its call is stopped.
func TestShrinkingWaitsItsTurnOrItsCaller(t *testing.T) {
	shrinking <- struct{}{}
	defer func() { <-shrinking }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok, err := shrinkImage(ctx, encoded(t, picture(2000, 100, false, false), "png"), "image/png", 5<<20)
	if ok || !errors.Is(err, context.Canceled) {
		t.Errorf("ok %v, err %v; want it stopped", ok, err)
	}
}

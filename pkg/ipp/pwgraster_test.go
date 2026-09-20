package ipp

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// pwgFixture builds a PWG Raster stream: the "RaS2" sync word, one 1796-
// byte page header and the given bitmap data.
func pwgFixture(width, height, bpp, colorSpace, numColors int, orientation, copies uint32, bitmap []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(pwgSyncWord))
	hdr := make([]byte, pwgHeaderSize)
	copy(hdr[0:], "PwgRaster")
	put := func(off int, v uint32) { binary.BigEndian.PutUint32(hdr[off:], v) }
	put(pwgOffHWResolution, 300)
	put(pwgOffHWResolution+4, 300)
	put(pwgOffMediaPosition, 1) // main
	put(pwgOffNumCopies, copies)
	put(pwgOffOrientation, orientation)
	put(pwgOffWidth, uint32(width))
	put(pwgOffHeight, uint32(height))
	put(pwgOffBitsPerColor, 8)
	put(pwgOffBitsPerPixel, uint32(bpp))
	put(pwgOffBytesPerLine, uint32((width*bpp+7)/8))
	put(pwgOffColorOrder, 0)
	put(pwgOffColorSpace, uint32(colorSpace))
	put(pwgOffNumColors, uint32(numColors))
	put(pwgOffTotalPageCount, 1)
	buf.Write(hdr)
	buf.Write(bitmap)
	return buf.Bytes()
}

// literalLine encodes one scan line: a line-repeat count of 1, then one
// control byte for the literal run (257 - n chunks) and the chunk data.
// chunks is the number of pixel chunks (1 per pixel at 8 bpp, 1 per RGB
// triple at 24 bpp).
func literalLine(chunks int, pixels []byte) []byte {
	out := []byte{0x00, byte(257 - chunks)}
	return append(out, pixels...)
}

func TestDecodePWGRasterGray(t *testing.T) {
	// 4x4 8-bit grayscale page. Line 0: 10 20 30 40; line 1: identical
	// content sent as a line repeat; line 2: 50 60 70 80.
	line0 := literalLine(4, []byte{10, 20, 30, 40})
	line1 := literalLine(4, []byte{10, 20, 30, 40})
	line1[0] = 0x01 // repeat the previous line once more
	line2 := literalLine(4, []byte{50, 60, 70, 80})
	bitmap := append(append(line0, line1...), line2...)

	data := pwgFixture(4, 4, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, bitmap)
	pages, err := DecodePWGRaster(data)
	if err != nil {
		t.Fatalf("DecodePWGRaster: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	p := pages[0]
	if p.Width != 4 || p.Height != 4 {
		t.Fatalf("page size %dx%d, want 4x4", p.Width, p.Height)
	}
	if p.BitsPerPixel != 8 || p.ColorSpace != ColorSpaceBlack {
		t.Fatalf("page format %d bpp / colorspace %d, want 8/%d", p.BitsPerPixel, p.ColorSpace, ColorSpaceBlack)
	}
	if p.NumCopies != 1 || p.MediaPosition != 1 {
		t.Fatalf("NumCopies=%d MediaPosition=%d, want 1/1", p.NumCopies, p.MediaPosition)
	}
	want := []uint8{10, 20, 30, 40, 10, 20, 30, 40, 10, 20, 30, 40, 50, 60, 70, 80}
	for i, v := range want {
		if p.Image.Pix[i] != v {
			t.Fatalf("pixel %d = %d, want %d", i, p.Image.Pix[i], v)
		}
	}
}

func TestDecodePWGRasterSRGB(t *testing.T) {
	// 2x2 24-bit sRGB page: pure red, green, blue, white.
	row1 := []byte{255, 0, 0, 0, 255, 0}
	row2 := []byte{0, 0, 255, 255, 255, 255}
	bitmap := append(literalLine(2, row1), literalLine(2, row2)...)
	data := pwgFixture(2, 2, 24, ColorSpaceSRGB, 3, OrientationPortrait, 1, bitmap)
	pages, err := DecodePWGRaster(data)
	if err != nil {
		t.Fatalf("DecodePWGRaster: %v", err)
	}
	p := pages[0]
	if p.BitsPerPixel != 24 || p.ColorSpace != ColorSpaceSRGB {
		t.Fatalf("page format %d bpp / colorspace %d, want 24/%d", p.BitsPerPixel, p.ColorSpace, ColorSpaceSRGB)
	}
	want := []uint8{77, 149, 29, 255}
	for i, v := range want {
		if p.Image.Pix[i] != v {
			t.Fatalf("pixel %d = %d, want %d (gray conversion)", i, p.Image.Pix[i], v)
		}
	}
}

func TestDecodePWGRasterLandscapeNotRotated(t *testing.T) {
	// The bitmap is transmitted in print orientation: a landscape page
	// (Orientation=1) arrives wider than tall and must be printed as-is.
	pixels := []byte{1, 2, 3, 4}
	data := pwgFixture(4, 1, 8, ColorSpaceSGray, 1, OrientationLandscape, 1, literalLine(4, pixels))
	pages, err := DecodePWGRaster(data)
	if err != nil {
		t.Fatalf("DecodePWGRaster: %v", err)
	}
	p := pages[0]
	if p.Orientation != OrientationLandscape {
		t.Fatalf("Orientation = %d, want %d", p.Orientation, OrientationLandscape)
	}
	if p.Width != 4 || p.Height != 1 {
		t.Fatalf("landscape page size %dx%d, want 4x1 (bitmap kept as transmitted)", p.Width, p.Height)
	}
	if p.Image.Pix[0] != 1 || p.Image.Pix[3] != 4 {
		t.Fatal("landscape bitmap content was modified")
	}
}

func TestDecodePWGRasterMultiPage(t *testing.T) {
	data := pwgFixture(2, 1, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, literalLine(2, []byte{1, 2}))
	data = append(data, pwgFixture(2, 1, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, literalLine(2, []byte{3, 4}))[4:]...)
	pages, err := DecodePWGRaster(data)
	if err != nil {
		t.Fatalf("DecodePWGRaster: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if pages[0].Image.Pix[0] != 1 || pages[1].Image.Pix[0] != 3 {
		t.Fatal("page content not decoded per page")
	}
}

func TestDecodePWGRasterErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"bad sync", []byte("NOPE")},
		{"wrong dpi", func() []byte {
			d := pwgFixture(2, 2, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, literalLine(2, []byte{1, 2}))
			binary.BigEndian.PutUint32(d[4+pwgOffHWResolution:], 600)
			return d
		}()},
		{"unsupported colorspace", pwgFixture(2, 2, 8, ColorSpaceBlack+100, 1, OrientationPortrait, 1, literalLine(2, []byte{1, 2}))},
		{"color page as gray", pwgFixture(2, 2, 24, ColorSpaceSRGB, 1, OrientationPortrait, 1, literalLine(1, []byte{1, 2, 3}))},
		{"truncated bitmap", pwgFixture(2, 2, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, []byte{0x00, 1})},
		{"repeat overflows page", func() []byte {
			d := pwgFixture(2, 2, 8, ColorSpaceBlack, 1, OrientationPortrait, 1, []byte{0x02, 0xFE, 1, 2})
			return d
		}()},
	}
	for _, c := range cases {
		if _, err := DecodePWGRaster(c.data); err == nil {
			t.Errorf("%s: expected error, got none", c.name)
		}
	}
}

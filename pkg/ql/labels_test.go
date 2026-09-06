package ql

import (
	"bytes"
	"math"
	"testing"
)

func TestLookupMedia(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "29"},
		{"29", "29"},
		{"29mm", "29"},
		{"29 mm", "29"},
		{"29MM", "29"},
		{"62x100", "62x100"},
		{"62x100mm", "62x100"},
		{"d24", "d24"},
	}
	for _, tc := range cases {
		m, err := LookupMedia(tc.in)
		if err != nil {
			t.Errorf("LookupMedia(%q): %v", tc.in, err)
			continue
		}
		if m.ID != tc.want {
			t.Errorf("LookupMedia(%q) = %q, want %q", tc.in, m.ID, tc.want)
		}
	}
	if _, err := LookupMedia("13mm"); err == nil {
		t.Error("expected error for unknown media")
	}
}

func TestMediaTableValues(t *testing.T) {
	// Dot counts cross-checked against the official Command Reference,
	// section 3.2.2/3.2.5 (QL-500/550/560/570/580N/650TD/700).
	want := map[string]struct {
		width, height, rightMargin, feed int
	}{
		"12":     {106, 0, 29, 35},
		"29":     {306, 0, 6, 35},
		"38":     {413, 0, 12, 35},
		"50":     {554, 0, 12, 35},
		"54":     {590, 0, 0, 35},
		"62":     {696, 0, 12, 35},
		"17x54":  {165, 566, 0, 0},
		"17x87":  {165, 956, 0, 0},
		"23x23":  {236, 202, 42, 0},
		"29x90":  {306, 991, 6, 0},
		"38x90":  {413, 991, 12, 0},
		"39x48":  {425, 495, 6, 0},
		"52x29":  {578, 271, 0, 0},
		"62x29":  {696, 271, 12, 0},
		"62x100": {696, 1109, 12, 0},
		"d12":    {94, 94, 113, 35},
		"d24":    {236, 236, 42, 0},
		"d58":    {618, 618, 51, 0},
	}
	for id, w := range want {
		m, err := LookupMedia(id)
		if err != nil {
			t.Errorf("LookupMedia(%q): %v", id, err)
			continue
		}
		if m.PrintableWidthDots != w.width || m.PrintableHeightDots != w.height ||
			m.RightMarginDots != w.rightMargin || m.FeedMarginDots != w.feed {
			t.Errorf("%s: got %dx%d right=%d feed=%d, want %dx%d right=%d feed=%d",
				id, m.PrintableWidthDots, m.PrintableHeightDots, m.RightMarginDots, m.FeedMarginDots,
				w.width, w.height, w.rightMargin, w.feed)
		}
	}
}

func TestMMDotsConversion(t *testing.T) {
	if d := MMToDots(12.7); d != 150 {
		t.Errorf("MMToDots(12.7) = %d, want 150", d)
	}
	if d := MMToDots(1000); d != 11811 {
		t.Errorf("MMToDots(1000) = %d, want 11811", d)
	}
	if d := MMToDots(29); d != 343 {
		t.Errorf("MMToDots(29) = %d, want 343", d)
	}
	if mm := DotsToMM(150); math.Abs(mm-12.7) > 1e-9 {
		t.Errorf("DotsToMM(150) = %f, want 12.7", mm)
	}
	// Round-trip sanity (max error is half a dot = 0.042 mm).
	for _, mm := range []float64{12.7, 25, 40, 62.5, 100, 1000} {
		if got := DotsToMM(MMToDots(mm)); math.Abs(got-mm) > 0.05 {
			t.Errorf("round trip %.1f mm -> %.4f mm", mm, got)
		}
	}
}

func TestRasterPlacement(t *testing.T) {
	// A single black pixel at image x=0 (left edge of the print area) on
	// 29mm media (right margin 6 pins, width 306 dots) must land at
	// transmitted bit t = 6 + 306 - 1 - 0 = 311, i.e. bit 7 of byte 38
	// (MSB first).
	m, err := LookupMedia("29")
	if err != nil {
		t.Fatal(err)
	}
	img := newCanvas(306, 1)
	img.set(0, 0, 0) // black at x=0
	rows, err := rasterize(img, m, 306, 1, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0]) != 90 {
		t.Fatalf("unexpected raster shape %d x %d", len(rows), len(rows[0]))
	}
	expect := make([]byte, 90)
	expect[38] = 0x01
	if !bytes.Equal(rows[0], expect) {
		t.Errorf("pixel x=0: got % X, want % X", rows[0], expect)
	}

	// A black pixel at the right edge of the print area (x=305) lands at
	// transmitted bit t = 6, i.e. bit 1 of byte 0.
	img2 := newCanvas(306, 1)
	img2.set(305, 0, 0)
	rows2, err := rasterize(img2, m, 306, 1, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	expect2 := make([]byte, 90)
	expect2[0] = 0x02
	if !bytes.Equal(rows2[0], expect2) {
		t.Errorf("pixel x=305: got % X, want % X", rows2[0], expect2)
	}
}

func TestRasterPlacement62mmOfficialExample(t *testing.T) {
	// The official reference gives a raster line for a solid print area on
	// 62mm tape as: 00 0F FF(86x) F0 00. A fully black print area (696
	// dots, right margin 12) must reproduce exactly that pattern.
	m, err := LookupMedia("62")
	if err != nil {
		t.Fatal(err)
	}
	img := newCanvas(696, 1)
	for x := 0; x < 696; x++ {
		img.set(x, 0, 0)
	}
	rows, err := rasterize(img, m, 696, 1, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x0F}
	want = append(want, bytes.Repeat([]byte{0xFF}, 86)...)
	want = append(want, 0xF0, 0x00)
	if !bytes.Equal(rows[0], want) {
		t.Errorf("62mm solid row:\ngot  % X\nwant % X", rows[0], want)
	}
}

func TestModelConstants(t *testing.T) {
	if QL570.RowBytes != 90 || QL570.RowPins != 720 {
		t.Errorf("QL-570 row geometry: %d bytes / %d pins", QL570.RowBytes, QL570.RowPins)
	}
	if QL570.MinLengthDots != 150 || QL570.MaxLengthDots != 11811 {
		t.Errorf("QL-570 length bounds: %d..%d", QL570.MinLengthDots, QL570.MaxLengthDots)
	}
	if QL570.SupportsCompressionTIFF {
		t.Error("QL-570 must not support TIFF compression")
	}
	if QL570.SupportsModeSwitch {
		t.Error("QL-570 must not require command mode switch")
	}
}

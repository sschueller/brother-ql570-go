package ql

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func textJob(text string, lengthMM float64) *Job {
	return &Job{Text: []string{text}, LengthMM: lengthMM}
}

func TestRenderText(t *testing.T) {
	j := textJob("HELLO", 40)
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob: %v", err)
	}
	if len(rows) != MMToDots(40) {
		t.Fatalf("row count %d, want %d", len(rows), MMToDots(40))
	}
	black := 0
	for _, row := range rows {
		for _, b := range row {
			black += popcount(b)
		}
	}
	if black < 500 {
		t.Errorf("expected text to produce ink, got %d black dots", black)
	}
}

func TestRenderMirror(t *testing.T) {
	j := textJob("R", 40)
	j.Mirror = true
	j.DefaultJobValues()
	media, _ := j.Validate()
	mirrored, err := RenderJob(j, media)
	if err != nil {
		t.Fatal(err)
	}
	j.Mirror = false
	plain, err := RenderJob(j, media)
	if err != nil {
		t.Fatal(err)
	}
	same := true
	for i := range mirrored {
		if !bytes.Equal(mirrored[i], plain[i]) {
			same = false
			break
		}
	}
	if same {
		// "R" is asymmetric; its mirror must differ.
		t.Error("mirrored raster should differ from plain raster for left-aligned text")
	}
}

func TestRenderHires(t *testing.T) {
	j := textJob("TEST", 40)
	j.Hires = true
	j.DefaultJobValues()
	media, _ := j.Validate()
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob: %v", err)
	}
	if len(rows) != 2*MMToDots(40) {
		t.Fatalf("hires row count %d, want %d", len(rows), 2*MMToDots(40))
	}
	for _, row := range rows {
		if len(row) != QL570.RowBytes {
			t.Fatalf("hires row width %d, want %d", len(row), QL570.RowBytes)
		}
	}
}

func TestRenderJobImage(t *testing.T) {
	j := textJob("HELLO", 40)
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	img, err := RenderJobImage(j, media)
	if err != nil {
		t.Fatalf("RenderJobImage: %v", err)
	}
	if img.Bounds().Dx() != media.PrintableWidthDots {
		t.Errorf("image width %d, want %d", img.Bounds().Dx(), media.PrintableWidthDots)
	}
	if img.Bounds().Dy() != MMToDots(40) {
		t.Errorf("image height %d, want %d", img.Bounds().Dy(), MMToDots(40))
	}
	ink, white := 0, 0
	for _, v := range img.Pix {
		switch {
		case v < 128:
			ink++
		case v == 255:
			white++
		}
	}
	if ink < 500 {
		t.Errorf("expected text ink in preview, got %d dark pixels", ink)
	}
	if white == 0 {
		t.Error("expected white background in preview")
	}
}

func TestRenderJobImageHires(t *testing.T) {
	j := textJob("TEST", 40)
	j.Hires = true
	j.DefaultJobValues()
	media, _ := j.Validate()
	img, err := RenderJobImage(j, media)
	if err != nil {
		t.Fatalf("RenderJobImage hires: %v", err)
	}
	if img.Bounds().Dy() != 2*MMToDots(40) {
		t.Errorf("hires image height %d, want %d", img.Bounds().Dy(), 2*MMToDots(40))
	}
}

func TestRenderJobImageAutoFit(t *testing.T) {
	j := textJob("SW-01 uplink", 0)
	j.FontSize = 20
	j.Text = []string{"SW-01", "eth1/1"}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	img, err := RenderJobImage(j, media)
	if err != nil {
		t.Fatalf("RenderJobImage auto: %v", err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dy() != len(rows) {
		t.Errorf("preview height %d != raster row count %d", img.Bounds().Dy(), len(rows))
	}
	if img.Bounds().Dy() >= MMToDots(MaxLengthMM) {
		t.Errorf("auto-fit preview not trimmed: %d px tall", img.Bounds().Dy())
	}
}

func TestRenderRotate180(t *testing.T) {
	j := textJob("HELLO", 40)
	j.Rotate = 180
	j.DefaultJobValues()
	media, _ := j.Validate()
	if _, err := RenderJob(j, media); err != nil {
		t.Fatalf("RenderJob rotate 180: %v", err)
	}
}

func TestRenderRotate90RejectedWhenTooTall(t *testing.T) {
	// 29mm media is 306px wide and 40mm long (472px): a long line of
	// text rotated 90 degrees would need more than 306px of width.
	j := textJob("THIS IS A VERY LONG LINE OF TEXT", 40)
	j.Rotate = 90
	j.DefaultJobValues()
	media, _ := j.Validate()
	if _, err := RenderJob(j, media); err == nil {
		t.Error("expected error for over-wide rotated content")
	}
}

func TestRenderQRAndBarcode(t *testing.T) {
	j := &Job{
		Text:     []string{"rack-7"},
		QR:       "https://example.com/rack-7",
		Barcode:  "ABC123",
		LengthMM: 60,
	}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob: %v", err)
	}
	black := 0
	for _, row := range rows {
		for _, b := range row {
			black += popcount(b)
		}
	}
	if black < 2000 {
		t.Errorf("expected QR + barcode ink, got %d black dots", black)
	}
}

// TestRenderBarcodeNotSolidBlock is a regression test: a Code128 barcode
// must render as alternating bars, not a solid black rectangle (boombuler
// fills the background with opaque white, which was once mistaken for ink).
func TestRenderBarcodeNotSolidBlock(t *testing.T) {
	j := &Job{Barcode: "ABC-123456", LengthMM: 30}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob: %v", err)
	}
	width := media.PrintableWidthDots
	// The barcode occupies the top 10 mm (118 dots) of the label.
	barHeight := MMToDots(10)
	if barHeight > len(rows) {
		t.Fatalf("label too short for barcode: %d rows", len(rows))
	}
	// Count black/white transitions across a row through the barcode and
	// the total ink. A solid block has 1 transition; a Code128 barcode of
	// this length has well over 50.
	maxTransitions := 0
	totalInk := 0
	for y := 0; y < barHeight; y++ {
		transitions := 0
		prev := false
		for x := 0; x < width; x++ {
			cur := bitAt(rows, media, width, x, y)
			if cur != prev {
				transitions++
				prev = cur
			}
			if cur {
				totalInk++
			}
		}
		if transitions > maxTransitions {
			maxTransitions = transitions
		}
	}
	if maxTransitions < 20 {
		t.Errorf("barcode renders as a block: only %d black/white transitions across a row", maxTransitions)
	}
	area := width * barHeight
	if totalInk > area*9/10 {
		t.Errorf("barcode nearly solid: %d/%d dots are black", totalInk, area)
	}
}

// bitAt extracts one pixel from the raster rows, undoing the right-margin
// placement used by rasterize.
func bitAt(rows [][]byte, media Media, width, x, y int) bool {
	p := media.RightMarginDots + width - 1 - x
	return rows[y][p>>3]&(1<<(7-p&7)) != 0
}

func TestRenderContentOverflow(t *testing.T) {
	j := &Job{
		Text:     []string{"line1", "line2", "line3", "line4", "line5", "line6", "line7", "line8", "line9", "line10", "line11", "line12"},
		LengthMM: 12.7,
	}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderJob(j, media); err == nil {
		t.Error("expected overflow error for content taller than the label")
	}
}

// TestRenderAutoFit verifies that continuous media without an explicit
// length produces exactly the height needed for the content (no trailing
// whitespace beyond the layout gap).
func TestRenderAutoFit(t *testing.T) {
	j := textJob("SW-01 uplink", 0)
	j.FontSize = 20 // taller than the 12.7mm minimum clamp
	j.Text = []string{"SW-01", "eth1/1", "rack-7"}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob auto: %v", err)
	}
	// The auto-fitted length must equal the content height when the same
	// job is rendered with an oversized explicit length and then trimmed.
	jLong := *j
	jLong.LengthMM = 100
	longRows, err := RenderJob(&jLong, media)
	if err != nil {
		t.Fatal(err)
	}
	lastInk := 0
	for i, row := range longRows {
		if !allZero(row) {
			lastInk = i
		}
	}
	trimmed := lastInk + 1
	if len(rows) != trimmed {
		t.Errorf("auto-fitted rows %d != trimmed content rows %d", len(rows), trimmed)
	}
	if len(rows) >= len(longRows) {
		t.Errorf("auto-fitted label should be much shorter than the 100mm canvas: %d", len(rows))
	}
	if len(rows) < MMToDots(MinLengthMM) {
		t.Errorf("auto-fitted label shorter than the printer minimum: %d rows", len(rows))
	}
}

// TestRenderAutoFitMinimum verifies that tiny content is clamped up to the
// printer's minimum label length (12.7 mm = 150 dots).
func TestRenderAutoFitMinimum(t *testing.T) {
	j := textJob(".", 0)
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob auto: %v", err)
	}
	if len(rows) != MMToDots(MinLengthMM) {
		t.Errorf("minimum clamp: got %d rows, want %d", len(rows), MMToDots(MinLengthMM))
	}
}

// TestRenderCable verifies the cable wrap multiplier: the auto-fitted
// length is multiplied by CableFactor (default 2.5), applied after the
// minimum clamp.
func TestRenderCable(t *testing.T) {
	j := textJob("SW-01 uplink", 0)
	j.FontSize = 20
	j.Text = []string{"SW-01", "rack-7"}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := RenderJob(j, media)
	if err != nil {
		t.Fatal(err)
	}
	jc := *j
	jc.Cable = true
	jc.CableFactor = 2
	cable, err := RenderJob(&jc, media)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) < MMToDots(MinLengthMM) {
		t.Fatalf("test content below minimum clamp: %d rows", len(plain))
	}
	if len(cable) != len(plain)*2 {
		t.Errorf("cable rows %d != 2x plain rows %d", len(cable), len(plain))
	}
	// The wrapped label must be identical to the plain label in its first
	// (content) rows, and blank afterwards.
	for i := range plain {
		if !bytes.Equal(cable[i], plain[i]) {
			t.Errorf("cable row %d differs from plain row", i)
		}
	}
	for i := len(plain); i < len(cable); i++ {
		if !allZero(cable[i]) {
			t.Errorf("cable row %d should be blank (wrap area)", i)
		}
	}

	// Tiny content: the factor applies to the minimum-clamped length.
	jt := textJob(".", 0)
	jt.Cable = true
	jt.CableFactor = 2
	jt.DefaultJobValues()
	cableTiny, err := RenderJob(jt, media)
	if err != nil {
		t.Fatal(err)
	}
	if len(cableTiny) != MMToDots(MinLengthMM)*2 {
		t.Errorf("tiny cable rows %d != 2x minimum %d", len(cableTiny), MMToDots(MinLengthMM)*2)
	}
}

// TestRenderAutoFitExceedsMax verifies that content taller than the
// maximum label length is rejected in auto mode.
func TestRenderAutoFitExceedsMax(t *testing.T) {
	// ~260 lines at 10pt exceeds 1000 mm of tape.
	lines := make([]string, 260)
	for i := range lines {
		lines[i] = "long line of text"
	}
	j := &Job{Text: lines}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderJob(j, media); err == nil {
		t.Error("expected error for content exceeding the maximum label length")
	}
}

func TestRenderTextTooWide(t *testing.T) {
	j := textJob("A VERY LONG LINE OF TEXT THAT CANNOT POSSIBLY FIT IN TWENTY NINE MILLIMETERS", 40)
	j.DefaultJobValues()
	media, _ := j.Validate()
	if _, err := RenderJob(j, media); err == nil {
		t.Error("expected error for text wider than the media")
	}
}

// TestRenderHorizontalMargins verifies that margin_left_mm shifts the ink
// right and margin_right_mm narrows the printable area.
func TestRenderHorizontalMargins(t *testing.T) {
	j := textJob("MARGIN", 40)
	j.Media = "62mm"
	j.MarginLeftMM = 10
	j.MarginRightMM = 10
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatalf("RenderJob: %v", err)
	}
	width := media.PrintableWidthDots
	minX, maxX := width, -1
	for y := range rows {
		for x := 0; x < width; x++ {
			if bitAt(rows, media, width, x, y) {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
			}
		}
	}
	if maxX < 0 {
		t.Fatal("no ink rendered")
	}
	leftDots, rightDots := MMToDots(10), MMToDots(10)
	if minX < leftDots {
		t.Errorf("ink starts at x=%d, want >= left margin %d", minX, leftDots)
	}
	if maxX >= width-rightDots {
		t.Errorf("ink reaches x=%d, want < width - right margin = %d", maxX, width-rightDots)
	}

	// The same text without margins must start further left.
	plain := textJob("MARGIN", 40)
	plain.Media = "62mm"
	plain.DefaultJobValues()
	plainRows, err := RenderJob(plain, media)
	if err != nil {
		t.Fatal(err)
	}
	plainMin := width
	for y := range plainRows {
		for x := 0; x < width; x++ {
			if bitAt(plainRows, media, width, x, y) && x < plainMin {
				plainMin = x
			}
		}
	}
	if plainMin >= minX {
		t.Errorf("left margin should shift ink right: plain min x %d, margined min x %d", plainMin, minX)
	}
}

// TestRenderHorizontalMarginTextTooWide verifies that a text line that fits
// the raw media but not the margined content area is rejected.
func TestRenderHorizontalMarginTextTooWide(t *testing.T) {
	j := textJob("MARGIN", 40)
	j.MarginLeftMM = 24 // leaves ~2 mm of content width on 29 mm media
	j.DefaultJobValues()
	media, _ := j.Validate()
	if _, err := RenderJob(j, media); err == nil {
		t.Error("expected error for text wider than the margined content area")
	}
}

func TestRenderDieCut(t *testing.T) {
	j := &Job{Media: "62x100", Text: []string{"PATCH PANEL 42"}}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := RenderJob(j, media)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1109 {
		t.Fatalf("die-cut row count %d, want 1109", len(rows))
	}
}

// writeTestPNG writes a w x h white image with a black vertical strip on
// the left edge and returns the file path.
func writeTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/10 {
				img.SetGray(x, y, color.Gray{Y: 0})
			}
		}
	}
	path := filepath.Join(t.TempDir(), "test.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRenderImageFitLabel verifies image_fit=label scales the whole image
// into the printable area of a fixed-length label, where the default
// width fit would overflow.
func TestRenderImageFitLabel(t *testing.T) {
	// 10x400 px: the width fit would be 228x9120 px, far taller than the
	// 202 px printable height of 23x23 media.
	path := writeTestPNG(t, 10, 400)
	j := &Job{Media: "23x23", Image: path}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderJob(j, media); err == nil {
		t.Fatal("expected overflow error for a tall image with the default width fit")
	}
	j.ImageFit = ImageFitLabel
	img, err := RenderJobImage(j, media)
	if err != nil {
		t.Fatalf("RenderJobImage fit=label: %v", err)
	}
	if b := img.Bounds(); b.Dx() != media.PrintableWidthDots || b.Dy() != media.PrintableHeightDots {
		t.Fatalf("label size %dx%d, want %dx%d", b.Dx(), b.Dy(), media.PrintableWidthDots, media.PrintableHeightDots)
	}
	// The fitted image is ~5 px wide (aspect preserved); the ink must be a
	// narrow strip near the left edge, not full width.
	maxInkX := -1
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.GrayAt(x, y).Y < 128 {
				if x > maxInkX {
					maxInkX = x
				}
			}
		}
	}
	if maxInkX < 0 {
		t.Fatal("no ink rendered")
	}
	if maxInkX > 20 {
		t.Errorf("fitted image should be a narrow strip, ink reaches x=%d", maxInkX)
	}
}

// TestRenderImageFitLabelContinuous verifies that image_fit=label on
// continuous media with an explicit length fits the image within both the
// width and the chosen length.
func TestRenderImageFitLabelContinuous(t *testing.T) {
	// 1000x1000 px on 29x60 mm media: width fit gives 298x298 px (fits),
	// so this exercises the label box rather than an overflow.
	path := writeTestPNG(t, 1000, 1000)
	j := &Job{Media: "29", Image: path, LengthMM: 60, ImageFit: ImageFitLabel}
	j.DefaultJobValues()
	media, err := j.Validate()
	if err != nil {
		t.Fatal(err)
	}
	img, err := RenderJobImage(j, media)
	if err != nil {
		t.Fatalf("RenderJobImage fit=label: %v", err)
	}
	if img.Bounds().Dy() != MMToDots(60) {
		t.Fatalf("label height %d, want %d", img.Bounds().Dy(), MMToDots(60))
	}
	// Find the ink bounding box; it must be fully inside the label and
	// keep the square aspect ratio (width == height within rounding).
	minX, maxX, minY, maxY := 1<<30, -1, 1<<30, -1
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.GrayAt(x, y).Y < 128 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		t.Fatal("no ink rendered")
	}
	if minY < 0 || maxY >= img.Bounds().Dy() {
		t.Errorf("ink outside the label vertically: y %d..%d", minY, maxY)
	}
	w, h := maxX-minX+1, maxY-minY+1
	if diff := w - h; diff < -2 || diff > 2 {
		t.Errorf("aspect not preserved: %dx%d", w, h)
	}
}

func TestBuildJobBytes(t *testing.T) {
	j := textJob("SW-01 uplink", 40)
	j.DefaultJobValues()
	stream, media, err := BuildJobBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	if media.ID != "29" {
		t.Errorf("media: %q", media.ID)
	}
	// 200 invalid + init + status request header.
	if len(stream) < 205 {
		t.Fatalf("stream too short: %d", len(stream))
	}
	if !bytes.Equal(stream[0:2], []byte{0x1B, 0x40}) || stream[1] != 0x40 {
		if stream[200] != 0x1B || stream[201] != 0x40 {
			t.Errorf("missing initialize command at offset 200")
		}
	}
	if stream[len(stream)-1] != 0x1A {
		t.Errorf("stream must end with 1A, got %02X", stream[len(stream)-1])
	}
}

func popcount(b byte) int {
	n := 0
	for ; b != 0; b &= b - 1 {
		n++
	}
	return n
}

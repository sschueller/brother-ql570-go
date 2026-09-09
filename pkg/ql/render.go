package ql

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"

	// Image decoders for Job.Image (PNG/JPEG/GIF). image.Decode sniffs
	// the format; without these registrations it cannot decode anything.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/skip2/go-qrcode"
	"golang.org/x/image/draw"
)

// canvas is a grayscale image with the native image.Gray convention:
// 0 = black, 255 = white.
type canvas struct {
	gray *image.Gray
	w, h int
}

func newCanvas(w, h int) *canvas {
	c := &canvas{gray: image.NewGray(image.Rect(0, 0, w, h)), w: w, h: h}
	for i := range c.gray.Pix {
		c.gray.Pix[i] = 0xFF
	}
	return c
}

// ensureH grows the canvas to at least minH rows, filling the new rows
// white (0xFF) and preserving the existing pixels.
func (c *canvas) ensureH(minH int) {
	if minH <= c.h {
		return
	}
	newH := c.h * 2
	if newH < minH {
		newH = minH
	}
	ng := image.NewGray(image.Rect(0, 0, c.w, newH))
	copy(ng.Pix, c.gray.Pix)
	for i := c.w * c.h; i < len(ng.Pix); i++ {
		ng.Pix[i] = 0xFF
	}
	c.gray = ng
	c.h = newH
}

// ensureW grows the canvas to at least minW columns, filling the new
// columns white and preserving the existing pixels. Used for rotated
// images, which can be laid out wider than the printable width. The width
// grows exactly: padding columns would become leading blank rows after a
// 270-degree rotation.
func (c *canvas) ensureW(minW int) {
	if minW <= c.w {
		return
	}
	newW := minW
	ng := image.NewGray(image.Rect(0, 0, newW, c.h))
	for y := 0; y < c.h; y++ {
		copy(ng.Pix[y*newW:y*newW+c.w], c.gray.Pix[y*c.w:y*c.w+c.w])
		for x := c.w; x < newW; x++ {
			ng.Pix[y*newW+x] = 0xFF
		}
	}
	c.gray = ng
	c.w = newW
}

func (c *canvas) at(x, y int) uint8 {
	if x < 0 || x >= c.w || y < 0 || y >= c.h {
		return 255
	}
	return c.gray.GrayAt(x, y).Y
}

func (c *canvas) set(x, y int, v uint8) {
	c.gray.SetGray(x, y, color.Gray{Y: v})
}

// sub copies the region [x0,x0+w) x [y0,y0+h) into a new canvas.
func (c *canvas) sub(x0, y0, w, h int) *canvas {
	out := newCanvas(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.set(x, y, c.at(x0+x, y0+y))
		}
	}
	return out
}

// RenderJob renders the job's content into QL-570 raster lines, including
// the right-margin placement required by the raster protocol. The job must
// already have been validated with Job.Validate.
//
// For continuous media without an explicit LengthMM, the label length
// fits the rendered content exactly (plus margins), clamped to the
// printer's 12.7..1000 mm range; with Job.Cable the auto-fitted length is
// multiplied by CableFactor (default 2.5) so the label wraps around a
// cable with overlap.
func RenderJob(job *Job, media Media) ([][]byte, error) {
	img, err := renderJobToCanvas(job, media)
	if err != nil {
		return nil, err
	}
	return rasterize(img, media, img.w, img.h, job.Threshold, job.Dither)
}

// RenderJobImage renders the job's content into a grayscale image of the
// final label, exactly as it would be printed but before thresholding.
// The image is white-background with black content: 300 dpi across the
// label width and 300 dpi (600 dpi with Job.Hires) along the length.
// It is used by the web UI preview endpoint.
func RenderJobImage(job *Job, media Media) (*image.Gray, error) {
	c, err := renderJobToCanvas(job, media)
	if err != nil {
		return nil, err
	}
	out := image.NewGray(c.gray.Bounds())
	copy(out.Pix, c.gray.Pix)
	return out, nil
}

// renderJobToCanvas composes the job's content into the final grayscale
// label canvas: layout, rotation, hires column squeeze, placement,
// mirroring and auto-fit length trimming. The canvas is 300 dpi across
// the width and 300 (or 600 with Job.Hires) dpi along the length.
func renderJobToCanvas(job *Job, media Media) (*canvas, error) {
	width := media.PrintableWidthDots
	scale := 1
	if job.Hires {
		scale = 2
	}
	auto := media.FormFactor == Endless && job.LengthMM <= 0
	height := job.LengthDots(media)
	if auto {
		// The label length is derived from the content below; use the
		// maximum as the logical limit for overflow checks only.
		height = QL570.MaxLengthDots
	}

	// cw, ch are the canvas dimensions in scale units. ch is the logical
	// label height used for overflow checks; for auto-fit labels the
	// backing canvas starts small and grows as elements are added, so a
	// short label does not allocate the full maximum length.
	cw, ch := width*scale, height*scale
	face, err := loadFace(job.Font, job.FontSize*DPI/72*float64(scale))
	if err != nil {
		return nil, err
	}

	topMargin := MMToDots(job.MarginTopMM) * scale
	bottomMargin := MMToDots(job.MarginBottomMM) * scale
	leftMargin := MMToDots(job.MarginLeftMM) * scale
	rightMargin := MMToDots(job.MarginRightMM) * scale
	sideMargin := 4 * scale
	gap := 4 * scale

	allocH := ch
	if auto {
		allocH = topMargin + 16*scale
		if allocH > ch {
			allocH = ch
		}
	}
	img := newCanvas(cw, allocH)

	// Horizontal content area: the printable width minus the left/right
	// margins. All elements are aligned within this area.
	contentW := cw - leftMargin - rightMargin
	if contentW <= 0 {
		return nil, fmt.Errorf("left + right margins leave no printable width on media %s", media.ID)
	}

	y := topMargin

	// overflowErr reports a vertical overflow. With an explicit length the
	// user can extend the label; with auto-fit the content simply exceeds
	// what a continuous label can hold.
	overflowErr := func(elem string, need int) error {
		if auto {
			return fmt.Errorf("content too tall for a continuous label: %s needs %.1f mm but the maximum label length is %.0f mm",
				elem, DotsToMM(need/scale), MaxLengthMM)
		}
		return fmt.Errorf("label too short for all elements: %s needs %d px but only %d px remain (increase --length)",
			elem, need, ch-bottomMargin-y)
	}

	metrics := face.Metrics()
	// Int26_6.Ceil/Round already return whole pixels.
	ascent := metrics.Ascent.Ceil()
	descent := metrics.Descent.Ceil()
	lineHeight := (ascent + descent) * 6 / 5
	if lineHeight <= 0 {
		lineHeight = int(job.FontSize*DPI/72*float64(scale)) * 6 / 5
	}

	for _, line := range job.Text {
		lineW := measureString(face, line)
		if lineW > float64(contentW) {
			return nil, fmt.Errorf("text line too wide for media %s: %.0f px > %d px (%.2f mm > %.2f mm); use a smaller --font-size or wider media",
				media.ID, lineW, contentW, lineW*25.4/DPI, float64(contentW)*25.4/DPI)
		}
		if y+lineHeight > ch-bottomMargin {
			return nil, overflowErr("the text", lineHeight)
		}
		img.ensureH(y + lineHeight)
		x := leftMargin + alignedX(job.Align, int(lineW), contentW)
		drawText(img, face, x, y+ascent, line)
		y += lineHeight
	}
	if y > topMargin {
		y += gap
	}

	if job.QR != "" {
		qr, err := qrcode.New(job.QR, qrcode.Medium)
		if err != nil {
			return nil, fmt.Errorf("generating QR code: %w", err)
		}
		// Target size: up to the full printable width, capped at 30 mm so
		// a standalone QR stays scannable without dominating the label.
		maxQr := MMToDots(30) * scale
		qrSize := contentW - 2*sideMargin
		if qrSize > maxQr {
			qrSize = maxQr
		}
		if qrSize < 21 {
			return nil, fmt.Errorf("media %s is too narrow (%d px) for a scannable QR code", media.ID, contentW)
		}
		if y+qrSize > ch-bottomMargin {
			return nil, overflowErr("the QR code", qrSize)
		}
		img.ensureH(y + qrSize)
		x := leftMargin + alignedX(job.Align, qrSize, contentW)
		drawQR(img, qr.Bitmap(), x, y, qrSize)
		y += qrSize + gap
	}

	if job.Barcode != "" {
		bc, err := code128.EncodeWithoutChecksum(job.Barcode)
		if err != nil {
			return nil, fmt.Errorf("encoding Code128 barcode: %w", err)
		}
		barW := contentW - 2*sideMargin
		barH := MMToDots(10) * scale
		if y+barH > ch-bottomMargin {
			return nil, overflowErr("the barcode", barH)
		}
		img.ensureH(y + barH)
		x := leftMargin + alignedX(job.Align, barW, contentW)
		scaled, err := barcode.Scale(bc, barW, barH)
		if err != nil {
			return nil, fmt.Errorf("scaling barcode: %w", err)
		}
		drawBinary(img, scaled, x, y)
		y += barH + gap
	}

	if job.Image != "" {
		src, err := decodeImageFile(job.Image)
		if err != nil {
			return nil, err
		}
		b := src.Bounds()
		if b.Dx() <= 0 || b.Dy() <= 0 {
			return nil, fmt.Errorf("image %s has invalid size %dx%d", job.Image, b.Dx(), b.Dy())
		}
		imgW := contentW - 2*sideMargin
		imgH := imgW * b.Dy() / b.Dx()
		if job.Rotate == 90 || job.Rotate == 270 {
			// Lay the image out for its rotated orientation: after the
			// rotation the image's height becomes its width, so "fill the
			// printable width" means the pre-rotation height equals the
			// printable width minus the side margins. The pre-rotation
			// width follows the aspect ratio and may exceed the printable
			// width (the canvas is widened accordingly).
			boxW := contentW - 2*sideMargin
			boxH := ch - topMargin - bottomMargin
			s := float64(boxW) / float64(b.Dy())
			if job.ImageFit == ImageFitLabel {
				// With a fixed label length also keep the rotated height
				// (the pre-rotation width) inside the label.
				s = math.Min(s, float64(boxH)/float64(b.Dx()))
			}
			imgH = int(math.Round(float64(b.Dy()) * s))
			imgW = int(math.Round(float64(b.Dx()) * s))
		} else if job.ImageFit == ImageFitLabel {
			// Fit the whole image inside the printable area (width and
			// length), preserving the aspect ratio. With an auto-fit
			// length the label grows with the content, so the width is
			// the binding constraint, as in the default width fit.
			boxW := contentW - 2*sideMargin
			boxH := ch - topMargin - bottomMargin
			s := math.Min(float64(boxW)/float64(b.Dx()), float64(boxH)/float64(b.Dy()))
			imgW = int(math.Round(float64(b.Dx()) * s))
			imgH = int(math.Round(float64(b.Dy()) * s))
		}
		if imgW < 1 {
			imgW = 1
		}
		if imgH < 1 {
			imgH = 1
		}
		if y+imgH > ch-bottomMargin {
			return nil, overflowErr("the image", imgH)
		}
		img.ensureH(y + imgH)
		x := leftMargin + alignedX(job.Align, imgW, contentW)
		if job.Rotate == 90 || job.Rotate == 270 {
			// The rotated content is centered on the final label, so the
			// pre-rotation position only determines the canvas extent.
			x = leftMargin
		}
		if x+imgW > img.w {
			img.ensureW(x + imgW)
		}
		dst := image.Rect(x, y, x+imgW, y+imgH)
		draw.BiLinear.Scale(img.gray, dst, src, b, draw.Over, nil)
		y += imgH + gap
	}

	if y <= topMargin {
		return nil, fmt.Errorf("internal error: no content rendered")
	}
	contentH := y - gap

	// Stage 1: rotate the composed content (still in scale units).
	var rot *canvas
	switch job.Rotate {
	case 0:
		rot = img.sub(0, 0, cw, contentH)
	case 180:
		rot = newCanvas(cw, contentH)
		for yy := 0; yy < contentH; yy++ {
			for xx := 0; xx < cw; xx++ {
				rot.set(xx, yy, img.at(cw-1-xx, contentH-1-yy))
			}
		}
	case 90, 270:
		if width > height {
			return nil, fmt.Errorf("cannot rotate 90/270 degrees: media %s is wider (%d px) than long (%d px)", media.ID, width, height)
		}
		if contentH > cw {
			return nil, fmt.Errorf("cannot rotate 90/270 degrees: content is %d px tall but the printable width is only %d px", contentH, cw)
		}
		// The canvas may be wider than the printable width when a rotated
		// image fills the label (its pre-rotation width becomes the label
		// length), so rotate the full canvas.
		rW, rH := contentH, img.w
		rot = newCanvas(rW, rH)
		for yy := 0; yy < rH; yy++ {
			for xx := 0; xx < rW; xx++ {
				var sx, sy int
				if job.Rotate == 90 {
					sx, sy = yy, rW-1-xx
				} else {
					sx, sy = rH-1-yy, xx
				}
				rot.set(xx, yy, img.at(sx, sy))
			}
		}
	}

	// Stage 2: squeeze horizontally in hires mode. The canvas was
	// composed at 600x600 dpi; the printer only has 300 dpi across the
	// width, so pairs of adjacent columns are combined (black wins).
	if scale == 2 {
		sq := newCanvas(rot.w/2, rot.h)
		for yy := 0; yy < rot.h; yy++ {
			for xx := 0; xx < sq.w; xx++ {
				a := rot.at(2*xx, yy)
				b := rot.at(2*xx+1, yy)
				if a < b {
					sq.set(xx, yy, a)
				} else {
					sq.set(xx, yy, b)
				}
			}
		}
		rot = sq
	}

	// Stage 3: paste into the final canvas, horizontally centered. For
	// auto-fit labels the canvas is only as tall as the rotated content;
	// the rows below it are all white anyway.
	finalW, finalH := width, height*scale
	if auto {
		finalH = rot.h
	}
	out := newCanvas(finalW, finalH)
	if rot.w > finalW || rot.h > finalH {
		if job.Rotate == 90 || job.Rotate == 270 {
			return nil, fmt.Errorf("rotated content needs %.1f mm but the label is only %.1f mm long (increase --length)",
				DotsToMM(rot.h/scale), DotsToMM(finalH/scale))
		}
		return nil, fmt.Errorf("internal error: rotated content %dx%d does not fit %dx%d", rot.w, rot.h, finalW, finalH)
	}
	x0 := (finalW - rot.w) / 2
	for yy := 0; yy < rot.h; yy++ {
		for xx := 0; xx < rot.w; xx++ {
			out.set(x0+xx, yy, rot.at(xx, yy))
		}
	}

	// Stage 4: mirror the whole label horizontally.
	if job.Mirror {
		for yy := 0; yy < finalH; yy++ {
			for xx := 0; xx < finalW/2; xx++ {
				l := out.at(xx, yy)
				r := out.at(finalW-1-xx, yy)
				out.set(xx, yy, r)
				out.set(finalW-1-xx, yy, l)
			}
		}
	}

	// Stage 5: auto-fit. With no explicit length, the label is trimmed to
	// the last row containing ink (plus the bottom margin), clamped to the
	// printer's 12.7..1000 mm range; cable wrap labels then multiply the
	// fitted length by CableFactor so the label wraps the cable with
	// overlap. Any pixel that is not pure white counts as ink, which is
	// independent of the threshold/dither settings used for printing.
	if auto {
		last := -1
		for y := 0; y < finalH; y++ {
			for x := 0; x < finalW; x++ {
				if out.at(x, y) < 255 {
					last = y
					break
				}
			}
		}
		fitted := last + 1 + bottomMargin
		if min := MMToDots(MinLengthMM) * scale; fitted < min {
			fitted = min
		}
		if job.Cable {
			fitted = int(math.Round(float64(fitted) * job.CableFactor))
		}
		if fitted > QL570.MaxLengthDots*scale {
			return nil, fmt.Errorf("auto-fitted label length %.1f mm exceeds the maximum of %.0f mm; reduce the content or set --length explicitly",
				DotsToMM(fitted/scale), MaxLengthMM)
		}
		trimmed := newCanvas(finalW, fitted)
		for y := 0; y < fitted && y < finalH; y++ {
			for x := 0; x < finalW; x++ {
				trimmed.set(x, y, out.at(x, y))
			}
		}
		out = trimmed
	}
	return out, nil
}

// rasterize converts the final grayscale canvas to 1-bit raster lines and
// places them inside the 720-pin raster row according to the official
// raster line arrangement (section 3.2.5): the first transmitted pins are
// the right margin, so image pixel x maps to transmitted bit
// rightMargin + width - 1 - x, with the MSB of each byte first.
func rasterize(img *canvas, media Media, width, height, thresholdPct int, dither bool) ([][]byte, error) {
	if img.w != width || img.h != height {
		return nil, fmt.Errorf("internal error: canvas %dx%d does not match %dx%d", img.w, img.h, width, height)
	}
	t := thresholdPct
	if t <= 0 {
		t = 50
	}
	threshold := 255 * t / 100

	bits := make([]bool, width*height)
	if dither {
		buf := make([]int, width*height)
		for i, y := 0, 0; y < height; y++ {
			for x := 0; x < width; x, i = x+1, i+1 {
				buf[i] = int(img.at(x, y))
			}
		}
		for i, y := 0, 0; y < height; y++ {
			for x := 0; x < width; x, i = x+1, i+1 {
				v := buf[i]
				nv := 0
				if v >= 128 {
					nv = 255
				}
				bits[i] = nv == 0
				err := v - nv
				if x+1 < width {
					buf[i+1] += err * 7 / 16
				}
				if y+1 < height {
					if x > 0 {
						buf[i+width-1] += err * 3 / 16
					}
					buf[i+width] += err * 5 / 16
					if x+1 < width {
						buf[i+width+1] += err * 1 / 16
					}
				}
			}
		}
	} else {
		for i, y := 0, 0; y < height; y++ {
			for x := 0; x < width; x, i = x+1, i+1 {
				bits[i] = int(img.at(x, y)) < threshold
			}
		}
	}

	rows := make([][]byte, height)
	for y := 0; y < height; y++ {
		row := make([]byte, QL570.RowBytes)
		for x := 0; x < width; x++ {
			if bits[y*width+x] {
				p := media.RightMarginDots + width - 1 - x
				row[p>>3] |= 1 << (7 - p&7)
			}
		}
		rows[y] = row
	}
	return rows, nil
}

// alignedX computes the left x coordinate of an element of width w inside
// a canvas of width cw for the given alignment.
func alignedX(align string, w, cw int) int {
	switch align {
	case AlignCenter:
		return (cw - w) / 2
	case AlignRight:
		return cw - w
	default:
		return 0
	}
}

// drawQR renders a QR bitmap (without quiet zone) at (x, y) with the given
// total size including a 4-module quiet zone on each side.
func drawQR(img *canvas, bitmap [][]bool, x, y, size int) {
	n := len(bitmap)
	total := n + 8
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if !bitmap[r][c] {
				continue
			}
			x0 := x + size*(c+4)/total
			x1 := x + size*(c+5)/total
			y0 := y + size*(r+4)/total
			y1 := y + size*(r+5)/total
			if x1 > x0 {
				x1--
			}
			if y1 > y0 {
				y1--
			}
			for yy := y0; yy <= y1; yy++ {
				for xx := x0; xx <= x1; xx++ {
					img.set(xx, yy, 0)
				}
			}
		}
	}
}

// drawBinary copies a binary (black on white/transparent) image onto the
// canvas at (x, y). Pixels that are transparent or light (luminance >= 50%)
// are treated as white. Note: boombuler's barcode.Scale fills the
// background with opaque white, so checking alpha alone would paint the
// whole area black.
func drawBinary(img *canvas, src image.Image, x, y int) {
	b := src.Bounds()
	for yy := 0; yy < b.Dy(); yy++ {
		for xx := 0; xx < b.Dx(); xx++ {
			r, g, bl, a := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
			if a < 0x8000 {
				continue
			}
			if (r+g+bl)/3 >= 0x8000 {
				continue
			}
			img.set(x+xx, y+yy, 0)
		}
	}
}

// decodeImageFile reads and decodes a PNG/JPEG/GIF image file.
func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening image %s: %w", path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding image %s (PNG/JPEG/GIF supported): %w", path, err)
	}
	return img, nil
}

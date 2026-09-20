// Package ipp implements an IPP (Internet Printing Protocol, RFC 8010/8011)
// print server for the Brother QL-570 so driverless clients such as Android's
// Default Print Service can print labels over the network. It contains the
// IPP message handling, a PWG Raster decoder, an IPP media catalog mapping to
// ql.Media and a job store. The HTTP transport lives in Server.
package ipp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
)

// PWG Raster (PWG 5102.4-2012 "PWG Raster Format", the format behind the
// IPP MIME media type "image/pwg-raster") constants.
//
// A PWG Raster stream starts with the 4-byte sync word "RaS2", followed by
// one or more pages. Each page is a fixed 1796-byte header (all fields
// big-endian uint32 unless noted) followed by the packed bitmap data. The
// header field offsets below are taken from Table 1 of PWG 5102.4-2012.
const (
	pwgSyncWord   = 0x52615332 // "RaS2"
	pwgHeaderSize = 1796

	pwgOffCutMedia       = 268
	pwgOffDuplex         = 272
	pwgOffHWResolution   = 276 // two uint32s: feed, then cross-feed
	pwgOffMediaPosition  = 324
	pwgOffNumCopies      = 340
	pwgOffOrientation    = 344
	pwgOffWidth          = 372
	pwgOffHeight         = 376
	pwgOffBitsPerColor   = 384
	pwgOffBitsPerPixel   = 388
	pwgOffBytesPerLine   = 392
	pwgOffColorOrder     = 396
	pwgOffColorSpace     = 400
	pwgOffNumColors      = 420
	pwgOffTotalPageCount = 452
	pwgOffPageSizeName   = 1732 // CString, 64 bytes
)

// Orientation values (PWG 5102.4 Table 6). The bitmap data is always
// transmitted in print orientation: a landscape page arrives already
// rotated (Width > Height) and is printed as-is.
const (
	OrientationPortrait         = 0
	OrientationLandscape        = 1
	OrientationReversePortrait  = 2
	OrientationReverseLandscape = 3
)

// Color space values (PWG 5102.4 Table 3) supported by this decoder.
const (
	ColorSpaceDeviceRGB = 1
	ColorSpaceBlack     = 3
	ColorSpaceSGray     = 18
	ColorSpaceSRGB      = 19
)

// RequiredDPI is the resolution the QL-570 prints at. PWG Raster pages must
// use 300x300 dpi so that one pixel maps to one printer dot.
const RequiredDPI = 300

// PWGPage is one decoded PWG Raster page.
type PWGPage struct {
	// Width and Height are the bitmap dimensions in pixels.
	Width, Height int
	// BitsPerPixel is 8 (grayscale) or 24 (sRGB).
	BitsPerPixel int
	// ColorSpace is the raw PWG color space value (ColorSpaceBlack,
	// ColorSpaceSGray, ColorSpaceSRGB or ColorSpaceDeviceRGB).
	ColorSpace uint32
	// NumCopies, Orientation, MediaPosition and TotalPageCount are the
	// corresponding page header fields.
	NumCopies      uint32
	Orientation    uint32
	MediaPosition  uint32
	TotalPageCount uint32
	// PageSizeName is the media self-describing name from the header
	// (e.g. "iso_a4_210x297mm"), empty when not provided.
	PageSizeName string
	// Image is the decoded grayscale bitmap.
	Image *image.Gray
}

// DecodePWGRaster decodes all pages of a PWG Raster stream into grayscale
// images. Only 8-bit grayscale (1 channel) and 24-bit sRGB (3 channels)
// pages at 300x300 dpi are accepted; anything else is rejected with an
// error describing the unsupported value.
func DecodePWGRaster(data []byte) ([]*PWGPage, error) {
	if len(data) < 4 {
		return nil, errors.New("PWG Raster: stream too short")
	}
	if binary.BigEndian.Uint32(data[:4]) != pwgSyncWord {
		return nil, fmt.Errorf("PWG Raster: bad sync word 0x%08X (want 0x%08X)", binary.BigEndian.Uint32(data[:4]), pwgSyncWord)
	}
	off := 4
	var pages []*PWGPage
	for {
		remaining := data[off:]
		if len(remaining) == 0 {
			break
		}
		if len(remaining) < pwgHeaderSize {
			return nil, fmt.Errorf("PWG Raster: truncated page header (%d bytes left)", len(remaining))
		}
		page, next, err := decodePWGRasterPage(remaining)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
		off += next
	}
	if len(pages) == 0 {
		return nil, errors.New("PWG Raster: no pages")
	}
	return pages, nil
}

// decodePWGRasterPage decodes one page starting at data[0]. It returns the
// page and the number of bytes consumed (header + bitmap).
func decodePWGRasterPage(data []byte) (*PWGPage, int, error) {
	h := data[:pwgHeaderSize]
	u := func(off int) uint32 { return binary.BigEndian.Uint32(h[off:]) }

	feedDPI := u(pwgOffHWResolution)
	crossDPI := u(pwgOffHWResolution + 4)
	if feedDPI != RequiredDPI || crossDPI != RequiredDPI {
		return nil, 0, fmt.Errorf("PWG Raster: resolution %dx%d dpi not supported (want %dx%d)",
			feedDPI, crossDPI, RequiredDPI, RequiredDPI)
	}

	width := int(u(pwgOffWidth))
	height := int(u(pwgOffHeight))
	bitsPerPixel := int(u(pwgOffBitsPerPixel))
	bytesPerLine := int(u(pwgOffBytesPerLine))
	colorSpace := u(pwgOffColorSpace)
	numColors := u(pwgOffNumColors)

	channels := 1
	switch colorSpace {
	case ColorSpaceBlack, ColorSpaceSGray:
		if bitsPerPixel != 8 || numColors != 1 {
			return nil, 0, fmt.Errorf("PWG Raster: grayscale page must be 8 bpp with 1 color, got %d bpp / %d colors", bitsPerPixel, numColors)
		}
	case ColorSpaceSRGB, ColorSpaceDeviceRGB:
		if bitsPerPixel != 24 || numColors != 3 {
			return nil, 0, fmt.Errorf("PWG Raster: sRGB page must be 24 bpp with 3 colors, got %d bpp / %d colors", bitsPerPixel, numColors)
		}
		channels = 3
	default:
		return nil, 0, fmt.Errorf("PWG Raster: color space %d not supported (only DeviceBlack, sGray, sRGB and DeviceRGB)", colorSpace)
	}

	if width <= 0 || height <= 0 {
		return nil, 0, fmt.Errorf("PWG Raster: invalid bitmap dimensions %dx%d", width, height)
	}
	wantBPL := (width*bitsPerPixel + 7) / 8
	if bytesPerLine != wantBPL {
		return nil, 0, fmt.Errorf("PWG Raster: BytesPerLine %d does not match %d for %d pixels at %d bpp", bytesPerLine, wantBPL, width, bitsPerPixel)
	}

	img, n, err := decodePWGImage(data[pwgHeaderSize:], width, height, channels)
	if err != nil {
		return nil, 0, err
	}

	page := &PWGPage{
		Width:          width,
		Height:         height,
		BitsPerPixel:   bitsPerPixel,
		ColorSpace:     colorSpace,
		NumCopies:      u(pwgOffNumCopies),
		Orientation:    u(pwgOffOrientation),
		MediaPosition:  u(pwgOffMediaPosition),
		TotalPageCount: u(pwgOffTotalPageCount),
		PageSizeName:   cstring(h[pwgOffPageSizeName : pwgOffPageSizeName+64]),
		Image:          img,
	}
	return page, pwgHeaderSize + n, nil
}

// decodePWGImage decodes the packed bitmap into a grayscale image. The
// format is a modified PackBits encoding applied per scan line: every line
// starts with a repeat count (n+1 identical lines), then the line content
// is a sequence of chunks: a control byte < 128 repeats the next chunk
// (count+1) times, a control byte >= 128 is followed by (257-count)
// literal chunks. A chunk is one pixel for 8 bpp, one RGB triple for
// 24 bpp.
func decodePWGImage(data []byte, width, height, channels int) (*image.Gray, int, error) {
	img := image.NewGray(image.Rect(0, 0, width, height))
	row := make([]byte, width*channels)
	off := 0
	rowIdx := 0
	for rowIdx < height {
		if off >= len(data) {
			return nil, 0, fmt.Errorf("PWG Raster: bitmap truncated at row %d/%d", rowIdx, height)
		}
		repeat := int(data[off]) + 1
		off++
		if rowIdx+repeat > height {
			return nil, 0, fmt.Errorf("PWG Raster: line repeat %d overflows page height at row %d/%d", repeat, rowIdx, height)
		}
		rowPos := 0
		for rowPos < len(row) {
			if off >= len(data) {
				return nil, 0, fmt.Errorf("PWG Raster: bitmap truncated mid-row %d", rowIdx)
			}
			ctrl := data[off]
			off++
			if ctrl < 128 {
				n := int(ctrl) + 1
				if off+channels > len(data) {
					return nil, 0, fmt.Errorf("PWG Raster: bitmap truncated mid-row %d", rowIdx)
				}
				chunk := data[off : off+channels]
				off += channels
				for i := 0; i < n && rowPos+channels <= len(row); i++ {
					copy(row[rowPos:], chunk)
					rowPos += channels
				}
			} else {
				n := (257 - int(ctrl)) * channels
				if n <= 0 || rowPos+n > len(row) || off+n > len(data) {
					return nil, 0, fmt.Errorf("PWG Raster: bad literal run at row %d (pos %d, run %d)", rowIdx, rowPos, n)
				}
				copy(row[rowPos:], data[off:off+n])
				off += n
				rowPos += n
			}
		}
		// The same scan line repeats 'repeat' times.
		dst := img.Pix[img.Stride*rowIdx:]
		for r := 0; r < repeat; r++ {
			if channels == 1 {
				copy(dst[:width], row)
			} else {
				for x := 0; x < width; x++ {
					r, g, b := row[x*3], row[x*3+1], row[x*3+2]
					dst[x] = uint8((77*uint16(r) + 150*uint16(g) + 29*uint16(b) + 128) >> 8)
				}
			}
			dst = dst[img.Stride:]
		}
		rowIdx += repeat
	}
	return img, off, nil
}

// cstring converts a NUL-terminated byte field into a string.
func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

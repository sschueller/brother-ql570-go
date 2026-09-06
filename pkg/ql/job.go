package ql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Alignment of label elements.
const (
	AlignLeft   = "left"
	AlignCenter = "center"
	AlignRight  = "right"
)

// Compression modes. The QL-570 only supports "none"; "tiff" is rejected
// during validation.
const (
	CompressNone = "none"
	CompressTIFF = "tiff"
)

// Job is a fully JSON-serializable print job definition. It is the single
// schema shared by the Go library, the CLI (--job file.json) and the HTTP
// daemon (POST /v1/print).
type Job struct {
	// Media selects the label size, e.g. "29" (default), "29mm", "62x100".
	Media string `json:"media,omitempty"`

	// Text lines to print, from top to bottom.
	Text []string `json:"text,omitempty"`

	// QR renders a QR code containing this string.
	QR string `json:"qr,omitempty"`

	// Barcode renders a Code128 barcode containing this string.
	Barcode string `json:"barcode,omitempty"`

	// Image is the path to a PNG/JPEG image file to print.
	Image string `json:"image,omitempty"`

	// Font is the path to a TTF file. Empty uses the embedded Go Regular
	// font.
	Font string `json:"font,omitempty"`

	// FontSize is the font size in points (default 10).
	FontSize float64 `json:"font_size,omitempty"`

	// LengthMM is the label length in millimeters. For continuous media
	// it may be omitted: the length then fits the content exactly (plus
	// margins), clamped to the printer's 12.7..1000 mm range. For die-cut
	// labels it must be omitted (the length is fixed by the media).
	LengthMM float64 `json:"length_mm,omitempty"`

	// Cable makes the label a cable wrap label when no explicit length is
	// given: the auto-fitted length is multiplied by CableFactor so the
	// label wraps around the cable with overlap (Brother recommends wrap +
	// overlap for cable flag labels; 2.5x is a good default).
	Cable bool `json:"cable,omitempty"`

	// CableFactor is the cable wrap multiplier (default 2.5).
	CableFactor float64 `json:"cable_factor,omitempty"`

	// Copies is the number of pages to print (1..255, default 1).
	Copies int `json:"copies,omitempty"`

	// Cut enables the automatic cutter (default true).
	Cut *bool `json:"cut,omitempty"`

	// CutEvery cuts after every n-th label when auto cut is enabled
	// (1..255, default 1).
	CutEvery int `json:"cut_every,omitempty"`

	// Mirror mirrors the whole label horizontally.
	Mirror bool `json:"mirror,omitempty"`

	// Rotate rotates the composed content by 0, 90, 180 or 270 degrees.
	Rotate int `json:"rotate,omitempty"`

	// MarginTopMM and MarginBottomMM add vertical whitespace around the
	// content.
	MarginTopMM    float64 `json:"margin_top_mm,omitempty"`
	MarginBottomMM float64 `json:"margin_bottom_mm,omitempty"`

	// MarginLeftMM and MarginRightMM add horizontal whitespace inside the
	// printable width. Content is laid out within the remaining area
	// (Aligned within it by Align).
	MarginLeftMM  float64 `json:"margin_left_mm,omitempty"`
	MarginRightMM float64 `json:"margin_right_mm,omitempty"`

	// Align is the horizontal alignment of text/barcode/QR elements:
	// left (default), center or right.
	Align string `json:"align,omitempty"`

	// Compress selects the raster compression mode. Only "none" is
	// supported by the QL-570.
	Compress string `json:"compress,omitempty"`

	// Dither applies Floyd-Steinberg dithering instead of a fixed
	// threshold when converting grayscale content to 1 bit.
	Dither bool `json:"dither,omitempty"`

	// Threshold is the grayscale threshold in percent (0..100, default 50)
	// used without dithering. Pixels darker than the threshold print
	// black.
	Threshold int `json:"threshold,omitempty"`

	// Hires prints at 600 dpi in the length direction (double vertical
	// resolution, ESC i K bit 6).
	Hires bool `json:"hires,omitempty"`

	// FeedDots overrides the margin/feed amount in dots (ESC i d).
	// Defaults: 35 for continuous tape, 0 for die-cut labels.
	FeedDots int `json:"feed_dots,omitempty"`

	// Quality gives priority to print quality over speed in the print
	// information command (PI_QUALITY flag, default true).
	Quality *bool `json:"quality,omitempty"`

	// Device is the printer device path, e.g. /dev/usb/lp0. Empty selects
	// the auto-discovered QL-570.
	Device string `json:"device,omitempty"`
}

// DefaultJobValues fills the zero values of j with defaults.
func (j *Job) DefaultJobValues() {
	if j.Media == "" {
		j.Media = DefaultMediaID
	}
	if j.FontSize == 0 {
		j.FontSize = 10
	}
	if j.Copies == 0 {
		j.Copies = 1
	}
	if j.CutEvery == 0 {
		j.CutEvery = 1
	}
	if j.CableFactor == 0 {
		j.CableFactor = DefaultCableFactor
	}
	if j.Cut == nil {
		t := true
		j.Cut = &t
	}
	if j.Quality == nil {
		t := true
		j.Quality = &t
	}
	if j.Align == "" {
		j.Align = AlignLeft
	}
	if j.Compress == "" {
		j.Compress = CompressNone
	}
}

// AutoCut reports whether the automatic cutter is enabled.
func (j *Job) AutoCut() bool { return j.Cut == nil || *j.Cut }

// Quality reports whether print quality has priority over speed.
func (j *Job) QualityPriority() bool { return j.Quality == nil || *j.Quality }

// DefaultCableFactor is the default multiplier for cable wrap labels: the
// auto-fitted length is multiplied by 2.5 so the label wraps around the
// cable with overlap.
const DefaultCableFactor = 2.5

// Validate checks the job for internal consistency and reports the media
// resolved from j.Media.
func (j *Job) Validate() (Media, error) {
	media, err := LookupMedia(j.Media)
	if err != nil {
		return Media{}, err
	}
	if j.Copies < 1 || j.Copies > 255 {
		return Media{}, fmt.Errorf("copies must be 1..255, got %d", j.Copies)
	}
	if j.CutEvery < 1 || j.CutEvery > 255 {
		return Media{}, fmt.Errorf("cut_every must be 1..255, got %d", j.CutEvery)
	}
	if j.FontSize <= 0 {
		return Media{}, fmt.Errorf("font_size must be > 0, got %g", j.FontSize)
	}
	if j.Threshold < 0 || j.Threshold > 100 {
		return Media{}, fmt.Errorf("threshold must be 0..100, got %d", j.Threshold)
	}
	if j.MarginLeftMM < 0 {
		return Media{}, fmt.Errorf("margin_left_mm must be >= 0, got %g", j.MarginLeftMM)
	}
	if j.MarginRightMM < 0 {
		return Media{}, fmt.Errorf("margin_right_mm must be >= 0, got %g", j.MarginRightMM)
	}
	if MMToDots(j.MarginLeftMM)+MMToDots(j.MarginRightMM) >= media.PrintableWidthDots {
		return Media{}, fmt.Errorf("left + right margins (%.1f + %.1f mm) leave no printable width on media %q", j.MarginLeftMM, j.MarginRightMM, media.ID)
	}
	switch j.Rotate {
	case 0, 90, 180, 270:
	default:
		return Media{}, fmt.Errorf("rotate must be 0, 90, 180 or 270, got %d", j.Rotate)
	}
	switch j.Align {
	case AlignLeft, AlignCenter, AlignRight:
	default:
		return Media{}, fmt.Errorf("align must be left, center or right, got %q", j.Align)
	}
	switch j.Compress {
	case CompressNone:
	case CompressTIFF:
		return Media{}, fmt.Errorf("tiff compression is not supported by the QL-570 (only uncompressed raster data)")
	default:
		return Media{}, fmt.Errorf("compress must be none or tiff, got %q", j.Compress)
	}
	if media.FormFactor == Endless {
		if j.LengthMM > 0 && (j.LengthMM < MinLengthMM || j.LengthMM > MaxLengthMM) {
			return Media{}, fmt.Errorf("length %.1f mm out of range (%.1f..%.0f mm)", j.LengthMM, MinLengthMM, MaxLengthMM)
		}
	} else {
		if j.LengthMM > 0 {
			return Media{}, fmt.Errorf("length is fixed for die-cut media %q; remove --length", media.ID)
		}
		if j.Cable {
			return Media{}, fmt.Errorf("cable wrap labels need continuous media; remove --cable or use a continuous --media")
		}
	}
	if j.CableFactor < 1 || j.CableFactor > 10 {
		return Media{}, fmt.Errorf("cable_factor must be 1..10, got %g", j.CableFactor)
	}
	if !j.HasText() && j.QR == "" && j.Barcode == "" && j.Image == "" {
		return Media{}, fmt.Errorf("job has no printable content (use --text, --qr, --barcode, --image or a job file)")
	}
	return media, nil
}

// LengthDots returns the label length in dots at 300 dpi for this job.
// For continuous media it converts LengthMM; for die-cut media it returns
// the fixed printable height.
func (j *Job) LengthDots(media Media) int {
	if media.FormFactor == Endless {
		return MMToDots(j.LengthMM)
	}
	return media.PrintableHeightDots
}

// HasText reports whether any text lines are defined.
func (j *Job) HasText() bool {
	for _, t := range j.Text {
		if strings.TrimSpace(t) != "" {
			return true
		}
	}
	return false
}

// ParseJobs parses a job definition from JSON. Two forms are accepted:
//
//	a single job object:  {"text": ["hello"], "length_mm": 40}
//	an array of jobs:     [{"text": ["a"], "length_mm": 30}, {...}]
//
// The array form prints one label per entry, as pages of a single print
// job (intermediate pages use the 0C print command, the last page 1A with
// feeding). All entries must use the same media.
func ParseJobs(data []byte) ([]Job, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty job definition")
	}
	switch trimmed[0] {
	case '[':
		var jobs []Job
		if err := json.Unmarshal(trimmed, &jobs); err != nil {
			return nil, fmt.Errorf("parsing job array: %w", err)
		}
		if len(jobs) == 0 {
			return nil, fmt.Errorf("job array is empty")
		}
		return jobs, nil
	case '{':
		var j Job
		if err := json.Unmarshal(trimmed, &j); err != nil {
			return nil, fmt.Errorf("parsing job: %w", err)
		}
		return []Job{j}, nil
	default:
		return nil, fmt.Errorf("job definition must be a JSON object or array of objects")
	}
}

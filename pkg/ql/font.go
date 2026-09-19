package ql

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// fontStyle describes the bold/italic style of a text line.
type fontStyle struct {
	bold   bool
	italic bool
}

// isPlain reports whether the style is normal (no bold, no italic).
func (s fontStyle) isPlain() bool { return !s.bold && !s.italic }

// String returns the canonical style name.
func (s fontStyle) String() string {
	switch {
	case s.bold && s.italic:
		return TextStyleBoldItalic
	case s.bold:
		return TextStyleBold
	case s.italic:
		return TextStyleItalic
	default:
		return TextStyleNormal
	}
}

// parseFontStyle parses a per-line style string: "normal" (the default),
// "bold", "italic" or "bold-italic". The tokens may be separated by "-",
// ",", "+" or whitespace, in any order and any case (e.g. "italic,bold").
func parseFontStyle(s string) (fontStyle, error) {
	var st fontStyle
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		switch r {
		case '-', ',', '+', ' ', '\t':
			return true
		}
		return false
	}) {
		switch tok {
		case "", "normal":
		case "bold":
			st.bold = true
		case "italic", "italics":
			st.italic = true
		default:
			return fontStyle{}, fmt.Errorf("unknown text style %q (use normal, bold, italic or bold-italic)", tok)
		}
	}
	return st, nil
}

// embeddedFamily bundles the four style variants of one embedded font
// family (BSD-licensed Go fonts, latin coverage). The styles array is
// indexed by bold (bit 0) | italic (bit 1).
type embeddedFamily struct {
	name   string
	styles [4][]byte
}

// ttf returns the TTF bytes of the variant matching the style.
func (f *embeddedFamily) ttf(st fontStyle) []byte {
	idx := 0
	if st.bold {
		idx |= 1
	}
	if st.italic {
		idx |= 2
	}
	return f.styles[idx]
}

// embeddedFamilies maps the --font names to their variants. "" selects
// the default family (go).
var embeddedFamilies = map[string]*embeddedFamily{
	"go": {
		name:   "go",
		styles: [4][]byte{goregular.TTF, gobold.TTF, goitalic.TTF, gobolditalic.TTF},
	},
	"go-mono": {
		name:   "go-mono",
		styles: [4][]byte{gomono.TTF, gomonobold.TTF, gomonoitalic.TTF, gomonobolditalic.TTF},
	},
}

// DefaultFontName is the embedded font family used when Font is empty.
const DefaultFontName = "go"

// EmbeddedFonts returns the names of the embedded font families, sorted.
func EmbeddedFonts() []string {
	names := make([]string, 0, len(embeddedFamilies))
	for n := range embeddedFamilies {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// fontCacheMu guards the parsed-font and face caches. Parsing a TTF on
// every request dominates preview latency on slow hosts (e.g. a
// Raspberry Pi), so both the parsed font and the size-specific faces are
// cached for the process lifetime. Faces are read-only once created, so
// they may be shared across concurrent requests.
var (
	fontCacheMu sync.Mutex
	fontCache   = map[string]*opentype.Font{}
	faceCache   = map[string]font.Face{}
)

// loadStyledFace returns a font.Face of the given size for the font name
// and style. sizePx is the em size in pixels (at 300 dpi: sizePx =
// points * 300 / 72). fontName is "" or an embedded family name ("go",
// "go-mono") or the path to a TTF file.
//
// The embedded families have real bold/italic variants; for a custom TTF
// file only the base face exists, so the requested style is reported in
// synth for synthetic application at draw time.
func loadStyledFace(fontName string, st fontStyle, sizePx float64) (font.Face, fontStyle, error) {
	family, embedded := embeddedFamilies[fontName]
	if fontName == "" {
		family, embedded = embeddedFamilies[DefaultFontName], true
	}
	// parseKey identifies the underlying TTF: "family|style" for embedded
	// variants, the file path for custom fonts (whose style is synthesized
	// from the same base face).
	parseKey := fontName
	synth := st
	if embedded {
		parseKey = family.name + "|" + st.String()
		synth = fontStyle{}
	}
	faceKey := fmt.Sprintf("%s|%.3f", parseKey, sizePx)

	fontCacheMu.Lock()
	if f, ok := faceCache[faceKey]; ok {
		fontCacheMu.Unlock()
		return f, synth, nil
	}
	parsed := fontCache[parseKey]
	fontCacheMu.Unlock()

	if parsed == nil {
		var data []byte
		if embedded {
			data = family.ttf(st)
		} else {
			var err error
			data, err = os.ReadFile(fontName)
			if err != nil {
				return nil, fontStyle{}, fmt.Errorf("reading font %s: %w (use a TTF path or an embedded font: %s)",
					fontName, err, strings.Join(EmbeddedFonts(), ", "))
			}
		}
		var err error
		parsed, err = opentype.Parse(data)
		if err != nil {
			return nil, fontStyle{}, fmt.Errorf("parsing font %s: %w", parseKey, err)
		}
		fontCacheMu.Lock()
		fontCache[parseKey] = parsed
		fontCacheMu.Unlock()
	}

	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		// NewFace scales the em size by Size * DPI / 72. With DPI 72,
		// Size directly equals the em size in pixels.
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fontStyle{}, fmt.Errorf("creating face: %w", err)
	}

	fontCacheMu.Lock()
	faceCache[faceKey] = face
	fontCacheMu.Unlock()
	return face, synth, nil
}

// italicSlant is the horizontal shear applied for synthetic italics
// (about 14 degrees, matching the slant of the Go italic variants).
const italicSlant = 0.25

// syntheticBoldOffset returns the double-strike offset in pixels for a
// synthetic bold face, about 1/24 of the recommended line height (at
// least 1 px).
func syntheticBoldOffset(m font.Metrics) int {
	off := m.Height.Ceil() / 24
	if off < 1 {
		off = 1
	}
	return off
}

// syntheticItalicShift returns the top-row shear shift in pixels for a
// synthetic italic face. The shear pivots on the baseline, so the top
// row (ascent above the baseline) shifts right by this amount.
func syntheticItalicShift(m font.Metrics) int {
	asc := m.Ascent.Ceil()
	if asc < 0 {
		asc = 0
	}
	return int(math.Round(float64(asc) * italicSlant))
}

// blackImage is an opaque black source for the font drawer.
var blackImage = image.NewUniform(color.Black)

// measureString returns the advance width of s for the given face in
// pixels.
func measureString(face font.Face, s string) float64 {
	d := &font.Drawer{Face: face}
	return float64(d.MeasureString(s)) / 64
}

// measureStyledString returns the width of the rendered ink of s for the
// face with the given synthetic style, in pixels. For plain text this is
// the font advance; synthetic bold adds the double-strike offset and
// synthetic italic adds the top-row shear shift.
func measureStyledString(face font.Face, s string, synth fontStyle) float64 {
	w := measureString(face, s)
	if synth.isPlain() {
		return w
	}
	m := face.Metrics()
	if synth.bold {
		w += float64(syntheticBoldOffset(m))
	}
	if synth.italic {
		w += float64(syntheticItalicShift(m))
	}
	return w
}

// drawText renders s onto dst (a grayscale canvas where 0 = white,
// 255 = black) with the face, baseline at (x, y), returning the advance
// width in pixels.
func drawText(dst *canvas, face font.Face, x, y int, s string) float64 {
	d := &font.Drawer{
		Dst:  dst.gray,
		Src:  blackImage,
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
	return measureString(face, s)
}

// drawStyledText renders s onto dst with the face, baseline at (x, y),
// applying the synthetic bold/italic effects (custom TTF fonts have no
// real variants). Synthetic bold double-strikes the glyphs with a small
// offset; synthetic italic shears the rendered glyphs about the baseline.
// The rendered ink matches measureStyledString's width.
func drawStyledText(dst *canvas, face font.Face, x, y int, s string, synth fontStyle) {
	if synth.isPlain() {
		drawText(dst, face, x, y, s)
		return
	}
	m := face.Metrics()
	asc, desc := m.Ascent.Ceil(), m.Descent.Ceil()
	if asc < 0 {
		asc = 0
	}
	if desc < 0 {
		desc = 0
	}
	th := asc + desc
	if th < 1 {
		th = 1
	}
	w := int(math.Ceil(measureString(face, s)))
	boldOff := 0
	if synth.bold {
		boldOff = syntheticBoldOffset(m)
	}
	shiftTop := 0
	if synth.italic {
		shiftTop = syntheticItalicShift(m)
	}

	// Render into a small temp canvas at the exact glyph bounds, then
	// double-strike and shear, then blit. Blitting black on black is
	// idempotent, so the double strike merges cleanly.
	temp := newCanvas(w+boldOff+shiftTop, th)
	drawText(temp, face, 0, asc, s)
	if synth.bold {
		drawText(temp, face, boldOff, asc, s)
	}
	var out *canvas = temp
	if synth.italic {
		out = newCanvas(w+boldOff+shiftTop, th)
		for yy := 0; yy < th; yy++ {
			// Pivot on the baseline: rows above it shift right, the
			// baseline and descenders stay put.
			shift := int(math.Round(float64(asc-yy) * italicSlant))
			if shift < 0 {
				shift = 0
			}
			for xx := 0; xx < w+boldOff; xx++ {
				out.set(xx+shift, yy, temp.at(xx, yy))
			}
		}
	}
	for yy := 0; yy < th; yy++ {
		for xx := 0; xx < w+boldOff+shiftTop; xx++ {
			if v := out.at(xx, yy); v < 255 {
				dst.set(x+xx, y-asc+yy, v)
			}
		}
	}
}

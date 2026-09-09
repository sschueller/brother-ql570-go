package ql

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

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

// loadFace returns a font.Face of the given size. sizePx is the em size in
// pixels (at 300 dpi: sizePx = points * 300 / 72). If path is empty, the
// embedded Go Regular font (BSD licensed, latin coverage) is used.
func loadFace(path string, sizePx float64) (font.Face, error) {
	faceKey := fmt.Sprintf("%s|%.3f", path, sizePx)

	fontCacheMu.Lock()
	if f, ok := faceCache[faceKey]; ok {
		fontCacheMu.Unlock()
		return f, nil
	}
	parsed := fontCache[path]
	fontCacheMu.Unlock()

	if parsed == nil {
		var data []byte
		if path == "" {
			data = goregular.TTF
		} else {
			var err error
			data, err = os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading font %s: %w", path, err)
			}
		}
		var err error
		parsed, err = opentype.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parsing font %s: %w", path, err)
		}
		fontCacheMu.Lock()
		fontCache[path] = parsed
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
		return nil, fmt.Errorf("creating face: %w", err)
	}

	fontCacheMu.Lock()
	faceCache[faceKey] = face
	fontCacheMu.Unlock()
	return face, nil
}

// blackImage is an opaque black source for the font drawer.
var blackImage = image.NewUniform(color.Black)

// measureString returns the advance width of s for the given face in
// pixels.
func measureString(face font.Face, s string) float64 {
	d := &font.Drawer{Face: face}
	return float64(d.MeasureString(s)) / 64
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

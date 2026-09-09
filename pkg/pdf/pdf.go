// Package pdf rasterizes PDF documents into grayscale images for printing.
//
// Rendering uses Google's PDFium engine compiled to WebAssembly and
// executed by the pure-Go wazero runtime, so the package builds and runs
// with CGO_ENABLED=0 and without any system PDF library. The WebAssembly
// module is embedded in the binary, which is why the ql570 binary grows by
// a few megabytes once this package is linked.
package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// MaxPages caps the number of pages one operation may render. Rendering
// runs in a WebAssembly sandbox and takes a few hundred milliseconds per
// page, so an unbounded multi-page PDF would stall the caller.
const MaxPages = 30

// MaxPixels caps the largest dimension of a rendered page. Pages are
// rendered at 300 dpi (the printer's resolution); pages larger than
// MaxPixels on either side are scaled down so a poster-size page cannot
// exhaust memory.
const MaxPixels = 4096

// instanceTimeout bounds how long instance acquisition waits for the
// WebAssembly worker.
const instanceTimeout = 60 * time.Second

// The WebAssembly engine is compiled on first use (a few seconds); the
// pool is shared by all callers for the lifetime of the process.
var (
	initOnce sync.Once
	pool     pdfium.Pool
	initErr  error
)

// instance returns a PDFium instance from the lazily initialized pool.
func instance() (pdfium.Pdfium, error) {
	initOnce.Do(func() {
		pool, initErr = webassembly.Init(webassembly.Config{
			MinIdle:  1,
			MaxIdle:  1,
			MaxTotal: 1,
		})
	})
	if initErr != nil {
		return nil, initErr
	}
	inst, err := pool.GetInstance(instanceTimeout)
	if err != nil {
		return nil, fmt.Errorf("starting the PDF engine: %w", err)
	}
	return inst, nil
}

// IsPDF reports whether data starts with the PDF magic header.
func IsPDF(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-"))
}

// PageCount returns the number of pages in data.
func PageCount(data []byte) (int, error) {
	inst, err := instance()
	if err != nil {
		return 0, err
	}
	defer inst.Close()
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return 0, fmt.Errorf("opening PDF: %w", err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return 0, fmt.Errorf("reading page count: %w", err)
	}
	return count.PageCount, nil
}

// RenderPage renders page (0-based) of data into a grayscale image. The
// page is rendered at 300 dpi (the printer's resolution); if that exceeds
// maxPixels on either side, the whole page is scaled down to fit within
// maxPixels while preserving the aspect ratio. A maxPixels of 0 selects
// MaxPixels.
func RenderPage(data []byte, page, maxPixels int) (*image.Gray, error) {
	if maxPixels <= 0 {
		maxPixels = MaxPixels
	}
	inst, err := instance()
	if err != nil {
		return nil, err
	}
	defer inst.Close()
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return nil, fmt.Errorf("opening PDF: %w", err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	return renderPage(inst, doc.Document, page, maxPixels)
}

// RenderPNGFiles renders the given pages (0-based) of data into dir as
// "<prefix>-p1.png", "<prefix>-p2.png", ... (numbered by page, not by
// list position) and returns the file paths. The whole document shares
// one engine instance, so the one-time engine startup is paid once per
// call.
func RenderPNGFiles(data []byte, pages []int, maxPixels int, dir, prefix string) ([]string, error) {
	if len(pages) == 0 {
		return nil, nil
	}
	inst, err := instance()
	if err != nil {
		return nil, err
	}
	defer inst.Close()
	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return nil, fmt.Errorf("opening PDF: %w", err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	paths := make([]string, 0, len(pages))
	for _, page := range pages {
		img, err := renderPage(inst, doc.Document, page, maxPixels)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-p%d.png", prefix, page+1))
		f, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("storing page %d: %w", page+1, err)
		}
		encErr := png.Encode(f, img)
		closeErr := f.Close()
		if encErr != nil {
			return nil, fmt.Errorf("encoding page %d: %w", page+1, encErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("storing page %d: %w", page+1, closeErr)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// renderPage renders a single page of an already-open document. The pixel
// buffer handed back by the WebAssembly engine is only valid until
// Cleanup is called, so it is copied into a fresh image first.
func renderPage(inst pdfium.Pdfium, doc references.FPDF_DOCUMENT, page, maxPixels int) (*image.Gray, error) {
	size, err := inst.GetPageSize(&requests.GetPageSize{
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: page}},
	})
	if err != nil {
		return nil, fmt.Errorf("reading page %d size: %w", page+1, err)
	}
	w := int(math.Round(size.Width * 300 / 72))
	h := int(math.Round(size.Height * 300 / 72))
	if w < 1 || h < 1 {
		return nil, fmt.Errorf("page %d has invalid size %.1fx%.1f pt", page+1, size.Width, size.Height)
	}
	if s := math.Min(float64(maxPixels)/float64(w), float64(maxPixels)/float64(h)); s < 1 {
		w = int(math.Round(float64(w) * s))
		h = int(math.Round(float64(h) * s))
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
	}
	res, err := inst.RenderPageInPixels(&requests.RenderPageInPixels{
		Page:        requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: page}},
		Width:       w,
		Height:      h,
		ImageFormat: requests.RenderImageFormatGrayscale,
	})
	if err != nil {
		return nil, fmt.Errorf("rendering page %d: %w", page+1, err)
	}
	g, ok := res.Result.RenderedImage.(*image.Gray)
	if !ok {
		res.Cleanup()
		return nil, fmt.Errorf("rendering page %d: unexpected image type %T", page+1, res.Result.RenderedImage)
	}
	// The WebAssembly engine's bitmap rows carry padding bytes, so the
	// image's Stride can be larger than its width. Copy row-aware via
	// draw.Draw: a raw Pix copy would shift every row by the padding and
	// render the page progressively sheared ("skewed").
	out := image.NewGray(g.Bounds())
	draw.Draw(out, out.Bounds(), g, g.Bounds().Min, draw.Src)
	res.Cleanup()
	return out, nil
}

package pdf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// minimalPDF returns a small one-page PDF (200x100 pt, a filled black
// rectangle in the middle) with a valid xref table.
func minimalPDF() []byte {
	stream := "0 0 0 rg\n20 20 160 60 re f"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objs)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestIsPDF(t *testing.T) {
	if !IsPDF(minimalPDF()) {
		t.Error("minimal PDF not detected")
	}
	if IsPDF([]byte("\x89PNG\r\n\x1a\n")) {
		t.Error("PNG detected as PDF")
	}
	if IsPDF([]byte("")) {
		t.Error("empty data detected as PDF")
	}
}

func TestPageCount(t *testing.T) {
	n, err := PageCount(minimalPDF())
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("PageCount = %d, want 1", n)
	}
	if _, err := PageCount([]byte("not a pdf")); err == nil {
		t.Fatal("PageCount accepted garbage")
	}
}

func TestRenderPage(t *testing.T) {
	img, err := RenderPage(minimalPDF(), 0, MaxPixels)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	b := img.Bounds()
	// 200x100 pt at 300 dpi = 833x417 px.
	if b.Dx() < 800 || b.Dx() > 850 || b.Dy() < 400 || b.Dy() > 430 {
		t.Fatalf("rendered size %dx%d, want ~833x417", b.Dx(), b.Dy())
	}
	// Corner is white (background), center is black (the rectangle).
	if img.GrayAt(b.Min.X+5, b.Min.Y+5).Y < 200 {
		t.Error("page corner should be white")
	}
	if img.GrayAt(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2).Y > 80 {
		t.Error("page center should be black (filled rectangle)")
	}
}

func TestRenderPageCapped(t *testing.T) {
	img, err := RenderPage(minimalPDF(), 0, 100)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("capped size %dx%d, want 100x50", b.Dx(), b.Dy())
	}
}

func TestRenderPNGFiles(t *testing.T) {
	dir := t.TempDir()
	paths, err := RenderPNGFiles(minimalPDF(), []int{0}, MaxPixels, dir, "test")
	if err != nil {
		t.Fatalf("RenderPNGFiles: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	if want := filepath.Join(dir, "test-p1.png"); paths[0] != want {
		t.Fatalf("path %q, want %q", paths[0], want)
	}
	st, err := os.Stat(paths[0])
	if err != nil {
		t.Fatalf("rendered file missing: %v", err)
	}
	if st.Size() < 100 {
		t.Fatalf("rendered file suspiciously small: %d bytes", st.Size())
	}
}

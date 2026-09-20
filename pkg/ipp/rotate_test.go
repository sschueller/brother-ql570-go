package ipp

import (
	"image"
	"image/color"
	"testing"
)

// testGray builds a grayscale image with distinct pixel values so
// rotations can be verified.
func testGray(w, h int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetGray(x, y, gray8(x+10*y))
		}
	}
	return img
}

func TestRotations(t *testing.T) {
	src := testGray(3, 2)
	// 3x2 -> 2x3
	ccw := rotateGray90CCW(src)
	if ccw.Rect.Dx() != 2 || ccw.Rect.Dy() != 3 {
		t.Fatalf("ccw size %dx%d, want 2x3", ccw.Rect.Dx(), ccw.Rect.Dy())
	}
	if ccw.GrayAt(0, 0) != src.GrayAt(2, 0) || ccw.GrayAt(0, 2) != src.GrayAt(0, 0) {
		t.Fatal("ccw rotation placed pixels incorrectly")
	}
	cw := rotateGray90CW(src)
	if cw.Rect.Dx() != 2 || cw.Rect.Dy() != 3 {
		t.Fatalf("cw size %dx%d, want 2x3", cw.Rect.Dx(), cw.Rect.Dy())
	}
	if cw.GrayAt(0, 0) != src.GrayAt(0, 1) {
		t.Fatal("cw rotation placed pixels incorrectly")
	}
	// 90 CW + 90 CCW = identity
	back := rotateGray90CCW(cw)
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			if back.GrayAt(x, y) != src.GrayAt(x, y) {
				t.Fatal("ccw(cw(img)) != img")
			}
		}
	}
	// 180 rotation
	rot := rotateGray180(src)
	if rot.GrayAt(0, 0) != src.GrayAt(2, 1) || rot.GrayAt(2, 1) != src.GrayAt(0, 0) {
		t.Fatal("180 rotation placed pixels incorrectly")
	}
}

func TestApplyOrientation(t *testing.T) {
	portrait := testGray(3, 5)  // tall
	landscape := testGray(5, 3) // wide

	// Landscape request: portrait pages rotate, landscape pages stay.
	rot := applyOrientation(portrait, orientationLandscape)
	if rot.Rect.Dx() != 5 || rot.Rect.Dy() != 3 {
		t.Fatalf("portrait+landscape size %dx%d, want 5x3", rot.Rect.Dx(), rot.Rect.Dy())
	}
	if got := applyOrientation(landscape, orientationLandscape); got != landscape {
		t.Fatal("landscape+landscape changed the image")
	}

	// Portrait request: landscape pages rotate, portrait pages stay.
	rot = applyOrientation(landscape, orientationPortrait)
	if rot.Rect.Dx() != 3 || rot.Rect.Dy() != 5 {
		t.Fatalf("landscape+portrait size %dx%d, want 3x5", rot.Rect.Dx(), rot.Rect.Dy())
	}
	if got := applyOrientation(portrait, orientationPortrait); got != portrait {
		t.Fatal("portrait+portrait changed the image")
	}

	// Reverse requests rotate 180°.
	if rot := applyOrientation(portrait, orientationReversePortrait); rot.GrayAt(0, 0) != portrait.GrayAt(2, 4) {
		t.Fatal("reverse-portrait did not rotate 180")
	}
	if rot := applyOrientation(portrait, orientationReverseLandscape); rot.Rect.Dx() != 5 || rot.Rect.Dy() != 3 {
		t.Fatal("reverse-landscape did not rotate portrait pages")
	}
}

// gray8 is a tiny helper returning a color.Gray for distinct pixel values.
func gray8(v int) color.Gray { return color.Gray{Y: uint8(v & 0xff)} }

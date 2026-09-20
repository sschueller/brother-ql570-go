package ipp

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

// IPP orientation-requested enum values (RFC 8011 / PWG 5100.13).
const (
	orientationPortrait         = 3
	orientationLandscape        = 4
	orientationReversePortrait  = 5
	orientationReverseLandscape = 6
)

// applyOrientation rotates a page image according to the
// orientation-requested job attribute. Android sends PDF documents as-is
// and expects the printer to orient the pages: a landscape request rotates
// portrait pages 90° (and vice versa), reverse requests flip by 180°.
func applyOrientation(img *image.Gray, requested int) *image.Gray {
	landscape := img.Rect.Dx() > img.Rect.Dy()
	switch requested {
	case orientationLandscape:
		if !landscape {
			return rotateGray90CCW(img)
		}
	case orientationPortrait:
		if landscape {
			return rotateGray90CCW(img)
		}
	case orientationReversePortrait:
		return rotateGray180(img)
	case orientationReverseLandscape:
		if !landscape {
			return rotateGray90CW(img)
		}
	}
	return img
}

// rotateGray90CCW rotates a grayscale image 90° counter-clockwise.
// The result has the source's height as width.
func rotateGray90CCW(src *image.Gray) *image.Gray {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewGray(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			dst.Pix[(w-1-x)*dst.Stride+y] = row[x]
		}
	}
	return dst
}

// rotateGray90CW rotates a grayscale image 90° clockwise. The result has
// the source's height as width.
func rotateGray90CW(src *image.Gray) *image.Gray {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewGray(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			dst.Pix[x*dst.Stride+(h-1-y)] = row[x]
		}
	}
	return dst
}

// rotateGray180 rotates a grayscale image 180°.
func rotateGray180(src *image.Gray) *image.Gray {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			dst.Pix[(h-1-y)*dst.Stride+(w-1-x)] = row[x]
		}
	}
	return dst
}

// readGrayPNG decodes a PNG file into a grayscale image.
func readGrayPNG(path string) (*image.Gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	gray, ok := img.(*image.Gray)
	if ok {
		return gray, nil
	}
	b := img.Bounds()
	out := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x, y, color.GrayModel.Convert(img.At(x, y)))
		}
	}
	return out, nil
}

// writeGrayPNG stores a grayscale image as a PNG file, replacing the
// existing file when one is present.
func writeGrayPNG(path string, img *image.Gray) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

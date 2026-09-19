package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder

	xdraw "golang.org/x/image/draw"
)

var errBadImage = errors.New("media: not a valid image")

const (
	maxSide      = 2048
	thumbSide    = 480
	maxPixels    = 60_000_000 // decompression-bomb guard, checked before decoding
	jpegQuality  = 85
	thumbQuality = 78
)

// processImage decodes (JPEG/PNG only), applies the EXIF orientation, caps the
// long edge, and re-encodes as JPEG — which drops EXIF/GPS metadata and any
// trailing payload — plus a thumbnail. The original bytes are never stored.
func processImage(src []byte) (full, thumb []byte, w, h int, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, nil, 0, 0, errBadImage
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, nil, 0, 0, errBadImage
	}
	img = orient(img, exifOrientation(src))
	img = fit(img, maxSide)
	full, err = encodeJPEG(img, jpegQuality)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	thumb, err = encodeJPEG(fit(img, thumbSide), thumbQuality)
	b := img.Bounds()
	return full, thumb, b.Dx(), b.Dy(), err
}

func encodeJPEG(img image.Image, q int) ([]byte, error) {
	// JPEG has no alpha: flatten transparent PNGs onto white, not black.
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(image.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: q}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fit scales so the longer edge is at most side (never upscales).
func fit(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := max(w, h)
	if long <= side {
		return img
	}
	nw, nh := w*side/long, h*side/long
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// exifOrientation reads the EXIF orientation tag (1–8) from a JPEG, or 1.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) && b[i] == 0xFF {
		marker := b[i+1]
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if size < 2 || i+2+size > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		if marker == 0xDA { // start of scan: no more headers
			return 1
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder = binary.LittleEndian
	if string(t[:2]) == "MM" {
		bo = binary.BigEndian
	} else if string(t[:2]) != "II" {
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient applies EXIF orientation. Mirrored variants (2,4,5,7) are rare from
// phone cameras and are treated as their unmirrored rotation.
func orient(img image.Image, o int) image.Image {
	switch o {
	case 3, 4:
		return rotate(img, 180)
	case 6, 5:
		return rotate(img, 90)
	case 8, 7:
		return rotate(img, 270)
	}
	return img
}

// rotate turns the image clockwise by deg (90, 180 or 270).
func rotate(src image.Image, deg int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	if deg == 180 {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			switch deg {
			case 90:
				dst.Set(h-1-y, x, c)
			case 180:
				dst.Set(w-1-x, h-1-y, c)
			case 270:
				dst.Set(y, w-1-x, c)
			}
		}
	}
	return dst
}

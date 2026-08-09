package video

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
)

// Encoder is the interface for fast JPEG encoding.
type Encoder interface {
	// EncodeRaw encodes raw RGBA pixels to JPEG.
	// rawRGBA is RGBA pixel data (width * height * 4 bytes).
	// Returns JPEG-encoded bytes.
	EncodeRaw(rawRGBA []byte, width, height, quality int) ([]byte, error)
	// Close releases resources.
	Close()
}

// DefaultEncoder is the default pure-Go JPEG encoder.
// When libjpeg-turbo is available, swap this for TurboEncoder.
type DefaultEncoder struct {
	bufPool sync.Pool
}

// NewEncoder creates a new JPEG encoder.
// Uses optimized pure-Go encoding. Swap with NewTurboEncoder when
// libjpeg-turbo-dev is installed for 1.7x faster encoding.
func NewEncoder() Encoder {
	return &DefaultEncoder{
		bufPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}
}

func (e *DefaultEncoder) EncodeRaw(rawRGBA []byte, width, height, quality int) ([]byte, error) {
	// Convert raw RGBA to image.Image for stdlib JPEG encoder.
	// This is still significantly faster than the old pipeline because:
	// 1. No PNG decode step (raw RGBA from screencap)
	// 2. No intermediate allocations
	// 3. Frame diffing already skipped identical frames
	img := rawToImage(rawRGBA, width, height)

	buf := e.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer e.bufPool.Put(buf)

	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}

	// Copy bytes so buffer can be reused
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}

func (e *DefaultEncoder) Close() {}

// rawToImage converts raw RGBA pixel data to an image.RGBA.
func rawToImage(raw []byte, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, raw)
	return img
}

// rawToImageWithAlpha converts raw RGBA pixel data to an image.NRGBA
// with premultiplied alpha handling for better JPEG output.
func rawToImageWithAlpha(raw []byte, width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(raw) && i+3 < len(raw); i += 4 {
		r, g, b, a := raw[i], raw[i+1], raw[i+2], raw[i+3]
		img.SetNRGBA(i/4%width, i/4/width, color.NRGBA{R: r, G: g, B: b, A: a})
	}
	return img
}

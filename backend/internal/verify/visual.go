package verify

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

// Report is the deterministic verdict on one screenshot: what the pixels
// say before any model looks at them. Model eyes are expensive and lenient;
// these checks are free and strict.
type Report struct {
	Width  int
	Height int
	// BlankFraction is the share of pixels that are near-white or near-black.
	BlankFraction float64
	// Blank means the screen is effectively empty — a white or black dead
	// screen, a surface that never drew, a capture of nothing.
	Blank bool
	// Reason names the finding for gate summaries.
	Reason string
}

// BlankThreshold: above this share of blank pixels the screen counts as
// empty. 0.97 leaves room for status bars, nav bars, and sparse layouts
// while catching white/black screens of death.
const BlankThreshold = 0.97

// Analyze decodes one screenshot and reports its deterministic verdict.
// Undecodable bytes are not a blank screen — they are a capture failure,
// reported as a non-blank report with the reason set, so callers fail on
// capture health instead of misreading pixels.
func Analyze(png []byte) Report {
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return Report{Reason: "undecodable screenshot"}
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return Report{Reason: "zero-size screenshot"}
	}
	blank := 0
	// Sample every 4th pixel each way: 1/16th of the image is plenty for a
	// blank-screen verdict and keeps full-res screenshots cheap.
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			// RGBA() returns 16-bit values: white is 0xffff, black is 0.
			// Near-white (all channels > 0xf000) or near-black (< 0x1000).
			if (r > 0xf000 && g > 0xf000 && bl > 0xf000) ||
				(r < 0x1000 && g < 0x1000 && bl < 0x1000) {
				blank++
			}
		}
	}
	sampled := (w + 3) / 4 * ((h + 3) / 4)
	frac := float64(blank) / float64(sampled)
	rep := Report{Width: w, Height: h, BlankFraction: frac}
	if frac >= BlankThreshold {
		rep.Blank = true
		rep.Reason = "blank screen"
	}
	return rep
}

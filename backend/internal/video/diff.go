package video

import "sync"

const (
	// pixelDiffThreshold is the max per-channel difference to consider pixels equal.
	// Accounts for device noise in framebuffer reads.
	pixelDiffThreshold = 3
	// blockCheckStep is the stride for block-based fast comparison.
	// Check every Nth pixel for quick equality test before full compare.
	blockCheckStep = 16
)

// FrameDiffer tracks the previous frame for fast comparison.
type FrameDiffer struct {
	prevRaw  []byte
	prevSize int
	mu       sync.Mutex
}

// NewFrameDiffer creates a new frame differ.
func NewFrameDiffer() *FrameDiffer {
	return &FrameDiffer{}
}

// IsSame returns true if rawRGBA is effectively identical to the previous frame.
// Uses a two-pass approach:
// 1. Quick block check — sample every Nth pixel for fast rejection
// 2. Full pixel compare — only if block check passes
func (d *FrameDiffer) IsSame(rawRGBA []byte) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.prevRaw == nil || len(d.prevRaw) != len(rawRGBA) {
		d.prevRaw = make([]byte, len(rawRGBA))
		copy(d.prevRaw, rawRGBA)
		return false
	}

	// Quick block check: sample pixels at regular intervals
	if len(rawRGBA) >= blockCheckStep*4 {
		for i := 0; i < len(rawRGBA); i += blockCheckStep * 4 {
			if absDiff(rawRGBA[i], d.prevRaw[i]) > pixelDiffThreshold ||
				absDiff(rawRGBA[i+1], d.prevRaw[i+1]) > pixelDiffThreshold ||
				absDiff(rawRGBA[i+2], d.prevRaw[i+2]) > pixelDiffThreshold {
				// Different — update stored frame
				copy(d.prevRaw, rawRGBA)
				return false
			}
		}
	}

	// Full pixel compare
	if !FastCompareRaw(rawRGBA, d.prevRaw, pixelDiffThreshold) {
		copy(d.prevRaw, rawRGBA)
		return false
	}

	return true
}

// Reset clears the stored previous frame.
func (d *FrameDiffer) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prevRaw = nil
	d.prevSize = 0
}

// FastCompareRaw compares two raw RGBA buffers with a per-channel threshold.
// Returns true if all pixels are within threshold.
func FastCompareRaw(a, b []byte, threshold byte) bool {
	if len(a) != len(b) {
		return false
	}

	// Process 4 bytes (one pixel) at a time
	for i := 0; i < len(a); i += 4 {
		if absDiff(a[i], b[i]) > threshold ||
			absDiff(a[i+1], b[i+1]) > threshold ||
			absDiff(a[i+2], b[i+2]) > threshold {
			return false
		}
		// Skip alpha channel comparison for JPEG (not encoded)
	}
	return true
}

// absDiff returns the absolute difference between two bytes.
func absDiff(a, b byte) byte {
	if a > b {
		return a - b
	}
	return b - a
}

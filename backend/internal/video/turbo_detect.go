//go:build cgo && !nocgo

package video

// tryTurboEncoder attempts to create a TurboEncoder.
// Returns nil if the library is not available at runtime.
func tryTurboEncoder() Encoder {
	return &TurboEncoder{}
}

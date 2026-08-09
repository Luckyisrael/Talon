//go:build nocgo || !cgo

package video

// tryTurboEncoder always returns nil when CGo is not available.
func tryTurboEncoder() Encoder {
	return nil
}

package video

import (
	"bytes"
	"sync"
	"testing"
)

func BenchmarkEncoder(b *testing.B) {
	width, height := 1920, 1080
	raw := make([]byte, width*height*4)
	for i := 0; i < len(raw); i += 4 {
		raw[i] = 128
		raw[i+1] = 200
		raw[i+2] = 50
		raw[i+3] = 255
	}

	b.Run("Stdlib", func(b *testing.B) {
		enc := &DefaultEncoder{
			bufPool: sync.Pool{
				New: func() interface{} { return new(bytes.Buffer) },
			},
		}
		defer enc.Close()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = enc.EncodeRaw(raw, width, height, 80)
		}
	})

	b.Run("Turbo", func(b *testing.B) {
		enc := &TurboEncoder{}
		defer enc.Close()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = enc.EncodeRaw(raw, width, height, 80)
		}
	})
}

package verify

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// BenchmarkAnalyze measures the deterministic blank-screen check on a
// phone-sized screenshot. It must stay in the low milliseconds — it runs
// before every vision call.
func BenchmarkAnalyze(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1080, 2400))
	for y := 0; y < 2400; y += 2 {
		for x := 0; x < 1080; x += 2 {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		b.Fatal(err)
	}
	data := buf.Bytes()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Analyze(data)
	}
}

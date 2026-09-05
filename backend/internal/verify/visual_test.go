package verify

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func solidPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnalyzeWhiteScreen(t *testing.T) {
	rep := Analyze(solidPNG(t, 200, 400, color.White))
	if !rep.Blank || rep.Width != 200 || rep.Height != 400 {
		t.Errorf("solid white should be blank: %+v", rep)
	}
}

func TestAnalyzeBlackScreen(t *testing.T) {
	rep := Analyze(solidPNG(t, 200, 400, color.Black))
	if !rep.Blank {
		t.Errorf("solid black should be blank: %+v", rep)
	}
}

func TestAnalyzeContent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 200; x++ {
			// Diagonal gradient: no large blank region anywhere.
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	rep := Analyze(buf.Bytes())
	if rep.Blank {
		t.Errorf("gradient content should not be blank: %+v", rep)
	}
	if rep.BlankFraction > 0.1 {
		t.Errorf("gradient blank fraction should be tiny, got %f", rep.BlankFraction)
	}
}

func TestAnalyzeGarbage(t *testing.T) {
	rep := Analyze([]byte("not a png"))
	if rep.Blank {
		t.Error("undecodable bytes must not read as blank")
	}
	if rep.Reason == "" {
		t.Error("undecodable bytes need a reason")
	}
}

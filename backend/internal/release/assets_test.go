package release

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestValidateScreenshot(t *testing.T) {
	if _, err := ValidateScreenshot(solidPNG(t, 1080, 2400)); err != nil {
		t.Errorf("1080x2400 phone shot should pass: %v", err)
	}
	if o, err := ValidateScreenshot(solidPNG(t, 1920, 1080)); err != nil || o != Landscape {
		t.Errorf("1920x1080 should pass as landscape: %v %v", o, err)
	}
	if _, err := ValidateScreenshot(solidPNG(t, 800, 600)); err == nil {
		t.Error("800x600 should be rejected")
	}
	if _, err := ValidateScreenshot([]byte("not an image")); err == nil {
		t.Error("garbage should be rejected")
	}
}

func TestValidateScreenshotSetOrientation(t *testing.T) {
	good := [][]byte{solidPNG(t, 1080, 2400), solidPNG(t, 1080, 2400)}
	if err := ValidateScreenshotSet(good); err != nil {
		t.Errorf("consistent portrait set should pass: %v", err)
	}
	mixed := [][]byte{solidPNG(t, 1080, 2400), solidPNG(t, 1920, 1080)}
	if err := ValidateScreenshotSet(mixed); err == nil {
		t.Error("mixed orientation set should be flagged")
	}
}

func TestValidateIconAndBanner(t *testing.T) {
	if err := ValidateIcon(solidPNG(t, 512, 512)); err != nil {
		t.Errorf("512 square icon should pass: %v", err)
	}
	if err := ValidateIcon(solidPNG(t, 512, 256)); err == nil {
		t.Error("non-square icon should be rejected")
	}
	if err := ValidateIcon(solidPNG(t, 1024, 1024)); err == nil {
		t.Error("oversize icon should be rejected")
	}
	if err := ValidateBanner(solidPNG(t, 1200, 600)); err != nil {
		t.Errorf("1200x600 banner should pass: %v", err)
	}
	if err := ValidateBanner(solidPNG(t, 1080, 2400)); err == nil {
		t.Error("phone screenshot as banner should be rejected")
	}
}

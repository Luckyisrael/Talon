package video

import (
	"testing"
)

func TestTurboEncoder_EncodeRaw(t *testing.T) {
	enc := NewEncoder()
	defer enc.Close()

	// Create a 100x100 red RGBA test image
	width, height := 100, 100
	raw := make([]byte, width*height*4)
	for i := 0; i < len(raw); i += 4 {
		raw[i] = 255   // R
		raw[i+1] = 0   // G
		raw[i+2] = 0   // B
		raw[i+3] = 255 // A
	}

	jpeg, err := enc.EncodeRaw(raw, width, height, 80)
	if err != nil {
		t.Fatalf("EncodeRaw failed: %v", err)
	}

	if len(jpeg) == 0 {
		t.Fatal("EncodeRaw returned empty JPEG")
	}

	// Check JPEG magic bytes
	if jpeg[0] != 0xFF || jpeg[1] != 0xD8 {
		t.Fatalf("Not a valid JPEG (got %x %x)", jpeg[0], jpeg[1])
	}

	t.Logf("Encoded %dx%d RGBA -> %d bytes JPEG", width, height, len(jpeg))
}

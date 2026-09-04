package release

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

// Asset dimension rules come from Solana's published guidelines (and the
// phase spec §2.6): screenshots must be at least 1080px on both sides with a
// consistent orientation across the set; icons are square up to 512px;
// banners are exactly 1200×600. Screenshots, icons, and banners are distinct
// asset types — an emulator screenshot is never silently accepted as an icon.
const (
	minScreenshotSide = 1080
	maxIconSide       = 512
	bannerWidth       = 1200
	bannerHeight      = 600
)

type Orientation string

const (
	Portrait  Orientation = "portrait"
	Landscape Orientation = "landscape"
	Square    Orientation = "square"
)

func orientationOf(w, h int) Orientation {
	switch {
	case w == h:
		return Square
	case w > h:
		return Landscape
	default:
		return Portrait
	}
}

func dimensions(data []byte) (int, int, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, "", fmt.Errorf("not a readable PNG/JPEG image: %w", err)
	}
	return cfg.Width, cfg.Height, format, nil
}

// Dimensions reports width and height without full decode, for recording
// validated assets.
func Dimensions(data []byte) (int, int, error) {
	w, h, _, err := dimensions(data)
	return w, h, err
}

// ValidateScreenshot checks one store screenshot: decodable, at least 1080px
// on both sides. Returns its orientation for the set-consistency check.
func ValidateScreenshot(data []byte) (Orientation, error) {
	w, h, _, err := dimensions(data)
	if err != nil {
		return "", err
	}
	if w < minScreenshotSide || h < minScreenshotSide {
		return "", fmt.Errorf("screenshot is %dx%d, need at least %dx%d on both sides",
			w, h, minScreenshotSide, minScreenshotSide)
	}
	return orientationOf(w, h), nil
}

// ValidateScreenshotSet checks that every screenshot in the set shares one
// orientation — mixed portrait/landscape sets are rejected at submission.
func ValidateScreenshotSet(shots [][]byte) error {
	var first Orientation
	for i, s := range shots {
		o, err := ValidateScreenshot(s)
		if err != nil {
			return fmt.Errorf("screenshot %d: %w", i+1, err)
		}
		if i == 0 {
			first = o
			continue
		}
		if o != first {
			return fmt.Errorf("mixed orientation: screenshot 1 is %s but screenshot %d is %s — all must match",
				first, i+1, o)
		}
	}
	return nil
}

// ValidateIcon checks a store icon: square, at most 512px per side.
func ValidateIcon(data []byte) error {
	w, h, _, err := dimensions(data)
	if err != nil {
		return err
	}
	if w != h {
		return fmt.Errorf("icon is %dx%d, must be square", w, h)
	}
	if w > maxIconSide {
		return fmt.Errorf("icon is %dx%d, max is %dx%d", w, h, maxIconSide, maxIconSide)
	}
	return nil
}

// ValidateBanner checks a store banner: exactly 1200×600.
func ValidateBanner(data []byte) error {
	w, h, _, err := dimensions(data)
	if err != nil {
		return err
	}
	if w != bannerWidth || h != bannerHeight {
		return fmt.Errorf("banner is %dx%d, must be %dx%d", w, h, bannerWidth, bannerHeight)
	}
	return nil
}

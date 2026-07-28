package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestProcessJPEGWithinLimit(t *testing.T) {
	input := encodeJPEG(t, noisyImage(900, 700))
	result, err := Process(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.MIMEType != "image/jpeg" || len(result.Data) > MaxOutputBytes {
		t.Fatalf("unexpected result: mime=%s size=%d", result.MIMEType, len(result.Data))
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 256 || cfg.Height > 256 {
		t.Fatalf("unexpected dimensions: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestProcessTransparentPNGWithinLimit(t *testing.T) {
	src := noisyImage(700, 500)
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			src.SetRGBA(x, y, color.RGBA{})
		}
	}
	input := encodePNG(t, src)
	result, err := Process(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.MIMEType != "image/png" || len(result.Data) > MaxOutputBytes {
		t.Fatalf("unexpected result: mime=%s size=%d", result.MIMEType, len(result.Data))
	}
	decoded, err := png.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	if alpha != 0 {
		t.Fatalf("expected transparent pixel, alpha=%d", alpha)
	}
}

func TestProcessAnimatedGIFPreservesAnimationWithinLimit(t *testing.T) {
	palette := color.Palette{
		color.RGBA{0, 0, 0, 0},
		color.RGBA{240, 80, 80, 255},
		color.RGBA{80, 120, 240, 255},
	}
	frames := make([]*image.Paletted, 12)
	delays := make([]int, len(frames))
	for i := range frames {
		frame := image.NewPaletted(image.Rect(0, 0, 180, 180), palette)
		for y := 0; y < 180; y++ {
			for x := 0; x < 180; x++ {
				if (x+y+i*7)%31 < 15 {
					frame.SetColorIndex(x, y, 1)
				} else {
					frame.SetColorIndex(x, y, 2)
				}
			}
		}
		frames[i] = frame
		delays[i] = 5
	}
	var input bytes.Buffer
	if err := gif.EncodeAll(&input, &gif.GIF{
		Image: frames,
		Delay: delays,
		Config: image.Config{
			ColorModel: palette,
			Width:      180,
			Height:     180,
		},
		LoopCount: 0,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := Process(input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Animated || result.MIMEType != "image/gif" || len(result.Data) > MaxOutputBytes {
		t.Fatalf("unexpected result: animated=%v mime=%s size=%d", result.Animated, result.MIMEType, len(result.Data))
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Image) < 2 {
		t.Fatalf("expected animated GIF, got %d frame", len(decoded.Image))
	}
}

func TestProcessRejectsInvalidAndOversizedInput(t *testing.T) {
	if _, err := Process([]byte("not an image")); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("expected ErrInvalidImage, got %v", err)
	}
	if _, err := Process(make([]byte, MaxInputBytes+1)); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("expected ErrImageTooLarge, got %v", err)
	}
}

func noisyImage(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var value uint32 = 1
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value = value*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(value >> 24),
				G: uint8(value >> 16),
				B: uint8(value >> 8),
				A: 255,
			})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

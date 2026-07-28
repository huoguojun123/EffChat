package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

const (
	MaxInputBytes  = 10 << 20
	MaxOutputBytes = 100 << 10
	maxDimension   = 4096
	maxPixels      = 16_000_000
	maxGIFFrames   = 300
	maxGIFWork     = 120_000_000
)

var (
	ErrInvalidImage   = errors.New("仅支持有效的 JPEG、PNG 或 GIF 图片")
	ErrImageTooLarge  = errors.New("图片尺寸或动画帧数过大")
	ErrCannotCompress = errors.New("无法将头像压缩到 100 KiB 以下")
)

type Result struct {
	Data     []byte
	Ext      string
	MIMEType string
	Animated bool
}

func Process(input []byte) (*Result, error) {
	if len(input) == 0 || len(input) > MaxInputBytes {
		return nil, ErrImageTooLarge
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(input))
	if err != nil || (format != "jpeg" && format != "png" && format != "gif") {
		return nil, ErrInvalidImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxDimension || cfg.Height > maxDimension ||
		int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, ErrImageTooLarge
	}

	if format == "gif" {
		return processGIF(input, cfg)
	}

	img, decodedFormat, err := image.Decode(bytes.NewReader(input))
	if err != nil || decodedFormat != format {
		return nil, ErrInvalidImage
	}
	if format == "jpeg" {
		return processJPEG(img)
	}
	return processPNG(img)
}

func processJPEG(src image.Image) (*Result, error) {
	for _, side := range []int{256, 224, 192, 160, 128, 96, 64} {
		resized := resize(src, side)
		for _, quality := range []int{86, 76, 66, 56, 46} {
			var out bytes.Buffer
			if err := jpeg.Encode(&out, resized, &jpeg.Options{Quality: quality}); err != nil {
				return nil, err
			}
			if out.Len() <= MaxOutputBytes {
				return &Result{Data: out.Bytes(), Ext: "jpg", MIMEType: "image/jpeg"}, nil
			}
		}
	}
	return nil, ErrCannotCompress
}

func processPNG(src image.Image) (*Result, error) {
	for _, side := range []int{256, 224, 192, 160, 128, 96, 64} {
		resized := resize(src, side)
		for _, levels := range []int{6, 5, 4, 3} {
			paletted := quantize(resized, levels)
			var out bytes.Buffer
			encoder := png.Encoder{CompressionLevel: png.BestCompression}
			if err := encoder.Encode(&out, paletted); err != nil {
				return nil, err
			}
			if out.Len() <= MaxOutputBytes {
				return &Result{Data: out.Bytes(), Ext: "png", MIMEType: "image/png"}, nil
			}
		}
	}
	return nil, ErrCannotCompress
}

func processGIF(input []byte, cfg image.Config) (*Result, error) {
	decoded, err := gif.DecodeAll(bytes.NewReader(input))
	if err != nil || len(decoded.Image) == 0 {
		return nil, ErrInvalidImage
	}
	if len(decoded.Image) > maxGIFFrames ||
		int64(cfg.Width)*int64(cfg.Height)*int64(len(decoded.Image)) > maxGIFWork {
		return nil, ErrImageTooLarge
	}

	animated := len(decoded.Image) > 1
	for _, step := range []int{1, 2, 3, 4, 6, 8, 12, 16} {
		if animated && (len(decoded.Image)+step-1)/step < 2 {
			continue
		}
		delays := groupedDelays(decoded.Delay, len(decoded.Image), step)
		for _, side := range []int{192, 160, 128, 112, 96, 80, 64, 48, 32} {
			for _, levels := range []int{6, 5, 4, 3, 2} {
				frames, width, height := renderGIFFrames(decoded, step, side, levels)
				if len(frames) != len(delays) {
					return nil, ErrInvalidImage
				}
				outGIF := &gif.GIF{
					Image:     frames,
					Delay:     delays,
					Disposal:  make([]byte, len(frames)),
					LoopCount: decoded.LoopCount,
					Config: image.Config{
						ColorModel: frames[0].Palette,
						Width:      width,
						Height:     height,
					},
				}
				for i := range outGIF.Disposal {
					outGIF.Disposal[i] = gif.DisposalBackground
				}
				var out bytes.Buffer
				if err := gif.EncodeAll(&out, outGIF); err != nil {
					return nil, err
				}
				if out.Len() <= MaxOutputBytes {
					return &Result{
						Data:     out.Bytes(),
						Ext:      "gif",
						MIMEType: "image/gif",
						Animated: animated,
					}, nil
				}
			}
		}
	}
	return nil, ErrCannotCompress
}

func renderGIFFrames(src *gif.GIF, step, maxSide, levels int) ([]*image.Paletted, int, int) {
	canvasBounds := image.Rect(0, 0, src.Config.Width, src.Config.Height)
	canvas := image.NewRGBA(canvasBounds)
	var previous *image.RGBA
	selected := make([]*image.Paletted, 0, (len(src.Image)+step-1)/step)
	width, height := scaledDimensions(canvasBounds.Dx(), canvasBounds.Dy(), maxSide)

	for i, frame := range src.Image {
		if i > 0 {
			switch disposalAt(src.Disposal, i-1) {
			case gif.DisposalBackground:
				draw.Draw(canvas, src.Image[i-1].Bounds(), image.Transparent, image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				if previous != nil {
					draw.Draw(canvas, canvasBounds, previous, image.Point{}, draw.Src)
				}
			}
		}
		if disposalAt(src.Disposal, i) == gif.DisposalPrevious {
			previous = cloneRGBA(canvas)
		} else {
			previous = nil
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if i%step == 0 {
			selected = append(selected, quantize(resizeTo(canvas, width, height), levels))
		}
	}
	return selected, width, height
}

func groupedDelays(delays []int, frameCount, step int) []int {
	grouped := make([]int, 0, (frameCount+step-1)/step)
	for start := 0; start < frameCount; start += step {
		end := min(start+step, frameCount)
		delay := 0
		for i := start; i < end; i++ {
			if i < len(delays) {
				delay += delays[i]
			}
		}
		if delay == 0 {
			delay = 1
		}
		grouped = append(grouped, delay)
	}
	return grouped
}

func disposalAt(disposals []byte, index int) byte {
	if index >= 0 && index < len(disposals) {
		return disposals[index]
	}
	return gif.DisposalNone
}

func resize(src image.Image, maxSide int) image.Image {
	bounds := src.Bounds()
	width, height := scaledDimensions(bounds.Dx(), bounds.Dy(), maxSide)
	if width == bounds.Dx() && height == bounds.Dy() && bounds.Min.X == 0 && bounds.Min.Y == 0 {
		return src
	}
	return resizeTo(src, width, height)
}

func resizeTo(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func scaledDimensions(width, height, maxSide int) (int, int) {
	if width <= maxSide && height <= maxSide {
		return width, height
	}
	if width >= height {
		return maxSide, max(1, height*maxSide/width)
	}
	return max(1, width*maxSide/height), maxSide
}

func quantize(src image.Image, levels int) *image.Paletted {
	p := cubePalette(levels)
	dst := image.NewPaletted(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()), p)
	draw.FloydSteinberg.Draw(dst, dst.Bounds(), src, src.Bounds().Min)
	return dst
}

func cubePalette(levels int) color.Palette {
	p := color.Palette{color.RGBA{0, 0, 0, 0}}
	for r := 0; r < levels; r++ {
		for g := 0; g < levels; g++ {
			for b := 0; b < levels; b++ {
				p = append(p, color.RGBA{
					R: uint8(r * 255 / (levels - 1)),
					G: uint8(g * 255 / (levels - 1)),
					B: uint8(b * 255 / (levels - 1)),
					A: 255,
				})
			}
		}
	}
	return p
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

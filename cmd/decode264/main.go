package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rcarmo/go-264/decode"
)

func main() {
	input := flag.String("i", "", "input H.264 Annex B file")
	outDir := flag.String("o", ".", "output directory for decoded frames")
	format := flag.String("f", "color", "output format: png (greyscale), color (YUV→RGB), or yuv (raw planar)")
	flag.Parse()

	if *input == "" {
		fmt.Fprintln(os.Stderr, "Usage: decode264 -i input.h264 [-o outdir] [-f color|png|yuv]")
		os.Exit(1)
	}

	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Input: %s (%d bytes)\n", *input, len(data))

	dec := decode.NewDecoder()
	start := time.Now()
	frames, err := dec.Decode(data)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "decode: %v\n", err)
		os.Exit(1)
	}

	for _, sps := range dec.SPS {
		fmt.Printf("Stream: %dx%d, profile=%d, level=%d\n",
			sps.Width, sps.Height, sps.ProfileIDC, sps.LevelIDC)
	}
	fmt.Printf("Decoded %d frames in %v (%.1f fps)\n",
		len(frames), elapsed.Round(time.Millisecond),
		float64(len(frames))/elapsed.Seconds())
	frames = orderFramesForOutput(frames, dec)

	os.MkdirAll(*outDir, 0755)
	for i, f := range frames {
		switch *format {
		case "color":
			outPath := filepath.Join(*outDir, fmt.Sprintf("frame_%04d.png", i))
			if err := writeFrameColor(f, outPath); err != nil {
				fmt.Fprintf(os.Stderr, "write frame %d: %v\n", i, err)
			} else {
				fmt.Printf("  %s (%dx%d) [color]\n", outPath, f.Width, f.Height)
			}
		case "png":
			outPath := filepath.Join(*outDir, fmt.Sprintf("frame_%04d.png", i))
			if err := writeFramePNG(f, outPath); err != nil {
				fmt.Fprintf(os.Stderr, "write frame %d: %v\n", i, err)
			} else {
				fmt.Printf("  %s (%dx%d)\n", outPath, f.Width, f.Height)
			}
		case "yuv":
			outPath := filepath.Join(*outDir, fmt.Sprintf("frame_%04d.yuv", i))
			if err := writeFrameYUV(f, outPath); err != nil {
				fmt.Fprintf(os.Stderr, "write frame %d: %v\n", i, err)
			} else {
				fmt.Printf("  %s (%dx%d)\n", outPath, f.Width, f.Height)
			}
		}
	}
}

func orderFramesForOutput(frames []*decode.DecodedFrame, dec *decode.Decoder) []*decode.DecodedFrame {
	if len(frames) < 2 || dec == nil {
		return frames
	}
	maxPOC := 0
	for _, sps := range dec.SPS {
		if sps != nil && sps.Log2MaxPocLsb > 0 && sps.Log2MaxPocLsb < 31 {
			v := 1 << sps.Log2MaxPocLsb
			if v > maxPOC {
				maxPOC = v
			}
		}
	}
	ordered := append([]*decode.DecodedFrame(nil), frames...)
	if maxPOC <= 0 {
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].POC < ordered[j].POC })
		return ordered
	}
	type keyed struct {
		frame *decode.DecodedFrame
		poc   int
		idx   int
	}
	keys := make([]keyed, 0, len(ordered))
	cycle, prev := 0, ordered[0].POC
	for i, f := range ordered {
		raw := f.POC
		if i > 0 {
			if prev > (3*maxPOC)/4 && raw < maxPOC/4 {
				cycle += maxPOC
			} else if prev < maxPOC/4 && raw > (3*maxPOC)/4 {
				cycle -= maxPOC
			}
		}
		keys = append(keys, keyed{frame: f, poc: cycle + raw, idx: i})
		prev = raw
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].poc != keys[j].poc {
			return keys[i].poc < keys[j].poc
		}
		return keys[i].idx < keys[j].idx
	})
	for i, k := range keys {
		ordered[i] = k.frame
	}
	return ordered
}

// writeFrameColor converts YUV→RGB using ITU-R BT.601 coefficients and writes
// a full-color PNG. This is the default output mode.
func writeFrameColor(f *decode.DecodedFrame, path string) error {
	img := image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width; x++ {
			lY := int(f.PixelY(x, y))
			cU := int(f.PixelU(x/2, y/2)) - 128
			cV := int(f.PixelV(x/2, y/2)) - 128
			// BT.601 full-range matrix (scaled by 1024 to avoid float)
			r := clamp(lY + (cV*1436)>>10)
			g := clamp(lY - (cU*352+cV*731)>>10)
			b := clamp(lY + (cU*1814)>>10)
			img.SetRGBA(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255})
		}
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, img)
}

// writeFramePNG writes a greyscale (luma-only) PNG.
func writeFramePNG(f *decode.DecodedFrame, path string) error {
	img := image.NewGray(image.Rect(0, 0, f.Width, f.Height))
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width; x++ {
			img.Pix[y*img.Stride+x] = f.PixelY(x, y)
		}
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, img)
}

// writeFrameYUV writes raw YUV 4:2:0 planar data (Y then U then V).
func writeFrameYUV(f *decode.DecodedFrame, path string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	for y := 0; y < f.Height; y++ {
		if _, err := out.Write(f.Y[y*f.StrideY : y*f.StrideY+f.Width]); err != nil {
			return err
		}
	}
	for y := 0; y < f.Height/2; y++ {
		if _, err := out.Write(f.U[y*f.StrideC : y*f.StrideC+f.Width/2]); err != nil {
			return err
		}
	}
	for y := 0; y < f.Height/2; y++ {
		if _, err := out.Write(f.V[y*f.StrideC : y*f.StrideC+f.Width/2]); err != nil {
			return err
		}
	}
	return nil
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

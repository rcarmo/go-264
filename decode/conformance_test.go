package decode

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"
)

func psnr(a, b []uint8, w, h, strideA, strideB int) float64 {
	var mse float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := float64(a[y*strideA+x]) - float64(b[y*strideB+x])
			mse += d * d
		}
	}
	mse /= float64(w * h)
	if mse < 1e-10 {
		return 99.0
	}
	return 10 * math.Log10(255*255/mse)
}

func TestConformanceGray16(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/gray16.h264")
	if err != nil {
		t.Skip("no test file")
	}
	dec := NewDecoder()
	frames, err := dec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	f := frames[0]
	// All pixels should be ~128 (gray)
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width; x++ {
			v := f.PixelY(x, y)
			if v < 124 || v > 132 {
				t.Fatalf("pixel(%d,%d)=%d, want ~128", x, y, v)
			}
		}
	}
	t.Log("Gray 16×16: pixel-accurate ✓")
}

func TestConformanceBBB(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/bbb_annexb.h264")
	if err != nil {
		t.Skip("no test file")
	}
	dec := NewDecoder()
	frames, err := dec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 10 {
		t.Fatalf("decoded %d frames, want >=10", len(frames))
	}
	t.Logf("BBB: decoded %d frames at %dx%d", len(frames), frames[0].Width, frames[0].Height)
	// Check first frame has non-trivial content
	unique := map[uint8]bool{}
	for y := 0; y < frames[0].Height; y++ {
		for x := 0; x < frames[0].Width; x++ {
			unique[frames[0].PixelY(x, y)] = true
		}
	}
	if len(unique) < 50 {
		t.Fatalf("only %d unique values, want >=50", len(unique))
	}
	t.Logf("BBB frame 0: %d unique pixel values ✓", len(unique))
}

func TestConformanceBaseline(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/testsrc_bl.h264")
	if err != nil {
		t.Skip("no test file")
	}
	dec := NewDecoder()
	frames, err := dec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 5 {
		t.Fatalf("decoded %d frames, want >=5", len(frames))
	}
	t.Logf("Baseline: %d frames, %dx%d", len(frames), frames[0].Width, frames[0].Height)
}

func TestConformancePSNRRegression(t *testing.T) {
	cases := []struct {
		name   string
		stream string
		refs   []string
		minAvg float64
	}{
		{"dark64", "/workspace/tmp/dark64.h264", []string{"/workspace/tmp/dark_ref.png"}, 30.0},
		{"baseline", "/workspace/tmp/testsrc_bl.h264", []string{
			"/workspace/tmp/bl_allref_0001.png", "/workspace/tmp/bl_allref_0002.png",
			"/workspace/tmp/bl_allref_0003.png", "/workspace/tmp/bl_allref_0004.png",
			"/workspace/tmp/bl_allref_0005.png", "/workspace/tmp/bl_allref_0006.png",
			"/workspace/tmp/bl_allref_0007.png", "/workspace/tmp/bl_allref_0008.png",
			"/workspace/tmp/bl_allref_0009.png", "/workspace/tmp/bl_allref_0010.png",
		}, 7.35},
		{"bbb-frame0", "/workspace/tmp/bbb_annexb.h264", []string{"/workspace/tmp/bbb_ref_0001.png"}, 8.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.stream)
			if err != nil {
				t.Skipf("stream fixture missing: %v", err)
			}
			for _, rp := range tc.refs {
				if _, err := os.Stat(rp); err != nil {
					t.Skipf("reference fixture missing: %s", rp)
				}
			}
			frames, err := NewDecoder().Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) < len(tc.refs) {
				t.Fatalf("decoded %d frames want >=%d", len(frames), len(tc.refs))
			}
			var sum float64
			for i, rp := range tc.refs {
				ref, err := readGrayPNG(rp)
				if err != nil {
					t.Fatal(err)
				}
				f := frames[i]
				if f.Width != ref.W || f.Height != ref.H {
					t.Fatalf("frame %d size=%dx%d ref=%dx%d", i, f.Width, f.Height, ref.W, ref.H)
				}
				sum += psnr(f.Y, ref.Pix, f.Width, f.Height, f.StrideY, ref.Stride)
			}
			avg := sum / float64(len(tc.refs))
			t.Logf("%s avg PSNR %.2f dB", tc.name, avg)
			if avg < tc.minAvg {
				t.Fatalf("avg PSNR %.2f < %.2f", avg, tc.minAvg)
			}
		})
	}
}

type grayFixture struct {
	Pix    []uint8
	W, H   int
	Stride int
}

func readGrayPNG(path string) (*grayFixture, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			pix[y*w+x] = color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray).Y
		}
	}
	return &grayFixture{Pix: pix, W: w, H: h, Stride: w}, nil
}

func TestConformanceChromaPlanes(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/testsrc_bl.h264")
	if err != nil {
		t.Skip("no baseline fixture")
	}
	frames, err := NewDecoder().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	f := frames[0]
	uniqU, uniqV := map[uint8]bool{}, map[uint8]bool{}
	for y := 0; y < f.Height/2; y++ {
		for x := 0; x < f.Width/2; x++ {
			uniqU[f.PixelU(x, y)] = true
			uniqV[f.PixelV(x, y)] = true
		}
	}
	if len(uniqU) < 16 || len(uniqV) < 16 {
		t.Fatalf("chroma diversity too low: U=%d V=%d", len(uniqU), len(uniqV))
	}
	t.Logf("baseline chroma diversity: U=%d V=%d", len(uniqU), len(uniqV))
}

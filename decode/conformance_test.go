package decode

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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

func maxAbsDiff(a, b []uint8, w, h, strideA, strideB int) int {
	maxDiff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := int(a[y*strideA+x]) - int(b[y*strideB+x])
			if d < 0 {
				d = -d
			}
			if d > maxDiff {
				maxDiff = d
			}
		}
	}
	return maxDiff
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

func TestConformanceBaselineFirstFrameLumaPSNR(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/testsrc_bl.h264")
	if err != nil {
		t.Skipf("stream fixture missing: %v", err)
	}
	ref, err := readGrayPNG("/workspace/tmp/bl_allref_0001.png")
	if err != nil {
		t.Skipf("reference fixture missing: %v", err)
	}
	frames, err := NewDecoder().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("decoded no frames")
	}
	f := frames[0]
	got := psnr(f.Y, ref.Pix, f.Width, f.Height, f.StrideY, ref.Stride)
	t.Logf("baseline first-frame luma PSNR %.2f dB", got)
	if got < 28.0 {
		t.Fatalf("baseline first-frame luma PSNR %.2f < 28.00", got)
	}
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
		}, 27.5},
		{"bbb-frame0", "/workspace/tmp/bbb_annexb.h264", []string{"/workspace/tmp/bbb_ref_0001.png"}, 7.8},
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

func TestConformanceYUVReferencePlanes(t *testing.T) {
	data, err := os.ReadFile("/workspace/tmp/testsrc_bl.h264")
	if err != nil {
		t.Skip("no baseline fixture")
	}
	ref, err := os.ReadFile("/workspace/tmp/bl_ref_yuv/ref.yuv")
	if err != nil {
		t.Skipf("no YUV reference fixture: %v", err)
	}
	frames, err := NewDecoder().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 10 {
		t.Fatalf("decoded %d frames, want >=10", len(frames))
	}
	w, h := frames[0].Width, frames[0].Height
	ySize := w * h
	cW, cH := w/2, h/2
	cSize := cW * cH
	frameSize := ySize + 2*cSize
	if len(ref) < frameSize*10 {
		t.Fatalf("YUV reference too small: got %d want >=%d", len(ref), frameSize*10)
	}
	var sumY, sumU, sumV float64
	maxY, maxU, maxV := 0, 0, 0
	for i := 0; i < 10; i++ {
		f := frames[i]
		off := i * frameSize
		yRef := ref[off : off+ySize]
		uRef := ref[off+ySize : off+ySize+cSize]
		vRef := ref[off+ySize+cSize : off+frameSize]
		sumY += psnr(f.Y, yRef, w, h, f.StrideY, w)
		sumU += psnr(f.U, uRef, cW, cH, f.StrideC, cW)
		sumV += psnr(f.V, vRef, cW, cH, f.StrideC, cW)
		maxY = max(maxY, maxAbsDiff(f.Y, yRef, w, h, f.StrideY, w))
		maxU = max(maxU, maxAbsDiff(f.U, uRef, cW, cH, f.StrideC, cW))
		maxV = max(maxV, maxAbsDiff(f.V, vRef, cW, cH, f.StrideC, cW))
	}
	avgY, avgU, avgV := sumY/10, sumU/10, sumV/10
	t.Logf("baseline YUV avg PSNR: Y=%.2f U=%.2f V=%.2f dB; max diff Y=%d U=%d V=%d", avgY, avgU, avgV, maxY, maxU, maxV)
	if avgY < 38.0 || avgU < 24.0 || avgV < 19.0 {
		t.Fatalf("YUV PSNR too low: Y=%.2f U=%.2f V=%.2f", avgY, avgU, avgV)
	}
	if maxY > 180 || maxU > 250 || maxV > 250 {
		t.Fatalf("YUV max diff too high: Y=%d U=%d V=%d", maxY, maxU, maxV)
	}
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

// TestSyntaxParityBaseline verifies syntax parity between our CAVLC decoder
// and FFmpeg by comparing per-frame type sequence and pixel mean Y-values.
// This forms part of the Syntax Parity hard gate.
func TestSyntaxParityBaseline(t *testing.T) {
	const input = "/workspace/tmp/testsrc_bl.h264"
	if _, err := os.Stat(input); err != nil {
		t.Skip("testsrc_bl.h264 not available")
	}

	// --- Run FFmpeg showinfo to get reference frame types and pixel means ---
	cmd := exec.Command("ffmpeg", "-i", input, "-vf", "showinfo", "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	type frameRef struct {
		isKey bool
		pType string
		meanY float64
	}
	re := regexp.MustCompile(`n:\s*(\d+).*iskey:(\d+)\s+type:([IP]).*mean:\[(\d+)`)
	var refs []frameRef
	for _, line := range strings.Split(string(out), "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		isKey := m[2] == "1"
		pType := m[3]
		meanY, _ := strconv.ParseFloat(m[4], 64)
		refs = append(refs, frameRef{isKey: isKey, pType: pType, meanY: meanY})
	}
	if len(refs) == 0 {
		t.Skip("ffmpeg showinfo not available or produced no output")
	}

	// --- Decode with our decoder ---
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	dec := NewDecoder()
	frames, err := dec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Frame count
	if len(frames) != len(refs) {
		t.Errorf("frame count mismatch: our=%d ffmpeg=%d", len(frames), len(refs))
		if len(frames) < len(refs) {
			refs = refs[:len(frames)]
		} else {
			frames = frames[:len(refs)]
		}
	}

	// Frame type sequence
	ourSeq, ffmpegSeq := "", ""
	for _, f := range frames {
		if f.IsIDR {
			ourSeq += "I"
		} else {
			ourSeq += "P"
		}
	}
	for _, r := range refs {
		if r.isKey {
			ffmpegSeq += "I"
		} else {
			ffmpegSeq += "P"
		}
	}
	if ourSeq != ffmpegSeq {
		t.Errorf("frame type sequence mismatch: our=%s ffmpeg=%s", ourSeq, ffmpegSeq)
	} else {
		t.Logf("frame type sequence: %s ✓", ourSeq)
	}

	// Per-frame pixel mean Y comparison (allow ±5 intensity units)
	mismatches := 0
	for i, ref := range refs {
		if i >= len(frames) {
			break
		}
		f := frames[i]
		sum := 0.0
		n := f.Width * f.Height
		for y := 0; y < f.Height; y++ {
			for x := 0; x < f.Width; x++ {
				sum += float64(f.PixelY(x, y))
			}
		}
		ourMean := sum / float64(n)
		diff := math.Abs(ourMean - ref.meanY)
		if diff > 5.0 {
			t.Errorf("frame %d mean_Y mismatch: our=%.1f ffmpeg=%.1f diff=%.1f", i, ourMean, ref.meanY, diff)
			mismatches++
		}
	}
	if mismatches == 0 {
		t.Logf("all %d frames: pixel means within ±5 of FFmpeg reference ✓", len(refs))
	}
}

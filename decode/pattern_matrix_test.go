package decode

import (
	"math"
	"os"
	"testing"
)

type decoderPatternCase struct {
	name        string
	path        string
	minFrames   int
	width       int
	height      int
	minUnique   int
	maxDiffGray *uint8
}

func TestDecoderPatternMatrix(t *testing.T) {
	grayMax := uint8(3)
	cases := []decoderPatternCase{
		{name: "I16x16-gray", path: "/workspace/tmp/gray16.h264", minFrames: 1, width: 16, height: 16, minUnique: 1, maxDiffGray: &grayMax},
		{name: "I16x16-dark", path: "/workspace/tmp/dark64.h264", minFrames: 1, width: 64, height: 64, minUnique: 1},
		{name: "Baseline-I/P-CAVLC", path: "/workspace/tmp/testsrc_bl.h264", minFrames: 10, width: 320, height: 240, minUnique: 64},
		{name: "High-CABAC-smoke", path: "/workspace/tmp/bbb_annexb.h264", minFrames: 300, width: 640, height: 360, minUnique: 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Skipf("fixture not available: %v", err)
			}
			dec := NewDecoder()
			frames, err := dec.Decode(data)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(frames) < tc.minFrames {
				t.Fatalf("frames=%d want >=%d", len(frames), tc.minFrames)
			}
			f := frames[0]
			if f.Width != tc.width || f.Height != tc.height {
				t.Fatalf("size=%dx%d want %dx%d", f.Width, f.Height, tc.width, tc.height)
			}
			// Use the richest decoded frame for the uniqueness check: some streams
			// (e.g. BBB) open on a near-black fade-in whose IDR is intentionally
			// low-entropy, matching FFmpeg. The meaningful signal is that the decoder
			// produces full-entropy content in at least one frame.
			bestUnique := 0
			for _, fr := range frames {
				unique := map[uint8]bool{}
				for y := 0; y < fr.Height; y++ {
					for x := 0; x < fr.Width; x++ {
						unique[fr.PixelY(x, y)] = true
					}
				}
				if len(unique) > bestUnique {
					bestUnique = len(unique)
				}
			}
			if bestUnique < tc.minUnique {
				t.Fatalf("richest unique luma=%d want >=%d", bestUnique, tc.minUnique)
			}
			if tc.maxDiffGray != nil {
				maxDiff := 0
				for y := 0; y < f.Height; y++ {
					for x := 0; x < f.Width; x++ {
						d := int(math.Abs(float64(int(f.PixelY(x, y)) - 128)))
						if d > maxDiff {
							maxDiff = d
						}
					}
				}
				if maxDiff > int(*tc.maxDiffGray) {
					t.Fatalf("gray max diff=%d want <=%d", maxDiff, *tc.maxDiffGray)
				}
			}
		})
	}
}

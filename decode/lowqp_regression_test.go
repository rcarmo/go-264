package decode

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"testing"
)

func TestCABACBLowQPExact(t *testing.T) {
	data, err := os.ReadFile("testdata/cabac-b-lowqp.h264")
	if err != nil {
		t.Fatal(err)
	}
	frames, err := NewDecoder().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 {
		t.Fatalf("frames=%d, want 4", len(frames))
	}
	sort.SliceStable(frames, func(i, j int) bool { return frames[i].FullPOC < frames[j].FullPOC })
	h := sha256.New()
	for _, f := range frames {
		if f.Width != 128 || f.Height != 96 {
			t.Fatalf("dimensions %dx%d", f.Width, f.Height)
		}
		for y := 0; y < f.Height; y++ {
			h.Write(f.Y[y*f.StrideY : y*f.StrideY+f.Width])
		}
		for _, p := range [][]byte{f.U, f.V} {
			for y := 0; y < f.Height/2; y++ {
				h.Write(p[y*f.StrideC : y*f.StrideC+f.Width/2])
			}
		}
	}
	const want = "54bdddd49d3ec6f13f6147abb300f1d96e3e0159944cc7142800ad667cb3944b"
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Fatalf("YUV SHA256=%s, want %s (FFmpeg 8.1.2)", got, want)
	}
}

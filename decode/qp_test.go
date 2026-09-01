package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
)

func TestUpdateQPWrapsArbitraryDeltas(t *testing.T) {
	cases := []struct {
		current, delta int
		want           int
	}{
		{26, 0, 26},
		{51, 1, 0},
		{0, -1, 51},
		{50, 5, 3},
		{10, -70, 44},
	}
	for _, tc := range cases {
		if got := updateQP(tc.current, tc.delta); got != tc.want {
			t.Fatalf("updateQP(%d,%d) got %d want %d", tc.current, tc.delta, got, tc.want)
		}
	}
}

func TestCAVLCPSkipPreservesQPForDeblocking(t *testing.T) {
	t.Setenv("GO264_DISABLE_DEBLOCK", "")
	d := NewDecoder()
	d.SPS[0] = &nal.SPS{
		ProfileIDC: 66, ConstraintFlags: 0xc0, LevelIDC: 10,
		ChromaFormatIDC: 1, FrameMbsOnlyFlag: true,
		BitDepthLuma: 8, BitDepthChroma: 8,
		Log2MaxFrameNum: 4, PicOrderCntType: 2, MaxNumRefFrames: 1,
		PicWidthInMbs: 2, PicHeightInMapUnits: 1, Width: 32, Height: 16,
	}
	d.PPS[0] = &nal.PPS{
		PicInitQP: 26, NumRefIdxL0Active: 1, NumRefIdxL1Active: 1,
		NumSliceGroups: 1, DeblockingFilterControl: true,
	}
	ref := frame.NewFrame(32, 16)
	ref.IsRef = true
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(100)
			if x == 16 {
				v = 104
			} else if x > 16 {
				v = 108
			}
			ref.Y[y*ref.StrideY+x] = v
		}
	}
	d.DPB.Add(ref)

	// P slice, frame_num=1, slice_qp_delta=10 (QP 36), deblocking enabled.
	// MB 0 is P16x16 with MVD (4,0), CBP 0; MB 1 is a final skip run of 1.
	// Their different motion vectors give bS=1 at x=16. The skipped MB must
	// retain QP 36: an uninitialized QP 0 lowers the boundary average to 18
	// and incorrectly suppresses filtering of [100,104 | 104,108].
	f, err := d.decodeSlice(nal.Unit{
		Type: nal.TypeSliceNonIDR, RefIDC: 1,
		Payload: []byte{0xe2, 0x02, 0x9f, 0x11, 0xa8},
	})
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 16; y++ {
		for dx, want := range []uint8{102, 103, 105, 106} {
			x := 14 + dx
			if got := f.Y[y*f.StrideY+x]; got != want {
				t.Fatalf("deblocked pixel (%d,%d)=%d, want %d", x, y, got, want)
			}
		}
	}
}

package decode

import (
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/syntax"
)

func intraNeighborTestState() (*Decoder, *frame.Frame) {
	f := frame.NewFrame(48, 48)
	p := &pictureState{mbSliceID: make([]int, 9), mbIsIntra: make([]bool, 9)}
	for i := range p.mbSliceID {
		p.mbSliceID[i] = 1
		p.mbIsIntra[i] = true
	}
	d := &Decoder{mbW: 3, mbH: 3, intraModes: make([]int8, 12*12),
		picture: p, slice: &sliceState{id: 1, pps: &nal.PPS{}}}
	for i := range d.intraModes {
		d.intraModes[i] = pred.Intra4x4DC
	}
	// The current MB has not yet had its type written back.
	p.mbIsIntra[4] = false
	// Give the top, left and diagonal distinct values. These samples belong to
	// different macroblocks, so excluding the diagonal must not exclude edges.
	for i := 0; i < 16; i++ {
		f.SetPixelY(16+i, 15, 40)
		f.SetPixelY(15, 16+i, 80)
	}
	f.SetPixelY(15, 15, 200)
	for i := 0; i < 8; i++ {
		f.SetPixelU(8+i, 7, 40)
		f.SetPixelU(7, 8+i, 80)
		f.SetPixelV(8+i, 7, 40)
		f.SetPixelV(7, 8+i, 80)
	}
	return d, f
}

func TestIntraNeighborAvailability(t *testing.T) {
	d, f := intraNeighborTestState()
	d.picture.mbSliceID[0] = -1 // not decoded
	d.picture.mbSliceID[1] = 0  // another slice
	d.picture.mbIsIntra[3] = false
	for _, constrained := range []bool{false, true} {
		d.slice.pps.ConstrainedIntraPred = constrained
		for _, tc := range []struct {
			name string
			x, y int
			want bool
		}{
			{"picture-edge", -1, 16, false},
			{"undecoded-diagonal", 15, 15, false},
			{"other-slice-top", 16, 15, false},
			{"inter-left", 15, 16, !constrained},
			{"intra-top-right", 32, 15, true},
			{"current-macroblock", 16, 16, true},
		} {
			if got := d.intraLumaSampleAvailable(f, 1, 1, tc.x, tc.y); got != tc.want {
				t.Errorf("%s constrained=%v: got %v want %v", tc.name, constrained, got, tc.want)
			}
		}
	}
}

func TestIntraDCPredictionUsesAvailableSliceNeighbors(t *testing.T) {
	for _, tc := range []struct {
		name                string
		topSlice, leftSlice int
		topIntra, leftIntra bool
		constrained         bool
		want                uint8
	}{
		{"both", 1, 1, true, true, false, 60},
		{"cross-slice-top", 0, 1, true, true, false, 80},
		{"cross-slice-left", 1, 0, true, true, false, 40},
		{"cross-slice-both", 0, 0, true, true, false, 128},
		{"unconstrained-inter", 1, 1, false, false, false, 60},
		{"constrained-top", 1, 1, false, true, true, 80},
		{"constrained-left", 1, 1, true, false, true, 40},
		{"constrained-both", 1, 1, false, false, true, 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newState := func() (*Decoder, *frame.Frame) {
				d, f := intraNeighborTestState()
				d.picture.mbSliceID[0] = 0 // no top-left reference filter input
				d.picture.mbSliceID[1], d.picture.mbSliceID[3] = tc.topSlice, tc.leftSlice
				d.picture.mbIsIntra[1], d.picture.mbIsIntra[3] = tc.topIntra, tc.leftIntra
				d.slice.pps.ConstrainedIntraPred = tc.constrained
				return d, f
			}
			for _, size := range []int{4, 8, 16} {
				d, f := newState()
				mb := &syntax.MBIntra{Intra16x16PredMode: pred.Intra16x16DC}
				for i := range mb.IntraPredMode {
					mb.IntraPredMode[i] = -1
				}
				for i := range mb.I8x8PredMode {
					mb.I8x8PredMode[i] = pred.Intra4x4DC
				}
				switch size {
				case 4:
					d.reconstruct4x4(f, mb, 1, 1, 26)
				case 8:
					d.reconstruct8x8(f, mb, 1, 1, 26)
				case 16:
					d.reconstruct16x16(f, mb, 1, 1, 26)
				}
				for y := 0; y < size; y++ {
					for x := 0; x < size; x++ {
						if got := f.PixelY(16+x, 16+y); got != tc.want {
							t.Fatalf("luma %dx%d [%d,%d]=%d want %d", size, size, x, y, got, tc.want)
						}
					}
				}
			}
			d, f := newState()
			for comp := 0; comp < 2; comp++ {
				got := d.predictChroma8x8(f, comp, 1, 1, 0)
				// The first chroma DC quadrant uses both available edges.
				for y := 0; y < 4; y++ {
					for x := 0; x < 4; x++ {
						if got[y*8+x] != tc.want {
							t.Fatalf("chroma %d [%d,%d]=%d want %d", comp, x, y, got[y*8+x], tc.want)
						}
					}
				}
			}
		})
	}
	// H.264 8.3.2.2.1 filters each available edge using the available corner,
	// even when constrained intra excludes the other edge. Here top=40,
	// left=80 and corner=200. The first filtered sample is 80 for top-only
	// ((200+2*40+40+2)>>2) or 110 for left-only ((200+2*80+80+2)>>2).
	// The remaining seven samples are unchanged, so DC is respectively
	// (80+7*40+4)>>3 = 45 or (110+7*80+4)>>3 = 84.
	for _, tc := range []struct {
		name    string
		interMB int
		want    uint8
	}{
		{"top-only-with-intra-corner", 3, 45},
		{"left-only-with-intra-corner", 1, 84},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, f := intraNeighborTestState()
			d.slice.pps.ConstrainedIntraPred = true
			d.picture.mbIsIntra[tc.interMB] = false
			mb := &syntax.MBIntra{}
			for i := range mb.I8x8PredMode {
				mb.I8x8PredMode[i] = pred.Intra4x4DC
			}
			d.reconstruct8x8(f, mb, 1, 1, 26)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					if got := f.PixelY(16+x, 16+y); got != tc.want {
						t.Fatalf("first luma 8x8 [%d,%d]=%d want %d", x, y, got, tc.want)
					}
				}
			}
		})
	}
}

func TestIntra4x4PredictedModeIgnoresUnavailableNeighbors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		blockedMB   int
		constrained bool
		want        int8
	}{
		{"both-intra", -1, false, 0},
		{"cross-slice-left", 3, false, 2},
		{"cross-slice-top", 1, false, 2},
		{"constrained-inter-left", 3, true, 2},
		{"constrained-inter-top", 1, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, f := intraNeighborTestState()
			d.intraModes[4*12+3] = 0
			d.intraModes[3*12+4] = 1
			if tc.blockedMB >= 0 {
				if tc.constrained {
					d.slice.pps.ConstrainedIntraPred = true
					d.picture.mbIsIntra[tc.blockedMB] = false
				} else {
					d.picture.mbSliceID[tc.blockedMB] = 0
				}
			}
			mb := &syntax.MBIntra{}
			for i := range mb.IntraPredMode {
				mb.IntraPredMode[i] = -1
			}
			d.reconstruct4x4(f, mb, 1, 1, 26)
			if got := d.traceIntra4x4PredMode[0]; got != tc.want {
				t.Fatalf("predicted mode=%d want %d", got, tc.want)
			}
		})
	}
}

func TestConstrainedIntraKeepsCurrentMacroblockDecodeOrder(t *testing.T) {
	d, f := intraNeighborTestState()
	d.slice.pps.ConstrainedIntraPred = true
	d.picture.mbIsIntra[3] = false
	d.intraModes[3*12+5] = 1
	mb := &syntax.MBIntra{}
	for i := range mb.IntraPredMode {
		mb.IntraPredMode[i] = -1
	}
	mb.IntraPredMode[0] = 0 // vertical, although the unavailable left forces predicted DC
	d.reconstruct4x4(f, mb, 1, 1, 26)
	if got := d.traceIntra4x4PredMode[1]; got != 0 {
		t.Fatalf("second block must use the first block's vertical mode, got %d", got)
	}
	if got := f.PixelY(20, 16); got != 40 {
		t.Fatalf("second block vertical sample=%d want 40", got)
	}

	// Block 3's top-right belongs to later block 4 in the same macroblock.
	// Same-MB availability must not bypass the 4x4 block decoding order.
	d, f = intraNeighborTestState()
	for y := 16; y < 32; y++ {
		for x := 16; x < 32; x++ {
			f.SetPixelY(x, y, 200)
		}
	}
	mb = &syntax.MBIntra{}
	for i := range mb.IntraPredMode {
		mb.IntraPredMode[i] = -1
	}
	mb.IntraPredMode[3] = 6 // vertical-left; the unavailable top-right extends 50
	d.reconstruct4x4(f, mb, 1, 1, 26)
	if got := f.PixelY(23, 23); got != 50 {
		t.Fatalf("block 3 read a not-yet-decoded top-right block: got %d want 50", got)
	}
}

func TestIntraTopRightSamplesRespectSliceAndConstrainedIntra(t *testing.T) {
	for _, blocked := range []string{"none", "slice", "inter"} {
		for _, size := range []int{4, 8} {
			d, f := intraNeighborTestState()
			for i := 0; i < 16; i++ {
				f.SetPixelY(32+i, 15, 200)
			}
			if blocked == "slice" {
				d.picture.mbSliceID[2] = 0
			} else if blocked == "inter" {
				d.slice.pps.ConstrainedIntraPred = true
				d.picture.mbIsIntra[2] = false
			}
			mb := &syntax.MBIntra{}
			var got uint8
			if size == 4 {
				for i := range mb.IntraPredMode {
					mb.IntraPredMode[i] = -1
				}
				mb.IntraPredMode[5] = 6 // rem mode 6 + predicted DC => vertical-left 7
				d.reconstruct4x4(f, mb, 1, 1, 26)
				got = f.PixelY(31, 19)
			} else {
				mb.I8x8PredMode = [4]int8{2, pred.Intra4x4DiagDownLeft, 2, 2}
				d.reconstruct8x8(f, mb, 1, 1, 26)
				got = f.PixelY(31, 23)
			}
			want := uint8(200)
			if blocked != "none" {
				want = 40 // unavailable top-right extends the final top sample
			}
			if got != want {
				t.Errorf("size=%d blocked=%s: got %d want %d", size, blocked, got, want)
			}
		}
	}
}

func TestIntra8x8FiltersEdgesWithoutUnavailableCorner(t *testing.T) {
	for _, blocked := range []string{"slice", "inter"} {
		for _, mode := range []int8{pred.Intra4x4Vertical, pred.Intra4x4Horizontal, pred.Intra4x4DC} {
			d, f := intraNeighborTestState()
			if blocked == "slice" {
				d.picture.mbSliceID[0] = 0
			} else {
				d.slice.pps.ConstrainedIntraPred = true
				d.picture.mbIsIntra[0] = false
			}
			mb := &syntax.MBIntra{I8x8PredMode: [4]int8{mode, 2, 2, 2}}
			d.reconstruct8x8(f, mb, 1, 1, 26)
			want := map[int8]uint8{pred.Intra4x4Vertical: 40, pred.Intra4x4Horizontal: 80, pred.Intra4x4DC: 60}[mode]
			if got := f.PixelY(16, 16); got != want {
				t.Errorf("blocked=%s mode=%d first filtered sample=%d want %d", blocked, mode, got, want)
			}
		}
	}
}

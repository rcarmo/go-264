package decode

import (
	"errors"
	"math"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func pocTestSPS(kind uint32) *nal.SPS {
	return &nal.SPS{PicOrderCntType: kind, Log2MaxFrameNum: 4, Log2MaxPocLsb: 4}
}

func TestPOCTypeZeroUsesPreviousReference(t *testing.T) {
	sps := pocTestSPS(0)
	var history pocHistory
	for i, tc := range []struct {
		lsb uint32
		ref bool
		poc int64
	}{
		{0, true, 0}, {6, true, 6}, {14, false, 14}, {2, true, 2},
		{8, true, 8}, {0, true, 16}, {4, false, 20}, {8, true, 24},
	} {
		o, err := derivePictureOrder(history, sps, &syntax.Header{FrameNum: uint32(i), PicOrderCntLsb: tc.lsb}, i == 0, tc.ref)
		if err != nil || o.top != tc.poc || o.bottom != tc.poc {
			t.Fatalf("picture %d: %+v, %v; want %d", i, o, err, tc.poc)
		}
		if !tc.ref && (o.next.refMSB != history.refMSB || o.next.refLSB != history.refLSB) {
			t.Fatal("non-reference picture changed type-0 predecessor")
		}
		history = o.next
	}
	// The two half-range tests are deliberately asymmetric (>= versus >).
	for _, tc := range []struct {
		previous, lsb, want int64
	}{
		{8, 0, 16}, {0, 8, 8}, {0, 9, -7}, {7, 0, 0},
	} {
		o, err := derivePictureOrder(pocHistory{refLSB: tc.previous}, sps, &syntax.Header{PicOrderCntLsb: uint32(tc.lsb)}, false, true)
		if err != nil || o.top != tc.want {
			t.Fatalf("previous %d, LSB %d: %+v, %v", tc.previous, tc.lsb, o, err)
		}
	}
}

func TestPOCTypeOneCyclesAndDeltas(t *testing.T) {
	sps := pocTestSPS(1)
	sps.NumRefFramesInPicOrderCntCycle = 3
	sps.OffsetForRefFrame = [255]int32{2, -1, 5}
	sps.OffsetForNonRefPic, sps.OffsetForTopToBottomField = -2, 1
	for _, tc := range []struct {
		name        string
		previous    pocHistory
		number      uint32
		ref         bool
		delta       [2]int32
		top, bottom int64
		offset      int64
	}{
		{"first cycle", pocHistory{}, 3, true, [2]int32{}, 6, 7, 0},
		{"next cycle", pocHistory{}, 4, true, [2]int32{}, 8, 9, 0},
		{"nonref and signed deltas", pocHistory{}, 4, false, [2]int32{3, -4}, 7, 4, 0},
		{"frame number wrap", pocHistory{frameNum: 15}, 0, true, [2]int32{}, 32, 33, 16},
		{"previous nonref already wrapped", pocHistory{frameNum: 0, frameNumOffset: 16}, 0, true, [2]int32{}, 32, 33, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := derivePictureOrder(tc.previous, sps, &syntax.Header{FrameNum: tc.number, DeltaPicOrderCnt: tc.delta}, false, tc.ref)
			if err != nil || o.top != tc.top || o.bottom != tc.bottom || o.next.frameNumOffset != tc.offset {
				t.Fatalf("got %+v, %v; want top/bottom %d/%d offset %d", o, err, tc.top, tc.bottom, tc.offset)
			}
		})
	}
	sps.NumRefFramesInPicOrderCntCycle = 0
	o, err := derivePictureOrder(pocHistory{frameNumOffset: 32}, sps, &syntax.Header{FrameNum: 9, DeltaPicOrderCnt: [2]int32{4, -3}}, false, false)
	if err != nil || o.top != 2 || o.bottom != 0 {
		t.Fatalf("empty cycle still applies nonref and slice offsets: %+v, %v", o, err)
	}
}

func TestPOCTypeTwoWrapAndNonReference(t *testing.T) {
	sps := pocTestSPS(2)
	var history pocHistory
	for number := 0; number < 50; number++ {
		h := &syntax.Header{FrameNum: uint32(number % 16)}
		if number > 0 {
			// The non-reference picture advances previous-picture history,
			// including the wrap at 15 -> 0. The reference must not wrap twice.
			o, err := derivePictureOrder(history, sps, h, false, false)
			if err != nil || o.top != int64(2*number-1) {
				t.Fatalf("nonref %d: %+v, %v", number, o, err)
			}
			history = o.next
		}
		o, err := derivePictureOrder(history, sps, h, number == 0, true)
		if err != nil || o.top != int64(2*number) || o.bottom != o.top {
			t.Fatalf("reference %d: %+v, %v", number, o, err)
		}
		history = o.next
	}
}

func TestPOCGapPicturesAdvanceHistoryWithoutPixels(t *testing.T) {
	for _, kind := range []uint32{0, 1, 2} {
		sps := pocTestSPS(kind)
		sps.NumRefFramesInPicOrderCntCycle = 1
		sps.OffsetForRefFrame[0] = 2
		d := NewDecoder()
		d.prevRefFrameNum, d.prevRefFrameNumValid = 15, true
		// A nonref frame_num0 has already crossed the wrap. Inferred0/1
		// follow it in decode order and must not cross the same wrap again.
		d.pocHistory = pocHistory{frameNum: 0, frameNumOffset: 16, refMSB: 16}
		before := d.pocHistory
		old := &frame.Frame{FrameNum: 13, POC: 26, FullPOC: 26, NonExisting: true}
		gap := &frame.Frame{FrameNum: 1, NonExisting: true}
		s := &sliceState{sps: sps, header: &syntax.Header{FrameNum: 2, PicOrderCntLsb: 2}, unit: nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1}}
		o, err := d.preparePicturePOC(s, []*frame.Frame{old, gap})
		if err != nil {
			t.Fatal(err)
		}
		if d.pocHistory != before || old.POC != 26 || len(gap.Y) != 0 {
			t.Fatal("preparing gaps mutated committed state, old metadata, or allocated pixels")
		}
		if kind == 0 {
			if gap.POC != 0 || o.top != 18 {
				t.Fatalf("type 0 gap changed POC: gap %+v, order %+v", gap, o)
			}
		} else if gap.POC != 34 || gap.FullPOC != 34 || o.top != 36 || o.next.frameNumOffset != 16 {
			t.Fatalf("type %d inferred progression: gap POC %d, order %+v", kind, gap.POC, o)
		}
	}
}

func TestPOCMaximumGapAndCycle(t *testing.T) {
	sps := pocTestSPS(1)
	sps.Log2MaxFrameNum = 16
	sps.NumRefFramesInPicOrderCntCycle = 255
	sps.GapsInFrameNumValueAllowedFlag = true
	for i := range sps.OffsetForRefFrame {
		sps.OffsetForRefFrame[i] = 2
	}
	d := NewDecoder()
	d.prevRefFrameNumValid = true
	gap := &frame.Frame{FrameNum: 65534, NonExisting: true}
	s := &sliceState{sps: sps, header: &syntax.Header{FrameNum: 65535}, unit: nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1}}
	o, err := d.preparePicturePOC(s, []*frame.Frame{gap})
	if err != nil || o.top != 131070 || o.bottom != 131070 || gap.FullPOC != 131068 || d.pocHistory != (pocHistory{}) {
		t.Fatalf("maximum inferred gap/cycle: order %+v, gap POC %d, error %v", o, gap.FullPOC, err)
	}
	standalone, err := derivePictureOrder(pocHistory{frameNum: 65534}, sps, s.header, false, true)
	if err != nil || standalone != o {
		t.Fatalf("shared cycle calculation differs: %+v, %v", standalone, err)
	}
}

func TestPOCRejectsOutOfRangeDerivedValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     uint32
		previous pocHistory
		header   syntax.Header
		idr      bool
	}{
		{"MSB positive wrap", 0, pocHistory{refMSB: math.MaxInt32 - 15, refLSB: 14}, syntax.Header{}, false},
		{"MSB negative wrap", 0, pocHistory{refMSB: math.MinInt32}, syntax.Header{PicOrderCntLsb: 15}, false},
		{"bottom addition", 0, pocHistory{refMSB: math.MaxInt32 - 15, refLSB: 14}, syntax.Header{PicOrderCntLsb: 15, DeltaPicOrderCntBottom: 1}, false},
		{"frame number offset", 2, pocHistory{frameNum: 15, frameNumOffset: math.MaxInt32 - 15}, syntax.Header{}, false},
		{"type two multiply", 2, pocHistory{frameNumOffset: 1 << 30}, syntax.Header{}, false},
		{"type one multiply", 1, pocHistory{}, syntax.Header{FrameNum: 2}, false},
		{"IDR nonzero min", 0, pocHistory{}, syntax.Header{PicOrderCntLsb: 2}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sps := pocTestSPS(tc.kind)
			sps.NumRefFramesInPicOrderCntCycle = 1
			sps.OffsetForRefFrame[0] = math.MaxInt32
			_, err := derivePictureOrder(tc.previous, sps, &tc.header, tc.idr, true)
			if !errors.Is(err, nal.ErrInvalidSyntax) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

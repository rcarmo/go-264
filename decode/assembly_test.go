package decode

import (
	"strings"
	"testing"

	"github.com/rcarmo/go-264/filter"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

type assemblyBits struct{ bits []byte }

func (w *assemblyBits) bit(b uint32) { w.bits = append(w.bits, byte(b&1)) }
func (w *assemblyBits) uint(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		w.bit(v >> i)
	}
}
func (w *assemblyBits) ue(v uint32) {
	n := 0
	for x := v + 1; x > 1; x >>= 1 {
		n++
	}
	for i := 0; i < n; i++ {
		w.bit(0)
	}
	w.uint(v+1, n+1)
}
func (w *assemblyBits) se(v int32) {
	if v > 0 {
		w.ue(uint32(2*v - 1))
	} else {
		w.ue(uint32(-2 * v))
	}
}
func (w *assemblyBits) align() {
	for len(w.bits)%8 != 0 {
		w.bit(0)
	}
}
func (w *assemblyBits) bytes() []byte {
	out := make([]byte, len(w.bits)/8)
	for i, b := range w.bits {
		out[i/8] |= b << uint(7-i%8)
	}
	return out
}

func assemblyDecoder(widthMB, heightMB int) *Decoder {
	d := NewDecoder()
	d.SPS[0] = &nal.SPS{ProfileIDC: 66, ChromaFormatIDC: 1, BitDepthLuma: 8, BitDepthChroma: 8,
		FrameMbsOnlyFlag: true, Log2MaxFrameNum: 4, PicOrderCntType: 2, MaxNumRefFrames: 4,
		PicWidthInMbs: uint32(widthMB), PicHeightInMapUnits: uint32(heightMB), Width: widthMB * 16, Height: heightMB * 16}
	d.PPS[0] = &nal.PPS{NumSliceGroups: 1, PicInitQP: 26, NumRefIdxL0Active: 1, NumRefIdxL1Active: 1, DeblockingFilterControl: true}
	return d
}

func pcmAssemblySlice(first int, values ...byte) nal.Unit {
	return numberedPCMAssemblySlice(first, 0, values...)
}

// Picture zero is IDR; subsequent pictures are reference I pictures.
func numberedPCMAssemblySlice(first int, number uint32, values ...byte) nal.Unit {
	return pcmAssemblySliceWithPOC(first, number, -1, values...)
}

// A nonnegative POC adds a four-bit type-0 pic_order_cnt_lsb; -1 uses type 2.
func pcmAssemblySliceWithPOC(first int, number uint32, poc int, values ...byte) nal.Unit {
	w := &assemblyBits{}
	w.ue(uint32(first))
	w.ue(syntax.SliceTypeI)
	w.ue(0)
	w.uint(number, 4)
	typ := uint8(nal.TypeSliceNonIDR)
	if number == 0 {
		typ = nal.TypeSliceIDR
		w.ue(0) // idr_pic_id
	}
	if poc >= 0 {
		w.uint(uint32(poc), 4)
	}
	if number == 0 {
		w.bit(0) // no_output_of_prior_pics_flag
	}
	w.bit(0) // long_term_reference_flag / adaptive_ref_pic_marking_mode_flag
	w.ue(0)
	w.ue(1) // QP delta 0, filter disabled
	for _, value := range values {
		w.ue(25)
		w.align()
		for i := 0; i < 384; i++ {
			w.uint(uint32(value), 8)
		}
	}
	w.bit(1)
	w.align()
	return nal.Unit{Type: typ, RefIDC: 3, Payload: w.bytes()}
}

func assemblyInput(units ...nal.Unit) []byte {
	var out []byte
	for _, u := range units {
		out = appendTestNAL(out, u)
	}
	return out
}

func prefixAssemblyInput(withPrefixes bool) []byte {
	var units []nal.Unit
	for number, values := range [][2]byte{{81, 149}, {97, 173}} {
		for first, value := range values {
			slice := numberedPCMAssemblySlice(first, uint32(number), value)
			if withPrefixes {
				// F.7.3.1.1/F.7.3.2.12.1: SVC extension, no inter-layer
				// prediction, layer/temporal IDs zero, output enabled,
				// reserved bits 3, no base-picture storage or extension data.
				payload := []byte{0x80, 0x80, 0x07, 0x20}
				if slice.Type == nal.TypeSliceIDR {
					payload[0] |= 0x40 // idr_flag matches the associated slice
				}
				units = append(units, nal.Unit{Type: 14, RefIDC: slice.RefIDC, Payload: payload})
			}
			units = append(units, slice)
		}
	}
	return assemblyInput(units...) // no AUD between the two primary pictures
}

func checkPrefixPictures(t *testing.T, frames []*frame.Frame) {
	t.Helper()
	if len(frames) != 2 {
		t.Fatalf("got %d pictures, want 2", len(frames))
	}
	for i, values := range [][2]byte{{81, 149}, {97, 173}} {
		if frames[i].FrameNum != i || frames[i].PixelY(0, 0) != values[0] || frames[i].PixelY(16, 0) != values[1] {
			t.Fatalf("picture %d lost its identity or slice samples", i)
		}
	}
}

func TestMultiSliceSVCPrefixes(t *testing.T) {
	for _, name := range []string{"without prefixes", "with prefixes"} {
		t.Run(name, func(t *testing.T) {
			frames, err := assemblyDecoder(2, 1).Decode(prefixAssemblyInput(name == "with prefixes"))
			if err != nil {
				t.Fatal(err)
			}
			checkPrefixPictures(t, frames)
		})
	}
}

func TestMultiSlicePublishesOneCompletePicture(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		d := assemblyDecoder(2, 1)
		units := []nal.Unit{pcmAssemblySlice(0, 81), pcmAssemblySlice(1, 149)}
		if reverse {
			units[0], units[1] = units[1], units[0]
		}
		frames, err := d.Decode(assemblyInput(units...))
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 || len(d.Frames) != 1 || len(d.DPB.Frames) != 1 {
			t.Fatalf("published per slice: frames=%d history=%d DPB=%d", len(frames), len(d.Frames), len(d.DPB.Frames))
		}
		if frames[0].PixelY(0, 0) != 81 || frames[0].PixelY(16, 0) != 149 {
			t.Fatal("one slice overwrote or lost another's samples")
		}
	}
}

func TestMultiSlicePMotionAndReferenceOwnership(t *testing.T) {
	d := assemblyDecoder(2, 1)
	for number := 0; number < 2; number++ {
		ref := frame.NewFrame(32, 16)
		ref.FrameNum, ref.POC, ref.FullPOC = number, number*2, number*2
		ref.IsRef = true
		for y := 0; y < 16; y++ {
			for x := 0; x < 32; x++ {
				ref.SetPixelY(x, y, byte(number*80+x))
			}
		}
		d.DPB.Add(ref)
	}
	makeP := func(first uint32, older bool, mvdX int32) nal.Unit {
		w := &assemblyBits{}
		w.ue(first)
		w.ue(syntax.SliceTypeP)
		w.ue(0)
		w.uint(2, 4) // frame_num
		w.bit(0)     // one active reference, from PPS
		if older {
			w.bit(1)
			w.ue(0)
			w.ue(1) // select frame_num 0 instead of the default frame_num 1
			w.ue(3)
		} else {
			w.bit(0) // default List 0
		}
		w.bit(0) // no adaptive reference marking
		w.se(0)
		w.ue(1) // QP delta zero; filtering disabled
		w.ue(0)
		w.ue(0) // no skip run; P_L0_16x16
		w.se(mvdX)
		w.se(0)
		w.ue(0) // no residual
		w.bit(1)
		w.align()
		return nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 3, Payload: w.bytes()}
	}
	// Slice 1's ref_idx 0 names a different picture than slice 0's. Its zero
	// MVD must not inherit slice 0's one-pixel motion across the slice boundary.
	frames, err := d.Decode(assemblyInput(makeP(0, false, 4), makeP(1, true, 0)))
	if err != nil || len(frames) != 1 {
		t.Fatalf("P picture: frames=%d err=%v", len(frames), err)
	}
	f := frames[0]
	if f.PixelY(0, 0) != 81 || f.PixelY(16, 0) != 16 {
		t.Errorf("slice prediction samples=(%d,%d), want (81,16)", f.PixelY(0, 0), f.PixelY(16, 0))
	}
	if f.MotionL0[0] != [2]int16{4, 0} || f.MotionL0[4] != [2]int16{} {
		t.Errorf("saved motion=(%v,%v), want (4,0),(0,0)", f.MotionL0[0], f.MotionL0[4])
	}
	if f.RefIdxL0[0] != 0 || f.RefIdxL0[4] != 0 {
		t.Errorf("raw slice reference indices=(%d,%d), want (0,0)", f.RefIdxL0[0], f.RefIdxL0[4])
	}
	if len(f.RefListL0Num) != 2 || f.RefListL0Num[0] != 1 || f.RefListL0Num[1] != 0 || f.TemporalRefIdxL0[0] != 0 || f.TemporalRefIdxL0[4] != 1 {
		t.Errorf("saved List 0 numbers=%v temporal indices=(%d,%d), want [1,0] and (0,1)", f.RefListL0Num, f.TemporalRefIdxL0[0], f.TemporalRefIdxL0[4])
	}
}

func TestMultiSliceFailureDoesNotPublishOrFlush(t *testing.T) {
	idr := func(first int, values ...byte) nal.Unit {
		return pcmAssemblySliceWithPOC(first, 0, 0, values...)
	}
	for _, tt := range []struct {
		name  string
		units []nal.Unit
		want  string
	}{
		{"missing tail", []nal.Unit{idr(0, 81)}, "incomplete picture"},
		{"missing head", []nal.Unit{idr(1, 81)}, "incomplete picture"},
		{"duplicate", []nal.Unit{idr(0, 81), idr(0, 81)}, "overlapping"},
		{"overlap after completion", []nal.Unit{idr(0, 81, 149), idr(1, 81)}, "overlapping"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := assemblyDecoder(2, 1)
			d.SPS[0].PicOrderCntType, d.SPS[0].Log2MaxPocLsb = 0, 4
			// Establish real nonzero reference POC history before an IDR resets
			// it provisionally. A failed IDR must restore that history, not zeroes.
			if _, err := d.Decode(assemblyInput(idr(0, 90, 110), pcmAssemblySliceWithPOC(0, 1, 6, 91, 111))); err != nil {
				t.Fatal(err)
			}
			if len(d.Frames) != 2 || d.Frames[1].FullPOC != 6 {
				t.Fatal("fixture did not establish nonzero POC history")
			}
			good := append([]*frame.Frame(nil), d.DPB.Frames...)
			before := d.pocState()
			frames, err := d.Decode(assemblyInput(tt.units...))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if len(frames) != 0 || len(d.Frames) != 2 || len(d.DPB.Frames) != len(good) {
				t.Fatal("failed IDR published or flushed references")
			}
			for i, ref := range good {
				if d.DPB.Frames[i] != ref {
					t.Fatal("failed IDR replaced a reference")
				}
			}
			if d.picture != nil || d.pocState() != before {
				t.Fatal("failed picture retained reconstruction/POC state")
			}
		})
	}
}

func TestMultiSliceRejectsParameterReplacementWithinPicture(t *testing.T) {
	d := assemblyDecoder(2, 1)
	d.TraceMB = func(MBTraceEvent) { d.PPS[0].PicInitQP++ }
	_, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 81), pcmAssemblySlice(1, 149)))
	if err == nil || !strings.Contains(err.Error(), "parameter sets changed") {
		t.Fatalf("got %v", err)
	}
	if len(d.DPB.Frames) != 0 {
		t.Fatal("mixed parameter sets entered DPB")
	}
}

func TestMultiSliceMaxFramesStopsAfterCompletePicture(t *testing.T) {
	d := assemblyDecoder(2, 1)
	d.MaxFrames = 1
	// The next slice is malformed: the limit must stop before parsing it,
	// but only after both slices of the requested picture were reconstructed.
	frames, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 81), pcmAssemblySlice(1, 149), nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1, Payload: []byte{0}}))
	if err != nil || len(frames) != 1 {
		t.Fatalf("complete-picture limit: frames=%d err=%v", len(frames), err)
	}
	if frames[0].PixelY(0, 0) != 81 || frames[0].PixelY(16, 0) != 149 {
		t.Fatal("MaxFrames stopped before the picture's final macroblock")
	}
}

func TestDeblockingRespectsSliceBoundaryControl(t *testing.T) {
	t.Setenv("GO264_DISABLE_DEBLOCK", "")
	for _, tc := range []struct {
		name              string
		leftIDC, rightIDC int32
	}{
		{"enabled", 0, 0},
		{"disabled", 1, 1},
		{"within-slice-only", 2, 2},
		{"left-disabled", 1, 0},
		{"right-disabled", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := assemblyDecoder(2, 1)
			left := &sliceState{sps: d.SPS[0], pps: d.PPS[0], header: &syntax.Header{DisableDeblocking: tc.leftIDC}}
			right := &sliceState{sps: d.SPS[0], pps: d.PPS[0], header: &syntax.Header{DisableDeblocking: tc.rightIDC}}
			p := d.newPicture(left)
			d.picture = p
			d.mbW, d.mbH = 2, 1
			p.slices = []*sliceState{left, right}
			p.mbSliceID = []int{0, 1}
			p.decoded = 2
			p.deblock = []filter.MBDeblockInfo{{QP: 36, IsIntra: true}, {QP: 36, IsIntra: true}}
			for y := 0; y < 16; y++ {
				for x := 0; x < 32; x++ {
					v := byte(100)
					if x < 8 {
						v = 92
					} else if x >= 16 {
						v = 108
					}
					p.frame.SetPixelY(x, y, v)
				}
			}
			f, err := d.finishPicture()
			if err != nil {
				t.Fatal(err)
			}
			// The internal edge belongs to the left slice; the cross-slice
			// edge is controlled by the right slice, not the first/last header.
			internalChanged := f.PixelY(7, 0) != 92 || f.PixelY(8, 0) != 100
			crossChanged := f.PixelY(15, 0) != 100 || f.PixelY(16, 0) != 108
			if internalChanged != (tc.leftIDC != 1) || crossChanged != (tc.rightIDC == 0) {
				t.Fatalf("IDC=(%d,%d): internal changed=%v, cross-slice changed=%v", tc.leftIDC, tc.rightIDC, internalChanged, crossChanged)
			}
		})
	}
}

func TestDeblockReferenceIdentityIsNotPOC(t *testing.T) {
	d := assemblyDecoder(1, 1)
	s := &sliceState{sps: d.SPS[0], pps: d.PPS[0], header: &syntax.Header{SliceType: syntax.SliceTypeP}}
	p := d.newPicture(s)
	d.picture, d.slice = p, s
	d.mbW = 1
	a, b := frame.NewFrame(16, 16), frame.NewFrame(16, 16)
	a.IsRef, b.IsRef = true, true
	// POC type2 currently has zero POC metadata; they are still distinct refs.
	d.activeL0Refs = []*frame.Frame{a, b}
	d.DPB.Frames = []*frame.Frame{a, b}
	for i := range p.motion.ref[0] {
		p.motion.ref[0][i] = int8(i % 2)
	}
	d.saveSlice(s, 0, 1)
	if p.deblock[0].RefIDL0[0] == p.deblock[0].RefIDL0[1] {
		t.Fatal("distinct pictures collapsed by equal POC")
	}
}

func TestMultiSliceCannotBridgeAccessUnitDelimiter(t *testing.T) {
	for _, kind := range []uint8{nal.TypeSEI, nal.TypeSPS, nal.TypePPS, nal.TypeAUD, 10, 11} {
		d := assemblyDecoder(2, 1)
		delimiter := nal.Unit{Type: kind, Payload: []byte{0x80}}
		if kind == nal.TypeSPS || kind == nal.TypePPS {
			delimiter.RefIDC = 3
		}
		_, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 81), delimiter, pcmAssemblySlice(1, 149)))
		if err == nil || !strings.Contains(err.Error(), "incomplete picture") || len(d.DPB.Frames) != 0 {
			t.Fatalf("delimiter %d stitched incomplete pictures: %v", kind, err)
		}
	}
}

// spatialIndexWireInput is a complete 32x16 Main-profile Annex-B stream.
// Decode order is PCM I(POC 0), PCM I(POC 2), two-slice P(POC 6), B(POC 4).
// P slice 0 uses [frame1, frame0]; slice 1 uses [frame0, frame1]. Consequently
// slice 1's local index 0 maps to saved union index 1, and local 1 maps to union 0.
//
// The B picture's left MB is B_L0_16x16 with MV(+8,0), and its right MB is B-skip.
// The right MB inherits that nonzero MVP, but colZeroFlag must zero it exactly
// when the co-located P MB used slice-local ref_idx_l0 0, not union index 0.
func spatialIndexWireInput(localRefOne bool) []byte {
	// Main, POC type 0, 4-bit frame_num/POC, four reference frames, no crop;
	// PPS 0 is CAVLC, PPS 1 CABAC, both unweighted with one active ref per list.
	units := []nal.Unit{
		{Type: nal.TypeSPS, RefIDC: 3, Payload: []byte{0x4d, 0x00, 0x0a, 0xf2, 0x97, 0x20}},
		{Type: nal.TypePPS, RefIDC: 3, Payload: []byte{0xce, 0x3c, 0x80}},
		{Type: nal.TypePPS, RefIDC: 3, Payload: []byte{0x5b, 0x8f, 0x20}},
	}
	for number := uint32(0); number < 2; number++ {
		w := &assemblyBits{}
		w.ue(0) // first_mb_in_slice
		w.ue(syntax.SliceTypeI)
		w.ue(0)           // pic_parameter_set_id: CAVLC
		w.uint(number, 4) // frame_num
		kind := uint8(nal.TypeSliceNonIDR)
		if number == 0 {
			kind = nal.TypeSliceIDR
			w.ue(0) // idr_pic_id
		}
		w.uint(number*2, 4) // pic_order_cnt_lsb
		if number == 0 {
			w.bit(0) // no_output_of_prior_pics_flag
		}
		w.bit(0) // long_term_reference_flag / adaptive_ref_pic_marking_mode_flag
		w.ue(0)  // slice_qp_delta = 0, encoded as se(v)
		w.ue(1)  // disable_deblocking_filter_idc
		for mbx := 0; mbx < 2; mbx++ {
			w.ue(25) // I_PCM
			w.align()
			for y := 0; y < 16; y++ {
				for x := 0; x < 16; x++ {
					w.uint(40+number*20+uint32((mbx*16+x)*3+y), 8)
				}
			}
			for i := 0; i < 64; i++ {
				w.uint(90+number*10, 8)
			}
			for i := 0; i < 64; i++ {
				w.uint(130+number*10, 8)
			}
		}
		w.bit(1) // rbsp_stop_one_bit
		w.align()
		units = append(units, nal.Unit{Type: kind, RefIDC: 3, Payload: w.bytes()})
	}
	for first := uint32(0); first < 2; first++ {
		w := &assemblyBits{}
		w.ue(first) // first_mb_in_slice
		w.ue(syntax.SliceTypeP)
		w.ue(0)      // pic_parameter_set_id: CAVLC
		w.uint(2, 4) // frame_num
		w.uint(6, 4) // pic_order_cnt_lsb
		explicit := first == 1 && localRefOne
		if explicit {
			w.bit(1) // num_ref_idx_active_override_flag
			w.ue(1)  // num_ref_idx_l0_active_minus1
		} else {
			w.bit(0) // use the PPS default of one active List 0 reference
		}
		if first == 1 {
			w.bit(1) // ref_pic_list_modification_flag_l0
			w.ue(0)  // modification_of_pic_nums_idc: subtract from current PicNum
			w.ue(1)  // abs_diff_pic_num_minus1: PicNum 2 - 2 = 0 becomes ref0
			w.ue(3)  // end of reference-list modifications
		} else {
			w.bit(0) // use default List 0: frame 1, then frame 0
		}
		w.bit(0) // adaptive_ref_pic_marking_mode_flag: sliding-window marking
		w.ue(0)  // slice_qp_delta = 0
		w.ue(1)  // disable_deblocking_filter_idc
		if explicit {
			// No skips; P_L0_16x16, ref_idx_l0=1 (te(v) bit 0), zero MVD/CBP.
			w.ue(0)  // mb_skip_run
			w.ue(0)  // mb_type: P_L0_16x16
			w.bit(0) // ref_idx_l0 = 1 in the two-reference te(v) coding
			w.ue(0)  // mvd_l0[0] = 0
			w.ue(0)  // mvd_l0[1] = 0
			w.ue(0)  // coded_block_pattern = 0
		} else {
			w.ue(1) // one P-skip: local ref0 and zero motion
		}
		w.bit(1) // rbsp_stop_one_bit
		w.align()
		units = append(units, nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 3, Payload: w.bytes()})
	}
	// B header: PPS1, frame_num=3, POC=4, spatial direct, default refs,
	// cabac_init_idc=0, QP=26, disabled deblocking. Arithmetic payload encodes
	// B_L0_16x16/ref0/MVD(+8,0)/CBP0/end0 followed by B-skip/end1.
	// Independent interval synthesis and FFmpeg 9.0.1 decoding verified these
	// four CABAC bytes. The final two bytes are cabac_zero_word.
	units = append(units, nal.Unit{Type: nal.TypeSliceNonIDR, Payload: []byte{0xa4, 0x69, 0x1a, 0xef, 0xbc, 0xc8, 0x38, 0x00, 0x00}})
	return assemblyInput(units...)
}

func TestSpatialDirectUsesSliceLocalIndexWire(t *testing.T) {
	t.Setenv("GO264_DISABLE_DEBLOCK", "")
	for _, tc := range []struct {
		name        string
		localRefOne bool
	}{
		{"local0_to_union1", false}, {"local1_to_union0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := NewDecoder().Decode(spatialIndexWireInput(tc.localRefOne))
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != 4 {
				t.Fatalf("got %d frames, want 4", len(frames))
			}
			seen := 0
			for _, f := range frames {
				switch f.POC {
				case 0, 2, 4, 6:
					seen |= 1 << uint(f.POC/2)
				default:
					t.Fatalf("unexpected POC %d", f.POC)
				}
				for y := 0; y < 16; y++ {
					for x := 0; x < 32; x++ {
						base, sourceX := 40, x
						switch f.POC {
						case 2:
							base = 60
						case 4:
							base = 60
							// Eight quarter-pixels shift luma by two integer samples.
							// Only co-located local ref0 should zero the right MB's MV.
							if x < 16 || tc.localRefOne {
								sourceX = min(x+2, 31)
							}
						case 6:
							if x < 16 || tc.localRefOne {
								base = 60
							}
						}
						want := uint8(base + sourceX*3 + y)
						if got := f.PixelY(x, y); got != want {
							t.Fatalf("POC %d luma(%d,%d)=%d, want %d", f.POC, x, y, got, want)
						}
					}
				}
				for y := 0; y < 8; y++ {
					for x := 0; x < 16; x++ {
						inc := 0
						if f.POC == 2 || f.POC == 4 || (f.POC == 6 && (x < 8 || tc.localRefOne)) {
							inc = 10
						}
						if u, v := f.PixelU(x, y), f.PixelV(x, y); u != uint8(90+inc) || v != uint8(130+inc) {
							t.Fatalf("POC %d chroma(%d,%d)=%d/%d", f.POC, x, y, u, v)
						}
					}
				}
			}
			if seen != 15 {
				t.Fatalf("missing POC: mask %04b", seen)
			}
		})
	}
}

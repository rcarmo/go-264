package decode

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func referenceSkipSlice(number uint32, mods []syntax.RefPicListModification, mmco []syntax.MemoryManagementControl) nal.Unit {
	w := &assemblyBits{}
	w.ue(0)
	w.ue(syntax.SliceTypeP)
	w.ue(0)
	w.uint(number, 4)
	w.bit(0) // default one active reference
	if len(mods) > 0 {
		w.bit(1)
		for _, m := range mods {
			w.ue(m.Op)
			w.ue(m.Val)
		}
		w.ue(3)
	} else {
		w.bit(0)
	}
	if len(mmco) > 0 {
		w.bit(1)
		for _, m := range mmco {
			w.ue(m.Op)
			switch m.Op {
			case 1, 3:
				w.ue(m.DifferenceOfPicNumsMinus1)
			}
			switch m.Op {
			case 2:
				w.ue(m.LongTermPicNum)
			case 3, 6:
				w.ue(m.LongTermFrameIdx)
			case 4:
				w.ue(m.MaxLongTermFrameIdxPlus1)
			}
		}
		w.ue(0)
	} else {
		w.bit(0)
	}
	w.ue(0)
	w.ue(1)
	w.ue(1) // QP delta0, filter off, one skipped MB
	w.bit(1)
	w.align()
	return nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1, Payload: w.bytes()}
}

func primedReferenceDecoder(t *testing.T, gaps bool) *Decoder {
	t.Helper()
	d := assemblyDecoder(1, 1)
	d.SPS[0].GapsInFrameNumValueAllowedFlag = gaps
	if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLegalGapRetainsMetadataButNeverOutputsInferredPicture(t *testing.T) {
	for _, tt := range []struct {
		name string
		mmco []syntax.MemoryManagementControl
	}{
		{"retain gap", nil},
		// MMCO1 may remove a non-existing picture even though MMCO3 cannot
		// promote one to long-term reference (8.2.5.2).
		{"remove gap", []syntax.MemoryManagementControl{{Op: 1}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := primedReferenceDecoder(t, true)
			good := d.DPB.Frames[0]
			// frame_num1 is inferred; explicitly select actual frame_num0, not gap1.
			frames, err := d.Decode(assemblyInput(referenceSkipSlice(2, []syntax.RefPicListModification{{Op: 0, Val: 1}}, tt.mmco)))
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != 1 || len(d.Frames) != 2 || frames[0].PixelY(0, 0) != 91 {
				t.Fatal("gap produced pixels/output or changed prediction")
			}
			if len(tt.mmco) == 0 {
				if len(d.DPB.Frames) != 3 || !d.DPB.Frames[1].NonExisting || len(d.DPB.Frames[1].Y) != 0 || d.DPB.Frames[1].FrameNum != 1 {
					t.Fatal("gap reference metadata missing or owns pixels")
				}
			} else if len(d.DPB.Frames) != 2 || d.DPB.Frames[1].FrameNum != 2 || d.DPB.Frames[1].NonExisting {
				t.Fatal("MMCO1 did not remove the inferred picture")
			}
			if d.DPB.Frames[0] != good || !d.prevRefFrameNumValid || d.prevRefFrameNum != 2 {
				t.Fatal("real reference changed or reference progression not committed")
			}
		})
	}
}

func TestInvalidReferencesDoNotChangeCommittedState(t *testing.T) {
	for _, tt := range []struct {
		name string
		gap  bool
		unit nal.Unit
		want string
	}{
		{"unannounced gap", false, referenceSkipSlice(2, nil, nil), "unannounced frame_num gap"},
		{"sampling inferred reference", true, referenceSkipSlice(2, nil, nil), "non-existing frame_num"},
		{"modifying to inferred reference", true, referenceSkipSlice(2, []syntax.RefPicListModification{{Op: 0, Val: 0}}, nil), "non-existing frame_num"},
		{"missing modification", false, referenceSkipSlice(1, []syntax.RefPicListModification{{Op: 0, Val: 2}}, nil), "missing frame_num"},
		{"missing MMCO target", false, referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 1, DifferenceOfPicNumsMinus1: 2}}), "MMCO1 refers to missing"},
		{"MMCO fails after staged removal", false, referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 1}, {Op: 1}}), "MMCO1 refers to missing"},
		// Prediction uses the real frame0; only the later marking command
		// targets inferred frame1, after MMCO4 tentatively enables index0.
		{"promoting inferred reference", true, referenceSkipSlice(2, []syntax.RefPicListModification{{Op: 0, Val: 1}}, []syntax.MemoryManagementControl{{Op: 4, MaxLongTermFrameIdxPlus1: 1}, {Op: 3}}), "MMCO3 refers to non-existing frame_num"},
		{"repeated reference number", false, referenceSkipSlice(0, nil, nil), "repeats previous reference"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := primedReferenceDecoder(t, tt.gap)
			good := d.DPB.Frames[0]
			before := d.pocState()
			beforeMaxLongTerm := d.maxLongTermFrameIdx
			frames, err := d.Decode(assemblyInput(tt.unit))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if len(frames) != 0 || len(d.Frames) != 1 || len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != good || d.prevRefFrameNum != 0 || d.pocState() != before || d.maxLongTermFrameIdx != beforeMaxLongTerm {
				t.Fatal("failed picture mutated committed reference/output/POC state")
			}
		})
	}
}

func TestAdaptiveShortTermRemovalCommitsAfterCompletePicture(t *testing.T) {
	d := primedReferenceDecoder(t, false)
	frames, err := d.Decode(assemblyInput(referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 1}})))
	if err != nil || len(frames) != 1 {
		t.Fatalf("valid MMCO1: %v", err)
	}
	if len(d.DPB.Frames) != 1 || d.DPB.Frames[0].FrameNum != 1 || d.prevRefFrameNum != 1 {
		t.Fatal("MMCO1 did not replace reference")
	}
	// A later IDR resets both the stored reference set and number progression.
	if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 121))); err != nil {
		t.Fatal(err)
	}
	if len(d.DPB.Frames) != 1 || d.prevRefFrameNum != 0 || d.DPB.Frames[0].PixelY(0, 0) != 121 {
		t.Fatal("IDR retained prior references/progression")
	}
}

func TestLaterSliceCannotBypassReferenceGates(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*Decoder, *sliceState)
		want  string
	}{
		{"P with zero SPS references", func(d *Decoder, s *sliceState) {
			d.picture.sps.MaxNumRefFrames = 0
			s.sps.MaxNumRefFrames = 0
			s.header.SliceType = syntax.SliceTypeP
		}, "max_num_ref_frames"},
		{"B with long-term reference", func(d *Decoder, s *sliceState) {
			d.picture.referenceFrames = []*frame.Frame{{IsRef: true, IsLongTerm: true}}
			s.header.SliceType = syntax.SliceTypeB
		}, "B pictures with long-term"},
		{"B across inferred gaps", func(d *Decoder, s *sliceState) {
			d.picture.referenceFrames = []*frame.Frame{{IsRef: true, NonExisting: true}}
			s.header.SliceType = syntax.SliceTypeB
		}, "B pictures across"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := assemblyDecoder(2, 1)
			first, err := d.parseSlice(pcmAssemblySlice(0, 91))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.addSlice(first); err != nil {
				t.Fatal(err)
			}
			later, err := d.parseSlice(pcmAssemblySlice(1, 121))
			if err != nil {
				t.Fatal(err)
			}
			tt.setup(d, later)
			if err := d.addSlice(later); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("later slice bypassed gate: %v", err)
			}
		})
	}
}

func TestSPSReferenceLayoutChangeRequiresIDR(t *testing.T) {
	for _, change := range []func(*nal.SPS){
		func(s *nal.SPS) { s.Log2MaxFrameNum = 5 },
		func(s *nal.SPS) { s.MaxNumRefFrames = 1 },
	} {
		d := primedReferenceDecoder(t, false)
		prior := d.DPB.Frames[0]
		next := *d.SPS[0]
		change(&next)
		d.SPS[0] = &next
		unit := referenceSkipSlice(1, nil, nil)
		// Admission receives already parsed fields; exercise the cross-picture
		// state check independently of the changed frame_num header bit width.
		s := &sliceState{unit: unit, sps: &next, pps: d.PPS[0], header: &syntax.Header{SliceType: syntax.SliceTypeP, FrameNum: 1}}
		if _, _, _, err := d.preparePictureReferences(s); err == nil || !strings.Contains(err.Error(), "SPS changed") {
			t.Fatalf("layout changed without reset: %v", err)
		}
		if len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != prior {
			t.Fatal("rejected SPS changed reference storage")
		}
		s.unit.Type = nal.TypeSliceIDR
		s.header.FrameNum = 0
		if _, _, _, err := d.preparePictureReferences(s); err != nil {
			t.Fatalf("IDR did not permit SPS replacement: %v", err)
		}
	}
}

func TestOrderedLongTermReferenceMarking(t *testing.T) {
	short := shortRefs(0, 1, 2)
	short[0].Y = []byte{71}
	long := &frame.Frame{IsRef: true, IsLongTerm: true, LongTermFrameIdx: 1, FrameNum: 30, Y: []byte{93}}
	current := &frame.Frame{FrameNum: 3, IsRef: true}
	for _, tt := range []struct {
		name           string
		ops            []syntax.MemoryManagementControl
		wantShort      []int
		wantLong       map[int]int // index -> original frame_num
		wantMax        int
		wantCurrentNum int
	}{
		{"promote replaces long", []syntax.MemoryManagementControl{{Op: 3, DifferenceOfPicNumsMinus1: 2, LongTermFrameIdx: 1}}, []int{1, 2, 3}, map[int]int{1: 0}, 2, 3},
		{"promote then remove", []syntax.MemoryManagementControl{{Op: 3, DifferenceOfPicNumsMinus1: 2, LongTermFrameIdx: 1}, {Op: 2, LongTermPicNum: 1}}, []int{1, 2, 3}, nil, 2, 3},
		{"remove then current replaces long", []syntax.MemoryManagementControl{{Op: 1}, {Op: 6, LongTermFrameIdx: 1}}, []int{0, 1}, map[int]int{1: 3}, 2, 3},
		{"shrink maximum", []syntax.MemoryManagementControl{{Op: 4, MaxLongTermFrameIdxPlus1: 1}}, []int{0, 1, 2, 3}, nil, 0, 3},
		{"disable long term", []syntax.MemoryManagementControl{{Op: 4}}, []int{0, 1, 2, 3}, nil, -1, 3},
		{"reset", []syntax.MemoryManagementControl{{Op: 5}}, []int{0}, nil, -1, 0},
		{"reset then enable and mark", []syntax.MemoryManagementControl{{Op: 5}, {Op: 4, MaxLongTermFrameIdxPlus1: 2}, {Op: 6, LongTermFrameIdx: 1}}, nil, map[int]int{1: 0}, 1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			previous := append(append([]*frame.Frame(nil), short...), long)
			h := &syntax.Header{FrameNum: 3, AdaptiveRefPicMarking: true, MemoryManagementControls: tt.ops}
			refs, marked, maxLT, err := stageReferenceMarking(previous, current, h, 32, 4, 2)
			if err != nil {
				t.Fatal(err)
			}
			if maxLT != tt.wantMax || marked.FrameNum != tt.wantCurrentNum || refs[len(refs)-1] != marked {
				t.Fatalf("result maxLT=%d current=%+v", maxLT, marked)
			}
			var gotShort []int
			gotLong := make(map[int]int)
			for _, ref := range refs {
				if ref.IsLongTerm {
					gotLong[ref.LongTermFrameIdx] = ref.FrameNum
				} else {
					gotShort = append(gotShort, ref.FrameNum)
				}
			}
			if fmt.Sprint(gotShort) != fmt.Sprint(tt.wantShort) || len(gotLong) != len(tt.wantLong) {
				t.Fatalf("short=%v long=%v", gotShort, gotLong)
			}
			for index, num := range tt.wantLong {
				if got, ok := gotLong[index]; !ok || got != num {
					t.Fatalf("long[%d]=%d,%v want %d", index, got, ok, num)
				}
			}
			if short[0].IsLongTerm || short[0].Y[0] != 71 || !long.IsLongTerm || long.LongTermFrameIdx != 1 || long.Y[0] != 93 || current.FrameNum != 3 || current.IsLongTerm {
				t.Fatal("marking changed prior Frame metadata or samples")
			}
			if previous[0] != short[0] || previous[3] != long {
				t.Fatal("marking changed caller's reference slice")
			}
		})
	}
}

func TestLongTermMarkingFailuresAreTransactional(t *testing.T) {
	for _, tt := range []struct {
		name  string
		maxLT int
		ops   []syntax.MemoryManagementControl
		want  string
	}{
		{"disabled promotion", -1, []syntax.MemoryManagementControl{{Op: 3}}, "limit -1"},
		{"disabled current", -1, []syntax.MemoryManagementControl{{Op: 6}}, "limit -1"},
		{"oversized current", 1, []syntax.MemoryManagementControl{{Op: 6, LongTermFrameIdx: ^uint32(0)}}, "exceeds limit"},
		{"invalid maximum", 1, []syntax.MemoryManagementControl{{Op: 4, MaxLongTermFrameIdxPlus1: 5}}, "max_num_ref_frames"},
		{"promotion before later failure", 1, []syntax.MemoryManagementControl{{Op: 3}, {Op: 1}}, "MMCO1 refers to missing"},
		{"current removed", 1, []syntax.MemoryManagementControl{{Op: 6}, {Op: 2}}, "unmark current"},
		{"current replaced", 1, []syntax.MemoryManagementControl{{Op: 6}, {Op: 3}}, "unmark current"},
		{"current above shrunken maximum", 1, []syntax.MemoryManagementControl{{Op: 6, LongTermFrameIdx: 1}, {Op: 4, MaxLongTermFrameIdxPlus1: 1}}, "unmark current"},
		{"remove missing long", 1, []syntax.MemoryManagementControl{{Op: 2, LongTermPicNum: 1}}, "missing long_term"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder()
			d.maxLongTermFrameIdx = tt.maxLT
			d.DPB.Frames = shortRefs(0)
			prior := d.DPB.Frames[0]
			current := &frame.Frame{FrameNum: 1, IsRef: true}
			p := &pictureState{frame: current, sps: &nal.SPS{Log2MaxFrameNum: 5, MaxNumRefFrames: 4}, referenceFrames: d.DPB.Frames,
				slices: []*sliceState{{header: &syntax.Header{FrameNum: 1, AdaptiveRefPicMarking: true, MemoryManagementControls: tt.ops}}}}
			if err := d.commitPictureReferences(p); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
			if len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != prior || prior.IsLongTerm || d.maxLongTermFrameIdx != tt.maxLT || p.frame != current || d.prevRefFrameNumValid {
				t.Fatal("failed marking changed committed/picture state")
			}
		})
	}
}

func TestLongTermIDRAndSlidingCapacity(t *testing.T) {
	for _, maxRefs := range []uint32{0, 1} {
		for _, longIDR := range []bool{false, true} {
			t.Run(fmt.Sprintf("max_refs_%d/long_%v", maxRefs, longIDR), func(t *testing.T) {
				d := assemblyDecoder(1, 1)
				d.SPS[0].MaxNumRefFrames = maxRefs
				if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
					t.Fatal(err)
				}
				prior, before := d.DPB.Frames[0], d.pocState()
				idr := pcmAssemblySlice(0, 121)
				// Give the second consecutive IDR a distinct ID. Rebuild its
				// header through PCM alignment, retaining the fixture's samples.
				w := &assemblyBits{}
				w.ue(0)
				w.ue(syntax.SliceTypeI)
				w.ue(0)
				w.uint(0, 4)
				w.ue(1) // idr_pic_id
				w.bit(0)
				if longIDR {
					w.bit(1)
				} else {
					w.bit(0)
				}
				w.se(0)
				w.ue(1)
				w.ue(25)
				w.align()
				copy(idr.Payload, w.bytes())
				frames, err := d.Decode(assemblyInput(idr))
				if maxRefs == 0 && longIDR {
					if err == nil || !strings.Contains(err.Error(), "long-term IDR requires max_num_ref_frames greater than zero") {
						t.Fatalf("invalid long-term IDR: %v", err)
					}
					if len(frames) != 0 || len(d.Frames) != 1 || len(d.DPB.Frames) != 1 || d.DPB.Frames[0] != prior || d.pocState() != before || d.maxLongTermFrameIdx != -1 {
						t.Fatal("rejected IDR changed committed picture/reference state")
					}
					return
				}
				wantMax := -1
				if longIDR {
					wantMax = 0
				}
				if err != nil || len(frames) != 1 || frames[0].PixelY(0, 0) != 121 || len(d.DPB.Frames) != 1 || d.DPB.Frames[0].IsLongTerm != longIDR || d.maxLongTermFrameIdx != wantMax {
					t.Fatalf("IDR long=%v max_refs=%d: %v", longIDR, maxRefs, err)
				}
				if maxRefs != 0 {
					_, err = d.Decode(assemblyInput(referenceSkipSlice(1, nil, nil)))
					if longIDR != (err != nil) || (err != nil && !strings.Contains(err.Error(), "no short-term reference")) {
						t.Fatalf("full DPB long=%v: %v", longIDR, err)
					}
				}
			})
		}
	}
}

func TestLongTermCurrentMarkingCapacityOrder(t *testing.T) {
	for _, tt := range []struct {
		name         string
		previousLong bool
		ops          []syntax.MemoryManagementControl
		wantError    bool
	}{
		{"remove too late", false, []syntax.MemoryManagementControl{{Op: 6}, {Op: 1}}, true},
		{"remove before marking", false, []syntax.MemoryManagementControl{{Op: 1}, {Op: 6}}, false},
		{"replace occupied long-term slot", true, []syntax.MemoryManagementControl{{Op: 6}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := assemblyDecoder(1, 1)
			d.SPS[0].MaxNumRefFrames = 2
			if _, err := d.Decode(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
				t.Fatal(err)
			}
			setup := []syntax.MemoryManagementControl{{Op: 4, MaxLongTermFrameIdxPlus1: 1}}
			if tt.previousLong {
				setup = append(setup, syntax.MemoryManagementControl{Op: 6})
			}
			if _, err := d.Decode(assemblyInput(referenceSkipSlice(1, nil, setup))); err != nil {
				t.Fatal(err)
			}
			first, prior, before := d.DPB.Frames[0], d.DPB.Frames[1], d.pocState()
			// The store is full. MMCO6 must have room at its own position in
			// the command sequence, after replacing a matching long-term index.
			frames, err := d.Decode(assemblyInput(referenceSkipSlice(2, nil, tt.ops)))
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "MMCO6 leaves no slot for current picture") {
					t.Fatalf("over-capacity MMCO6: %v", err)
				}
				if len(frames) != 0 || len(d.Frames) != 2 || len(d.DPB.Frames) != 2 || d.DPB.Frames[0] != first || d.DPB.Frames[1] != prior || d.prevRefFrameNum != 1 || d.pocState() != before || d.maxLongTermFrameIdx != 0 {
					t.Fatal("rejected MMCO6 changed committed picture/reference state")
				}
				return
			}
			if err != nil || len(frames) != 1 || frames[0].PixelY(0, 0) != 91 || len(d.DPB.Frames) != 2 || d.DPB.Frames[0] != first {
				t.Fatalf("valid MMCO6 order/replacement: %v", err)
			}
			current := d.DPB.Frames[1]
			if current.FrameNum != 2 || !current.IsLongTerm || current.LongTermFrameIdx != 0 || prior.IsLongTerm != tt.previousLong || prior.FrameNum != 1 {
				t.Fatal("wrong long-term replacement or mutated previously published metadata")
			}
		})
	}
}

func TestLongTermPromotionAndLookupDecode(t *testing.T) {
	d := primedReferenceDecoder(t, false)
	prior := d.DPB.Frames[0]
	frames, err := d.Decode(assemblyInput(referenceSkipSlice(1, nil, []syntax.MemoryManagementControl{{Op: 4, MaxLongTermFrameIdxPlus1: 2}, {Op: 3, LongTermFrameIdx: 1}})))
	if err != nil || len(frames) != 1 {
		t.Fatalf("promoting reference: %v", err)
	}
	if prior.IsLongTerm || d.maxLongTermFrameIdx != 1 || findLongReference(d.DPB.Frames, 1) < 0 {
		t.Fatal("promotion changed published metadata or failed to store marking")
	}
	frames, err = d.Decode(assemblyInput(referenceSkipSlice(2, []syntax.RefPicListModification{{Op: 2, Val: 1}}, []syntax.MemoryManagementControl{{Op: 2, LongTermPicNum: 1}})))
	if err != nil || len(frames) != 1 || frames[0].PixelY(0, 0) != 91 || !frames[0].HasLongTermReferences {
		t.Fatalf("long-term lookup: frames=%d err=%v", len(frames), err)
	}
	if findLongReference(d.DPB.Frames, 1) >= 0 {
		t.Fatal("MMCO2 retained long-term reference")
	}
	s := &sliceState{unit: nal.Unit{RefIDC: 0}, sps: d.SPS[0], header: &syntax.Header{SliceType: syntax.SliceTypeB}}
	if err := validateSliceReferences(s, d.DPB.Frames); err == nil || !strings.Contains(err.Error(), "long-term direct-prediction") {
		t.Fatalf("B accepted short-term colocated picture with long-term motion: %v", err)
	}
}

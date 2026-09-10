package decode

import (
	"errors"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func TestPictureOutputLimits(t *testing.T) {
	for _, tt := range []struct {
		name                         string
		change                       func(*nal.SPS)
		capacity, buffering, reorder int
		invalid                      bool
	}{
		{name: "720p level3.1", capacity: 5, buffering: 5, reorder: 5},
		{name: "1080p level4", change: func(s *nal.SPS) {
			s.LevelIDC, s.PicWidthInMbs, s.PicHeightInMapUnits, s.MaxNumRefFrames = 40, 120, 68, 4
		}, capacity: 4, buffering: 4, reorder: 4},
		{name: "1080p exceeds level3.1 frame size", change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 120, 68
		}, invalid: true},
		{name: "small picture caps DPB at16", change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 1, 1
		}, capacity: 16, buffering: 16, reorder: 16},
		{name: "explicit VUI does not shrink output-order DPB", change: func(s *nal.SPS) {
			s.BitstreamRestriction = true
			s.MaxNumReorderFrames, s.MaxDecFrameBuffering, s.MaxNumRefFrames = 2, 3, 3
		}, capacity: 5, buffering: 3, reorder: 2},
		{name: "explicit zero differs from absent VUI", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxNumRefFrames = true, 0
		}, capacity: 5, buffering: 0},
		{name: "High Intra absent VUI inference", change: func(s *nal.SPS) {
			s.ProfileIDC, s.ConstraintFlags, s.MaxNumRefFrames = 100, 0x10, 0
		}, capacity: 5, buffering: 0},
		{name: "CB level1b", change: func(s *nal.SPS) {
			s.LevelIDC, s.ConstraintFlags = 11, 0xd0
			s.PicWidthInMbs, s.PicHeightInMapUnits, s.MaxNumRefFrames = 9, 11, 4
		}, capacity: 4, buffering: 4, reorder: 4},
		{name: "CB level1.1", change: func(s *nal.SPS) {
			s.LevelIDC = 11
			s.PicWidthInMbs, s.PicHeightInMapUnits = 9, 11
		}, capacity: 9, buffering: 9, reorder: 9},
		{name: "references exceed level DPB", change: func(s *nal.SPS) {
			s.MaxNumRefFrames = 6
		}, invalid: true},
		{name: "references exceed VUI buffering", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering = true, 4
		}, invalid: true},
		{name: "VUI buffering exceeds level DPB", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering = true, 6
		}, invalid: true},
		{name: "reordering exceeds VUI buffering", change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering, s.MaxNumReorderFrames = true, 5, 6
		}, invalid: true},
		{name: "level dimension bound", change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 171, 1
		}, invalid: true},
		{name: "unknown level", change: func(s *nal.SPS) { s.LevelIDC = 0 }, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &nal.SPS{ProfileIDC: 66, ConstraintFlags: 0xc0, LevelIDC: 31,
				FrameMbsOnlyFlag: true, PicWidthInMbs: 80, PicHeightInMapUnits: 45, MaxNumRefFrames: 5}
			if tt.change != nil {
				tt.change(s)
			}
			got, err := pictureOutputLimits(s)
			if tt.invalid {
				if err == nil {
					t.Fatalf("invalid restrictions accepted: %+v", got)
				}
				return
			}
			if err != nil || got.capacity != tt.capacity || got.buffering != tt.buffering || got.reorder != tt.reorder {
				t.Fatalf("limits = %+v, %v; want capacity%d buffering%d reorder%d", got, err, tt.capacity, tt.buffering, tt.reorder)
			}
		})
	}
}

// The queue tests use small pixel buffers; this SPS supplies a two-frame DPB.
func outputQueueSPS() *nal.SPS {
	return &nal.SPS{ProfileIDC: 66, ConstraintFlags: 0xc0, LevelIDC: 11,
		FrameMbsOnlyFlag: true, PicWidthInMbs: 18, PicHeightInMapUnits: 22, MaxNumRefFrames: 2}
}

func outputQueueFrame(poc int, reference bool) *frame.Frame {
	f := frame.NewFrame(16, 16)
	f.POC, f.FullPOC, f.IsRef = poc, poc, reference
	return f
}

func outputQueue(t *testing.T, s *nal.SPS, emitted *[]int) *outputBuffer {
	t.Helper()
	limits, err := pictureOutputLimits(s)
	if err != nil {
		t.Fatal(err)
	}
	return &outputBuffer{limits: limits, output: func(f *frame.Frame) error {
		*emitted = append(*emitted, f.FullPOC)
		return nil
	}}
}

func TestOutputBufferReorderBound(t *testing.T) {
	var emitted []int
	sps := outputQueueSPS()
	// Capacity is 16, so every output below must be triggered by the reorder
	// bound rather than by pressure on the reference/output union.
	sps.PicWidthInMbs, sps.PicHeightInMapUnits = 1, 1
	sps.BitstreamRestriction = true
	sps.MaxNumReorderFrames, sps.MaxDecFrameBuffering = 1, 3
	q := outputQueue(t, sps, &emitted)
	var refs []*frame.Frame
	for i, tt := range []struct {
		poc      int
		want     []int
		fullness int
	}{
		{0, nil, 1},
		{6, []int{0}, 2},
		{2, []int{0, 2}, 2},
		{4, []int{0, 2, 4}, 3},
	} {
		f := outputQueueFrame(tt.poc, true)
		f.IsIDR = i == 0
		refs = append(refs, f)
		if len(refs) > int(sps.MaxNumRefFrames) {
			refs = refs[1:]
		}
		if err := q.add(f, sps, refs); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(emitted, tt.want) {
			t.Fatalf("after POC%d: outputs%v, want%v", tt.poc, emitted, tt.want)
		}
		// Delivered references still occupy storage, and POC6 must stay
		// pending after sliding marking has released it as a reference.
		if got := q.fullness(refs); got != tt.fullness {
			t.Fatalf("after POC%d: fullness%d, want%d", tt.poc, got, tt.fullness)
		}
	}
	if err := q.flush(); err != nil || !reflect.DeepEqual(emitted, []int{0, 2, 4, 6}) {
		t.Fatalf("drained outputs%v, error%v; want [0 2 4 6]", emitted, err)
	}
}

func TestOutputBufferFullReferenceUnion(t *testing.T) {
	for _, reference := range []bool{false, true} {
		name := "non-reference bypass"
		if reference {
			name = "reference needs two bumps"
		}
		t.Run(name, func(t *testing.T) {
			var emitted []int
			sps := outputQueueSPS()
			q := outputQueue(t, sps, &emitted)
			a, b := outputQueueFrame(0, true), outputQueueFrame(4, false)
			q.pending = []*frame.Frame{a, b}
			current := outputQueueFrame(2, reference)
			refs := []*frame.Frame{a}
			wantOutput, wantPending := []int{0, 2}, b
			if reference {
				current.POC, current.FullPOC = 6, 6
				refs = append(refs, current)
				wantOutput, wantPending = []int{0, 4}, current
			}
			if err := q.add(current, sps, refs); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(emitted, wantOutput) || len(q.pending) != 1 || q.pending[0] != wantPending {
				t.Fatalf("outputs = %v, pending = %v; want %v and POC%d", emitted, q.pending, wantOutput, wantPending.FullPOC)
			}
			if got := q.fullness(refs); got != 2 {
				t.Fatalf("reference/output union = %d, want2", got)
			}
		})
	}
	// Already-output references can occupy every slot with no pending pictures.
	var emitted []int
	sps := outputQueueSPS()
	q := outputQueue(t, sps, &emitted)
	refs := []*frame.Frame{outputQueueFrame(0, true), outputQueueFrame(2, true)}
	if err := q.add(outputQueueFrame(4, false), sps, refs); err != nil || !reflect.DeepEqual(emitted, []int{4}) || len(q.pending) != 0 {
		t.Fatalf("full-reference non-reference bypass: outputs%v pending%d error%v", emitted, len(q.pending), err)
	}
}

func TestOutputBufferReferenceMetadataCopies(t *testing.T) {
	var emitted []int
	sps := outputQueueSPS()
	q := outputQueue(t, sps, &emitted)
	a, current := outputQueueFrame(0, true), outputQueueFrame(2, true)
	q.pending = []*frame.Frame{a}
	// MMCO3 and current-picture marking copy metadata but not coded pixels.
	promoted, markedCurrent := *a, *current
	promoted.IsLongTerm, promoted.LongTermFrameIdx = true, 1
	refs := []*frame.Frame{&promoted, &markedCurrent}
	if err := q.add(current, sps, refs); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 0 || len(q.pending) != 2 || q.fullness(refs) != 2 {
		t.Fatalf("metadata copies double-counted references: outputs%v pending%d fullness%d", emitted, len(q.pending), q.fullness(refs))
	}
	gap := &frame.Frame{IsRef: true, NonExisting: true, FrameNum: 3}
	if got := q.fullness([]*frame.Frame{&promoted, gap}); got != 3 {
		t.Fatalf("non-existing reference did not occupy its own slot: %d", got)
	}
}

func TestOutputBufferGapBumpingBeforeInsertion(t *testing.T) {
	for _, maxRefs := range []int{1, 2} {
		var emitted []int
		sps := outputQueueSPS()
		sps.MaxNumRefFrames = uint32(maxRefs)
		q := outputQueue(t, sps, &emitted)
		a, b := outputQueueFrame(0, true), outputQueueFrame(4, false)
		q.pending = []*frame.Frame{a, b}
		refs, next, err := stageFrameNumGapsWithOutput([]*frame.Frame{a}, 0, 2, 16, maxRefs, true, q.gap)
		want := []int{0}
		if maxRefs == 2 {
			want = append(want, 4)
		}
		if err != nil || next != 1 || !reflect.DeepEqual(emitted, want) || len(refs) != maxRefs || !refs[len(refs)-1].NonExisting {
			t.Fatalf("maxRefs%d: outputs%v refs%v next%d error%v", maxRefs, emitted, refs, next, err)
		}
		if maxRefs == 1 {
			// Sliding unmarks A before the callback, so outputting A frees a slot.
			if len(q.pending) != 1 || q.pending[0] != b {
				t.Fatal("gap callback ran before sliding-window eviction")
			}
			continue
		}
		// With A still referenced, the gap forced both prior outputs. Current
		// MMCO removes A and gap1, but must not undo those earlier DPB bumps.
		current := outputQueueFrame(6, true)
		current.FrameNum = 2
		h := &syntax.Header{FrameNum: 2, AdaptiveRefPicMarking: true,
			MemoryManagementControls: []syntax.MemoryManagementControl{{Op: 1, DifferenceOfPicNumsMinus1: 1}, {Op: 1}}}
		refs, current, _, err = stageReferenceMarking(refs, current, h, 16, 2, -1)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.add(current, sps, refs); err != nil {
			t.Fatal(err)
		}
		idr := outputQueueFrame(0, true)
		idr.IsIDR, idr.NoOutputOfPriorPics = true, true
		if err := q.add(idr, sps, []*frame.Frame{idr}); err != nil {
			t.Fatal(err)
		}
		if err := q.flush(); err != nil || !reflect.DeepEqual(emitted, []int{0, 4, 0}) {
			t.Fatalf("later IDR changed gap-triggered outputs: %v, %v", emitted, err)
		}
	}
}

func TestOutputBufferEpochBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name    string
		idr     bool
		discard bool
		change  func(*nal.SPS)
		want    []int
	}{
		{name: "IDR flush", idr: true, want: []int{2, 4, 0}},
		{name: "IDR discard", idr: true, discard: true, want: []int{0}},
		{name: "MMCO5 flush", want: []int{2, 4, 0}},
		{name: "IDR geometry change infers discard", idr: true, change: func(s *nal.SPS) {
			s.PicWidthInMbs, s.PicHeightInMapUnits = 22, 18
		}, want: []int{0}},
		{name: "IDR buffering change infers discard", idr: true, change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering, s.MaxNumRefFrames = true, 1, 1
		}, want: []int{0}},
		{name: "explicit unchanged buffering flushes", idr: true, change: func(s *nal.SPS) {
			s.BitstreamRestriction, s.MaxDecFrameBuffering = true, 2
		}, want: []int{2, 4, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var emitted []int
			sps := outputQueueSPS()
			q := outputQueue(t, sps, &emitted)
			q.pending = []*frame.Frame{outputQueueFrame(4, false), outputQueueFrame(2, true)}
			if tt.change != nil {
				tt.change(sps)
			}
			current := outputQueueFrame(0, true)
			current.IsIDR, current.NoOutputOfPriorPics, current.ResetsPictureOrder = tt.idr, tt.discard, true
			if err := q.add(current, sps, []*frame.Frame{current}); err != nil {
				t.Fatal(err)
			}
			if err := q.flush(); err != nil || !reflect.DeepEqual(emitted, tt.want) {
				t.Fatalf("outputs = %v, %v; want %v", emitted, err, tt.want)
			}
		})
	}
}

func TestOutputBufferPropagatesCallbackError(t *testing.T) {
	want := errors.New("consumer stopped")
	q := &outputBuffer{limits: outputLimits{capacity: 1}, output: func(*frame.Frame) error { return want }}
	q.pending = []*frame.Frame{outputQueueFrame(0, false)}
	if _, _, err := stageFrameNumGapsWithOutput(nil, 0, 2, 16, 1, true, q.gap); !errors.Is(err, want) {
		t.Fatalf("gap output error = %v, want %v", err, want)
	}
	q = &outputBuffer{output: func(*frame.Frame) error { return want }}
	sps := outputQueueSPS()
	sps.BitstreamRestriction, sps.MaxDecFrameBuffering = true, 2
	f := outputQueueFrame(0, true)
	if err := q.add(f, sps, []*frame.Frame{f}); !errors.Is(err, want) {
		t.Fatalf("zero-reorder output error = %v, want %v", err, want)
	}
}

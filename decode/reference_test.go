package decode

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func shortRefs(numbers ...int) []*frame.Frame {
	refs := make([]*frame.Frame, len(numbers))
	for i, n := range numbers {
		refs[i] = &frame.Frame{FrameNum: n, IsRef: true}
	}
	return refs
}

func referenceNumbers(refs []*frame.Frame) []int {
	numbers := make([]int, len(refs))
	for i, f := range refs {
		if f == nil {
			numbers[i] = -1
		} else {
			numbers[i] = f.FrameNum
		}
	}
	return numbers
}

func TestReferenceMarkingCommandConstraints(t *testing.T) {
	for _, diff := range []uint32{32, ^uint32(0)} {
		for _, op := range []uint32{1, 3} {
			hdr := &syntax.Header{AdaptiveRefPicMarking: true, MemoryManagementControls: []syntax.MemoryManagementControl{{Op: op, DifferenceOfPicNumsMinus1: diff}}}
			if err := validateReferenceMarking(hdr, 32); err == nil || !strings.Contains(err.Error(), "MaxPicNum") {
				t.Fatalf("out-of-range MMCO %d difference %d: %v", op, diff, err)
			}
		}
	}
	for _, ops := range [][]uint32{{4, 4}, {5, 5}, {6, 6}, {6, 5}, {1, 5}, {5, 2}, {3, 5}, {0}, {7}} {
		hdr := &syntax.Header{AdaptiveRefPicMarking: true}
		for _, op := range ops {
			hdr.MemoryManagementControls = append(hdr.MemoryManagementControls, syntax.MemoryManagementControl{Op: op})
		}
		if err := validateReferenceMarking(hdr, 32); err == nil {
			t.Fatalf("illegal MMCO order %v accepted", ops)
		}
	}
	for _, hdr := range []*syntax.Header{
		{},
		{LongTermReference: true},
		{AdaptiveRefPicMarking: true},
		{AdaptiveRefPicMarking: true, MemoryManagementControls: []syntax.MemoryManagementControl{{Op: 1, DifferenceOfPicNumsMinus1: 31}}},
		{AdaptiveRefPicMarking: true, MemoryManagementControls: []syntax.MemoryManagementControl{{Op: 5}, {Op: 4}, {Op: 6}}},
	} {
		if err := validateReferenceMarking(hdr, 32); err != nil {
			t.Fatalf("supported marking rejected: %v", err)
		}
	}
}

func TestPReferenceListFrameNumWrap(t *testing.T) {
	for _, maxFrameNum := range []int{16, 32, 256, 65536} {
		t.Run(fmt.Sprint(maxFrameNum), func(t *testing.T) {
			refs := shortRefs(maxFrameNum-1, 0, maxFrameNum-2)
			refs = append(refs, &frame.Frame{FrameNum: 1}) // Not a reference.
			list, err := buildPReferenceList(refs, 1, maxFrameNum, 3, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := []int{0, maxFrameNum - 1, maxFrameNum - 2}
			if got := referenceNumbers(list); !reflect.DeepEqual(got, want) {
				t.Fatalf("default list = %v, want %v", got, want)
			}
			if refs[0].FrameNum != maxFrameNum-1 {
				t.Fatal("building the list mutated DPB order")
			}
			// The prediction value crosses zero in both directions. Repeating
			// a selected picture must preserve the earlier occurrence.
			mods := []syntax.RefPicListModification{
				{Op: 0, Val: 1}, // 1 - 2 wraps to MaxFrameNum - 1.
				{Op: 1, Val: 0}, // +1 wraps to 0.
				{Op: 0, Val: 0}, // -1 wraps back to MaxFrameNum - 1.
			}
			list, err = buildPReferenceList(refs, 1, maxFrameNum, 3, mods)
			if err != nil {
				t.Fatal(err)
			}
			want = []int{maxFrameNum - 1, 0, maxFrameNum - 1}
			if got := referenceNumbers(list); !reflect.DeepEqual(got, want) {
				t.Fatalf("modified list = %v, want %v", got, want)
			}
		})
	}
}

func TestPReferenceListUsesWholeDPBAndFillsMissingEntries(t *testing.T) {
	refs := shortRefs(0, 1, 2, 3)
	list, err := buildPReferenceList(refs, 4, 32, 1, []syntax.RefPicListModification{{Op: 0, Val: 3}})
	if err != nil || len(list) != 1 || list[0] != refs[0] {
		t.Fatalf("selecting reference outside initial active list: %v, %v", referenceNumbers(list), err)
	}
	// activeCount may exceed the number of distinct stored references.
	mods := []syntax.RefPicListModification{{Op: 0, Val: 0}, {Op: 1, Val: 31}, {Op: 0, Val: 31}}
	list, err = buildPReferenceList(refs[:1], 1, 32, 3, mods)
	if err != nil || !reflect.DeepEqual(referenceNumbers(list), []int{0, 0, 0}) {
		t.Fatalf("repeated reference padding: %v, %v", referenceNumbers(list), err)
	}
}

func TestPReferenceListRejectsUnusableModifications(t *testing.T) {
	refs := shortRefs(0, 1)
	missing := &frame.Frame{FrameNum: 2, IsRef: true, NonExisting: true}
	for _, tc := range []struct {
		name   string
		refs   []*frame.Frame
		active int
		mods   []syntax.RefPicListModification
		want   string
	}{
		{"no_real_reference", []*frame.Frame{missing}, 1, nil, "no decoded reference"},
		{"unfilled_active_entry", refs, 3, nil, "entry 2"},
		{"missing_target", refs, 1, []syntax.RefPicListModification{{Op: 0, Val: 0}}, "missing frame_num 2"},
		{"non_existing_target", append(append([]*frame.Frame(nil), refs...), missing), 1, []syntax.RefPicListModification{{Op: 0, Val: 0}}, "non-existing frame_num 2"},
		{"missing_long_term", refs, 1, []syntax.RefPicListModification{{Op: 2}}, "missing long_term_pic_num"},
		{"too_many_modifications", refs, 1, []syntax.RefPicListModification{{Op: 0}, {Op: 1}}, "modifications"},
		{"oversized_difference", refs, 1, []syntax.RefPicListModification{{Op: 0, Val: 32}}, "MaxPicNum"},
		{"overflow_difference", refs, 1, []syntax.RefPicListModification{{Op: 0, Val: ^uint32(0)}}, "MaxPicNum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildPReferenceList(tc.refs, 3, 32, tc.active, tc.mods); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
	// Non-existing default entries are allowed when never used for prediction.
	list, err := buildPReferenceList([]*frame.Frame{refs[1], missing}, 3, 32, 2, nil)
	if err != nil || list[0] != missing || list[1] != refs[1] {
		t.Fatalf("default list containing gap: %v, %v", referenceNumbers(list), err)
	}
	list, err = buildPReferenceList([]*frame.Frame{refs[1], missing}, 3, 32, 1, nil)
	if err != nil || len(list) != 1 || list[0] != missing {
		t.Fatalf("real reference outside active prefix: %v, %v", referenceNumbers(list), err)
	}
}

func TestStageFrameNumGapsKeepsOnlyReferenceMetadata(t *testing.T) {
	refs := shortRefs(0, 1)
	refs[0].Y = []byte{71}
	refs[1].Y = []byte{93}
	staged, nextPrev, err := stageFrameNumGaps(refs, 1, 4, 32, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := referenceNumbers(staged); !reflect.DeepEqual(got, []int{1, 2, 3}) || nextPrev != 3 {
		t.Fatalf("staged %v nextPrev %d", got, nextPrev)
	}
	if !reflect.DeepEqual(referenceNumbers(refs), []int{0, 1}) || !refs[0].IsRef || refs[0].Y[0] != 71 {
		t.Fatal("staging changed committed references")
	}
	for _, f := range staged[1:] {
		if !f.IsRef || !f.NonExisting || len(f.Y)+len(f.U)+len(f.V)+len(f.MotionL0)+len(f.MotionL1) != 0 {
			t.Fatalf("gap allocated decoded picture data: %+v", f)
		}
	}
}

func TestStageFrameNumGapsWrapAndSlide(t *testing.T) {
	for _, maxFrameNum := range []int{16, 32, 65536} {
		refs := shortRefs(maxFrameNum-2, maxFrameNum-3) // Deliberately not oldest-first.
		staged, nextPrev, err := stageFrameNumGaps(refs, maxFrameNum-2, 1, maxFrameNum, 2, true)
		if err != nil {
			t.Fatal(err)
		}
		if got := referenceNumbers(staged); !reflect.DeepEqual(got, []int{maxFrameNum - 1, 0}) || nextPrev != 0 {
			t.Fatalf("MaxFrameNum %d: staged %v nextPrev %d", maxFrameNum, got, nextPrev)
		}
	}
	// A long legal gap keeps at most max_num_ref_frames placeholders, not an
	// array (or pixel buffers) proportional to the distance between pictures.
	staged, nextPrev, err := stageFrameNumGaps(shortRefs(0), 0, 65535, 65536, 3, true)
	if err != nil || len(staged) != 3 || nextPrev != 65534 {
		t.Fatalf("long gap: refs %d nextPrev %d err %v", len(staged), nextPrev, err)
	}
}

func TestStageFrameNumGapsRejectsLossAndReuse(t *testing.T) {
	for _, tc := range []struct {
		name          string
		refs          []*frame.Frame
		prev, current int
		allowed       bool
		want          string
	}{
		{"unannounced", shortRefs(0), 0, 2, false, "unannounced"},
		{"unannounced_wrap", shortRefs(30), 30, 1, false, "unannounced"},
		{"stored_gap_number", shortRefs(0, 2), 0, 3, true, "reuses stored reference 2"},
		{"stored_current_number", shortRefs(0, 1), 0, 1, true, "reuses stored reference 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := referenceNumbers(tc.refs)
			staged, nextPrev, err := stageFrameNumGaps(tc.refs, tc.prev, tc.current, 32, 4, tc.allowed)
			if err == nil || !strings.Contains(err.Error(), tc.want) || staged != nil || nextPrev != tc.prev {
				t.Fatalf("staged %v nextPrev %d error %v", staged, nextPrev, err)
			}
			if !reflect.DeepEqual(referenceNumbers(tc.refs), before) {
				t.Fatal("failure changed reference store")
			}
		})
	}
}

func TestStageFrameNumGapsNoGapDoesNotAdvancePrevRef(t *testing.T) {
	for _, current := range []int{31, 0} {
		refs := shortRefs(31)
		staged, nextPrev, err := stageFrameNumGaps(refs, 31, current, 32, 1, false)
		if err != nil || nextPrev != 31 || len(staged) != 1 || staged[0] != refs[0] {
			t.Fatalf("current %d: staged %v nextPrev %d err %v", current, staged, nextPrev, err)
		}
		staged[0] = nil
		if refs[0] == nil {
			t.Fatal("staged slice aliases committed slice")
		}
	}
}

func TestPReferenceListLongTermOrderAndModifications(t *testing.T) {
	short := shortRefs(31, 0)
	long0 := &frame.Frame{IsRef: true, IsLongTerm: true, LongTermFrameIdx: 0, FrameNum: 0}
	long2 := &frame.Frame{IsRef: true, IsLongTerm: true, LongTermFrameIdx: 2, FrameNum: 31}
	refs := []*frame.Frame{long2, short[0], long0, short[1]}
	list, err := buildPReferenceList(refs, 1, 32, 4, nil)
	if err != nil || !reflect.DeepEqual(list, []*frame.Frame{short[1], short[0], long0, long2}) {
		t.Fatalf("mixed default list: %v, %v", list, err)
	}
	// Op2 neither changes PicNumPred nor deduplicates earlier repetitions.
	mods := []syntax.RefPicListModification{{Op: 0, Val: 1}, {Op: 2, Val: 2}, {Op: 1}, {Op: 2, Val: 2}}
	list, err = buildPReferenceList(refs, 1, 32, 4, mods)
	if err != nil || !reflect.DeepEqual(list, []*frame.Frame{short[0], long2, short[1], long2}) {
		t.Fatalf("mixed modified list: %v, %v", list, err)
	}
	list, err = buildPReferenceList([]*frame.Frame{long0}, 1, 32, 2, []syntax.RefPicListModification{{Op: 2}, {Op: 2}})
	if err != nil || !reflect.DeepEqual(list, []*frame.Frame{long0, long0}) {
		t.Fatalf("long-term-only repeated list: %v, %v", list, err)
	}
	if _, err = buildPReferenceList([]*frame.Frame{long0}, 1, 32, 1, []syntax.RefPicListModification{{Op: 0}}); err == nil {
		t.Fatal("short-term modification selected matching long-term frame_num")
	}
	if _, err = buildPReferenceList([]*frame.Frame{long0}, 1, 32, 1, []syntax.RefPicListModification{{Op: 2, Val: ^uint32(0)}}); err == nil {
		t.Fatal("oversized long-term index accepted")
	}
}

func TestGapSlidingPreservesLongTermReferences(t *testing.T) {
	long := &frame.Frame{IsRef: true, IsLongTerm: true, LongTermFrameIdx: 0, FrameNum: 2}
	short := shortRefs(0)[0]
	staged, next, err := stageFrameNumGaps([]*frame.Frame{short, long}, 0, 3, 32, 2, true)
	if err != nil || next != 2 || len(staged) != 2 || staged[0] != long || !staged[1].NonExisting || staged[1].FrameNum != 2 {
		t.Fatalf("long-term frame_num may coincide with inferred short term: %+v, %d, %v", staged, next, err)
	}
	if _, _, err = stageFrameNumGaps([]*frame.Frame{long}, 0, 2, 32, 1, true); err == nil || !strings.Contains(err.Error(), "no short-term") {
		t.Fatalf("all-long-term full DPB: %v", err)
	}
	if long.FrameNum != 2 || !long.IsLongTerm || !short.IsRef {
		t.Fatal("sliding mutated original metadata")
	}
}

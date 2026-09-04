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

func TestShortTermReferenceMarkingRejectsDeferredFeatures(t *testing.T) {
	if err := validateShortTermReferenceMarking(&syntax.Header{LongTermReference: true}, 32); err == nil || !strings.Contains(err.Error(), "unsupported long-term IDR") {
		t.Fatalf("long-term IDR: %v", err)
	}
	for _, op := range []uint32{2, 3, 4, 5, 6} {
		hdr := &syntax.Header{AdaptiveRefPicMarking: true, MemoryManagementControls: []syntax.MemoryManagementControl{{Op: op}}}
		if err := validateShortTermReferenceMarking(hdr, 32); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("deferred MMCO %d: %v", op, err)
		}
	}
	for _, diff := range []uint32{32, ^uint32(0)} {
		hdr := &syntax.Header{AdaptiveRefPicMarking: true, MemoryManagementControls: []syntax.MemoryManagementControl{{Op: 1, DifferenceOfPicNumsMinus1: diff}}}
		if err := validateShortTermReferenceMarking(hdr, 32); err == nil || !strings.Contains(err.Error(), "MaxPicNum") {
			t.Fatalf("out-of-range MMCO 1 difference %d: %v", diff, err)
		}
	}
	for _, hdr := range []*syntax.Header{
		{},
		{AdaptiveRefPicMarking: true},
		{AdaptiveRefPicMarking: true, MemoryManagementControls: []syntax.MemoryManagementControl{{Op: 1, DifferenceOfPicNumsMinus1: 31}}},
	} {
		if err := validateShortTermReferenceMarking(hdr, 32); err != nil {
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
		{"long_term_deferred", refs, 1, []syntax.RefPicListModification{{Op: 2}}, "unsupported"},
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

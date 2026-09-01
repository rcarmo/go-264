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
	for _, tc := range []struct {
		name   string
		refs   []*frame.Frame
		active int
		mods   []syntax.RefPicListModification
		want   string
	}{
		{"no_real_reference", nil, 1, nil, "no decoded reference"},
		{"unfilled_active_entry", refs, 3, nil, "entry 2"},
		{"missing_target", refs, 1, []syntax.RefPicListModification{{Op: 0, Val: 0}}, "missing frame_num 2"},
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

}

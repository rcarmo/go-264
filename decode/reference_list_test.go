package decode

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func TestPReferenceListCompactMatchesOriginal(t *testing.T) {
	rng := rand.New(rand.NewSource(2641271))
	for trial := 0; trial < 20000; trial++ {
		modulus := []int{16, 32, 256, 65536}[trial%4]
		current := rng.Intn(modulus)
		n := rng.Intn(25)
		refs := make([]*frame.Frame, 0, n)
		// Unique short frame numbers/long indices, as in a validated DPB.
		for i := 0; i < n; i++ {
			f := &frame.Frame{FrameNum: (current - i - 1 + modulus) % modulus, IsRef: true, Y: []byte{byte(i)}}
			if i%3 == 0 {
				f.IsLongTerm = true
				f.LongTermFrameIdx = i
			}
			if i%7 == 0 {
				f.NonExisting = true
			}
			refs = append(refs, f)
		}
		rng.Shuffle(len(refs), func(i, j int) { refs[i], refs[j] = refs[j], refs[i] })
		if trial%5 == 0 {
			refs = append(refs, nil, &frame.Frame{FrameNum: 3})
		}
		before := slices.Clone(refs)
		active := 1 + rng.Intn(32)
		var mods []syntax.RefPicListModification
		predicted := current
		for i, count := 0, rng.Intn(active+2); i < count; i++ {
			if trial%2 == 0 && n > 0 {
				// Exercise valid selections/repetitions as well as rejects.
				selected := refs[rng.Intn(n)]
				if selected == nil || !selected.IsRef {
					continue
				}
				if selected.IsLongTerm {
					mods = append(mods, syntax.RefPicListModification{Op: 2, Val: uint32(selected.LongTermFrameIdx)})
				} else {
					diff := (predicted - selected.FrameNum + modulus) % modulus
					if diff == 0 {
						diff = modulus
					}
					mods = append(mods, syntax.RefPicListModification{Op: 0, Val: uint32(diff - 1)})
					predicted = selected.FrameNum
				}
			} else {
				mods = append(mods, syntax.RefPicListModification{Op: uint32(rng.Intn(4)), Val: uint32(rng.Intn(modulus + 1))})
			}
		}
		got, ge := buildPReferenceList(refs, current, modulus, active, mods)
		want, we := originalPReferenceList(refs, current, modulus, active, mods)
		if fmt.Sprint(ge) != fmt.Sprint(we) || !slices.Equal(got, want) {
			t.Fatalf("trial=%d got=%v/%v want=%v/%v", trial, referenceNumbers(got), ge, referenceNumbers(want), we)
		}
		if !slices.Equal(refs, before) {
			t.Fatal("input membership changed")
		}
		if ge == nil {
			got[0] = nil
			if !slices.Equal(refs, before) {
				t.Fatal("active list aliases store")
			}
		}
	}
}

func TestPReferenceListSingleOwnedAllocation(t *testing.T) {
	refs := shortRefs(0, 1, 2, 3)
	for _, mods := range [][]syntax.RefPicListModification{nil, {{Op: 0, Val: 3}, {Op: 1, Val: 0}, {Op: 0, Val: 0}}} {
		allocations := testing.AllocsPerRun(100, func() {
			list, err := buildPReferenceList(refs, 4, 32, 4, mods)
			if err != nil || len(list) != 4 {
				panic("reference list")
			}
		})
		if allocations > 1 {
			t.Fatalf("allocations=%g want<=1", allocations)
		}
	}
}

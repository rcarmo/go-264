package decode

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/frame"
)

// Explicit stepwise reference for the final-state shortcut. The callback path
// must retain this behaviour because output bumping observes every insertion.
func TestGapShortcutMatchesStepwise(t *testing.T) {
	for _, modulus := range []int{16, 32, 256} {
		for limit := 1; limit <= 4; limit++ {
			for longCount := 0; longCount <= limit; longCount++ {
				for prev := 0; prev < modulus; prev += max(1, modulus/4) {
					for distance := 1; distance < modulus; distance++ {
						refs := []*frame.Frame{nil, {FrameNum: 7}}
						for i := 0; i < longCount; i++ {
							refs = append(refs, &frame.Frame{IsRef: true, IsLongTerm: true, LongTermFrameIdx: i})
						}
						if longCount < limit {
							refs = append(refs, &frame.Frame{IsRef: true, FrameNum: prev, Y: []byte{71}})
						}
						before := append([]*frame.Frame(nil), refs...)
						current := (prev + distance) % modulus
						got, gn, ge := stageFrameNumGapsWithOutput(refs, prev, current, modulus, limit, true, nil)
						calls := 0
						want, wn, we := stageFrameNumGapsWithOutput(refs, prev, current, modulus, limit, true, func(_ []*frame.Frame) error { calls++; return nil })
						if fmt.Sprint(ge) != fmt.Sprint(we) || gn != wn || !reflect.DeepEqual(got, want) {
							t.Fatalf("mod=%d limit=%d long=%d prev=%d distance=%d: got %v/%d/%v want %v/%d/%v", modulus, limit, longCount, prev, distance, referenceNumbers(got), gn, ge, referenceNumbers(want), wn, we)
						}
						if !reflect.DeepEqual(refs, before) {
							t.Fatal("mutated input membership")
						}
						if we == nil && calls != distance-1 {
							t.Fatalf("callbacks=%d want=%d", calls, distance-1)
						}
						if len(got) > 0 {
							got[0] = nil
							if !reflect.DeepEqual(refs, before) {
								t.Fatal("aliased input slice")
							}
						}
					}
				}
			}
		}
	}
}

func TestLongGapAllocationsBounded(t *testing.T) {
	refs := shortRefs(0)
	allocations := testing.AllocsPerRun(20, func() {
		staged, next, err := stageFrameNumGaps(refs, 0, 65535, 65536, 3, true)
		if err != nil || len(staged) != 3 || next != 65534 {
			panic("gap")
		}
	})
	if allocations > 4 {
		t.Fatalf("long gap allocations=%g, want at most 4", allocations)
	}
}

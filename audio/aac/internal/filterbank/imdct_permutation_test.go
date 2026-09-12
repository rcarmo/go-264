package filterbank

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func bitReverseRuntime(x []complex128) {
	for i, j := 1, 0; i < len(x); i++ {
		bit := len(x) >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
}

func TestBitReverseSwapPlanMatchesRuntimeLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(26414))
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64}
	for _, n := range []int{shortTransform, longTransform} {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			got := make([]complex128, n)
			for i := range got {
				got[i] = complex(rng.NormFloat64(), rng.NormFloat64())
				if i < len(values) {
					got[i] = complex(values[i], values[len(values)-1-i])
				}
			}
			want := slices.Clone(got)
			bitReverseRuntime(want)
			p := makePlan(n)
			for _, pair := range p.bitReverseSwaps {
				i, j := int(pair[0]), int(pair[1])
				got[i], got[j] = got[j], got[i]
			}
			for i := range got {
				if !sameComplexBits(got[i], want[i]) {
					t.Fatalf("sample %d: got %v want %v", i, got[i], want[i])
				}
			}
			if len(p.bitReverseSwaps) == 0 || len(p.bitReverseSwaps) >= n/2 {
				t.Fatalf("unexpected swap count %d for n=%d", len(p.bitReverseSwaps), n)
			}
		})
	}
}

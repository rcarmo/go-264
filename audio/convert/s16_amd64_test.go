//go:build amd64 && !purego

package convert

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

func TestS16DispatchCanForceSSE2(t *testing.T) {
	old := s16HasAVX2
	s16HasAVX2 = false
	defer func() { s16HasAVX2 = old }()
	src := []float64{-1, -0.5 / 32768, math.Copysign(0, -1), 0.5 / 32768, 1}
	got, want := make([]int16, len(src)), make([]int16, len(src))
	s16Kernel(got, src)
	s16Scalar(want, src)
	if !slices.Equal(got, want) {
		t.Fatalf("SSE2 dispatch=%v scalar=%v", got, want)
	}
}

func TestS16AVX2MatchesSSE2(t *testing.T) {
	if !s16HasAVX2 {
		t.Skip("AVX2 with OS XMM/YMM state unavailable")
	}
	var src []float64
	for i := -32770; i <= 32770; i++ {
		v := (float64(i) + 0.5) / 32768
		src = append(src, math.Nextafter(v, math.Inf(-1)), v, math.Nextafter(v, math.Inf(1)), float64(i)/32768)
	}
	src = append(src, 0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64)
	rng := rand.New(rand.NewSource(2641602))
	for i := 0; i < 50000; i++ {
		v := math.Float64frombits(rng.Uint64())
		if allFiniteScalar([]float64{v}) {
			src = append(src, v)
		}
	}
	src = src[:len(src)&^3]
	got, want := make([]int16, len(src)), make([]int16, len(src))
	s16AVX2(&got[0], &src[0], len(src))
	s16SSE2(&want[0], &src[0], len(src))
	if !slices.Equal(got, want) {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("i=%d bits=%016x AVX2=%d SSE2=%d", i, math.Float64bits(src[i]), got[i], want[i])
			}
		}
	}
}

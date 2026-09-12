package convert

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func TestS16SIMDRoundingBoundaries(t *testing.T) {
	var src []float64
	for i := -32770; i <= 32770; i++ {
		v := (float64(i) + 0.5) / 32768
		src = append(src, math.Nextafter(v, math.Inf(-1)), v, math.Nextafter(v, math.Inf(1)), float64(i)/32768)
	}
	src = append(src, 0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64)
	rng := rand.New(rand.NewSource(264))
	for i := 0; i < 50000; i++ {
		v := math.Float64frombits(rng.Uint64())
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			src = append(src, v)
		}
	}
	want, got := make([]int16, len(src)), make([]int16, len(src)+1)
	got[len(src)] = 123
	s16Scalar(want, src)
	if err := S16(got, src); err != nil {
		t.Fatal(err)
	}
	for i := range src {
		if got[i] != want[i] {
			t.Fatalf("i%d input%g bits%x got%d want%d", i, src[i], math.Float64bits(src[i]), got[i], want[i])
		}
	}
	if got[len(src)] != 123 {
		t.Fatal("tail overwritten")
	}
}

func TestS16RejectsBeforeWriting(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		out := []int16{11, 22, 33}
		if S16(out, []float64{0.5, bad, 1}) == nil || !slices.Equal(out, []int16{11, 22, 33}) {
			t.Fatal("nonfinite changed output", out)
		}
	}
}

func checkPCMFloatBits(t *testing.T, a, b []float64) {
	t.Helper()
	for i := range a {
		if math.Float64bits(a[i]) != math.Float64bits(b[i]) {
			t.Fatalf("sample%d got%x want%x", i, math.Float64bits(a[i]), math.Float64bits(b[i]))
		}
	}
}

func TestLayoutKernelParity(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 1, -1, math.MaxFloat64 / 4, -math.MaxFloat64 / 4}
	for _, n := range []int{0, 1, 2, 3, 7, 128, 1024} {
		for offset := 0; offset < 2; offset++ {
			t.Run(fmt.Sprintf("n%d/off%d", n, offset), func(t *testing.T) {
				src := make([]float64, 2*n+2)
				for i := range src {
					src[i] = values[i%len(values)]
				}
				got, want := make([]float64, n+2), make([]float64, n+2)
				for i := range got {
					got[i] = 123
					want[i] = 123
				}
				stereoToMono(got[offset:offset+n], src[offset:offset+2*n])
				stereoToMonoScalar(want[offset:offset+n], src[offset:offset+2*n])
				checkPCMFloatBits(t, got, want)
				wide, wideRef := make([]float64, 2*n+2), make([]float64, 2*n+2)
				for i := range wide {
					wide[i] = 456
					wideRef[i] = 456
				}
				monoToStereo(wide[offset:offset+2*n], src[offset:offset+n])
				monoToStereoScalar(wideRef[offset:offset+2*n], src[offset:offset+n])
				checkPCMFloatBits(t, wide, wideRef)
				interleaveStereo(wide[offset:offset+2*n], src[offset:offset+n], got[offset:offset+n])
				interleaveStereoScalar(wideRef[offset:offset+2*n], src[offset:offset+n], got[offset:offset+n])
				checkPCMFloatBits(t, wide, wideRef)
			})
		}
	}
}

func BenchmarkPCMNumeric(b *testing.B) {
	for _, n := range []int{128, 2048} {
		for _, kind := range []string{"s16", "stereo-mono", "mono-stereo", "interleave"} {
			for _, mode := range []string{"scalar", "dispatch"} {
				b.Run(fmt.Sprintf("%s/n%d/%s", kind, n, mode), func(b *testing.B) {
					src, out := make([]float64, n*2), make([]float64, n*2)
					pcm := make([]int16, n)
					for i := range src {
						src[i] = float64(i%201-100) / 128
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						switch kind {
						case "s16":
							if mode == "scalar" {
								s16Scalar(pcm, src[:n])
							} else {
								s16Kernel(pcm, src[:n])
							}
						case "stereo-mono":
							if mode == "scalar" {
								stereoToMonoScalar(out[:n], src)
							} else {
								stereoToMono(out[:n], src)
							}
						case "mono-stereo":
							if mode == "scalar" {
								monoToStereoScalar(out, src[:n])
							} else {
								monoToStereo(out, src[:n])
							}
						case "interleave":
							if mode == "scalar" {
								interleaveStereoScalar(out, src[:n], src[n:])
							} else {
								interleaveStereo(out, src[:n], src[n:])
							}
						}
					}
				})
			}
		}
	}
}

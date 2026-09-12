package pred

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestLumaAllFractionsAndEdges(t *testing.T) {
	rng := rand.New(rand.NewSource(264))
	for _, stride := range []int{1, 7, 23, 48} {
		ref := make([]byte, stride*29+stride/2)
		for pattern := 0; pattern < 5; pattern++ {
			for i := range ref {
				switch pattern {
				case 0:
					ref[i] = 0
				case 1:
					ref[i] = 255
				case 2:
					ref[i] = byte((i % 2) * 255)
				case 3:
					ref[i] = byte((i / stride % 2) * 255)
				default:
					ref[i] = byte(rng.Intn(256))
				}
			}
			for _, dim := range [][2]int{{1, 1}, {1, 16}, {16, 1}, {2, 2}, {4, 8}, {8, 4}, {15, 16}, {16, 15}, {16, 16}, {17, 17}} {
				w, h := dim[0], dim[1]
				outStride := w + 5
				for _, base := range [][2]int{{-3, -3}, {0, 0}, {stride - 1, 0}, {0, 28}, {stride - 1, 28}, {8, 7}, {100, 100}, {-100, -100}} {
					for fx := -4; fx < 4; fx++ {
						for fy := -4; fy < 4; fy++ {
							mv := MotionVector{X: int16(fx), Y: int16(fy)}
							want := bytes.Repeat([]byte{0xa5}, h*outStride+11)
							got := append([]byte(nil), want...)
							interPredLumaH264Scalar(want, outStride, ref, stride, base[0], base[1], w, h, mv)
							InterPredLumaH264(got, outStride, ref, stride, base[0], base[1], w, h, mv)
							if !bytes.Equal(got, want) {
								t.Fatalf("stride=%d pattern=%d dim=%v base=%v mv=%v", stride, pattern, dim, base, mv)
							}
						}
					}
				}
			}
		}
	}
}

func TestLumaAliasUsesLegacyWriteThrough(t *testing.T) {
	for _, offset := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {27, 0}, {0, 27}} {
		for fx := 0; fx < 4; fx++ {
			for fy := 0; fy < 4; fy++ {
				want := make([]byte, 1024)
				for i := range want {
					want[i] = byte(i*77 + 13)
				}
				got := append([]byte(nil), want...)
				mv := MotionVector{X: int16(fx), Y: int16(fy)}
				interPredLumaH264Scalar(want[offset[0]:], 24, want[offset[1]:], 24, 2, 2, 16, 16, mv)
				InterPredLumaH264(got[offset[0]:], 24, got[offset[1]:], 24, 2, 2, 16, 16, mv)
				if !bytes.Equal(got, want) {
					t.Fatalf("offset=%v mv=%v", offset, mv)
				}
			}
		}
	}
}

func TestLumaRejectedInputsUnchanged(t *testing.T) {
	for _, tc := range []struct{ outLen, stride, refLen, refStride, w, h int }{
		{255, 16, 1024, 32, 16, 16}, {256, 16, 1024, 0, 16, 16}, {256, 16, 0, 32, 16, 16}, {256, 16, 1024, -1, 16, 16}, {256, 16, 1024, 32, 0, 16}, {256, 16, 1024, 32, 16, 0},
	} {
		out := bytes.Repeat([]byte{0xa5}, tc.outLen)
		want := append([]byte(nil), out...)
		InterPredLumaH264(out, tc.stride, make([]byte, tc.refLen), tc.refStride, 0, 0, tc.w, tc.h, MotionVector{1, 2})
		if !bytes.Equal(out, want) {
			t.Fatal(tc)
		}
	}
}

func BenchmarkLumaInterpolation(b *testing.B) {
	for _, size := range []struct {
		name string
		w    int
	}{{"4", 4}, {"8", 8}, {"16", 16}} {
		for _, frac := range []struct {
			name string
			mv   MotionVector
		}{{"integer", MotionVector{0, 0}}, {"H", MotionVector{2, 0}}, {"V", MotionVector{0, 2}}, {"HV", MotionVector{2, 2}}, {"quarterHV", MotionVector{1, 2}}, {"quarterOdd", MotionVector{3, 3}}} {
			for _, scalar := range []bool{false, true} {
				name := "simd"
				if scalar {
					name = "scalar"
				}
				b.Run(size.name+"/"+frac.name+"/"+name, func(b *testing.B) {
					ref := make([]byte, 48*48)
					for i := range ref {
						ref[i] = byte(i*77 + 13)
					}
					var out [256]byte
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if scalar {
							interPredLumaH264Scalar(out[:], 16, ref, 48, 8, 8, size.w, size.w, frac.mv)
						} else {
							InterPredLumaH264(out[:], 16, ref, 48, 8, 8, size.w, size.w, frac.mv)
						}
					}
				})
			}
		}
	}
}

//go:build arm64 && !purego && go1.27

package transform

import (
	"bytes"
	"math/rand"
	"testing"
)

// Compare the fused path with the existing independent wide-arithmetic oracle across QPs,
// signed-16-bit overflow inputs and different row strides. Sentinel bytes catch
// oversized SIMD stores; coefficient immutability permits passing syntax data.
func TestReconstruct4x4MatchesWideOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(764))
	for n := 0; n < 16000; n++ {
		qp := n%53 - 1
		var coeff [16]int16
		for i := range coeff {
			coeff[i] = int16(rng.Intn(65536) - 32768)
		}
		if n < 16*7 {
			coeff = [16]int16{}
			coeff[n%16] = []int16{-32768, -32767, -1, 0, 1, 32766, 32767}[(n/16)%7]
		}
		original := coeff
		ds, ps := 4+n%29, 4+(n/29)%19
		dst := bytes.Repeat([]byte{0xA5}, 3*ds+4+10)
		want := append([]byte(nil), dst...)
		prediction := make([]byte, 3*ps+4)
		rng.Read(prediction)
		residual := coeff
		if qp >= 0 {
			for i := range residual {
				residual[i] *= dequant4Packed[qp][i]
			}
		}
		idct4WideReference(&residual)
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				want[5+y*ds+x] = byte(max(0, min(255, int(prediction[y*ps+x])+int(residual[y*4+x]))))
			}
		}
		if !Reconstruct4x4(dst[5:], prediction, &coeff, ds, ps, qp) {
			t.Skip("NEON disabled")
		}
		if !bytes.Equal(dst, want) {
			t.Fatalf("case %d qp %d mismatch: coeff=%v residual=%v prediction=%v got=%v want=%v", n, qp, coeff, residual, prediction, dst, want)
		}
		if coeff != original {
			t.Fatal("fused kernel modified syntax coefficients")
		}
	}
}

func TestReconstruct4x4RejectsInvalidFootprints(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		dstLen, predLen, dstS, predS int
	}{
		{"empty destination", 0, 16, 4, 4},
		{"short destination", 15, 16, 4, 4},
		{"short prediction", 16, 15, 4, 4},
		{"overlapping output rows", 16, 16, 3, 4},
		{"overlapping prediction rows", 16, 16, 4, 3},
		{"overflowing footprint", 16, 16, int(^uint(0) >> 1), 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := bytes.Repeat([]byte{0xa5}, 16)
			want := bytes.Clone(dst)
			prediction := make([]byte, tc.predLen)
			if Reconstruct4x4(dst[:tc.dstLen], prediction, new([16]int16), tc.dstS, tc.predS, 26) {
				t.Fatal("invalid footprint reached the fused kernel")
			}
			if !bytes.Equal(dst, want) {
				t.Fatal("rejected reconstruction modified the destination")
			}
		})
	}
}

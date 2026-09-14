//go:build amd64 && !purego

package transform

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestReconstruct4x4SSE2MatchesWideOracle(t *testing.T) {
	if !Reconstruct4x4Available() {
		t.Fatal("amd64 fused reconstruction dispatch unavailable")
	}
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
			t.Fatal("SSE2 unavailable")
		}
		if !bytes.Equal(dst, want) {
			t.Fatalf("case %d qp %d mismatch: got=%v want=%v", n, qp, dst, want)
		}
		if coeff != original {
			t.Fatal("fused kernel modified coefficients")
		}
	}
}

func BenchmarkReconstruct4x4SSE2(b *testing.B) {
	var coeff [16]int16
	for i := range coeff {
		coeff[i] = int16(i*37 - 251)
	}
	dst := make([]byte, 64)
	prediction := make([]byte, 64)
	for i := range prediction {
		prediction[i] = byte(i * 17)
	}
	b.Run("dispatch", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Reconstruct4x4(dst, prediction, &coeff, 16, 16, 26)
		}
	})
	b.Run("staged-SSE2", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			residual := coeff
			dequant4SSE2(&residual[0], &dequant4Packed[26][0])
			IDCT4x4_SSE2(&residual[0])
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					dst[y*16+x] = byte(max(0, min(255, int(prediction[y*16+x])+int(residual[y*4+x]))))
				}
			}
		}
	})
}

func TestReconstruct4x4SSE2RejectsInvalidFootprints(t *testing.T) {
	for _, tc := range []struct {
		dstLen, predLen, dstS, predS int
	}{{0, 16, 4, 4}, {15, 16, 4, 4}, {16, 15, 4, 4}, {16, 16, 3, 4}, {16, 16, 4, 3}, {16, 16, int(^uint(0) >> 1), 4}} {
		dst := bytes.Repeat([]byte{0xa5}, 16)
		want := bytes.Clone(dst)
		if Reconstruct4x4(dst[:tc.dstLen], make([]byte, tc.predLen), new([16]int16), tc.dstS, tc.predS, 26) {
			t.Fatal("invalid footprint reached SSE2")
		}
		if !bytes.Equal(dst, want) {
			t.Fatal("rejected reconstruction modified destination")
		}
	}
}

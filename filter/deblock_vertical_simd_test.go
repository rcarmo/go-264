package filter

import (
	"math/rand"
	"testing"
)

func scalarLumaLanes(in lumaVerticalLanes, bS, alpha, beta, indexA int) lumaVerticalLanes {
	out := in
	for i := 0; i < 4; i++ {
		p0, p1, p2, q0, q1, q2 := filterLumaSample(
			int(in.P3[i]), int(in.P2[i]), int(in.P1[i]), int(in.P0[i]),
			int(in.Q0[i]), int(in.Q1[i]), int(in.Q2[i]), int(in.Q3[i]),
			bS, alpha, beta, indexA,
		)
		out.P0[i], out.P1[i], out.P2[i] = int16(p0), int16(p1), int16(p2)
		out.Q0[i], out.Q1[i], out.Q2[i] = int16(q0), int16(q1), int16(q2)
	}
	return out
}

func TestLumaVerticalSIMDExact(t *testing.T) {
	rng := rand.New(rand.NewSource(7264))
	for qp := 0; qp < 52; qp++ {
		alpha, beta := alphaTable[qp], betaTable[qp]
		for bs := 1; bs <= 4; bs++ {
			tc0 := 0
			if bs < 4 {
				tc0 = tc0Table[qp][bs-1]
			}
			for sample := 0; sample < 1200; sample++ {
				var got lumaVerticalLanes
				for lane := 0; lane < 4; lane++ {
					values := [8]int{}
					for i := range values {
						values[i] = rng.Intn(256)
					}
					if sample < 256 {
						// Dense threshold-neighbour cases around a shared edge value.
						p0 := sample
						values = [8]int{p0, p0 + (sample%5 - 2), p0 + (sample%7 - 3), p0,
							p0 + (sample%9 - 4), p0 + (sample%7 - 3), p0 + (sample%5 - 2), p0}
						for i := range values {
							values[i] = Clip3(0, 255, values[i])
						}
					}
					got.P3[lane], got.P2[lane], got.P1[lane], got.P0[lane] = int16(values[0]), int16(values[1]), int16(values[2]), int16(values[3])
					got.Q0[lane], got.Q1[lane], got.Q2[lane], got.Q3[lane] = int16(values[4]), int16(values[5]), int16(values[6]), int16(values[7])
				}
				want := scalarLumaLanes(got, bs, alpha, beta, qp)
				if !filterLuma4SIMD(&got, bs, alpha, beta, (alpha>>2)+2, tc0) {
					t.Skip("SIMD unavailable for this build")
				}
				if got != want {
					t.Fatalf("qp=%d bs=%d sample=%d\ngot=%+v\nwant=%+v", qp, bs, sample, got, want)
				}
			}
		}
	}
}

func filterLumaVerticalGroupScalar(plane []byte, stride, x, firstRow, bs, indexA, indexB int) {
	alpha, beta := alphaTable[indexA], betaTable[indexB]
	for r := 0; r < 4; r++ {
		base := (firstRow+r)*stride + x
		p0, p1, p2, q0, q1, q2 := filterLumaSample(
			int(plane[base-4]), int(plane[base-3]), int(plane[base-2]), int(plane[base-1]),
			int(plane[base]), int(plane[base+1]), int(plane[base+2]), int(plane[base+3]),
			bs, alpha, beta, indexA,
		)
		plane[base-1], plane[base-2], plane[base-3] = p0, p1, p2
		plane[base], plane[base+1], plane[base+2] = q0, q1, q2
	}
}

func scalarChromaLanes(in chromaVerticalLanes, bS, alpha, beta, indexA int) chromaVerticalLanes {
	out := in
	for i := 0; i < 2; i++ {
		p0, q0 := filterChromaSample(int(in.P1[i]), int(in.P0[i]), int(in.Q0[i]), int(in.Q1[i]), bS, alpha, beta, indexA)
		out.P0[i], out.Q0[i] = int16(p0), int16(q0)
	}
	return out
}

func TestChromaVerticalSIMDExact(t *testing.T) {
	rng := rand.New(rand.NewSource(7265))
	for qp := 0; qp < 52; qp++ {
		alpha, beta := alphaTable[qp], betaTable[qp]
		for bs := 1; bs <= 4; bs++ {
			tc := 0
			if bs < 4 {
				tc = tc0Table[qp][bs-1] + 1
			}
			for sample := 0; sample < 1800; sample++ {
				var got chromaVerticalLanes
				for lane := 0; lane < 2; lane++ {
					values := [4]int{rng.Intn(256), rng.Intn(256), rng.Intn(256), rng.Intn(256)}
					if sample < 256 {
						p0 := sample
						values = [4]int{p0 + (sample%7 - 3), p0, p0 + (sample%9 - 4), p0 + (sample%7 - 3)}
						for i := range values {
							values[i] = Clip3(0, 255, values[i])
						}
					}
					got.P1[lane], got.P0[lane], got.Q0[lane], got.Q1[lane] = int16(values[0]), int16(values[1]), int16(values[2]), int16(values[3])
				}
				want := scalarChromaLanes(got, bs, alpha, beta, qp)
				if !filterChroma2SIMD(&got, bs, alpha, beta, tc) {
					t.Skip("SIMD unavailable for this build")
				}
				if got != want {
					t.Fatalf("qp=%d bs=%d sample=%d got=%+v want=%+v", qp, bs, sample, got, want)
				}
			}
		}
	}
}

func BenchmarkFilterLumaVerticalNormalGroup(b *testing.B) {
	lanes := lumaVerticalLanes{
		P3: [4]int16{100, 101, 102, 103}, P2: [4]int16{112, 113, 114, 115},
		P1: [4]int16{114, 115, 116, 117}, P0: [4]int16{115, 116, 117, 118},
		Q0: [4]int16{118, 119, 120, 121}, Q1: [4]int16{120, 121, 122, 123},
		Q2: [4]int16{121, 122, 123, 124}, Q3: [4]int16{122, 123, 124, 125},
	}
	alpha, beta, tc0 := alphaTable[31], betaTable[31], tc0Table[31][1]
	b.Run("scalar", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = scalarLumaLanes(lanes, 2, alpha, beta, 31)
		}
	})
	b.Run("simd", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			v := lanes
			filterLuma4SIMD(&v, 2, alpha, beta, (alpha>>2)+2, tc0)
		}
	})
	var plane [4 * 32]byte
	for r := 0; r < 4; r++ {
		copy(plane[r*32+8:r*32+16], []byte{100, 112, 114, 115, 118, 120, 121, 122})
	}
	b.Run("scalar-gather-store", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filterLumaVerticalGroupScalar(plane[:], 32, 12, 0, 2, 31, 31)
		}
	})
	b.Run("simd-gather-store", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filterLumaVerticalGroupFast(plane[:], 32, 12, 0, 2, alpha, beta, (alpha>>2)+2, tc0)
		}
	})
}

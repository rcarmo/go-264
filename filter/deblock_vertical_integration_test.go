package filter

import (
	"bytes"
	"math/rand"
	"testing"
)

func filterLumaEdgeVReference(plane []byte, stride, x, rowStart, nrows int, bS [4]int, indexA, indexB int) {
	if x < 4 || x+4 > stride || indexA < 0 || indexA > 51 || indexB < 0 || indexB > 51 {
		return
	}
	alpha, beta := alphaTable[indexA], betaTable[indexB]
	for g := 0; g < nrows/4 && g < 4; g++ {
		if bS[g] == 0 {
			continue
		}
		for r := 0; r < 4; r++ {
			base := (rowStart+g*4+r)*stride + x
			if base+4 > len(plane) || base-4 < 0 {
				continue
			}
			p0, p1, p2, q0, q1, q2 := filterLumaSample(
				int(plane[base-4]), int(plane[base-3]), int(plane[base-2]), int(plane[base-1]),
				int(plane[base]), int(plane[base+1]), int(plane[base+2]), int(plane[base+3]),
				bS[g], alpha, beta, indexA,
			)
			plane[base-1], plane[base-2], plane[base-3] = p0, p1, p2
			plane[base], plane[base+1], plane[base+2] = q0, q1, q2
		}
	}
}

func filterChromaEdgeVReference(plane []byte, stride, x, rowStart, nrows int, bS [4]int, indexA, indexB int) {
	if x < 2 || x+2 > stride || indexA < 0 || indexA > 51 || indexB < 0 || indexB > 51 {
		return
	}
	alpha, beta := alphaTable[indexA], betaTable[indexB]
	for g := 0; g < nrows/2 && g < 4; g++ {
		if bS[g] == 0 {
			continue
		}
		for r := 0; r < 2; r++ {
			base := (rowStart+g*2+r)*stride + x
			if base+2 > len(plane) || base-2 < 0 {
				continue
			}
			p0, q0 := filterChromaSample(int(plane[base-2]), int(plane[base-1]), int(plane[base]), int(plane[base+1]), bS[g], alpha, beta, indexA)
			plane[base-1], plane[base] = p0, q0
		}
	}
}

func TestVerticalDeblockDispatchMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(87264))
	for _, stride := range []int{8, 17, 32, 67} {
		height := 24
		for sample := 0; sample < 3000; sample++ {
			src := make([]byte, stride*height)
			for i := range src {
				src[i] = byte(rng.Intn(256))
			}
			ia, ib := rng.Intn(52), rng.Intn(52)
			bs := [4]int{rng.Intn(5), rng.Intn(5), rng.Intn(5), rng.Intn(5)}
			x := 4
			if stride > 8 {
				x = 4 + rng.Intn(stride-7)
			}
			rowStart := rng.Intn(5)
			got, want := append([]byte(nil), src...), append([]byte(nil), src...)
			FilterLumaEdgeV(got, stride, x, rowStart, 16, bs, ia, ib)
			filterLumaEdgeVReference(want, stride, x, rowStart, 16, bs, ia, ib)
			if !bytes.Equal(got, want) {
				t.Fatalf("luma stride=%d sample=%d x=%d row=%d ia=%d ib=%d bs=%v", stride, sample, x, rowStart, ia, ib, bs)
			}
			if stride >= 4 {
				cx := 2
				if stride > 4 {
					cx = 2 + rng.Intn(stride-3)
				}
				got, want = append([]byte(nil), src...), append([]byte(nil), src...)
				FilterChromaEdgeV(got, stride, cx, rowStart, 8, bs, ia, ib)
				filterChromaEdgeVReference(want, stride, cx, rowStart, 8, bs, ia, ib)
				if !bytes.Equal(got, want) {
					t.Fatalf("chroma stride=%d sample=%d x=%d row=%d ia=%d ib=%d bs=%v", stride, sample, cx, rowStart, ia, ib, bs)
				}
			}
		}
	}
}

func BenchmarkVerticalDeblockGroups(b *testing.B) {
	plane := make([]byte, 32*16)
	for i := range plane {
		plane[i] = byte(i*17 + 101)
	}
	bs := [4]int{2, 2, 2, 2}
	b.Run("luma", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			FilterLumaEdgeV(plane, 32, 8, 0, 16, bs, 31, 31)
		}
	})
	b.Run("chroma", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			FilterChromaEdgeV(plane, 32, 4, 0, 8, bs, 31, 31)
		}
	})
}

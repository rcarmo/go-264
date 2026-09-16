package decode

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestResidualAddStoreBoundariesAndStrides(t *testing.T) {
	residuals := []int16{-32768, -1024, -256, -255, -254, -1, 0, 1, 254, 255, 256, 1024, 32767}
	predictions := []byte{0, 1, 127, 128, 254, 255}
	for _, w := range []int{4, 8} {
		for _, dstStride := range []int{w, w + 3, 19} {
			for _, predStride := range []int{w, w + 5, 23} {
				for _, resStride := range []int{w, w + 2, 17} {
					h := 8
					dn, pn, rn := (h-1)*dstStride+w, (h-1)*predStride+w, (h-1)*resStride+w
					for _, rv := range residuals {
						for _, pv := range predictions {
							dst := bytes.Repeat([]byte{0xa5}, dn+11)
							want := append([]byte(nil), dst...)
							pred := bytes.Repeat([]byte{pv}, pn+13)
							res := make([]int16, rn+7)
							for i := range res {
								res[i] = rv
							}
							residualAddStoreScalar(want, dstStride, pred, predStride, res, resStride, w, h)
							residualAddStore(dst, dstStride, pred, predStride, res, resStride, w, h)
							if !bytes.Equal(dst, want) {
								for i := range dst {
									if dst[i] != want[i] {
										t.Fatalf("w=%d ds=%d ps=%d rs=%d residual=%d pred=%d index=%d got=%d want=%d", w, dstStride, predStride, resStride, rv, pv, i, dst[i], want[i])
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestResidualAddStoreRandomAndAliases(t *testing.T) {
	rng := rand.New(rand.NewSource(10264))
	for _, w := range []int{4, 8} {
		for sample := 0; sample < 10000; sample++ {
			stride, h := 16, 8
			pred := make([]byte, stride*h)
			res := make([]int16, stride*h)
			for i := range pred {
				pred[i] = byte(rng.Intn(256))
				res[i] = int16(rng.Intn(65536) - 32768)
			}
			want := bytes.Repeat([]byte{0xa5}, stride*h)
			got := append([]byte(nil), want...)
			residualAddStoreScalar(want, stride, pred, stride, res, stride, w, h)
			residualAddStore(got, stride, pred, stride, res, stride, w, h)
			if !bytes.Equal(got, want) {
				t.Fatalf("random w=%d sample=%d", w, sample)
			}
			inPlaceWant := append([]byte(nil), pred...)
			inPlaceGot := append([]byte(nil), pred...)
			residualAddStoreScalar(inPlaceWant, stride, inPlaceWant, stride, res, stride, w, h)
			residualAddStore(inPlaceGot, stride, inPlaceGot, stride, res, stride, w, h)
			if !bytes.Equal(inPlaceGot, inPlaceWant) {
				t.Fatalf("in-place w=%d sample=%d", w, sample)
			}
		}
	}
}

func TestResidualAddStorePartialAliasAndInvalid(t *testing.T) {
	backWant := make([]byte, 256)
	for i := range backWant {
		backWant[i] = byte(i*73 + 19)
	}
	backGot := append([]byte(nil), backWant...)
	res := make([]int16, 128)
	for i := range res {
		res[i] = int16(i*997 - 32000)
	}
	residualAddStoreScalar(backWant[3:], 16, backWant[1:], 16, res, 16, 8, 8)
	residualAddStore(backGot[3:], 16, backGot[1:], 16, res, 16, 8, 8)
	if !bytes.Equal(backGot, backWant) {
		t.Fatal("partial alias")
	}
	for _, tc := range []struct{ ds, ps, rs, w, h int }{{3, 4, 4, 4, 1}, {4, 3, 4, 4, 1}, {4, 4, 3, 4, 1}, {4, 4, 4, 0, 1}, {4, 4, 4, 4, 0}, {8, 8, 8, 8, 2}} {
		dst := bytes.Repeat([]byte{0xa5}, 8)
		residualAddStore(dst, tc.ds, make([]byte, 8), tc.ps, make([]int16, 8), tc.rs, tc.w, tc.h)
		if !bytes.Equal(dst, bytes.Repeat([]byte{0xa5}, 8)) {
			t.Fatal(tc)
		}
	}
}

func TestResidualAddStoreZeroAlloc(t *testing.T) {
	var dst, pred [128]byte
	var residual [128]int16
	if n := testing.AllocsPerRun(1000, func() { residualAddStore(dst[:], 16, pred[:], 16, residual[:], 16, 8, 8) }); n != 0 {
		t.Fatal(n)
	}
}

func BenchmarkResidualAddStore(b *testing.B) {
	var dst, pred [128]byte
	var residual [128]int16
	for i := range pred {
		pred[i], residual[i] = byte(i*73), int16(i*997-32000)
	}
	for _, w := range []int{4, 8} {
		b.Run("scalar-"+string(rune('0'+w)), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				residualAddStoreScalar(dst[:], 16, pred[:], 16, residual[:], 16, w, w)
			}
		})
		b.Run("dispatch-"+string(rune('0'+w)), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				residualAddStore(dst[:], 16, pred[:], 16, residual[:], 16, w, w)
			}
		})
	}
}

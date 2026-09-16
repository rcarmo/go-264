package convert

import (
	"math"
	"reflect"
	"testing"
)

func TestS16(t *testing.T) {
	src := []float64{-2, -1, -0.5, -0.5 / 32768, 0, 0.5 / 32768, 0.5, 1, 2}
	dst := make([]int16, len(src))
	if e := S16(dst, src); e != nil {
		t.Fatal(e)
	}
	want := []int16{-32768, -32768, -16384, -1, 0, 1, 16384, 32767, 32767}
	if !reflect.DeepEqual(dst, want) {
		t.Fatal(dst)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if e := S16(dst, []float64{v}); e == nil {
			t.Fatal(v)
		}
	}
	if S16(nil, src) == nil {
		t.Fatal("short dst")
	}
}
func TestInterleave(t *testing.T) {
	dst := make([]float64, 6)
	if e := Interleave(dst, [][]float64{{1, 2, 3}, {4, 5, 6}}); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(dst, []float64{1, 4, 2, 5, 3, 6}) {
		t.Fatal(dst)
	}
	if Interleave(dst, [][]float64{{1}, {2, 3}}) == nil {
		t.Fatal("shape")
	}
}

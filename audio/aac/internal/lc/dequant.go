package lc

import "math"

const quantLimit = 8191

// The bounded AAC magnitude domain permits an exact lookup of the original
// a*Cbrt(a) expression. The table changes no rounding and is not an approximation.
// Signed entries avoid per-lane sign branches when applying the band scale.
var dequantTable = func() [2*quantLimit + 1]float64 {
	var table [2*quantLimit + 1]float64
	for q := -quantLimit; q <= quantLimit; q++ {
		table[q+quantLimit] = dequantMagnitude(int32(q))
	}
	return table
}()

func dequantMagnitude(q int32) float64 {
	x := float64(q)
	a := math.Abs(x)
	x = a * math.Cbrt(a)
	if q < 0 {
		x = -x
	}
	return x
}

// dequantBand validates every index before dispatch. A false return leaves dst
// unchanged. Internal callers use equally sized, disjoint source/destination.
func dequantBand(dst []float64, quant []int32, scale float64) bool {
	if len(dst) != len(quant) {
		return false
	}
	for _, q := range quant {
		if q < -quantLimit || q > quantLimit {
			return false
		}
	}
	dequantApply(dst, quant, scale)
	return true
}

func dequantTableScalar(dst []float64, quant []int32, scale float64) {
	for i, q := range quant {
		dst[i] = dequantTable[int(q)+quantLimit] * scale
	}
}

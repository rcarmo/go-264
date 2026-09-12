package lc

func scaleBandScalar(dst, src []float64, gain float64) {
	for i := range dst {
		dst[i] = src[i] * gain
	}
}
func midSideScalar(left, right []float64) {
	for i, l := range left {
		r := right[i]
		left[i], right[i] = l+r, l-r
	}
}

// tnsBandScalar retains sequential sample feedback and ordered per-coefficient
// subtraction. SIMD implementations may pack products, never reassociate them.
func tnsBandScalar(spec, coeff []float64, reverse bool) {
	var hist [12]float64
	for n := 0; n < len(spec); n++ {
		i := n
		if reverse {
			i = len(spec) - 1 - n
		}
		y := spec[i]
		for j, a := range coeff {
			y -= a * hist[j]
		}
		for j := len(coeff) - 1; j > 0; j-- {
			hist[j] = hist[j-1]
		}
		hist[0] = y
		spec[i] = y
	}
}

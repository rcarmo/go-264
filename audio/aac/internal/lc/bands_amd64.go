//go:build amd64 && !purego

package lc

func scaleBand(dst, src []float64, gain float64) {
	if len(dst) > 0 {
		scaleBandSSE2(&dst[0], &src[0], len(dst), gain)
	}
}
func midSide(left, right []float64) {
	if len(left) > 0 {
		midSideSSE2(&left[0], &right[0], len(left))
	}
}
func tnsBand(spec, coeff []float64, reverse bool) {
	if len(spec) == 0 || len(coeff) == 0 {
		return
	}
	step := 8
	p := &spec[0]
	if reverse {
		step = -8
		p = &spec[len(spec)-1]
	}
	tnsBandSSE2(p, &coeff[0], len(spec), len(coeff), step)
}

//go:noescape
func scaleBandSSE2(dst, src *float64, n int, gain float64)

//go:noescape
func midSideSSE2(left, right *float64, n int)

//go:noescape
func tnsBandSSE2(spec, coeff *float64, n, order, step int)

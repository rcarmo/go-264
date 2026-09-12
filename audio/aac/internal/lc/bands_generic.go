//go:build !amd64 || purego

package lc

func scaleBand(dst, src []float64, gain float64)  { scaleBandScalar(dst, src, gain) }
func midSide(left, right []float64)               { midSideScalar(left, right) }
func tnsBand(spec, coeff []float64, reverse bool) { tnsBandScalar(spec, coeff, reverse) }

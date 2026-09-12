//go:build !amd64 || purego

package lc

func dequantApply(dst []float64, quant []int32, scale float64) {
	dequantTableScalar(dst, quant, scale)
}

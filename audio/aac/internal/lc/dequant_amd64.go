//go:build amd64 && !purego

package lc

func dequantApply(dst []float64, quant []int32, scale float64) {
	if len(quant) != 0 {
		dequantSSE2(&dst[0], &quant[0], &dequantTable[quantLimit], len(quant), scale)
	}
}

//go:noescape
func dequantSSE2(dst *float64, quant *int32, tableZero *float64, n int, scale float64)

//go:build arm64 && !purego

package lc

func dequantApply(dst []float64, quant []int32, scale float64) {
	if len(quant) != 0 {
		dequantNEON(&dst[0], &quant[0], &dequantTable[quantLimit], len(quant), scale)
	}
}

//go:noescape
func dequantNEON(dst *float64, quant *int32, tableZero *float64, n int, scale float64)

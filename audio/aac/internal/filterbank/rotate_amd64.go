//go:build amd64 && !purego

package filterbank

func rotateInput(dst []complex128, coeff []float64, rotation []complex128) {
	if len(coeff) != 0 {
		rotateInputSSE2(&dst[0], &coeff[0], &rotation[0], len(coeff))
	}
}

func rotateOutput(dst []float64, src, rotation []complex128, scale float64) {
	if len(src) != 0 {
		rotateOutputSSE2(&dst[0], &src[0], &rotation[0], len(src), scale)
	}
}

//go:noescape
func rotateInputSSE2(dst *complex128, coeff *float64, rotation *complex128, n int)

//go:noescape
func rotateOutputSSE2(dst *float64, src, rotation *complex128, n int, scale float64)

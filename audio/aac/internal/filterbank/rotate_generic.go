//go:build !amd64 || purego

package filterbank

func rotateInput(dst []complex128, coeff []float64, rotation []complex128) {
	rotateInputScalar(dst, coeff, rotation)
}

func rotateOutput(dst []float64, src, rotation []complex128, scale float64) {
	rotateOutputScalar(dst, src, rotation, scale)
}

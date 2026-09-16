package filterbank

// rotateInputScalar and rotateOutputScalar retain the original complex
// multiplication order, including signed-zero terms and the final scale.
func rotateInputScalar(dst []complex128, coeff []float64, rotation []complex128) {
	for i, c := range coeff {
		dst[i] = complex(c, 0) * rotation[i]
	}
}

func rotateOutputScalar(dst []float64, src, rotation []complex128, scale float64) {
	for i := range src {
		dst[i] = real(src[i]*rotation[i]) * scale
	}
}

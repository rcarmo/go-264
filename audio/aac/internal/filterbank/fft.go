package filterbank

// fftStageScalar is the operation-order reference for the positive-sign radix-2
// FFT butterfly stage. Callers supply valid transform-plan geometry. Independent
// butterflies may be vectorised; their multiply/add/subtract order is retained.
func fftStageScalar(x, roots []complex128, half, stride int) {
	for base := 0; base < len(x); base += 2 * half {
		for k := 0; k < half; k++ {
			u := x[base+k]
			v := x[base+k+half] * roots[k*stride]
			x[base+k] = u + v
			x[base+k+half] = u - v
		}
	}
}

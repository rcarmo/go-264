package resample

// dotScalar is the ordered arithmetic oracle: multiply then add in index
// order, with no FMA contraction or reassociation. Inputs must be equal length.
func dotScalar(a, b []float64) float64 {
	sum := 0.0
	for i, v := range a {
		sum += v * b[i]
	}
	return sum
}

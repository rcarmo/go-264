//go:build !amd64 || purego

package resample

func dot(a, b []float64) float64 { return dotScalar(a, b) }

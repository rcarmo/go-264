//go:build !amd64 || purego

package convert

func allFinite(src []float64) bool { return allFiniteScalar(src) }

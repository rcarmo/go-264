//go:build !amd64 || purego

package filter

func filterLuma4SIMD(lanes *lumaVerticalLanes, bS, alpha, beta, alphaQ2, tc0 int) bool {
	return false
}

func filterChroma2SIMD(lanes *chromaVerticalLanes, bS, alpha, beta, tc int) bool {
	return false
}

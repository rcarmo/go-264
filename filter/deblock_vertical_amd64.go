//go:build amd64 && !purego

package filter

func filterLuma4SIMD(lanes *lumaVerticalLanes, bS, alpha, beta, alphaQ2, tc0 int) bool {
	if lanes == nil || bS < 1 || bS > 4 || alpha < 0 || alpha > 255 || beta < 0 || beta > 255 {
		return false
	}
	if bS == 4 {
		filterLuma4StrongSSE2(lanes, alpha, beta, alphaQ2)
	} else {
		filterLuma4NormalSSE2(lanes, alpha, beta, tc0)
	}
	return true
}

func filterChroma2SIMD(lanes *chromaVerticalLanes, bS, alpha, beta, tc int) bool {
	if lanes == nil || bS < 1 || bS > 4 || alpha < 0 || alpha > 255 || beta < 0 || beta > 255 {
		return false
	}
	if bS == 4 {
		filterChroma2StrongSSE2(lanes, alpha, beta)
	} else {
		filterChroma2NormalSSE2(lanes, alpha, beta, tc)
	}
	return true
}

//go:noescape
func filterLuma4NormalSSE2(lanes *lumaVerticalLanes, alpha, beta, tc0 int)

//go:noescape
func filterLuma4StrongSSE2(lanes *lumaVerticalLanes, alpha, beta, alphaQ2 int)

//go:noescape
func filterChroma2NormalSSE2(lanes *chromaVerticalLanes, alpha, beta, tc int)

//go:noescape
func filterChroma2StrongSSE2(lanes *chromaVerticalLanes, alpha, beta int)

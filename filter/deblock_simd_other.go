//go:build !arm64 || purego || !go1.27

package filter

func filterLumaNormalHSIMD(plane []byte, stride, y, col, bs, alpha, beta, indexA int) bool {
	return false
}

func filterLumaNormalVSIMD(plane []byte, stride, x, row, bs, alpha, beta, indexA int) bool {
	return false
}

func filterChromaHSIMD(plane []byte, stride, y, col, ncols int, bs *[4]int, alpha, beta, indexA int) bool {
	return false
}

func filterChromaVSIMD(plane []byte, stride, x, row, nrows int, bs *[4]int, alpha, beta, indexA int) bool {
	return false
}

func filterLumaPairHSIMD(plane []byte, stride, y, col, bs0, bs1, alpha, beta, indexA int) bool {
	return false
}

func filterLumaPairVSIMD(plane []byte, stride, x, row, bs0, bs1, alpha, beta, indexA int) bool {
	return false
}

//go:build (!amd64 && !arm64) || purego

package decode

func biBlendFast(dst, predL0, predL1 []byte, stride, w, h, w0, w1, round, shift, offset int, plainAverage bool) bool {
	return false
}

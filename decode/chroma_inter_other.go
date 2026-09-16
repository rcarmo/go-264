//go:build (!amd64 && !arm64) || purego || (arm64 && !go1.27)

package decode

func chromaInterSIMD(dst, src []byte, stride, w, h, wa, wb, wc, wd int) bool {
	return false
}

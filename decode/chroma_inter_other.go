//go:build !arm64 || purego || !go1.27

package decode

func chromaInterSIMD(dst, src []byte, stride, w, h, wa, wb, wc, wd int) bool {
	return false
}

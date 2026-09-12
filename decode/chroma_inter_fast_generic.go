//go:build !amd64 || purego

package decode

func chromaInter8Fast(dst, plane []byte, stride, width, height, sx, sy, fracX, fracY int) bool {
	return false
}

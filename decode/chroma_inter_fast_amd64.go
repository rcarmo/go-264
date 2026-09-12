//go:build amd64 && !purego

package decode

import "unsafe"

func chromaInter8Fast(dst, plane []byte, stride, width, height, sx, sy, fracX, fracY int) bool {
	if len(dst) < 64 || stride <= 0 || width < 9 || width > stride || height < 9 || fracX < 0 || fracX > 7 || fracY < 0 || fracY > 7 || sx < 0 || sy < 0 || sx > width-9 || sy > height-9 || len(plane) < sx+9 || sy > (len(plane)-sx-9)/stride || byteSlicesOverlap(dst[:64], plane) {
		return false
	}
	chromaInter8SSE2(&dst[0], &plane[sy*stride+sx], stride, fracX, fracY)
	return true
}

func byteSlicesOverlap(a, b []byte) bool {
	ap, bp := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&b[0]))
	return ap <= bp && bp-ap < uintptr(len(a)) || bp < ap && ap-bp < uintptr(len(b))
}

//go:noescape
func chromaInter8SSE2(dst, src *byte, stride, fracX, fracY int)

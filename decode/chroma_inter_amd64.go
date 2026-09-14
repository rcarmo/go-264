//go:build amd64 && !purego

package decode

// chromaBilinearSSE2 reads a w+1 by h+1 source footprint and writes w samples
// per destination row (stride 8). Width is 2, 4 or 8; height is positive.
//
//go:noescape
func chromaBilinearSSE2(dst, src *byte, stride, w, h, wa, wb, wc, wd int)

// chromaInterSIMD mirrors the NEON kernel's supported partition widths. The
// caller has already validated the complete source and destination footprints.
func chromaInterSIMD(dst, src []byte, stride, w, h, wa, wb, wc, wd int) bool {
	if w != 2 && w != 4 && w != 8 {
		return false
	}
	chromaBilinearSSE2(&dst[0], &src[0], stride, w, h, wa, wb, wc, wd)
	return true
}

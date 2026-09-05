//go:build arm64 && !purego && go1.27

package decode

import "github.com/rcarmo/go-264/pred"

// chromaBilinearNEON reads a w+1 by h+1 source footprint and writes w samples
// per destination row (stride 8). Width is 2, 4 or 8; height is positive.
// Each load/store uses the exact row width, including two-wide partitions.
//
//go:noescape
func chromaBilinearNEON(dst, src *byte, stride, w, h, wa, wb, wc, wd int)

// chromaInterSIMD handles only the already-validated interior source rectangle.
// Keep the shared prediction switch so scalar conformance runs bypass all NEON.
func chromaInterSIMD(dst, src []byte, stride, w, h, wa, wb, wc, wd int) bool {
	if !pred.HasNEON || (w != 2 && w != 4 && w != 8) {
		return false
	}
	chromaBilinearNEON(&dst[0], &src[0], stride, w, h, wa, wb, wc, wd)
	return true
}

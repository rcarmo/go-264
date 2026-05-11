//go:build !amd64 && !arm64

package pred

import "unsafe"

var HasSSE2 = false

func IntraPred16x16DC_ASM(pred *uint8, dc uint8) {
	if pred == nil {
		return
	}
	buf := unsafe.Slice(pred, 256)
	for i := range buf {
		buf[i] = dc
	}
}

func IntraPred16x16V_ASM(pred *uint8, top *uint8) {
	if pred == nil || top == nil {
		return
	}
	buf := unsafe.Slice(pred, 256)
	t := unsafe.Slice(top, 16)
	for y := 0; y < 16; y++ {
		copy(buf[y*16:(y+1)*16], t)
	}
}

func IntraPred16x16H_ASM(pred *uint8, left *uint8) {
	if pred == nil || left == nil {
		return
	}
	buf := unsafe.Slice(pred, 256)
	l := unsafe.Slice(left, 16)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			buf[y*16+x] = l[y]
		}
	}
}

func hasNEONPred() bool                             { return false }
func intraPred16x16DC_NEON(pred *uint8, dc uint8)   { IntraPred16x16DC_ASM(pred, dc) }
func intraPred16x16V_NEON(pred *uint8, top *uint8)  { IntraPred16x16V_ASM(pred, top) }
func intraPred16x16H_NEON(pred *uint8, left *uint8) { IntraPred16x16H_ASM(pred, left) }

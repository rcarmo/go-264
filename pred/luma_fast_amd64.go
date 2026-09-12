//go:build amd64 && !purego

package pred

import "unsafe"

// These kernels only see bounded, padded local scratch, never reference-plane
// edges. H fits signed16 [-2550,10710]; HV uses signed32 until final clipping.
//
//go:noescape
func lumaH16(raw *int16, half *byte, src *byte, rows, width int)

//go:noescape
func lumaV8(half *byte, src *byte, rows, width int)

//go:noescape
func lumaHV4(half *byte, raw *int16, rows, width int)

//go:noescape
func lumaAvg(out *byte, outStride int, a *byte, aStride int, b *byte, bStride int, w, h int)

func lumaSlicesOverlap(a, b []byte) bool {
	ap, bp := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&b[0]))
	return ap <= bp && bp-ap < uintptr(len(a)) || bp < ap && ap-bp < uintptr(len(b))
}

func interLumaFast(out []byte, outStride int, ref []byte, refStride, baseX, baseY, w, h int, mv MotionVector) bool {
	// Unsupported/invalid shapes retain the original scalar semantics. Division
	// bounds avoid overflow before any multiplication or assembly call.
	if w <= 0 || w > 16 || h <= 0 || h > 16 || outStride < w || h > len(out)/outStride || refStride <= 0 || len(ref)/refStride == 0 || lumaSlicesOverlap(out, ref) {
		return false
	}
	refH := len(ref) / refStride
	rx, ry := baseX+(int(mv.X)>>2), baseY+(int(mv.Y)>>2)
	// Guard coordinate arithmetic in the padded fast path; unusual overflow
	// inputs go through the unchanged scalar implementation.
	const bound = int(^uint(0)>>1) - 32768
	if baseX > bound || baseX < -bound || baseY > bound || baseY < -bound || rx > bound || rx < -bound || ry > bound || ry < -bound {
		return false
	}
	fx, fy := int(mv.X)&3, int(mv.Y)&3
	if fx == 0 && fy == 0 {
		for y := 0; y < h; y++ {
			row := ref[min(max(ry+y, 0), refH-1)*refStride:]
			if rx >= 0 && rx <= refStride-w {
				copy(out[y*outStride:y*outStride+w], row[rx:rx+w])
			} else {
				for x := 0; x < w; x++ {
					out[y*outStride+x] = row[min(max(rx+x, 0), refStride-1)]
				}
			}
		}
		return true
	}
	const pitch = 24
	var padded [21 * pitch]byte
	width := (w + 7) &^ 7
	start, rows := 0, h+5
	if fy == 0 {
		start, rows = 2, h
	}
	for y := start; y < start+rows; y++ {
		src := ref[min(max(ry+y-2, 0), refH-1)*refStride:]
		dst := padded[y*pitch : y*pitch+width+5]
		if rx >= 2 && rx-2 <= refStride-len(dst) {
			copy(dst, src[rx-2:rx-2+len(dst)])
		} else {
			for x := range dst {
				dst[x] = src[min(max(rx+x-2, 0), refStride-1)]
			}
		}
	}
	var raw [21 * 16]int16
	var horizontal [21 * 16]byte
	var vertical, diagonal [16 * 16]byte
	needHV := fx != 0 && fy != 0 && (fx == 2 || fy == 2)
	if fx != 0 {
		if needHV {
			lumaH16(&raw[0], &horizontal[0], &padded[0], h+5, width)
		} else {
			off := 2
			if fy == 3 {
				off = 3
			}
			lumaH16(&raw[off*16], &horizontal[off*16], &padded[off*pitch], h, width)
		}
	}
	if fy != 0 && fx != 2 {
		col := 2
		if fx == 3 {
			col = 3
		}
		lumaV8(&vertical[0], &padded[col], h, width)
	}
	if needHV {
		lumaHV4(&diagonal[0], &raw[0], h, width)
	}
	var a, b []byte
	as, bs := 16, 16
	hOff := 2 * 16
	if fy == 3 {
		hOff = 3 * 16
	}
	switch {
	case fy == 0:
		a = horizontal[hOff:]
		if fx != 2 {
			col := 2
			if fx == 3 {
				col = 3
			}
			b = padded[2*pitch+col:]
			bs = pitch
		}
	case fx == 0:
		a = vertical[:]
		if fy != 2 {
			row := 2
			if fy == 3 {
				row = 3
			}
			b = padded[row*pitch+2:]
			bs = pitch
		}
	case fx == 2 && fy == 2:
		a = diagonal[:]
	case fx == 2:
		a, b = horizontal[hOff:], diagonal[:]
	case fy == 2:
		a, b = vertical[:], diagonal[:]
	default:
		a, b = horizontal[hOff:], vertical[:]
	}
	if b == nil {
		for y := 0; y < h; y++ {
			copy(out[y*outStride:y*outStride+w], a[y*as:y*as+w])
		}
	} else {
		lumaAvg(&out[0], outStride, &a[0], as, &b[0], bs, w, h)
	}
	return true
}

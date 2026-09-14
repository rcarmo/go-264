package pred

import "unsafe"

// h264LumaHalfPel applies the 6-tap FIR filter [1,-5,20,20,-5,1]/32 at half-pixel positions.
// H.264 §8.4.2.2.1

func clip8i(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func clampRef(ref []uint8, stride, x, y, h int) uint8 {
	if x < 0 {
		x = 0
	} else if x >= stride {
		x = stride - 1
	}
	if y < 0 {
		y = 0
	} else if y >= h {
		y = h - 1
	}
	return ref[y*stride+x]
}

// h264Tap6 returns the unrounded 6-tap sum; callers round after one or both passes.
func h264Tap6(a, b, c, d, e, f int) int {
	return a - 5*b + 20*c + 20*d - 5*e + f
}

// InterPredLumaH264 performs H.264-compliant luma inter prediction for an NxM block.
// It uses the 6-tap FIR filter for half-pel and averaging for quarter-pel.
func InterPredLumaH264(out []uint8, outStride int, ref []uint8, refStride int, baseX, baseY, w, h int, mv MotionVector) {
	if interLumaFast(out, outStride, ref, refStride, baseX, baseY, w, h, mv) {
		return
	}
	// Reusing source samples is valid only while output writes cannot change
	// them. Overlapping library inputs retain their original write-through order.
	if lumaSlicesOverlap(out, ref) {
		interPredLumaH264Scalar(out, outStride, ref, refStride, baseX, baseY, w, h, mv)
		return
	}
	interPredLumaH264Portable(out, outStride, ref, refStride, baseX, baseY, w, h, mv)
}

func lumaSlicesOverlap(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	ap, bp := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&b[0]))
	return ap <= bp && bp-ap < uintptr(len(a)) || bp < ap && ap-bp < uintptr(len(b))
}

// interPredLumaH264Portable shares interpolation work for disjoint source and
// output buffers. The caller retains the legacy scalar path for aliased inputs.
func interPredLumaH264Portable(out []uint8, outStride int, ref []uint8, refStride int, baseX, baseY, w, h int, mv MotionVector) {
	if w <= 0 || h <= 0 || outStride < w || len(out) < w || h-1 > (len(out)-w)/outStride || refStride <= 0 || len(ref) == 0 {
		return
	}
	refH := len(ref) / refStride
	mvx, mvy := int(mv.X), int(mv.Y)
	ix, iy := mvx>>2, mvy>>2
	fx, fy := mvx&3, mvy&3
	sx, sy := baseX+ix, baseY+iy

	getRef := func(x, y int) int {
		return int(clampRef(ref, refStride, x, y, refH))
	}

	if fx == 0 && fy == 0 {
		// Inside the reference plane, integer-pel prediction is just a row copy.
		// Keep per-sample edge extension for rectangles crossing a frame edge.
		if sx >= 0 && sy >= 0 && sx+w <= refStride && sy+h <= refH {
			for y := 0; y < h; y++ {
				copy(out[y*outStride:y*outStride+w], ref[(sy+y)*refStride+sx:(sy+y)*refStride+sx+w])
			}
			return
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out[y*outStride+x] = uint8(getRef(baseX+ix+x, baseY+iy+y))
			}
		}
		return
	}

	// At frame edges, extend the small source footprint once instead of
	// clamping each of its overlapping six-tap reads. Only pad the axes that
	// are fractional; a horizontal filter does not need a vertical halo.
	// Larger/smaller public API blocks retain the existing clipped path.
	if w >= 4 && w <= 16 && h >= 4 && h <= 16 {
		left, top, edgeW, edgeH := 0, 0, w, h
		if fx != 0 {
			left, edgeW = 2, w+5
		}
		if fy != 0 {
			top, edgeH = 2, h+5
		}
		x0, y0 := sx-left, sy-top
		insideX := x0 >= 0 && x0+edgeW <= refStride
		insideY := y0 >= 0 && y0+edgeH <= refH
		// These HV positions already clamp Y once per horizontal scratch row,
		// with no per-tap vertical reads. Copying a halo would add work there.
		rowClampedHV := fx == 2 && fy != 0 && insideX
		if (!insideX || !insideY) && !rowClampedHV {
			var edge [21 * 21]byte
			for y := 0; y < edgeH; y++ {
				ry := max(0, min(y0+y, refH-1))
				row := ref[ry*refStride : (ry+1)*refStride]
				for x := 0; x < edgeW; x++ {
					rx := max(0, min(x0+x, refStride-1))
					edge[y*edgeW+x] = row[rx]
				}
			}
			// The ordinary kernels now see an in-bounds source with unchanged
			// fractional positions, rounding and clipping.
			ref, refStride, refH = edge[:edgeW*edgeH], edgeW, edgeH
			sx, sy = left, top
		}
	}

	if interPredLumaSIMD(out, outStride, ref, refStride, sx, sy, w, h, fx, fy) {
		return
	}

	if fy == 0 {
		// The horizontal filter needs two samples before and three after the
		// block. Check the whole halo once; edge blocks retain sample clamping.
		inside := sx >= 2 && sx+w+3 <= refStride && sy >= 0 && sy+h <= refH
		for y := 0; y < h; y++ {
			ry := sy + y
			var row []uint8
			if inside {
				row = ref[ry*refStride+sx-2 : ry*refStride+sx+w+3]
			}
			for x := 0; x < w; x++ {
				rx := sx + x
				var sum int
				if inside {
					taps := row[x : x+6]
					sum = h264Tap6(int(taps[0]), int(taps[1]), int(taps[2]), int(taps[3]), int(taps[4]), int(taps[5]))
				} else {
					sum = h264Tap6(getRef(rx-2, ry), getRef(rx-1, ry), getRef(rx, ry), getRef(rx+1, ry), getRef(rx+2, ry), getRef(rx+3, ry))
				}
				half := clip8i((sum + 16) >> 5)
				if fx == 2 {
					out[y*outStride+x] = half
				} else {
					var intPel uint8
					if inside {
						intPel = row[x+2+(fx>>1)]
					} else {
						intPel = uint8(getRef(rx+(fx>>1), ry))
					}
					out[y*outStride+x] = uint8((int(intPel) + int(half) + 1) >> 1)
				}
			}
		}
		return
	}

	if fx == 0 {
		inside := sx >= 0 && sx+w <= refStride && sy >= 2 && sy+h+3 <= refH
		for y := 0; y < h; y++ {
			ry := sy + y
			var rows []uint8
			if inside {
				rows = ref[(ry-2)*refStride+sx : (ry+3)*refStride+sx+w]
			}
			for x := 0; x < w; x++ {
				rx := sx + x
				var sum int
				if inside {
					sum = h264Tap6Column(rows[x:], refStride)
				} else {
					sum = h264Tap6(getRef(rx, ry-2), getRef(rx, ry-1), getRef(rx, ry), getRef(rx, ry+1), getRef(rx, ry+2), getRef(rx, ry+3))
				}
				half := clip8i((sum + 16) >> 5)
				if fy == 2 {
					out[y*outStride+x] = half
				} else {
					var intPel uint8
					if inside {
						intPel = rows[(2+(fy>>1))*refStride+x]
					} else {
						intPel = uint8(getRef(rx, ry+(fy>>1)))
					}
					out[y*outStride+x] = uint8((int(intPel) + int(half) + 1) >> 1)
				}
			}
		}
		return
	}

	// A half-pel coordinate needs the true diagonal (horizontal then vertical)
	// result. Share the horizontal pass across output rows and quarter-pel blends.
	if fx == 2 || fy == 2 {
		interPredLumaHV(out, outStride, ref, refStride, sx, sy, w, h, fx, fy)
		return
	}

	// Odd/odd positions average one horizontal and one vertical half-pel;
	// neither needs a diagonal filter. Both halos fit within the same two-
	// before/three-after bounds, including the row/column shift for fraction 3.
	inside := sx >= 2 && sx+w+3 <= refStride && sy >= 2 && sy+h+3 <= refH
	for y := 0; y < h; y++ {
		ry, hRow := sy+y, sy+y+(fy>>1)
		var row, rows []uint8
		if inside {
			row = ref[hRow*refStride+sx-2 : hRow*refStride+sx+w+3]
			col := sx + (fx >> 1)
			rows = ref[(ry-2)*refStride+col : (ry+3)*refStride+col+w]
		}
		for x := 0; x < w; x++ {
			rx, vCol := sx+x, sx+x+(fx>>1)
			var hSum, vSum int
			if inside {
				taps := row[x : x+6]
				hSum = h264Tap6(int(taps[0]), int(taps[1]), int(taps[2]), int(taps[3]), int(taps[4]), int(taps[5]))
				vSum = h264Tap6Column(rows[x:], refStride)
			} else {
				hSum = h264Tap6(getRef(rx-2, hRow), getRef(rx-1, hRow), getRef(rx, hRow), getRef(rx+1, hRow), getRef(rx+2, hRow), getRef(rx+3, hRow))
				vSum = h264Tap6(getRef(vCol, ry-2), getRef(vCol, ry-1), getRef(vCol, ry), getRef(vCol, ry+1), getRef(vCol, ry+2), getRef(vCol, ry+3))
			}
			hHalf, vHalf := clip8i((hSum+16)>>5), clip8i((vSum+16)>>5)
			out[y*outStride+x] = uint8((int(hHalf) + int(vHalf) + 1) >> 1)
		}
	}
}

// h264Tap6Column reads a vertical six-tap footprint whose bounds were checked
// for the complete prediction block before entering its sample loop.
func h264Tap6Column(column []uint8, stride int) int {
	return h264Tap6(int(column[0]), int(column[stride]), int(column[2*stride]),
		int(column[3*stride]), int(column[4*stride]), int(column[5*stride]))
}

// interPredLumaHV filters the five fractional positions that use the diagonal
// half-pel sample. Horizontal sums must remain unrounded until the vertical pass:
// clipping them to pixels first changes the H.264 interpolation result.
func interPredLumaHV(out []uint8, outStride int, ref []uint8, refStride, sx, sy, w, h, fx, fy int) {
	// A 6-tap sum of bytes lies in [-2550,10710], so int16 preserves it exactly.
	// H.264 partitions are at most 16x16; larger public API calls keep working
	// through the dynamically sized fallback without enlarging normal scratch.
	var scratch [(16 + 5) * 16]int16
	n := (h + 5) * w
	horizontal := scratch[:]
	if n > len(horizontal) {
		horizontal = make([]int16, n)
	} else {
		horizontal = horizontal[:n]
	}
	refH := len(ref) / refStride
	insideX := sx >= 2 && sx+w+3 <= refStride
	for y := 0; y < h+5; y++ {
		ry := sy + y - 2
		if ry < 0 {
			ry = 0
		} else if ry >= refH {
			ry = refH - 1
		}
		dst := horizontal[y*w : (y+1)*w]
		if insideX {
			row := ref[ry*refStride+sx-2 : ry*refStride+sx+w+3]
			for x := range dst {
				taps := row[x : x+6]
				dst[x] = int16(h264Tap6(int(taps[0]), int(taps[1]), int(taps[2]), int(taps[3]), int(taps[4]), int(taps[5])))
			}
		} else {
			for x := range dst {
				rx := sx + x
				dst[x] = int16(h264Tap6(
					int(clampRef(ref, refStride, rx-2, ry, refH)), int(clampRef(ref, refStride, rx-1, ry, refH)),
					int(clampRef(ref, refStride, rx, ry, refH)), int(clampRef(ref, refStride, rx+1, ry, refH)),
					int(clampRef(ref, refStride, rx+2, ry, refH)), int(clampRef(ref, refStride, rx+3, ry, refH))))
			}
		}
	}

	verticalX := sx + (fx >> 1)
	insideV := fx != 2 && verticalX >= 0 && verticalX+w <= refStride && sy >= 2 && sy+h+3 <= refH
	for y := 0; y < h; y++ {
		a := horizontal[y*w : (y+1)*w]
		b := horizontal[(y+1)*w : (y+2)*w]
		c := horizontal[(y+2)*w : (y+3)*w]
		d := horizontal[(y+3)*w : (y+4)*w]
		e := horizontal[(y+4)*w : (y+5)*w]
		f := horizontal[(y+5)*w : (y+6)*w]
		dst := out[y*outStride : y*outStride+w]
		var verticalRows []uint8
		if insideV {
			verticalRows = ref[(sy+y-2)*refStride+verticalX : (sy+y+3)*refStride+verticalX+w]
		}
		for x := range dst {
			hv := clip8i((h264Tap6(int(a[x]), int(b[x]), int(c[x]), int(d[x]), int(e[x]), int(f[x])) + 512) >> 10)
			if fx == 2 && fy == 2 {
				dst[x] = hv
				continue
			}
			var half uint8
			if fx == 2 {
				// mc21/mc23 blend HV with the horizontal half-pel on the
				// upper/lower row; that unrounded sum is already in scratch.
				half = clip8i((int(horizontal[(y+2+(fy>>1))*w+x]) + 16) >> 5)
			} else {
				// mc12/mc32 blend HV with the left/right vertical half-pel.
				var sum int
				if insideV {
					sum = h264Tap6Column(verticalRows[x:], refStride)
				} else {
					rx, ry := verticalX+x, sy+y
					sum = h264Tap6(
						int(clampRef(ref, refStride, rx, ry-2, refH)), int(clampRef(ref, refStride, rx, ry-1, refH)),
						int(clampRef(ref, refStride, rx, ry, refH)), int(clampRef(ref, refStride, rx, ry+1, refH)),
						int(clampRef(ref, refStride, rx, ry+2, refH)), int(clampRef(ref, refStride, rx, ry+3, refH)))
				}
				half = clip8i((sum + 16) >> 5)
			}
			dst[x] = uint8((int(hv) + int(half) + 1) >> 1)
		}
	}
}

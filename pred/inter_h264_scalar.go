package pred

// interPredLumaH264Scalar preserves sequential source reads and output writes
// when library callers overlap the two buffers. It also serves as the simple
// scalar reference for architecture-specific interpolation tests.
func interPredLumaH264Scalar(out []uint8, outStride int, ref []uint8, refStride int, baseX, baseY, w, h int, mv MotionVector) {
	if w <= 0 || h <= 0 || outStride < w || len(out) < w || h-1 > (len(out)-w)/outStride || refStride <= 0 || len(ref) == 0 {
		return
	}
	refH := len(ref) / refStride
	mvx, mvy := int(mv.X), int(mv.Y)
	ix, iy := mvx>>2, mvy>>2
	fx, fy := mvx&3, mvy&3

	getRef := func(x, y int) int {
		return int(clampRef(ref, refStride, x, y, refH))
	}

	if fx == 0 && fy == 0 {
		// Integer pel
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out[y*outStride+x] = uint8(getRef(baseX+ix+x, baseY+iy+y))
			}
		}
		return
	}

	if fy == 0 {
		// Horizontal half/quarter pel only
		for y := 0; y < h; y++ {
			ry := baseY + iy + y
			for x := 0; x < w; x++ {
				rx := baseX + ix + x
				if fx == 2 {
					// Half pel horizontal: 6-tap
					v := h264Tap6(getRef(rx-2, ry), getRef(rx-1, ry), getRef(rx, ry), getRef(rx+1, ry), getRef(rx+2, ry), getRef(rx+3, ry))
					out[y*outStride+x] = clip8i((v + 16) >> 5)
				} else {
					// Quarter pel: average of integer and half
					half := h264Tap6(getRef(rx-2, ry), getRef(rx-1, ry), getRef(rx, ry), getRef(rx+1, ry), getRef(rx+2, ry), getRef(rx+3, ry))
					halfPel := clip8i((half + 16) >> 5)
					var intPel uint8
					if fx == 1 {
						intPel = uint8(getRef(rx, ry))
					} else {
						intPel = uint8(getRef(rx+1, ry))
					}
					out[y*outStride+x] = uint8((int(intPel) + int(halfPel) + 1) >> 1)
				}
			}
		}
		return
	}

	if fx == 0 {
		// Vertical half/quarter pel only
		for y := 0; y < h; y++ {
			ry := baseY + iy + y
			for x := 0; x < w; x++ {
				rx := baseX + ix + x
				if fy == 2 {
					v := h264Tap6(getRef(rx, ry-2), getRef(rx, ry-1), getRef(rx, ry), getRef(rx, ry+1), getRef(rx, ry+2), getRef(rx, ry+3))
					out[y*outStride+x] = clip8i((v + 16) >> 5)
				} else {
					half := h264Tap6(getRef(rx, ry-2), getRef(rx, ry-1), getRef(rx, ry), getRef(rx, ry+1), getRef(rx, ry+2), getRef(rx, ry+3))
					halfPel := clip8i((half + 16) >> 5)
					var intPel uint8
					if fy == 1 {
						intPel = uint8(getRef(rx, ry))
					} else {
						intPel = uint8(getRef(rx, ry+1))
					}
					out[y*outStride+x] = uint8((int(intPel) + int(halfPel) + 1) >> 1)
				}
			}
		}
		return
	}

	// Both fx and fy non-zero: diagonal
	// First compute horizontal half-pel at fy positions, then vertical 6-tap on that
	if fx == 2 && fy == 2 {
		// Full half-pel diagonal: H then V on the H result
		// Intermediate: horizontal 6-tap for rows [ry-2..ry+3+h]
		tmpH := make([]int, (h+5)*w)
		for y := -2; y < h+3; y++ {
			ry := baseY + iy + y
			for x := 0; x < w; x++ {
				rx := baseX + ix + x
				tmpH[(y+2)*w+x] = h264Tap6(getRef(rx-2, ry), getRef(rx-1, ry), getRef(rx, ry), getRef(rx+1, ry), getRef(rx+2, ry), getRef(rx+3, ry))
			}
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := h264Tap6(tmpH[(y)*w+x], tmpH[(y+1)*w+x], tmpH[(y+2)*w+x], tmpH[(y+3)*w+x], tmpH[(y+4)*w+x], tmpH[(y+5)*w+x])
				out[y*outStride+x] = clip8i((v + 512) >> 10)
			}
		}
		return
	}

	// Quarter-pel diagonal positions follow FFmpeg's h264_qpel mcXY table:
	// odd/odd positions average a horizontal and a vertical half-pel; positions
	// with one coordinate at half-pel average that directional half-pel with the
	// true diagonal half-pel (HV). Using only H/V halves for mc21/mc12/etc. makes
	// P/B inter prediction visibly drift even when CABAC and MVs are correct.
	hvHalf := func(rx, ry int) uint8 {
		var tmp [6]int
		for yy := -2; yy <= 3; yy++ {
			tmp[yy+2] = h264Tap6(getRef(rx-2, ry+yy), getRef(rx-1, ry+yy), getRef(rx, ry+yy), getRef(rx+1, ry+yy), getRef(rx+2, ry+yy), getRef(rx+3, ry+yy))
		}
		v := h264Tap6(tmp[0], tmp[1], tmp[2], tmp[3], tmp[4], tmp[5])
		return clip8i((v + 512) >> 10)
	}
	for y := 0; y < h; y++ {
		ry := baseY + iy + y
		for x := 0; x < w; x++ {
			rx := baseX + ix + x
			hRow := ry
			if fy == 3 {
				hRow = ry + 1
			}
			hHalf := clip8i((h264Tap6(getRef(rx-2, hRow), getRef(rx-1, hRow), getRef(rx, hRow), getRef(rx+1, hRow), getRef(rx+2, hRow), getRef(rx+3, hRow)) + 16) >> 5)
			vCol := rx
			if fx == 3 {
				vCol = rx + 1
			}
			vHalf := clip8i((h264Tap6(getRef(vCol, ry-2), getRef(vCol, ry-1), getRef(vCol, ry), getRef(vCol, ry+1), getRef(vCol, ry+2), getRef(vCol, ry+3)) + 16) >> 5)
			var a, b uint8
			switch {
			case fx == 2:
				a, b = hHalf, hvHalf(rx, ry)
			case fy == 2:
				a, b = vHalf, hvHalf(rx, ry)
			default:
				a, b = hHalf, vHalf
			}
			out[y*outStride+x] = uint8((int(a) + int(b) + 1) >> 1)
		}
	}
}

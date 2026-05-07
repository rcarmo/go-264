package pred

// H.264 inter prediction (motion compensation).
// ITU-T H.264 §8.4

// MotionVector represents a motion vector in quarter-pixel units.
type MotionVector struct {
	X, Y int16
}

// InterPred16x16 performs motion compensation for a 16x16 block from origin (0,0).
// ref: reference frame luma plane
// mv: motion vector in quarter-pixel units
// stride: reference frame stride
// Output: predicted 16x16 block
func InterPred16x16(out []uint8, ref []uint8, stride int, mv MotionVector) {
	InterPred16x16At(out, ref, stride, 0, 0, mv)
}

// InterPred16x16At performs full-pixel motion compensation for a 16x16 block at
// macroblock origin (baseX, baseY). Fractional MV bits are currently truncated,
// matching the existing decoder path.
//
// Fast path: when the requested 16x16 source rectangle is fully inside the
// reference plane, copy rows with the platform SIMD routine:
//
//	amd64: SSE2 MOVOU row copies
//	arm64: NEON row copies
//
// Scalar fallback handles clipped edges.
func InterPred16x16At(out []uint8, ref []uint8, stride int, baseX, baseY int, mv MotionVector) {
	if len(out) < 256 || stride <= 0 || len(ref) == 0 {
		return
	}
	mvx, mvy := int(mv.X), int(mv.Y)
	ix, iy := mvx>>2, mvy>>2
	fx, fy := mvx&3, mvy&3
	refH := len(ref) / stride
	sx := baseX + ix
	sy := baseY + iy

	if fx == 0 && fy == 0 && (HasSSE2 || hasNEONPred()) && sx >= 0 && sy >= 0 && sx+16 <= stride && sy+16 <= refH {
		InterPred16x16Copy_ASM(&out[0], &ref[sy*stride+sx], 16, stride)
		return
	}
	clip := func(x, y int) int {
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if x >= stride {
			x = stride - 1
		}
		if y >= refH {
			y = refH - 1
		}
		return int(ref[y*stride+x])
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			x0, y0 := sx+x, sy+y
			if fx == 0 && fy == 0 {
				out[y*16+x] = uint8(clip(x0, y0))
				continue
			}
			a := clip(x0, y0)
			b := clip(x0+1, y0)
			c := clip(x0, y0+1)
			d := clip(x0+1, y0+1)
			v := a*(4-fx)*(4-fy) + b*fx*(4-fy) + c*(4-fx)*fy + d*fx*fy
			out[y*16+x] = uint8((v + 8) >> 4)
		}
	}
}

// SubpelFilter6Tap applies the 6-tap FIR filter for half-pixel interpolation.
// Coefficients: [1, -5, 20, 20, -5, 1] / 32
func SubpelFilter6Tap(samples [6]uint8) uint8 {
	v := int(samples[0]) - 5*int(samples[1]) + 20*int(samples[2]) +
		20*int(samples[3]) - 5*int(samples[4]) + int(samples[5])
	v = (v + 16) >> 5 // round
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

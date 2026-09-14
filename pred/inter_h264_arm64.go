//go:build arm64 && !purego && go1.27

package pred

// The SIMD kernels read only their complete six-tap footprint and write exactly
// w samples per row. Four-wide loads/stores are genuinely four bytes, so narrow
// partitions do not require extra readable source or writable destination padding.
//
//go:noescape
func lumaHalfNEON(dst, src *byte, dstStride, srcStride, w, h, tapStep, fraction int)

//go:noescape
func lumaHorizontalRawNEON(dst *int16, src *byte, dstStride, srcStride, w, h int)

//go:noescape
func lumaVerticalRawNEON(dst *byte, src *int16, dstStride, srcStride, w, h int)

//go:noescape
func lumaAverageNEON(dst, src *byte, dstStride, srcStride, w, h int)

// interPredLumaSIMD handles the H.264 partition widths when all required taps
// are readable. Edge-emulated blocks use these same kernels; unusual public API
// dimensions and unpadded edges keep the scalar implementation.
func interPredLumaSIMD(out []byte, outStride int, ref []byte, refStride, sx, sy, w, h, fx, fy int) bool {
	if !HasNEON || (w != 4 && w != 8 && w != 16) || h > 16 {
		return false
	}
	left, top, right, bottom := 0, 0, 0, 0
	if fx != 0 {
		left, right = 2, 3
	}
	if fy != 0 {
		top, bottom = 2, 3
	}
	if sx < left || sy < top || sx+w+right > refStride || sy+h+bottom > len(ref)/refStride {
		return false
	}
	start := sy*refStride + sx
	if fy == 0 {
		lumaHalfNEON(&out[0], &ref[start-2], outStride, refStride, w, h, 1, fx)
		return true
	}
	if fx == 0 {
		lumaHalfNEON(&out[0], &ref[start-2*refStride], outStride, refStride, w, h, refStride, fy)
		return true
	}
	var other [16 * 16]byte
	if fx != 2 && fy != 2 {
		// Odd/odd positions average separately clipped H and V half samples.
		lumaHalfNEON(&out[0], &ref[start+(fy>>1)*refStride-2], outStride, refStride, w, h, 1, 2)
		lumaHalfNEON(&other[0], &ref[start+(fx>>1)-2*refStride], w, refStride, w, h, refStride, 2)
	} else {
		// Preserve the raw horizontal sums for the diagonal pass. Rounding or
		// clipping to bytes before the vertical pass would change the result.
		var horizontal [(16 + 5) * 16]int16
		lumaHorizontalRawNEON(&horizontal[0], &ref[start-2*refStride-2], w*2, refStride, w, h+5)
		lumaVerticalRawNEON(&out[0], &horizontal[0], outStride, w*2, w, h)
		if fx == 2 && fy == 2 {
			return true
		}
		if fx == 2 {
			lumaHalfNEON(&other[0], &ref[start+(fy>>1)*refStride-2], w, refStride, w, h, 1, 2)
		} else {
			lumaHalfNEON(&other[0], &ref[start+(fx>>1)-2*refStride], w, refStride, w, h, refStride, 2)
		}
	}
	lumaAverageNEON(&out[0], &other[0], outStride, w, w, h)
	return true
}

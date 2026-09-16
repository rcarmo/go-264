//go:build arm64 && !purego && go1.27

package filter

import "github.com/rcarmo/go-264/pred"

var HasSIMD = true

// deblockSIMDOffset checks a complete, disjoint kernel rectangle before any
// byte-offset multiplication. Public filter calls can supply arbitrary ints;
// computing an end offset first could wrap and admit an unsafe assembly load.
// rows and cols are fixed positive kernel dimensions at every call site.
func deblockSIMDOffset(plane []byte, stride, row, col, rows, cols int) (int, bool) {
	if stride < cols || row < 0 || col < 0 || col > stride-cols ||
		len(plane) < cols || col > len(plane)-cols {
		return 0, false
	}
	lastRow := (len(plane) - col - cols) / stride
	if row > lastRow-(rows-1) {
		return 0, false
	}
	return row*stride + col, true
}

// filterLumaNormalHNEON filters four columns at a horizontal bS=1..3 edge.
// src starts at p2; six rows are readable, and only p1/p0/q0/q1 are written.
//
//go:noescape
func filterLumaNormalHNEON(src *byte, stride, alpha, beta, tc0 int)

// filterLumaNormalVNEON filters four rows at a vertical bS=1..3 edge. Each
// row contains p3..q3; only its four central samples p1/p0/q0/q1 are written.
//
//go:noescape
func filterLumaNormalVNEON(src *byte, stride, alpha, beta, tc0 int)

// filterLumaNormalHSIMD batches only complete, independent four-column groups.
// Requiring disjoint rows preserves scalar ordering for unusual public inputs;
// partial groups and strong filtering retain the existing sample-wise path.
func filterLumaNormalHSIMD(plane []byte, stride, y, col, bs, alpha, beta, indexA int) bool {
	if !HasSIMD || !pred.HasNEON || bs < 1 || bs > 3 {
		return false
	}
	start, fits := deblockSIMDOffset(plane, stride, y-4, col, 8, 4)
	if !fits {
		return false
	}
	filterLumaNormalHNEON(&plane[start+stride], stride, alpha, beta, tc0Table[indexA][bs-1])
	return true
}

// filterLumaNormalVSIMD handles complete four-row groups; a group extending
// beyond either end of the plane retains the scalar per-row checks.
func filterLumaNormalVSIMD(plane []byte, stride, x, row, bs, alpha, beta, indexA int) bool {
	if !HasSIMD || !pred.HasNEON || bs < 1 || bs > 3 {
		return false
	}
	start, fits := deblockSIMDOffset(plane, stride, row, x-4, 4, 8)
	if !fits {
		return false
	}
	filterLumaNormalVNEON(&plane[start], stride, alpha, beta, tc0Table[indexA][bs-1])
	return true
}

// Pair kernels process two adjacent active normal-strength groups (eight
// samples), with shared alpha/beta and tc0 repeated for each four-sample group.
//
//go:noescape
func filterLumaPairHNEON(src *byte, stride, alpha, beta int, limits uint64)

//go:noescape
func filterLumaPairVNEON(src *byte, stride, alpha, beta int, limits uint64)

func lumaPairLimits(bs0, bs1, indexA int) uint64 {
	return uint64(tc0Table[indexA][bs0-1])*0x01010101 |
		uint64(tc0Table[indexA][bs1-1])*0x01010101<<32
}

// The caller has selected bS=1..3 for both groups. Require fully available,
// disjoint rows; partial public rectangles retain the four-sample fallback.
func filterLumaPairHSIMD(plane []byte, stride, y, col, bs0, bs1, alpha, beta, indexA int) bool {
	if !HasSIMD || !pred.HasNEON {
		return false
	}
	start, fits := deblockSIMDOffset(plane, stride, y-4, col, 8, 8)
	if !fits {
		return false
	}
	filterLumaPairHNEON(&plane[start+stride], stride, alpha, beta, lumaPairLimits(bs0, bs1, indexA))
	return true
}

// Vertical groups also need all eight rows; x/stride were checked by the caller.
func filterLumaPairVSIMD(plane []byte, stride, x, row, bs0, bs1, alpha, beta, indexA int) bool {
	if !HasSIMD || !pred.HasNEON {
		return false
	}
	start, fits := deblockSIMDOffset(plane, stride, row, x-4, 8, 8)
	if !fits {
		return false
	}
	filterLumaPairVNEON(&plane[start], stride, alpha, beta, lumaPairLimits(bs0, bs1, indexA))
	return true
}

// Chroma filtering has two samples per strength group. Process a complete
// eight-sample edge in one call, with each packed strength/limit repeated twice.
// Strong groups use zero normal-filter limit and a separate strong formula.
//
//go:noescape
func filterChromaHNEON(src *byte, stride, alpha, beta int, strengths, limits uint64)

//go:noescape
func filterChromaVNEON(src *byte, stride, alpha, beta int, strengths, limits uint64)

func chromaFilterParameters(bs *[4]int, indexA int) (strengths, limits uint64, ok bool) {
	for g, strength := range bs {
		if strength < 0 || strength > 4 {
			return 0, 0, false
		}
		shift := uint(g * 16)
		strengths |= uint64(strength) * 0x101 << shift
		if strength > 0 && strength < 4 {
			limits |= uint64(tc0Table[indexA][strength-1]+1) * 0x101 << shift
		}
	}
	return strengths, limits, true
}

// filterChromaHSIMD requires complete, disjoint rows. Partial or unusual public
// API rectangles retain the scalar per-sample bounds and processing order.
func filterChromaHSIMD(plane []byte, stride, y, col, ncols int, bs *[4]int, alpha, beta, indexA int) bool {
	if !HasSIMD || !pred.HasNEON || ncols != 8 {
		return false
	}
	start, fits := deblockSIMDOffset(plane, stride, y-2, col, 4, 8)
	if !fits {
		return false
	}
	strengths, limits, ok := chromaFilterParameters(bs, indexA)
	if !ok {
		return false
	}
	if strengths != 0 {
		filterChromaHNEON(&plane[start], stride, alpha, beta, strengths, limits)
	}
	return true
}

// filterChromaVSIMD gathers eight complete rows. The caller's x/stride guard
// already makes their four-byte source windows disjoint.
func filterChromaVSIMD(plane []byte, stride, x, row, nrows int, bs *[4]int, alpha, beta, indexA int) bool {
	if !HasSIMD || !pred.HasNEON || nrows != 8 {
		return false
	}
	start, fits := deblockSIMDOffset(plane, stride, row, x-2, 8, 4)
	if !fits {
		return false
	}
	strengths, limits, ok := chromaFilterParameters(bs, indexA)
	if !ok {
		return false
	}
	if strengths != 0 {
		filterChromaVNEON(&plane[start], stride, alpha, beta, strengths, limits)
	}
	return true
}

package filter

// H.264 in-loop deblocking filter.
// ITU-T H.264 §8.7
//
// Applied at macroblock edges (horizontal and vertical).
// Filters luma and chroma independently.
// Boundary strength (bS) depends on coding modes and non-zero coefficients:
//   bS=4  intra MB boundary edge
//   bS=3  intra internal 4×4 edge
//   bS=2  inter, at least one side has non-zero coefficients
//   bS=1  inter, different ref/MV
//   bS=0  inter, same ref/MV, no non-zero coefficients — skip

// Clip3 clamps v to [lo, hi].
func Clip3(lo, hi, v int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Clip1 clamps to [0, 255].
func Clip1(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Alpha and Beta threshold tables (Table 8-16, 8-17)
var alphaTable = [52]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	4, 4, 5, 6, 7, 8, 9, 10, 12, 13, 15, 17, 20, 22, 25, 28,
	32, 36, 40, 45, 50, 56, 63, 71, 80, 90, 101, 113, 127, 144, 162, 182,
	203, 226, 255, 255,
}

var betaTable = [52]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 6, 6, 7, 7, 8, 8,
	9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16,
	17, 17, 18, 18,
}

// TC0 table (Table 8-18) indexed by [indexA][bS-1]
var tc0Table = [52][3]int{
	{0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0},
	{0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0},
	{0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 0, 1},
	{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 1, 1}, {0, 1, 1}, {1, 1, 1},
	{1, 1, 1}, {1, 1, 1}, {1, 1, 1}, {1, 1, 2}, {1, 1, 2}, {1, 1, 2},
	{1, 1, 2}, {1, 2, 3}, {1, 2, 3}, {2, 2, 3}, {2, 2, 4}, {2, 3, 4},
	{2, 3, 4}, {3, 3, 5}, {3, 4, 6}, {3, 4, 6}, {4, 5, 7}, {4, 5, 8},
	{4, 6, 9}, {5, 7, 10}, {6, 8, 11}, {6, 8, 13}, {7, 10, 14}, {8, 11, 16},
	{9, 12, 18}, {10, 13, 20}, {11, 15, 23}, {13, 17, 25},
}

// FilterEdgeV applies the vertical deblocking filter to a 4-row edge.
// Each row is stored as 8 consecutive pixels: [p3,p2,p1,p0,q0,q1,q2,q3]
// packed in pq at offsets i*stride+[0..7] for row i=0..3.
// This layout avoids negative-index pointer arithmetic and is safe in Go.
// bS: boundary strength (0-4); indexA: QP-derived table index (0-51).
func FilterEdgeV(pq []uint8, stride, bS, indexA int) {
	if bS == 0 || indexA < 0 || indexA > 51 {
		return
	}

	alpha := alphaTable[indexA]
	beta := betaTable[indexA]

	for i := 0; i < 4; i++ {
		base := i * stride
		p3 := int(pq[base+0])
		p2 := int(pq[base+1])
		p1 := int(pq[base+2])
		p0 := int(pq[base+3])
		q0 := int(pq[base+4])
		q1 := int(pq[base+5])
		q2 := int(pq[base+6])
		q3 := int(pq[base+7])

		// Check filter condition
		if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
			continue
		}

		if bS < 4 {
			tc0 := tc0Table[indexA][bS-1]
			tc := tc0
			if abs(p2-p0) < beta {
				tc++
			}
			if abs(q2-q0) < beta {
				tc++
			}
			delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
			pq[base+3] = Clip1(p0 + delta)
			pq[base+4] = Clip1(q0 - delta)
			if abs(p2-p0) < beta {
				pq[base+2] = Clip1(p1 + Clip3(-tc0, tc0, (p2+((p0+q0+1)>>1)-(p1<<1))>>1))
			}
			if abs(q2-q0) < beta {
				pq[base+5] = Clip1(q1 + Clip3(-tc0, tc0, (q2+((p0+q0+1)>>1)-(q1<<1))>>1))
			}
		} else {
			// Strong filter (bS == 4, intra edges)
			if abs(p0-q0) < ((alpha>>2)+2) && abs(p2-p0) < beta {
				pq[base+3] = Clip1((p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4) >> 3)
				pq[base+2] = Clip1((p2 + p1 + p0 + q0 + 2) >> 2)
				pq[base+1] = Clip1((2*p3 + 3*p2 + p1 + p0 + q0 + 4) >> 3)
			} else {
				pq[base+3] = Clip1((2*p1 + p0 + q1 + 2) >> 2)
			}
			if abs(p0-q0) < ((alpha>>2)+2) && abs(q2-q0) < beta {
				pq[base+4] = Clip1((p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4) >> 3)
				pq[base+5] = Clip1((p0 + q0 + q1 + q2 + 2) >> 2)
				pq[base+6] = Clip1((2*q3 + 3*q2 + q1 + q0 + p0 + 4) >> 3)
			} else {
				pq[base+4] = Clip1((2*q1 + q0 + p1 + 2) >> 2)
			}
		}
	}
}

// filterLumaSample applies the deblocking filter to one luma sample pair across
// a vertical or horizontal edge. p/q are the four neighbour values on each side
// (p[0] closest to edge, p[3] furthest). Returns updated (p0,p1,p2,q0,q1,q2).
func filterLumaSample(p3, p2, p1, p0, q0, q1, q2, q3, bS, alpha, beta, indexA int) (rp0, rp1, rp2, rq0, rq1, rq2 uint8) {
	rp0, rp1, rp2 = Clip1(p0), Clip1(p1), Clip1(p2)
	rq0, rq1, rq2 = Clip1(q0), Clip1(q1), Clip1(q2)

	if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
		return
	}
	if bS == 4 {
		if abs(p0-q0) < ((alpha>>2)+2) && abs(p2-p0) < beta {
			rp0 = Clip1((p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4) >> 3)
			rp1 = Clip1((p2 + p1 + p0 + q0 + 2) >> 2)
			rp2 = Clip1((2*p3 + 3*p2 + p1 + p0 + q0 + 4) >> 3)
		} else {
			rp0 = Clip1((2*p1 + p0 + q1 + 2) >> 2)
		}
		if abs(p0-q0) < ((alpha>>2)+2) && abs(q2-q0) < beta {
			rq0 = Clip1((p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4) >> 3)
			rq1 = Clip1((p0 + q0 + q1 + q2 + 2) >> 2)
			rq2 = Clip1((2*q3 + 3*q2 + q1 + q0 + p0 + 4) >> 3)
		} else {
			rq0 = Clip1((2*q1 + q0 + p1 + 2) >> 2)
		}
	} else {
		tc0 := tc0Table[indexA][bS-1]
		tc := tc0
		if abs(p2-p0) < beta {
			tc++
		}
		if abs(q2-q0) < beta {
			tc++
		}
		delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
		rp0 = Clip1(p0 + delta)
		rq0 = Clip1(q0 - delta)
		if abs(p2-p0) < beta {
			rp1 = Clip1(p1 + Clip3(-tc0, tc0, (p2+((p0+q0+1)>>1)-(p1<<1))>>1))
		}
		if abs(q2-q0) < beta {
			rq1 = Clip1(q1 + Clip3(-tc0, tc0, (q2+((p0+q0+1)>>1)-(q1<<1))>>1))
		}
	}
	return
}

// filterChromaSample applies the chroma deblocking filter to one sample pair.
// Chroma uses only p1,p0,q0,q1 (no p2/q2 for strong path).
func filterChromaSample(p1, p0, q0, q1, bS, alpha, beta, indexA int) (rp0, rq0 uint8) {
	rp0, rq0 = Clip1(p0), Clip1(q0)
	if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
		return
	}
	if bS == 4 {
		rp0 = Clip1((2*p1 + p0 + q1 + 2) >> 2)
		rq0 = Clip1((2*q1 + q0 + p1 + 2) >> 2)
	} else {
		tc := tc0Table[indexA][bS-1] + 1
		delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
		rp0 = Clip1(p0 + delta)
		rq0 = Clip1(q0 - delta)
	}
	return
}

// FilterLumaEdgeV filters a vertical luma edge in-place on plane data.
// The edge is at column x (pixels x-1 and x are p0/q0).
// rowStart: first row to filter; nrows must be a multiple of 4 (up to 16).
// bS[4]: boundary strength for each group of 4 rows.
// indexA/B: clipped QP+offset indices into alpha/beta tables.
// filterLumaSample is inlined here to avoid multi-return call overhead.
func FilterLumaEdgeV(plane []uint8, stride, x, rowStart, nrows int, bS [4]int, indexA, indexB int) {
	if x < 4 || x+4 > stride || indexA < 0 || indexA > 51 || indexB < 0 || indexB > 51 {
		return
	}
	alpha := alphaTable[indexA]
	beta := betaTable[indexB]
	alphaQ2 := (alpha >> 2) + 2
	for g := 0; g < nrows/4 && g < 4; g++ {
		bs := bS[g]
		if bs == 0 {
			continue
		}
		for r := 0; r < 4; r++ {
			base := (rowStart + g*4 + r) * stride
			if base+x+4 > len(plane) || base+x-4 < 0 {
				continue
			}
			p3 := int(plane[base+x-4])
			p2 := int(plane[base+x-3])
			p1 := int(plane[base+x-2])
			p0 := int(plane[base+x-1])
			q0 := int(plane[base+x+0])
			q1 := int(plane[base+x+1])
			q2 := int(plane[base+x+2])
			q3 := int(plane[base+x+3])
			if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
				continue
			}
			if bs == 4 {
				cond := abs(p0-q0) < alphaQ2
				if cond && abs(p2-p0) < beta {
					plane[base+x-1] = Clip1((p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4) >> 3)
					plane[base+x-2] = Clip1((p2 + p1 + p0 + q0 + 2) >> 2)
					plane[base+x-3] = Clip1((2*p3 + 3*p2 + p1 + p0 + q0 + 4) >> 3)
				} else {
					plane[base+x-1] = Clip1((2*p1 + p0 + q1 + 2) >> 2)
				}
				if cond && abs(q2-q0) < beta {
					plane[base+x+0] = Clip1((p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4) >> 3)
					plane[base+x+1] = Clip1((p0 + q0 + q1 + q2 + 2) >> 2)
					plane[base+x+2] = Clip1((2*q3 + 3*q2 + q1 + q0 + p0 + 4) >> 3)
				} else {
					plane[base+x+0] = Clip1((2*q1 + q0 + p1 + 2) >> 2)
				}
			} else {
				tc0 := tc0Table[indexA][bs-1]
				tc := tc0
				p2p0 := abs(p2 - p0)
				q2q0 := abs(q2 - q0)
				if p2p0 < beta {
					tc++
				}
				if q2q0 < beta {
					tc++
				}
				delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
				plane[base+x-1] = Clip1(p0 + delta)
				plane[base+x+0] = Clip1(q0 - delta)
				if p2p0 < beta {
					plane[base+x-2] = Clip1(p1 + Clip3(-tc0, tc0, (p2+((p0+q0+1)>>1)-(p1<<1))>>1))
				}
				if q2q0 < beta {
					plane[base+x+1] = Clip1(q1 + Clip3(-tc0, tc0, (q2+((p0+q0+1)>>1)-(q1<<1))>>1))
				}
			}
		}
	}
}

// FilterLumaEdgeH filters a horizontal luma edge in-place on plane data.
// The edge is at row y (pixels y-1 and y are p0/q0).
// colStart: first column; ncols must be a multiple of 4 (up to 16).
// bS[4]: boundary strength for each group of 4 columns.
// filterLumaSample is inlined here to avoid multi-return call overhead.
func FilterLumaEdgeH(plane []uint8, stride, y, colStart, ncols int, bS [4]int, indexA, indexB int) {
	if y < 4 || indexA < 0 || indexA > 51 || indexB < 0 || indexB > 51 {
		return
	}
	alpha := alphaTable[indexA]
	beta := betaTable[indexB]
	alphaQ2 := (alpha >> 2) + 2
	s := stride
	for g := 0; g < ncols/4 && g < 4; g++ {
		bs := bS[g]
		if bs == 0 {
			continue
		}
		for c := 0; c < 4; c++ {
			col := colStart + g*4 + c
			base := y * s
			// The luma filter reads p3..q3, so an edge with q3 on the
			// final plane row is valid. Do not require a nonexistent q4.
			if base+3*s+col >= len(plane) || base-4*s+col < 0 {
				continue
			}
			p3 := int(plane[base-4*s+col])
			p2 := int(plane[base-3*s+col])
			p1 := int(plane[base-2*s+col])
			p0 := int(plane[base-s+col])
			q0 := int(plane[base+col])
			q1 := int(plane[base+s+col])
			q2 := int(plane[base+2*s+col])
			q3 := int(plane[base+3*s+col])
			if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
				continue
			}
			if bs == 4 {
				cond := abs(p0-q0) < alphaQ2
				if cond && abs(p2-p0) < beta {
					plane[base-s+col] = Clip1((p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4) >> 3)
					plane[base-2*s+col] = Clip1((p2 + p1 + p0 + q0 + 2) >> 2)
					plane[base-3*s+col] = Clip1((2*p3 + 3*p2 + p1 + p0 + q0 + 4) >> 3)
				} else {
					plane[base-s+col] = Clip1((2*p1 + p0 + q1 + 2) >> 2)
				}
				if cond && abs(q2-q0) < beta {
					plane[base+col] = Clip1((p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4) >> 3)
					plane[base+s+col] = Clip1((p0 + q0 + q1 + q2 + 2) >> 2)
					plane[base+2*s+col] = Clip1((2*q3 + 3*q2 + q1 + q0 + p0 + 4) >> 3)
				} else {
					plane[base+col] = Clip1((2*q1 + q0 + p1 + 2) >> 2)
				}
			} else {
				tc0 := tc0Table[indexA][bs-1]
				tc := tc0
				p2p0 := abs(p2 - p0)
				q2q0 := abs(q2 - q0)
				if p2p0 < beta {
					tc++
				}
				if q2q0 < beta {
					tc++
				}
				delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
				plane[base-s+col] = Clip1(p0 + delta)
				plane[base+col] = Clip1(q0 - delta)
				if p2p0 < beta {
					plane[base-2*s+col] = Clip1(p1 + Clip3(-tc0, tc0, (p2+((p0+q0+1)>>1)-(p1<<1))>>1))
				}
				if q2q0 < beta {
					plane[base+s+col] = Clip1(q1 + Clip3(-tc0, tc0, (q2+((p0+q0+1)>>1)-(q1<<1))>>1))
				}
			}
		}
	}
}

// FilterChromaEdgeV filters a vertical chroma edge in-place.
// x is the edge column; nrows must be a multiple of 2 (chroma height per MB = 8).
// bS[4] is shared with the luma edge (one per 4 luma rows = 2 chroma rows).
func FilterChromaEdgeV(plane []uint8, stride, x, rowStart, nrows int, bS [4]int, indexA, indexB int) {
	if x < 2 || x+2 > stride || indexA < 0 || indexA > 51 || indexB < 0 || indexB > 51 {
		return
	}
	alpha := alphaTable[indexA]
	beta := betaTable[indexB]
	for g := 0; g < nrows/2 && g < 4; g++ {
		bs := bS[g]
		if bs == 0 {
			continue
		}
		for r := 0; r < 2; r++ {
			base := (rowStart + g*2 + r) * stride
			if base+x+2 > len(plane) || base+x-2 < 0 {
				continue
			}
			p1 := int(plane[base+x-2])
			p0 := int(plane[base+x-1])
			q0 := int(plane[base+x+0])
			q1 := int(plane[base+x+1])
			if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
				continue
			}
			if bs == 4 {
				plane[base+x-1] = Clip1((2*p1 + p0 + q1 + 2) >> 2)
				plane[base+x+0] = Clip1((2*q1 + q0 + p1 + 2) >> 2)
			} else {
				tc := tc0Table[indexA][bs-1] + 1
				delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
				plane[base+x-1] = Clip1(p0 + delta)
				plane[base+x+0] = Clip1(q0 - delta)
			}
		}
	}
}

// FilterChromaEdgeH filters a horizontal chroma edge in-place.
// y is the edge row; ncols must be a multiple of 2.
// Each luma bS group spans four luma columns, hence two 4:2:0 chroma columns.
func FilterChromaEdgeH(plane []uint8, stride, y, colStart, ncols int, bS [4]int, indexA, indexB int) {
	if y < 2 || indexA < 0 || indexA > 51 || indexB < 0 || indexB > 51 {
		return
	}
	alpha := alphaTable[indexA]
	beta := betaTable[indexB]
	s := stride
	for g := 0; g < ncols/2 && g < 4; g++ {
		bs := bS[g]
		if bs == 0 {
			continue
		}
		for c := 0; c < 2; c++ {
			col := colStart + g*2 + c
			base := y * s
			// The chroma filter reads p1..q1; q1 may be on the final row.
			if base+s+col >= len(plane) || base-2*s+col < 0 {
				continue
			}
			p1 := int(plane[base-2*s+col])
			p0 := int(plane[base-s+col])
			q0 := int(plane[base+col])
			q1 := int(plane[base+s+col])
			if abs(p0-q0) >= alpha || abs(p1-p0) >= beta || abs(q1-q0) >= beta {
				continue
			}
			if bs == 4 {
				plane[base-s+col] = Clip1((2*p1 + p0 + q1 + 2) >> 2)
				plane[base+col] = Clip1((2*q1 + q0 + p1 + 2) >> 2)
			} else {
				tc := tc0Table[indexA][bs-1] + 1
				delta := Clip3(-tc, tc, ((q0-p0)*4+(p1-q1)+4)>>3)
				plane[base-s+col] = Clip1(p0 + delta)
				plane[base+col] = Clip1(q0 - delta)
			}
		}
	}
}

// MBDeblockInfo carries the per-macroblock data needed for boundary strength
// calculation. Store one per MB in scan order; pass current+neighbors to DeblockMB.
type MBDeblockInfo struct {
	QP        int  // luma QP
	ChromaQPU int  // mapped chroma QP for Cb
	ChromaQPV int  // mapped chroma QP for Cr
	IsIntra   bool // MB is intra coded
	Use8x8    bool // uses 8x8 transform (skip internal 4x4 edges)
	IsB       bool // B slice: compare both reference lists, including swapped pairs
	// Per-4×4 non-zero coefficient count (raster order, luma only).
	NZC [16]int
	// Per-4×4 reference-picture identities and quarter-sample motion vectors.
	// RefID is -1 when the list is unused. B-list IDs identify pictures rather
	// than syntax ref_idx values because L0 and L1 use different orderings.
	RefIDL0 [16]int
	RefIDL1 [16]int
	MVL0    [16][2]int16
	MVL1    [16][2]int16
}

// DeblockMBContext holds slice-level deblocking parameters.
type DeblockMBContext struct {
	DisableIDC  int // disable_deblocking_filter_idc (0=on, 1=off, 2=no cross-slice)
	AlphaOffset int // slice_alpha_c0_offset (already multiplied ×2 by parser)
	BetaOffset  int // slice_beta_offset (already multiplied ×2 by parser)
	ChromaQPOff int // chroma QP offset for indexA/B (frame.ChromaQP applied by caller)
}

// DeblockMB applies the in-loop deblocking filter to one macroblock.
// f: current frame (written in place); mbX/mbY: macroblock coordinates.
// cur: current MB info; left/top: left/top neighbor info (nil = none/out of frame).
// ctx: slice deblocking parameters.
// Implements H.264 §8.7 for 4:2:0, progressive, non-MBAFF content.
func DeblockMB(f interface {
	LumaPlane() []uint8
	LumaStride() int
	ChromaPlaneU() []uint8
	ChromaPlaneV() []uint8
	ChromaStride() int
}, mbX, mbY int, cur MBDeblockInfo, left, top *MBDeblockInfo, ctx DeblockMBContext) {
	if ctx.DisableIDC == 1 {
		return
	}
	// ... implemented below as package-private; callers use DeblockMBFrame instead.
}

// DeblockMBFrame applies in-loop deblocking for one macroblock directly on
// a frame.Frame-compatible struct.
// mbX, mbY: macroblock grid coordinates.
// cur: current MB; left/top may be nil when at the frame edge.
// ctx: slice-level deblocking parameters.
func DeblockMBFrame(
	yPlane []uint8, yStride int,
	uPlane, vPlane []uint8, cStride int,
	mbX, mbY int,
	cur MBDeblockInfo,
	left, top *MBDeblockInfo,
	ctx DeblockMBContext,
) {
	if ctx.DisableIDC == 1 {
		return
	}

	// §8.7: QP at a boundary is the average of the two MBs.
	// indexA = Clip3(0,51, avgQP + alphaOffset), indexB = Clip3(0,51, avgQP + betaOffset).
	// For internal edges: QP = current MB QP (no averaging needed).
	lumaQP := func(qpA, qpB int) int { return (qpA + qpB + 1) >> 1 }
	indexA := func(qp int) int { return Clip3(0, 51, qp+ctx.AlphaOffset) }
	indexB := func(qp int) int { return Clip3(0, 51, qp+ctx.BetaOffset) }

	chromaIndexA := func(qp int) int { return Clip3(0, 51, qp+ctx.AlphaOffset) }
	chromaIndexB := func(qp int) int { return Clip3(0, 51, qp+ctx.BetaOffset) }

	// ---- Vertical edges (dir=0): filter from left to right ----
	// Edge 0: left MB boundary (if left neighbor exists)
	if left != nil {
		qp := lumaQP(cur.QP, left.QP)
		ia, ib := indexA(qp), indexB(qp)
		bs := bsVertMB(cur, left)
		FilterLumaEdgeV(yPlane, yStride, mbX*16, mbY*16, 16, bs, ia, ib)
		// FFmpeg averages already-mapped chroma QPs across MB boundaries.
		cbs := chromaBSFrom(bs)
		qpu := lumaQP(cur.ChromaQPU, left.ChromaQPU)
		qpv := lumaQP(cur.ChromaQPV, left.ChromaQPV)
		FilterChromaEdgeV(uPlane, cStride, mbX*8, mbY*8, 8, cbs, chromaIndexA(qpu), chromaIndexB(qpu))
		FilterChromaEdgeV(vPlane, cStride, mbX*8, mbY*8, 8, cbs, chromaIndexA(qpv), chromaIndexB(qpv))
	}

	// Internal vertical edges (edges 1-3): 4×4 column boundaries within MB.
	for e := 1; e <= 3; e++ {
		col := mbX*16 + e*4
		bs := bsVertInternal(cur, e)
		if bsAllZero(bs) {
			continue
		}
		qp := cur.QP
		ia, ib := indexA(qp), indexB(qp)
		FilterLumaEdgeV(yPlane, yStride, col, mbY*16, 16, bs, ia, ib)
		// Chroma: filter at even luma edges only (e=2 → chroma col mbX*8+4).
		if e == 2 {
			cbs := chromaBSFrom(bs)
			FilterChromaEdgeV(uPlane, cStride, mbX*8+4, mbY*8, 8, cbs, chromaIndexA(cur.ChromaQPU), chromaIndexB(cur.ChromaQPU))
			FilterChromaEdgeV(vPlane, cStride, mbX*8+4, mbY*8, 8, cbs, chromaIndexA(cur.ChromaQPV), chromaIndexB(cur.ChromaQPV))
		}
	}

	// ---- Horizontal edges (dir=1): filter from top to bottom ----
	// Edge 0: top MB boundary.
	if top != nil {
		qp := lumaQP(cur.QP, top.QP)
		ia, ib := indexA(qp), indexB(qp)
		bs := bsHorizMB(cur, top)
		FilterLumaEdgeH(yPlane, yStride, mbY*16, mbX*16, 16, bs, ia, ib)
		cbs := chromaBSFrom(bs)
		qpu := lumaQP(cur.ChromaQPU, top.ChromaQPU)
		qpv := lumaQP(cur.ChromaQPV, top.ChromaQPV)
		FilterChromaEdgeH(uPlane, cStride, mbY*8, mbX*8, 8, cbs, chromaIndexA(qpu), chromaIndexB(qpu))
		FilterChromaEdgeH(vPlane, cStride, mbY*8, mbX*8, 8, cbs, chromaIndexA(qpv), chromaIndexB(qpv))
	}

	// Internal horizontal edges (edges 1-3).
	for e := 1; e <= 3; e++ {
		row := mbY*16 + e*4
		bs := bsHorizInternal(cur, e)
		if bsAllZero(bs) {
			continue
		}
		qp := cur.QP
		ia, ib := indexA(qp), indexB(qp)
		FilterLumaEdgeH(yPlane, yStride, row, mbX*16, 16, bs, ia, ib)
		if e == 2 {
			cbs := chromaBSFrom(bs)
			FilterChromaEdgeH(uPlane, cStride, mbY*8+4, mbX*8, 8, cbs, chromaIndexA(cur.ChromaQPU), chromaIndexB(cur.ChromaQPU))
			FilterChromaEdgeH(vPlane, cStride, mbY*8+4, mbX*8, 8, cbs, chromaIndexA(cur.ChromaQPV), chromaIndexB(cur.ChromaQPV))
		}
	}
}

// bsVertMB returns bS[4] for the vertical MB-boundary edge between cur and left.
// §8.7.2: if either MB is intra → bS=4; else inter bS from NZC/MV (bS≤2 here).
func bsVertMB(cur MBDeblockInfo, left *MBDeblockInfo) [4]int {
	var bs [4]int
	for g := 0; g < 4; g++ {
		if cur.IsIntra || (left != nil && left.IsIntra) {
			bs[g] = 4
		} else if left != nil {
			curNZ := cur.NZC[g*4]
			leftNZ := left.NZC[g*4+3]
			if curNZ != 0 || leftNZ != 0 {
				bs[g] = 2
			} else if interMotionBoundary(cur, g*4, *left, g*4+3) {
				bs[g] = 1
			}
		}
	}
	return bs
}

// bsHorizMB returns bS[4] for the horizontal MB-boundary edge between cur and top.
func bsHorizMB(cur MBDeblockInfo, top *MBDeblockInfo) [4]int {
	var bs [4]int
	for g := 0; g < 4; g++ {
		if cur.IsIntra || (top != nil && top.IsIntra) {
			bs[g] = 4
		} else if top != nil {
			curNZ := cur.NZC[g]
			topNZ := top.NZC[g+12]
			if curNZ != 0 || topNZ != 0 {
				bs[g] = 2
			} else if interMotionBoundary(cur, g, *top, g+12) {
				bs[g] = 1
			}
		}
	}
	return bs
}

// bsVertInternal returns bS[4] for internal vertical luma edges (edge 1-3).
// §8.7.2 table: if intra → bS=3; else NZC-based.
// luma 4×4 scan order columns: edge e covers blocks with col==e (0-indexed).
func bsVertInternal(cur MBDeblockInfo, edge int) [4]int {
	var bs [4]int
	// 8x8 transform: only filter at 8x8 grid boundaries (edge 2).
	if cur.Use8x8 && edge != 2 {
		return bs // all zero — no filtering
	}
	if cur.IsIntra {
		for g := range bs {
			bs[g] = 3
		}
		return bs
	}
	// edge column in 4×4 grid: 0..3. Block scan: blk = row*4 + col
	for row := 0; row < 4; row++ {
		blk := row*4 + edge
		blkPrev := row*4 + edge - 1
		if cur.NZC[blk] != 0 || cur.NZC[blkPrev] != 0 {
			bs[row] = 2
		} else if interMotionBoundary(cur, blk, cur, blkPrev) {
			bs[row] = 1
		}
	}
	return bs
}

// bsHorizInternal returns bS[4] for internal horizontal luma edges (edge 1-3).
func bsHorizInternal(cur MBDeblockInfo, edge int) [4]int {
	var bs [4]int
	// 8x8 transform: only filter at 8x8 grid boundaries (edge 2).
	if cur.Use8x8 && edge != 2 {
		return bs
	}
	if cur.IsIntra {
		for g := range bs {
			bs[g] = 3
		}
		return bs
	}
	// edge row in 4×4 grid: 0..3. Block scan: blk = row*4 + col
	for col := 0; col < 4; col++ {
		blk := edge*4 + col
		blkPrev := (edge-1)*4 + col
		if cur.NZC[blk] != 0 || cur.NZC[blkPrev] != 0 {
			bs[col] = 2
		} else if interMotionBoundary(cur, blk, cur, blkPrev) {
			bs[col] = 1
		}
	}
	return bs
}

// chromaBSFrom maps luma bS[4] groups to chroma bS[4] groups (one per 2 luma rows).
// bS for chroma = max(bS from the two corresponding luma groups).
func chromaBSFrom(luma [4]int) [4]int {
	return luma
}

func bsAllZero(bs [4]int) bool {
	return bs[0] == 0 && bs[1] == 0 && bs[2] == 0 && bs[3] == 0
}

// interMotionBoundary mirrors FFmpeg h264_loopfilter.c:check_mv for progressive
// pictures. A quarter-sample motion-vector difference of four luma samples or a
// different reference picture gives bS=1. B slices also accept swapped L0/L1
// reference pairs when the corresponding cross-list vectors match.
func interMotionBoundary(a MBDeblockInfo, ai int, b MBDeblockInfo, bi int) bool {
	if ai < 0 || ai >= 16 || bi < 0 || bi >= 16 {
		return false
	}
	same := func(ar int, am [2]int16, br int, bm [2]int16) bool {
		if ar != br {
			return false
		}
		if ar < 0 {
			return true
		}
		return abs(int(am[0])-int(bm[0])) < 4 && abs(int(am[1])-int(bm[1])) < 4
	}
	if !a.IsB && !b.IsB {
		return !same(a.RefIDL0[ai], a.MVL0[ai], b.RefIDL0[bi], b.MVL0[bi])
	}
	direct := same(a.RefIDL0[ai], a.MVL0[ai], b.RefIDL0[bi], b.MVL0[bi]) &&
		same(a.RefIDL1[ai], a.MVL1[ai], b.RefIDL1[bi], b.MVL1[bi])
	if direct {
		return false
	}
	swapped := same(a.RefIDL0[ai], a.MVL0[ai], b.RefIDL1[bi], b.MVL1[bi]) &&
		same(a.RefIDL1[ai], a.MVL1[ai], b.RefIDL0[bi], b.MVL0[bi])
	return !swapped
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

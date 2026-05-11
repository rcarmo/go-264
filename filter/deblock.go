package filter

// H.264 in-loop deblocking filter.
// ITU-T H.264 §8.7
//
// Applied at macroblock edges (horizontal and vertical).
// Filters luma and chroma independently.
// Strength depends on whether edge is at block/MB boundary and coding modes.

// Clip3 clamps v to [lo, hi].
func Clip3(lo, hi, v int) int {
	if v < lo { return lo }
	if v > hi { return hi }
	return v
}

// Clip1 clamps to [0, 255].
func Clip1(v int) uint8 {
	if v < 0 { return 0 }
	if v > 255 { return 255 }
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
			// Normal filter
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
			// Strong filter (bS == 4, for intra edges)
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

func abs(x int) int {
	if x < 0 { return -x }
	return x
}

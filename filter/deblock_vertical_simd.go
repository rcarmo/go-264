package filter

type lumaVerticalLanes struct {
	P3, P2, P1, P0 [4]int16
	Q0, Q1, Q2, Q3 [4]int16
}

type chromaVerticalLanes struct {
	P1, P0, Q0, Q1 [4]int16
}

func filterLumaVerticalGroupFast(plane []byte, stride, x, firstRow, bS, alpha, beta, alphaQ2, tc0 int) bool {
	if stride <= 0 || firstRow < 0 {
		return false
	}
	var lanes lumaVerticalLanes
	for r := 0; r < 4; r++ {
		base := (firstRow + r) * stride
		if base+x-4 < 0 || base+x+4 > len(plane) {
			return false
		}
		lanes.P3[r] = int16(plane[base+x-4])
		lanes.P2[r] = int16(plane[base+x-3])
		lanes.P1[r] = int16(plane[base+x-2])
		lanes.P0[r] = int16(plane[base+x-1])
		lanes.Q0[r] = int16(plane[base+x])
		lanes.Q1[r] = int16(plane[base+x+1])
		lanes.Q2[r] = int16(plane[base+x+2])
		lanes.Q3[r] = int16(plane[base+x+3])
	}
	if !filterLuma4SIMD(&lanes, bS, alpha, beta, alphaQ2, tc0) {
		return false
	}
	for r := 0; r < 4; r++ {
		base := (firstRow + r) * stride
		plane[base+x-3] = byte(lanes.P2[r])
		plane[base+x-2] = byte(lanes.P1[r])
		plane[base+x-1] = byte(lanes.P0[r])
		plane[base+x] = byte(lanes.Q0[r])
		plane[base+x+1] = byte(lanes.Q1[r])
		plane[base+x+2] = byte(lanes.Q2[r])
	}
	return true
}

func filterChromaVerticalGroupFast(plane []byte, stride, x, firstRow, bS, alpha, beta, tc int) bool {
	if stride <= 0 || firstRow < 0 {
		return false
	}
	var lanes chromaVerticalLanes
	for r := 0; r < 2; r++ {
		base := (firstRow + r) * stride
		if base+x-2 < 0 || base+x+2 > len(plane) {
			return false
		}
		lanes.P1[r] = int16(plane[base+x-2])
		lanes.P0[r] = int16(plane[base+x-1])
		lanes.Q0[r] = int16(plane[base+x])
		lanes.Q1[r] = int16(plane[base+x+1])
	}
	if !filterChroma2SIMD(&lanes, bS, alpha, beta, tc) {
		return false
	}
	for r := 0; r < 2; r++ {
		base := (firstRow + r) * stride
		plane[base+x-1] = byte(lanes.P0[r])
		plane[base+x] = byte(lanes.Q0[r])
	}
	return true
}

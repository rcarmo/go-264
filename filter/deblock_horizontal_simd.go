package filter

func filterLumaHorizontalGroupFast(plane []byte, stride, edgeY, firstCol, bS, alpha, beta, alphaQ2, tc0 int) bool {
	if !deblockPackedSIMD || stride <= 0 || edgeY < 4 || firstCol < 0 {
		return false
	}
	var lanes lumaVerticalLanes
	for c := 0; c < 4; c++ {
		col := firstCol + c
		base := edgeY*stride + col
		if base-4*stride < 0 || base+3*stride >= len(plane) {
			return false
		}
		lanes.P3[c] = int16(plane[base-4*stride])
		lanes.P2[c] = int16(plane[base-3*stride])
		lanes.P1[c] = int16(plane[base-2*stride])
		lanes.P0[c] = int16(plane[base-stride])
		lanes.Q0[c] = int16(plane[base])
		lanes.Q1[c] = int16(plane[base+stride])
		lanes.Q2[c] = int16(plane[base+2*stride])
		lanes.Q3[c] = int16(plane[base+3*stride])
	}
	if !filterLuma4SIMD(&lanes, bS, alpha, beta, alphaQ2, tc0) {
		return false
	}
	for c := 0; c < 4; c++ {
		col := firstCol + c
		base := edgeY*stride + col
		plane[base-3*stride] = byte(lanes.P2[c])
		plane[base-2*stride] = byte(lanes.P1[c])
		plane[base-stride] = byte(lanes.P0[c])
		plane[base] = byte(lanes.Q0[c])
		plane[base+stride] = byte(lanes.Q1[c])
		plane[base+2*stride] = byte(lanes.Q2[c])
	}
	return true
}

func filterChromaHorizontalGroupFast(plane []byte, stride, edgeY, firstCol, bS, alpha, beta, tc int) bool {
	if !deblockPackedSIMD || stride <= 0 || edgeY < 2 || firstCol < 0 {
		return false
	}
	var lanes chromaVerticalLanes
	for c := 0; c < 2; c++ {
		col := firstCol + c
		base := edgeY*stride + col
		if base-2*stride < 0 || base+stride >= len(plane) {
			return false
		}
		lanes.P1[c] = int16(plane[base-2*stride])
		lanes.P0[c] = int16(plane[base-stride])
		lanes.Q0[c] = int16(plane[base])
		lanes.Q1[c] = int16(plane[base+stride])
	}
	if !filterChroma2SIMD(&lanes, bS, alpha, beta, tc) {
		return false
	}
	for c := 0; c < 2; c++ {
		col := firstCol + c
		base := edgeY*stride + col
		plane[base-stride] = byte(lanes.P0[c])
		plane[base] = byte(lanes.Q0[c])
	}
	return true
}

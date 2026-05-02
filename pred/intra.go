package pred

// H.264 intra prediction for 4×4 and 16×16 luma blocks.
// ITU-T H.264 §8.3.1

// Intra4x4 prediction modes
const (
	Intra4x4Vertical   = 0
	Intra4x4Horizontal = 1
	Intra4x4DC         = 2
	Intra4x4DiagDownLeft  = 3
	Intra4x4DiagDownRight = 4
	Intra4x4VerticalRight = 5
	Intra4x4HorizontalDown = 6
	Intra4x4VerticalLeft  = 7
	Intra4x4HorizontalUp  = 8
)

// Intra16x16 prediction modes
const (
	Intra16x16Vertical   = 0
	Intra16x16Horizontal = 1
	Intra16x16DC         = 2
	Intra16x16Plane      = 3
)

// PredIntra4x4 generates the predicted 4×4 block from neighboring pixels.
// top: 4 pixels above (A,B,C,D), topRight: 4 pixels above-right (E,F,G,H)
// left: 4 pixels to the left (I,J,K,L), topLeft: pixel at top-left corner (M)
// Output written to pred[0:16] in raster order.
func PredIntra4x4(pred []uint8, mode int, top, topRight, left []uint8, topLeft uint8) {
	switch mode {
	case Intra4x4Vertical:
		for i := 0; i < 4; i++ {
			pred[i*4+0] = top[0]
			pred[i*4+1] = top[1]
			pred[i*4+2] = top[2]
			pred[i*4+3] = top[3]
		}

	case Intra4x4Horizontal:
		for i := 0; i < 4; i++ {
			pred[i*4+0] = left[i]
			pred[i*4+1] = left[i]
			pred[i*4+2] = left[i]
			pred[i*4+3] = left[i]
		}

	case Intra4x4DC:
		sum := uint16(0)
		for i := 0; i < 4; i++ {
			sum += uint16(top[i]) + uint16(left[i])
		}
		dc := uint8((sum + 4) >> 3)
		for i := range pred[:16] {
			pred[i] = dc
		}

	case Intra4x4DiagDownLeft:
		// p[y,x] = (t[x+y] + 2*t[x+y+1] + t[x+y+2] + 2) >> 2
		t := make([]uint8, 8)
		copy(t[:4], top)
		copy(t[4:], topRight)
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				idx := x + y
				if idx >= 6 {
					pred[y*4+x] = t[7]
				} else {
					pred[y*4+x] = uint8((uint16(t[idx]) + 2*uint16(t[idx+1]) + uint16(t[idx+2]) + 2) >> 2)
				}
			}
		}

	case Intra4x4DiagDownRight:
		// Uses top-left, top, and left neighbors
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				if x > y {
					pred[y*4+x] = uint8((uint16(top[x-y-1]) + 2*uint16(top[x-y]) + uint16(topOrLeft(top, left, topLeft, x-y+1, 0)) + 2) >> 2)
				} else if x < y {
					pred[y*4+x] = uint8((uint16(left[y-x-1]) + 2*uint16(left[y-x]) + uint16(topOrLeft(top, left, topLeft, 0, y-x+1)) + 2) >> 2)
				} else {
					pred[y*4+x] = uint8((uint16(top[0]) + 2*uint16(topLeft) + uint16(left[0]) + 2) >> 2)
				}
			}
		}

	default:
		// Remaining modes (5-8) implemented similarly
		// Fall back to DC for now
		PredIntra4x4(pred, Intra4x4DC, top, topRight, left, topLeft)
	}
}

func topOrLeft(top, left []uint8, topLeft uint8, tx, ly int) uint8 {
	if tx > 0 && tx <= 4 {
		return top[tx-1]
	}
	if ly > 0 && ly <= 4 {
		return left[ly-1]
	}
	return topLeft
}

// PredIntra16x16 generates a 16×16 predicted block.
// top: 16 pixels above, left: 16 pixels to the left, topLeft: corner pixel.
// Output written to pred[0:256].
func PredIntra16x16(pred []uint8, mode int, top, left []uint8, topLeft uint8) {
	switch mode {
	case Intra16x16Vertical:
		if HasSSE2 {
			IntraPred16x16V_ASM(&pred[0], &top[0])
		} else {
			for y := 0; y < 16; y++ {
				copy(pred[y*16:(y+1)*16], top[:16])
			}
		}

	case Intra16x16Horizontal:
		if HasSSE2 {
			IntraPred16x16H_ASM(&pred[0], &left[0])
		} else {
			for y := 0; y < 16; y++ {
				for x := 0; x < 16; x++ {
					pred[y*16+x] = left[y]
				}
			}
		}

	case Intra16x16DC:
		sum := uint32(0)
		for i := 0; i < 16; i++ {
			sum += uint32(top[i]) + uint32(left[i])
		}
		dc := uint8((sum + 16) >> 5)
		if HasSSE2 {
			IntraPred16x16DC_ASM(&pred[0], dc)
		} else {
			for i := range pred[:256] {
				pred[i] = dc
			}
		}

	case Intra16x16Plane:
		// H.264 §8.3.3.4: Plane prediction
		H := int32(0)
		for x := 0; x < 8; x++ {
			var l uint8
			if x == 7 {
				l = topLeft
			} else {
				l = top[6-x]
			}
			H += int32(x+1) * (int32(top[8+x]) - int32(l))
		}
		V := int32(0)
		for y := 0; y < 8; y++ {
			var u uint8
			if y == 7 {
				u = topLeft
			} else {
				u = left[6-y]
			}
			V += int32(y+1) * (int32(left[8+y]) - int32(u))
		}
		a := 16 * (int32(top[15]) + int32(left[15]))
		b := (5*H + 32) >> 6
		c := (5*V + 32) >> 6

		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				v := (a + b*(int32(x)-7) + c*(int32(y)-7) + 16) >> 5
				if v < 0 { v = 0 }
				if v > 255 { v = 255 }
				pred[y*16+x] = uint8(v)
			}
		}
	}
}

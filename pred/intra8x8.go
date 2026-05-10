package pred

// PredIntra8x8 generates the predicted 8×8 block from neighboring pixels.
// Implements all 9 H.264 §8.3.2.2 Intra_8x8 prediction modes.
//
//   - top:     16-byte slice (pixels above the block, top[0..7] + top-right top[8..15])
//   - left:    8-byte slice  (pixels to the left, left[0..7])
//   - topLeft: corner pixel at (x=-1, y=-1)
func PredIntra8x8(pred []uint8, mode int, top, left []uint8, topLeft uint8) {
	pt := func(i int) int {
		if i < 0 {
			return int(topLeft)
		}
		return int(top[i])
	}
	pl := func(i int) int {
		if i < 0 {
			return int(topLeft)
		}
		return int(left[i])
	}
	clip8 := func(v int) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}

	switch mode {
	case Intra4x4Vertical:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				pred[y*8+x] = top[x]
			}
		}

	case Intra4x4Horizontal:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				pred[y*8+x] = left[y]
			}
		}

	case Intra4x4DC:
		sum := 0
		for i := 0; i < 8; i++ {
			sum += pt(i) + pl(i)
		}
		dc := uint8((sum + 8) >> 4)
		for i := range pred[:64] {
			pred[i] = dc
		}

	case Intra4x4DiagDownLeft:
		// Needs top[0..14]: last two positions clamped.
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				if x == 7 && y == 7 {
					pred[y*8+x] = clip8((pt(14) + 3*pt(15) + 2) >> 2)
				} else {
					pred[y*8+x] = clip8((pt(x+y) + 2*pt(x+y+1) + pt(x+y+2) + 2) >> 2)
				}
			}
		}

	case Intra4x4DiagDownRight:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				if x > y {
					pred[y*8+x] = clip8((pt(x-y-2) + 2*pt(x-y-1) + pt(x-y) + 2) >> 2)
				} else if y > x {
					pred[y*8+x] = clip8((pl(y-x-2) + 2*pl(y-x-1) + pl(y-x) + 2) >> 2)
				} else {
					pred[y*8+x] = clip8((pt(0) + 2*pl(-1) + pl(0) + 2) >> 2)
				}
			}
		}

	case Intra4x4VerticalRight:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				zVR := 2*x - y
				switch {
				case zVR >= 0 && zVR%2 == 0:
					idx := x - (y >> 1)
					pred[y*8+x] = clip8((pt(idx-1) + pt(idx) + 1) >> 1)
				case zVR > 0 && zVR%2 == 1:
					idx := x - (y >> 1)
					pred[y*8+x] = clip8((pt(idx-2) + 2*pt(idx-1) + pt(idx) + 2) >> 2)
				case zVR == -1:
					pred[y*8+x] = clip8((pl(0) + 2*pl(-1) + pt(0) + 2) >> 2)
				default:
					pred[y*8+x] = clip8((pl(y-1) + 2*pl(y-2) + pl(y-3) + 2) >> 2)
				}
			}
		}

	case Intra4x4HorizontalDown:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				zHD := 2*y - x
				switch {
				case zHD >= 0 && zHD%2 == 0:
					idx := y - (x >> 1)
					pred[y*8+x] = clip8((pl(idx-1) + pl(idx) + 1) >> 1)
				case zHD > 0 && zHD%2 == 1:
					idx := y - (x >> 1)
					pred[y*8+x] = clip8((pl(idx-2) + 2*pl(idx-1) + pl(idx) + 2) >> 2)
				case zHD == -1:
					pred[y*8+x] = clip8((pt(0) + 2*pl(-1) + pl(0) + 2) >> 2)
				default:
					pred[y*8+x] = clip8((pt(x-1) + 2*pt(x-2) + pt(x-3) + 2) >> 2)
				}
			}
		}

	case Intra4x4VerticalLeft:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				idx := x + (y >> 1)
				if y&1 == 0 {
					pred[y*8+x] = clip8((pt(idx) + pt(idx+1) + 1) >> 1)
				} else {
					pred[y*8+x] = clip8((pt(idx) + 2*pt(idx+1) + pt(idx+2) + 2) >> 2)
				}
			}
		}

	case Intra4x4HorizontalUp:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				zHU := x + 2*y
				idx := y + (x >> 1)
				switch {
				case zHU%2 == 0 && idx < 7:
					pred[y*8+x] = clip8((pl(idx) + pl(idx+1) + 1) >> 1)
				case zHU%2 == 1 && idx < 6:
					pred[y*8+x] = clip8((pl(idx) + 2*pl(idx+1) + pl(idx+2) + 2) >> 2)
				case zHU%2 == 1 && idx == 6:
					pred[y*8+x] = clip8((pl(6) + 2*pl(7) + pl(7) + 2) >> 2)
				case zHU == 13:
					pred[y*8+x] = clip8((pl(6) + 3*pl(7) + 2) >> 2)
				default:
					pred[y*8+x] = uint8(pl(7))
				}
			}
		}

	default:
		sum := uint16(0)
		for i := 0; i < 8; i++ {
			sum += uint16(top[i]) + uint16(left[i])
		}
		dc := uint8((sum + 8) >> 4)
		for i := range pred[:64] {
			pred[i] = dc
		}
	}
}

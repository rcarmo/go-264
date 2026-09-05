package pred

import "testing"

func TestInterPred16x16(t *testing.T) {
	// Create a simple 32x32 reference frame
	stride := 32
	ref := make([]uint8, stride*32)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			ref[y*stride+x] = uint8((x + y) * 4)
		}
	}

	// Zero MV: should copy the top-left 16x16
	out := make([]uint8, 256)
	InterPred16x16(out, ref, stride, MotionVector{0, 0})
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			want := uint8((x + y) * 4)
			if out[y*16+x] != want {
				t.Fatalf("out[%d,%d]=%d want %d", y, x, out[y*16+x], want)
			}
		}
	}

	// MV = (4,4) in quarter-pixel = (1,1) full pixel
	InterPred16x16(out, ref, stride, MotionVector{4, 4})
	if out[0] != ref[1*stride+1] {
		t.Errorf("MV(4,4): out[0]=%d want %d", out[0], ref[1*stride+1])
	}
}

func TestInterPred16x16At(t *testing.T) {
	stride := 48
	ref := make([]uint8, stride*48)
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			ref[y*stride+x] = uint8((x*3 + y*5) & 0xff)
		}
	}

	out := make([]uint8, 256)
	InterPred16x16At(out, ref, stride, 16, 12, MotionVector{4, 8}) // +1,+2 px
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			want := ref[(12+2+y)*stride+(16+1+x)]
			if out[y*16+x] != want {
				t.Fatalf("out[%d,%d]=%d want %d", y, x, out[y*16+x], want)
			}
		}
	}

	// Clipped edge path must remain scalar-correct.
	InterPred16x16At(out, ref, stride, -4, -3, MotionVector{0, 0})
	if out[0] != ref[0] {
		t.Fatalf("clipped top-left: got %d want %d", out[0], ref[0])
	}
}

// lumaQpelReference evaluates each requested sample independently. In particular,
// HV uses a direct 36-term convolution, not the production two-pass scratch path.
func lumaQpelReference(ref []byte, stride, x, y, fx, fy int) byte {
	coeff := [6]int{1, -5, 20, 20, -5, 1}
	sample := func(x, y int) int {
		x = max(0, min(x, stride-1))
		y = max(0, min(y, len(ref)/stride-1))
		return int(ref[y*stride+x])
	}
	clip := func(v int) int { return max(0, min(v, 255)) }
	horizontal := func(x, y int) int {
		sum := 0
		for i, c := range coeff {
			sum += c * sample(x+i-2, y)
		}
		return clip((sum + 16) >> 5)
	}
	vertical := func(x, y int) int {
		sum := 0
		for i, c := range coeff {
			sum += c * sample(x, y+i-2)
		}
		return clip((sum + 16) >> 5)
	}
	diagonal := func() int {
		sum := 0
		for j, cy := range coeff {
			for i, cx := range coeff {
				sum += cy * cx * sample(x+i-2, y+j-2)
			}
		}
		return clip((sum + 512) >> 10)
	}
	average := func(a, b int) byte { return byte((a + b + 1) >> 1) }
	switch {
	case fx == 0 && fy == 0:
		return byte(sample(x, y))
	case fy == 0:
		if fx == 2 {
			return byte(horizontal(x, y))
		}
		return average(horizontal(x, y), sample(x+fx/2, y))
	case fx == 0:
		if fy == 2 {
			return byte(vertical(x, y))
		}
		return average(vertical(x, y), sample(x, y+fy/2))
	case fx == 2 && fy == 2:
		return byte(diagonal())
	case fx == 2:
		return average(diagonal(), horizontal(x, y+fy/2))
	case fy == 2:
		return average(diagonal(), vertical(x+fx/2, y))
	default:
		return average(horizontal(x, y+fy/2), vertical(x+fx/2, y))
	}
}

func TestInterPredLumaH264MatchesReference(t *testing.T) {
	// Include every H.264 partition shape and larger public API requests that
	// exceed stack scratch. Padded output rows must remain untouched.
	shapes := [][2]int{{1, 1}, {4, 4}, {4, 8}, {8, 4}, {8, 8}, {8, 16}, {16, 8}, {16, 16}, {17, 21}, {33, 5}}
	for _, plane := range [][2]int{{1, 1}, {5, 7}, {40, 29}} {
		stride, height := plane[0], plane[1]
		ref := make([]byte, stride*height)
		// Nonlinear, high-contrast input exercises negative/overshooting filter
		// sums as well as both rounding stages; a smooth ramp would miss these.
		state := uint32(0x264)
		for i := range ref {
			state = state*1664525 + 1013904223
			ref[i] = byte(state >> 24)
			if i%3 == 0 {
				ref[i] = byte((i % 2) * 255)
			}
		}
		positions := [][4]int{
			{6, 7, 0, 0}, {0, 0, 0, 0}, {-1, -1, 0, 0},
			{stride - 2, height - 2, 0, 0},
			{6, 7, -3, -2}, {6, 7, 5, -4},
			{6, 7, -8192, -8192}, {6, 7, 8191, 8191},
		}
		for _, shape := range shapes {
			w, h := shape[0], shape[1]
			outStride := w + 3
			for _, pos := range positions {
				for fy := 0; fy < 4; fy++ {
					for fx := 0; fx < 4; fx++ {
						mv := MotionVector{int16(pos[2]*4 + fx), int16(pos[3]*4 + fy)}
						got, want := make([]byte, h*outStride+6), make([]byte, h*outStride+6)
						for i := range got {
							got[i], want[i] = 0xa5, 0xa5
						}
						// Pass only the actual strided rectangle, without requiring
						// padding after its final row. This also models a partition
						// starting at a nonzero X offset inside a larger prediction.
						end := 3 + (h-1)*outStride + w
						InterPredLumaH264(got[3:end], outStride, ref, stride, pos[0], pos[1], w, h, mv)
						for y := 0; y < h; y++ {
							for x := 0; x < w; x++ {
								want[3+y*outStride+x] = lumaQpelReference(ref, stride, pos[0]+pos[2]+x, pos[1]+pos[3]+y, fx, fy)
							}
						}
						for i, v := range got {
							if v != want[i] {
								t.Fatalf("plane=%v shape=%v origin=(%d,%d) mv=%v offset=%d: got %d want %d", plane, shape, pos[0], pos[1], mv, i, v, want[i])
							}
						}
					}
				}
			}
		}
	}
}

func TestInterPredLumaH264PartitionScratchDoesNotAllocate(t *testing.T) {
	var ref [32 * 32]byte
	var out [16 * 16]byte
	// Numerical coverage above checks every fraction. These cases cover the
	// distinct scratch paths: directional, diagonal, blended, and edge-extended.
	for _, origin := range [][2]int{{5, 5}, {-1, -1}, {5, -1}} {
		for _, mv := range []MotionVector{{2, 0}, {0, 2}, {2, 2}, {1, 2}, {3, 3}} {
			allocs := testing.AllocsPerRun(10, func() {
				InterPredLumaH264(out[:], 16, ref[:], 32, origin[0], origin[1], 16, 16, mv)
			})
			if allocs != 0 {
				t.Errorf("origin=%v mv=%v: got %v allocations for a 16x16 partition", origin, mv, allocs)
			}
		}
	}
}

func BenchmarkInterPred16x16At(b *testing.B) {
	stride := 1920
	ref := make([]uint8, stride*1080)
	out := make([]uint8, 256)
	for i := range ref {
		ref[i] = uint8(i)
	}
	mv := MotionVector{4, 4}
	b.ReportAllocs()
	b.SetBytes(256)
	for i := 0; i < b.N; i++ {
		InterPred16x16At(out, ref, stride, 256, 256, mv)
	}
}

func BenchmarkInterPred16x16AtFractionalHorizontal(b *testing.B) {
	stride := 1920
	ref := make([]uint8, stride*1080)
	out := make([]uint8, 256)
	for i := range ref {
		ref[i] = uint8(i)
	}
	mv := MotionVector{2, 0}
	b.ReportAllocs()
	b.SetBytes(256)
	for i := 0; i < b.N; i++ {
		InterPred16x16At(out, ref, stride, 256, 256, mv)
	}
}

func BenchmarkInterPred16x16AtFractionalVertical(b *testing.B) {
	stride := 1920
	ref := make([]uint8, stride*1080)
	out := make([]uint8, 256)
	for i := range ref {
		ref[i] = uint8(i)
	}
	mv := MotionVector{0, 2}
	b.ReportAllocs()
	b.SetBytes(256)
	for i := 0; i < b.N; i++ {
		InterPred16x16At(out, ref, stride, 256, 256, mv)
	}
}

func BenchmarkInterPred16x16AtFractional(b *testing.B) {
	stride := 1920
	ref := make([]uint8, stride*1080)
	out := make([]uint8, 256)
	for i := range ref {
		ref[i] = uint8(i)
	}
	mv := MotionVector{1, 2}
	b.ReportAllocs()
	b.SetBytes(256)
	for i := 0; i < b.N; i++ {
		InterPred16x16At(out, ref, stride, 256, 256, mv)
	}
}

func BenchmarkInterPred16x16AtHalfDiagonal(b *testing.B) {
	stride := 1920
	ref := make([]uint8, stride*1080)
	out := make([]uint8, 256)
	for i := range ref {
		ref[i] = uint8(i)
	}
	mv := MotionVector{2, 2}
	b.ReportAllocs()
	b.SetBytes(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		InterPred16x16At(out, ref, stride, 256, 256, mv)
	}
}

func BenchmarkInterPred16x16AtFractionalClipped(b *testing.B) {
	stride := 1920
	ref := make([]uint8, stride*1080)
	out := make([]uint8, 256)
	for i := range ref {
		ref[i] = uint8(i)
	}
	for _, tc := range []struct {
		name string
		x, y int
		mv   MotionVector
	}{
		{"HorizontalHalf", -1, 5, MotionVector{2, 0}},
		{"VerticalHalf", 5, -1, MotionVector{0, 2}},
		{"HorizontalQuarter", -1, 5, MotionVector{3, 0}},
		{"VerticalQuarter", 5, -1, MotionVector{0, 3}},
		{"OddOdd", -1, -1, MotionVector{3, 3}},
		{"HV", -1, -1, MotionVector{1, 2}},
		{"HVTopOnly", 5, -1, MotionVector{2, 2}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(256)
			for i := 0; i < b.N; i++ {
				InterPred16x16At(out, ref, stride, tc.x, tc.y, tc.mv)
			}
		})
	}
}

func TestSubpelFilter(t *testing.T) {
	// Test with constant input: should return same value
	samples := [6]uint8{100, 100, 100, 100, 100, 100}
	v := SubpelFilter6Tap(samples)
	if v != 100 {
		t.Errorf("constant input: got %d want 100", v)
	}

	// Test with edge: ramp up
	samples = [6]uint8{0, 50, 100, 150, 200, 250}
	v = SubpelFilter6Tap(samples)
	// (0 - 250 + 2000 + 3000 - 1000 + 250) / 32 = 4000/32 = 125
	t.Logf("ramp: %d (expect ~125)", v)
}

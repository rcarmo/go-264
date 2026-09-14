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

// InterPred16x16At performs motion compensation for a 16x16 block at
// macroblock origin (baseX, baseY). Fractional positions use the H.264 six-tap
// half-pel filter and quarter-pel averaging. Interior integer positions use row
// copies; positions outside the reference plane repeat the nearest edge sample.
func InterPred16x16At(out []uint8, ref []uint8, stride int, baseX, baseY int, mv MotionVector) {
	if len(out) < 256 || stride <= 0 || len(ref) == 0 {
		return
	}
	InterPredLumaH264(out, 16, ref, stride, baseX, baseY, 16, 16, mv)
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

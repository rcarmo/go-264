package transform

// H.264 8×8 integer transform and quantization (High profile).
// ITU-T H.264 §8.5.12

// DCT8x8 performs the forward 8×8 integer transform (in-place).
func DCT8x8(block []int16) {
	// Horizontal pass
	for i := 0; i < 8; i++ {
		r := block[i*8 : i*8+8]
		a0 := r[0] + r[7]
		a1 := r[1] + r[6]
		a2 := r[2] + r[5]
		a3 := r[3] + r[4]
		a4 := r[0] - r[7]
		a5 := r[1] - r[6]
		a6 := r[2] - r[5]
		a7 := r[3] - r[4]

		b0 := a0 + a3
		b1 := a1 + a2
		b2 := a0 - a3
		b3 := a1 - a2

		r[0] = b0 + b1
		r[2] = b2 + (b3 >> 1)
		r[4] = b0 - b1
		r[6] = (b2 >> 1) - b3

		b4 := a4 + (a7 >> 1)
		b5 := a5 + a6
		b6 := a4 - (a7 >> 1) // a6 replaced with different combination
		b7 := -a5 + a6

		r[1] = b4 + b5 + (b4 >> 2)
		r[3] = b6 + b7 + (b7 >> 2)
		r[5] = b6 - b7 - (b7 >> 2) // approximate
		r[7] = -b4 + b5 - (b4 >> 2)
	}
	// Vertical pass
	for j := 0; j < 8; j++ {
		a0 := block[j] + block[7*8+j]
		a1 := block[8+j] + block[6*8+j]
		a2 := block[2*8+j] + block[5*8+j]
		a3 := block[3*8+j] + block[4*8+j]
		a4 := block[j] - block[7*8+j]
		a5 := block[8+j] - block[6*8+j]
		a6 := block[2*8+j] - block[5*8+j]
		a7 := block[3*8+j] - block[4*8+j]

		b0 := a0 + a3
		b1 := a1 + a2
		b2 := a0 - a3
		b3 := a1 - a2

		block[j] = b0 + b1
		block[2*8+j] = b2 + (b3 >> 1)
		block[4*8+j] = b0 - b1
		block[6*8+j] = (b2 >> 1) - b3

		b4 := a4 + (a7 >> 1)
		b5 := a5 + a6
		b6 := a4 - (a7 >> 1)
		b7 := -a5 + a6

		block[8+j] = b4 + b5 + (b4 >> 2)
		block[3*8+j] = b6 + b7 + (b7 >> 2)
		block[5*8+j] = b6 - b7 - (b7 >> 2)
		block[7*8+j] = -b4 + b5 - (b4 >> 2)
	}
}

// IDCT8x8 performs the inverse 8×8 integer transform (in-place).
func IDCT8x8(block []int16) {
	if HasAVX2 && len(block) >= 64 {
		IDCT8x8_ASM(&block[0])
		return
	}
	// Horizontal pass
	for i := 0; i < 8; i++ {
		r := block[i*8 : i*8+8]
		// Even part
		a0 := r[0] + r[4]
		a2 := r[0] - r[4]
		a4 := (r[2] >> 1) - r[6]
		a6 := r[2] + (r[6] >> 1)
		b0 := a0 + a6
		b2 := a2 + a4
		b4 := a2 - a4
		b6 := a0 - a6

		// Odd part
		a1 := -r[3] + r[5] - r[7] - (r[7] >> 1)
		a3 := r[1] + r[7] - r[3] - (r[3] >> 1)
		a5 := -r[1] + r[7] + r[5] + (r[5] >> 1)
		a7 := r[3] + r[5] + r[1] + (r[1] >> 1)
		b1 := (a7 >> 2) + a1
		b3 := a3 + (a5 >> 2)
		b5 := (a3 >> 2) - a5
		b7 := a7 - (a1 >> 2)

		r[0] = b0 + b7
		r[1] = b2 + b5
		r[2] = b4 + b3
		r[3] = b6 + b1
		r[4] = b6 - b1
		r[5] = b4 - b3
		r[6] = b2 - b5
		r[7] = b0 - b7
	}
	// Vertical pass
	for j := 0; j < 8; j++ {
		c := func(row int) int16 { return block[row*8+j] }
		a0 := c(0) + c(4)
		a2 := c(0) - c(4)
		a4 := (c(2) >> 1) - c(6)
		a6 := c(2) + (c(6) >> 1)
		b0 := a0 + a6
		b2 := a2 + a4
		b4 := a2 - a4
		b6 := a0 - a6

		a1 := -c(3) + c(5) - c(7) - (c(7) >> 1)
		a3 := c(1) + c(7) - c(3) - (c(3) >> 1)
		a5 := -c(1) + c(7) + c(5) + (c(5) >> 1)
		a7 := c(3) + c(5) + c(1) + (c(1) >> 1)
		b1 := (a7 >> 2) + a1
		b3 := a3 + (a5 >> 2)
		b5 := (a3 >> 2) - a5
		b7 := a7 - (a1 >> 2)

		block[0*8+j] = (b0 + b7 + 32) >> 6
		block[1*8+j] = (b2 + b5 + 32) >> 6
		block[2*8+j] = (b4 + b3 + 32) >> 6
		block[3*8+j] = (b6 + b1 + 32) >> 6
		block[4*8+j] = (b6 - b1 + 32) >> 6
		block[5*8+j] = (b4 - b3 + 32) >> 6
		block[6*8+j] = (b2 - b5 + 32) >> 6
		block[7*8+j] = (b0 - b7 + 32) >> 6
	}
}

// 8×8 dequantization scale factors (Table 8-15)
var dequantV8 = [6][6]int16{
	{20, 18, 32, 19, 25, 24},
	{22, 19, 35, 21, 28, 26},
	{26, 23, 42, 24, 33, 31},
	{28, 25, 45, 26, 35, 33},
	{32, 28, 51, 30, 40, 38},
	{36, 32, 58, 34, 46, 43},
}

var posToV8 = [64]int{
	0, 3, 4, 3, 0, 3, 4, 3,
	3, 1, 5, 1, 3, 1, 5, 1,
	4, 5, 2, 5, 4, 5, 2, 5,
	3, 1, 5, 1, 3, 1, 5, 1,
	0, 3, 4, 3, 0, 3, 4, 3,
	3, 1, 5, 1, 3, 1, 5, 1,
	4, 5, 2, 5, 4, 5, 2, 5,
	3, 1, 5, 1, 3, 1, 5, 1,
}

// Dequant8x8 dequantizes an 8×8 block.
func Dequant8x8(block []int16, qp int) {
	qpDiv6 := uint(qp / 6)
	qpMod6 := qp % 6
	for i := 0; i < 64; i++ {
		if block[i] != 0 {
			v := int32(dequantV8[qpMod6][posToV8[i]])
			block[i] = int16(int32(block[i]) * v << qpDiv6)
		}
	}
}

// ZigZag8x8 scan order.
var ZigZag8x8 = [64]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

// IDCT8x8Scalar is the pure Go reference for testing.
func IDCT8x8Scalar(block []int16) {
	// Horizontal pass
	for i := 0; i < 8; i++ {
		r := block[i*8 : i*8+8]
		a0 := r[0] + r[4]; a2 := r[0] - r[4]
		a4 := (r[2] >> 1) - r[6]; a6 := r[2] + (r[6] >> 1)
		b0 := a0 + a6; b2 := a2 + a4; b4 := a2 - a4; b6 := a0 - a6
		a1 := -r[3] + r[5] - r[7] - (r[7] >> 1)
		a3 := r[1] + r[7] - r[3] - (r[3] >> 1)
		a5 := -r[1] + r[7] + r[5] + (r[5] >> 1)
		a7 := r[3] + r[5] + r[1] + (r[1] >> 1)
		b1 := (a7 >> 2) + a1; b3 := a3 + (a5 >> 2)
		b5 := (a3 >> 2) - a5; b7 := a7 - (a1 >> 2)
		r[0] = b0+b7; r[1] = b2+b5; r[2] = b4+b3; r[3] = b6+b1
		r[4] = b6-b1; r[5] = b4-b3; r[6] = b2-b5; r[7] = b0-b7
	}
	// Vertical pass
	for j := 0; j < 8; j++ {
		c := func(row int) int16 { return block[row*8+j] }
		a0 := c(0)+c(4); a2 := c(0)-c(4)
		a4 := (c(2)>>1)-c(6); a6 := c(2)+(c(6)>>1)
		b0 := a0+a6; b2 := a2+a4; b4 := a2-a4; b6 := a0-a6
		a1 := -c(3)+c(5)-c(7)-(c(7)>>1)
		a3 := c(1)+c(7)-c(3)-(c(3)>>1)
		a5 := -c(1)+c(7)+c(5)+(c(5)>>1)
		a7 := c(3)+c(5)+c(1)+(c(1)>>1)
		b1 := (a7>>2)+a1; b3 := a3+(a5>>2); b5 := (a3>>2)-a5; b7 := a7-(a1>>2)
		block[0*8+j] = (b0+b7+32) >> 6; block[1*8+j] = (b2+b5+32) >> 6
		block[2*8+j] = (b4+b3+32) >> 6; block[3*8+j] = (b6+b1+32) >> 6
		block[4*8+j] = (b6-b1+32) >> 6; block[5*8+j] = (b4-b3+32) >> 6
		block[6*8+j] = (b2-b5+32) >> 6; block[7*8+j] = (b0-b7+32) >> 6
	}
}

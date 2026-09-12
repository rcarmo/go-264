package transform

// H.264 4×4 integer transform and quantization.
// ITU-T H.264 §8.5.12
//
// The H.264 "DCT" is not a true DCT — it's a scaled integer transform
// that avoids floating-point entirely. The core transform matrix is:
//
//   Cf = [ 1  1  1  1 ]
//        [ 2  1 -1 -2 ]
//        [ 1 -1 -1  1 ]
//        [ 1 -2  2 -1 ]
//
// Forward: Y = Cf * X * Cf^T (with post-scaling)
// Inverse: X = Ci^T * Y * Ci (with pre-scaling)
//
// Butterfly intermediates use int32; stored pass results narrow to int16.

// IDCT4x4 performs the inverse 4×4 integer transform (in-place).
// Input: dequantized coefficients in block[0:16].
// Output: residual pixel values.
func IDCT4x4(block []int16) {
	if hasSSE2Transform && len(block) >= 16 {
		IDCT4x4_SSE2(&block[0])
		return
	}
	if HasNEON && len(block) >= 16 {
		IDCT4x4_NEON(&block[0])
		return
	}
	IDCT4x4Scalar(block)
}

// Output: transform coefficients (before quantization).
func DCT4x4(block []int16) {
	if hasSSE2Transform && len(block) >= 16 {
		DCT4x4_SSE2(&block[0])
		return
	}
	if HasNEON && len(block) >= 16 {
		DCT4x4_NEON(&block[0])
		return
	}
	DCT4x4Scalar(block)
}

// Quantization tables (ITU-T H.264 §8.5.8)
// MF[qp%6] for 4×4 blocks.
var quantMF = [6][3]int16{
	{13107, 5243, 8066},
	{11916, 4660, 7490},
	{10082, 4194, 6554},
	{9362, 3647, 5825},
	{8192, 3355, 5243},
	{7282, 2893, 4559},
}

// Dequantization scale factors.
var dequantV = [6][3]int16{
	{10, 16, 13},
	{11, 18, 14},
	{13, 20, 16},
	{14, 23, 18},
	{16, 25, 20},
	{18, 29, 23},
}

// Position-to-V-index mapping for 4×4 block.
var posToV = [16]int{
	0, 2, 0, 2,
	2, 1, 2, 1,
	0, 2, 0, 2,
	2, 1, 2, 1,
}

var dequant4x4Scale [52][16]int32

func init() {
	for qp := 0; qp < 52; qp++ {
		qpDiv6 := uint(qp / 6)
		qpMod6 := qp % 6
		for i := 0; i < 16; i++ {
			dequant4x4Scale[qp][i] = int32(dequantV[qpMod6][posToV[i]]) << qpDiv6
		}
	}
}

// Dequant4x4 dequantizes a 4×4 block of transform coefficients.
// QP range: 0-51.
func Dequant4x4(block []int16, qp int) {
	dequant4x4Range(block, qp, 0)
}

// Dequant4x4Block dequantizes a full fixed-size 4×4 block. It is intended for
// hot decode paths that already work with [16]int16 residual arrays and avoids
// the public slice helper's length checks.
func Dequant4x4Block(block *[16]int16, qp int) {
	if qp < 0 {
		qp = 0
	} else if qp > 51 {
		qp = 51
	}
	dequant4Kernel(block[:], qp, 0)
}

// Dequant4x4AC dequantizes only AC coefficients (positions 1..15), preserving
// an already-transformed/dequantized DC coefficient. Used by Intra16x16 and
// chroma DC paths where DC has a separate Hadamard/dequant step.
func Dequant4x4AC(block []int16, qp int) {
	dequant4x4Range(block, qp, 1)
}

func dequant4x4Range(block []int16, qp int, start int) {
	if len(block) < 16 {
		return
	}
	if qp < 0 {
		qp = 0
	} else if qp > 51 {
		qp = 51
	}
	if start < 0 {
		start = 0
	} else if start > 16 {
		return
	}
	dequant4Kernel(block[:16], qp, start)
}

// Quant4x4 quantizes a 4×4 block of transform coefficients.
func Quant4x4(block []int16, qp int) {
	if len(block) < 16 {
		return
	}
	if qp < 0 {
		qp = 0
	} else if qp > 51 {
		qp = 51
	}
	qpDiv6 := qp / 6
	qpMod6 := qp % 6
	qbits := uint(15 + qpDiv6)
	add := int32(1<<qbits) / 3 // rounding offset (intra: 1/3, inter: 1/6)
	for i := 0; i < 16; i++ {
		if block[i] != 0 {
			mf := int32(quantMF[qpMod6][posToV[i]])
			v := int32(block[i])
			sign := int32(1)
			if v < 0 {
				sign = -1
				v = -v
			}
			block[i] = int16(sign * ((v*mf + add) >> qbits))
		}
	}
}

// Zig-zag scan order for 4×4 blocks.
var ZigZag4x4 = [16]int{
	0, 1, 4, 8,
	5, 2, 3, 6,
	9, 12, 13, 10,
	7, 11, 14, 15,
}

// DequantVTable returns the dequantization scale factor table.
func DequantVTable() [6][3]int16 { return dequantV }

// PosToVTable returns the position-to-V-index mapping.
func PosToVTable() [16]int { return posToV }

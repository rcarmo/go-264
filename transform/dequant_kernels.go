package transform

var dequant4Packed [52][16]int16
var dequant8Packed [52][64]int16

func init() {
	for qp := 0; qp < 52; qp++ {
		for i := range dequant4Packed[qp] {
			dequant4Packed[qp][i] = int16(int32(dequantV[qp%6][posToV[i]]) << uint(qp/6))
		}
		for i := range dequant8Packed[qp] {
			dequant8Packed[qp][i] = int16(int32(dequantV8[qp%6][posToV8[i]]) << uint(qp/6))
		}
	}
}
func dequant4Scalar(block []int16, qp, start int) {
	for i := start; i < 16; i++ {
		block[i] = int16(int32(block[i]) * dequant4x4Scale[qp][i])
	}
}
func dequant8Scalar(block []int16, qp int) {
	for i := 0; i < 64; i++ {
		if block[i] != 0 {
			scale := int32(dequantV8[qp%6][posToV8[i]]) << uint(qp/6)
			block[i] = int16((int32(block[i])*scale + 2) >> 2)
		}
	}
}

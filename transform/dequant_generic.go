//go:build (!amd64 && !arm64) || purego || (arm64 && !go1.27)

package transform

func dequant4Kernel(block []int16, qp, start int) { dequant4Scalar(block, qp, start) }
func dequant8Kernel(block []int16, qp int)        { dequant8Scalar(block, qp) }

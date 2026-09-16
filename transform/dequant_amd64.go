//go:build amd64 && !purego

package transform

func dequant4Kernel(block []int16, qp, start int) {
	if start == 16 {
		return
	}
	if start == 0 {
		dequant4SSE2(&block[0], &dequant4Packed[qp][0])
		return
	}
	if start == 1 {
		dc := block[0]
		dequant4SSE2(&block[0], &dequant4Packed[qp][0])
		block[0] = dc
		return
	}
	dequant4Scalar(block, qp, start)
}
func dequant8Kernel(block []int16, qp int) { dequant8SSE2(&block[0], &dequant8Packed[qp][0]) }

//go:noescape
func dequant4SSE2(block, scale *int16)

//go:noescape
func dequant8SSE2(block, scale *int16)

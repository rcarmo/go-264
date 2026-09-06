//go:build arm64 && !purego && go1.27

package transform

// dequant4Kernel scales all coefficients together; AC-only decoding restores
// the separately reconstructed DC coefficient after the vector multiply.
func dequant4Kernel(block []int16, qp, start int) {
	if !HasNEON || start > 1 {
		dequant4Scalar(block, qp, start)
		return
	}
	dc := block[0]
	dequant4x4NEON(&block[0], &dequant4Packed[qp][0])
	if start == 1 {
		block[0] = dc
	}
}

func dequant8Kernel(block []int16, qp int) { dequant8Scalar(block, qp) }

// dequant4x4NEON uses low-half products, matching int16 coefficient stores.
//
//go:noescape
func dequant4x4NEON(block, scale *int16)

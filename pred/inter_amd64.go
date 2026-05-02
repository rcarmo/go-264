//go:build amd64

package pred

// InterPred16x16_ASM performs full-pixel motion compensation using SSE2.
// Copies 16 bytes per row using MOVOU.
//go:noescape
func InterPred16x16Copy_ASM(dst *uint8, src *uint8, dstStride, srcStride int)

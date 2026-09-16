//go:build amd64 && !purego

package transform

// idct8Packed keeps narrow pass outputs exactly as the existing assembly.
// Packed transposes use stack-owned scratch; vector lanes operate on independent axes.
func idct8Packed(block []int16) {
	var transposed, pass [64]int16
	transpose8SSE2(&transposed[0], &block[0])
	idct8PassSSE2(&pass[0], &transposed[0], false)
	transpose8SSE2(&transposed[0], &pass[0])
	idct8PassSSE2(&block[0], &transposed[0], true)
}

//go:noescape
func idct8PassSSE2(dst, src *int16, final bool)

//go:noescape
func transpose8SSE2(dst, src *int16)

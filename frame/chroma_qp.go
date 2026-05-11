package frame

// ChromaQP converts a luma QP + chroma_qp_index_offset to chroma QP using
// H.264 Table A-1 (§7.4.2.1.1). The offset is typically pps.ChromaQPIndexOffset.
// Returns a value in [0, 51].
func ChromaQP(lumaQP, offset int) int {
	idx := lumaQP + offset
	if idx < 0 {
		idx = 0
	}
	if idx > 51 {
		idx = 51
	}
	return chromaQPTable[idx]
}

// chromaQPTable maps QPi (luma QP + chroma_qp_index_offset, clamped to 0..51)
// to chroma QP. H.264 §7.4.2.1.1 Table A-1.
var chromaQPTable = [52]int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
	12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27,
	28, 29, 29, 30, 31, 32, 32, 33, 34, 34, 35, 35, 36, 36, 37, 37,
	37, 38, 38, 38, 39, 39, 39, 39,
}

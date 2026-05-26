package decode

// decode/context.go — per-MB context helpers, block-index lookup tables,
// and CABAC context utility functions.

import "github.com/rcarmo/go-264/syntax"

// blk4x4X/Y: pixel offset of each luma 4x4 block within the 16×16 macroblock.
// Derived from syntax.Blk4x4Col/Row (column/row index 0-3) multiplied by 4.
var blk4x4X = func() [16]int {
	var a [16]int
	for i := 0; i < 16; i++ {
		a[i] = syntax.Blk4x4Col[i] * 4
	}
	return a
}()

var blk4x4Y = func() [16]int {
	var a [16]int
	for i := 0; i < 16; i++ {
		a[i] = syntax.Blk4x4Row[i] * 4
	}
	return a
}()

// blkXYToIdx aliases the canonical table from the slice package.
var blkXYToIdx = syntax.BlkXYToIdx

// nzCBFCtxLuma returns (nza, nzb) for the CABAC coded_block_flag context of
// luma 4x4 block blkIdx using in-MB non-zero tracking and left/top MB contexts.
func nzCBFCtxLuma(blkIdx int, nzMB *[16]int, leftNZ, topNZ *[16]int) (int, int) {
	col := syntax.Blk4x4Col[blkIdx]
	row := syntax.Blk4x4Row[blkIdx]
	var la, lb int
	if col > 0 {
		la = nzMB[blkXYToIdx[row][col-1]]
	} else if leftNZ != nil {
		la = leftNZ[blkXYToIdx[row][3]]
	}
	if row > 0 {
		lb = nzMB[blkXYToIdx[row-1][col]]
	} else if topNZ != nil {
		lb = topNZ[blkXYToIdx[3][col]]
	}
	nza, nzb := 0, 0
	if la > 0 {
		nza = 1
	}
	if lb > 0 {
		nzb = 1
	}
	return nza, nzb
}

// nzCBFCtxChroma returns (nza, nzb) for the CABAC coded_block_flag context of
// chroma 4x4 block blkIdx (0-3 in 2x2 grid) for component comp.
func nzCBFCtxChroma(comp, blkIdx int, nzMBChroma *[2][4]int, leftChromaNZ, topChromaNZ *[2][4]int) (int, int) {
	cx := blkIdx % 2
	cy := blkIdx / 2
	var la, lb int
	if cx > 0 {
		la = nzMBChroma[comp][cy*2+(cx-1)]
	} else if leftChromaNZ != nil {
		la = leftChromaNZ[comp][cy*2+1]
	}
	if cy > 0 {
		lb = nzMBChroma[comp][(cy-1)*2+cx]
	} else if topChromaNZ != nil {
		lb = topChromaNZ[comp][2+cx]
	}
	nza, nzb := 0, 0
	if la > 0 {
		nza = 1
	}
	if lb > 0 {
		nzb = 1
	}
	return nza, nzb
}

// cabacMBTypeFlag returns the CABAC mb_type context flag: 1 if I_16x16 or
// I_PCM (used as left/top neighbour gate in decode_cabac_intra_mb_type), 0
// otherwise.
func cabacMBTypeFlag(mbType uint32) uint32 {
	if mbType >= 1 && mbType <= 25 {
		return 1
	}
	return 0
}

// isCABACIntra16orPCM returns the stored mb_type flag directly (1 = I_16x16 or
// I_PCM, 0 = other). Used for the CABAC intra mb_type context calculation.
func isCABACIntra16orPCM(f uint32) uint32 { return f }

func cabacUseFFmpegEdgeContexts() bool { return true }

func cabacLeftCBPForCurrent(leftCBP uint32) uint32 {
	// FFmpeg's CABAC cache projects the left macroblock's CBP to the two right
	// edge 8x8 luma groups while preserving chroma/DC side-band bits. Raw CBP
	// works for top neighbours, but using it for left neighbours makes luma-CBP
	// contexts too broad (e.g. 0x0f must appear as 0x0a on the next MB).
	return (leftCBP & 0x7F0) | ((leftCBP >> 0) & 0x2) | (((leftCBP >> 2) & 0x2) << 2)
}

func cabacUnavailableCBP(leftCBP, topCBP uint32, mbX, mbY int, intra bool) (uint32, uint32) {
	defaultCBP := uint32(0x00F)
	if intra {
		defaultCBP = 0x7CF
	}
	if mbX == 0 {
		leftCBP = defaultCBP
	}
	if mbY == 0 {
		topCBP = defaultCBP
	}
	return leftCBP, topCBP
}

func cabacTraceEdgeNZ(mbX, mbY int, leftNZ, topNZ *[16]int) (*[16]int, *[16]int) {
	if !cabacUseFFmpegEdgeContexts() {
		return leftNZ, topNZ
	}
	if mbX == 0 {
		var nz [16]int
		for i := range nz {
			nz[i] = 1
		}
		leftNZ = &nz
	}
	if mbY == 0 {
		var nz [16]int
		for i := range nz {
			nz[i] = 1
		}
		topNZ = &nz
	}
	return leftNZ, topNZ
}

func cabacTraceEdgeChromaNZ(mbX, mbY int, leftNZ, topNZ *[2][4]int) (*[2][4]int, *[2][4]int) {
	if !cabacUseFFmpegEdgeContexts() {
		return leftNZ, topNZ
	}
	if mbX == 0 {
		var nz [2][4]int
		for comp := range nz {
			for blk := range nz[comp] {
				nz[comp][blk] = 1
			}
		}
		leftNZ = &nz
	}
	if mbY == 0 {
		var nz [2][4]int
		for comp := range nz {
			for blk := range nz[comp] {
				nz[comp][blk] = 1
			}
		}
		topNZ = &nz
	}
	return leftNZ, topNZ
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

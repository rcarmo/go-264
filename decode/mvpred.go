package decode

// decode/mvpred.go — motion vector prediction helpers and 4x4 MV/ref cache
// write-back. All functions are pure computations on the MV cache slices with
// no dependency on the frame or reconstruction path.

import "github.com/rcarmo/go-264/syntax"

// writeBackInter4x4 fills the 4x4 MV/ref cache for an inter macroblock after
// decoding. Each luma4x4BlkIdx cell is written with the partition MV and ref.
func writeBackInter4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, mbX, mbY int, mb *syntax.MBInter) {
	fill := func(x4, y4, w4, h4 int, mv syntax.MotionVector, ref int8) {
		baseX, baseY := mbX*4+x4, mbY*4+y4
		for y := 0; y < h4; y++ {
			row := (baseY+y)*stride4 + baseX
			for x := 0; x < w4; x++ {
				mv4[row+x] = mv
				ref4[row+x] = ref
			}
		}
	}
	switch mb.MBType {
	case syntax.PMBTypeP16x16:
		fill(0, 0, 4, 4, mb.MV[0], mb.RefIdx[0])
	case syntax.PMBTypeP16x8:
		fill(0, 0, 4, 2, mb.MV[0], mb.RefIdx[0])
		fill(0, 2, 4, 2, mb.MV[1], mb.RefIdx[1])
	case syntax.PMBTypeP8x16:
		fill(0, 0, 2, 4, mb.MV[0], mb.RefIdx[0])
		fill(2, 0, 2, 4, mb.MV[1], mb.RefIdx[1])
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		for part := 0; part < 4; part++ {
			baseX := (part & 1) * 2
			baseY := (part >> 1) * 2
			ref := mb.RefIdx[part]
			switch mb.SubMBType[part] {
			case 0: // 8x8
				fill(baseX, baseY, 2, 2, mb.SubMV[part*4], ref)
			case 1: // 8x4
				fill(baseX, baseY, 2, 1, mb.SubMV[part*4], ref)
				fill(baseX, baseY+1, 2, 1, mb.SubMV[part*4+1], ref)
			case 2: // 4x8
				fill(baseX, baseY, 1, 2, mb.SubMV[part*4], ref)
				fill(baseX+1, baseY, 1, 2, mb.SubMV[part*4+1], ref)
			case 3: // 4x4
				fill(baseX, baseY, 1, 1, mb.SubMV[part*4], ref)
				fill(baseX+1, baseY, 1, 1, mb.SubMV[part*4+1], ref)
				fill(baseX, baseY+1, 1, 1, mb.SubMV[part*4+2], ref)
				fill(baseX+1, baseY+1, 1, 1, mb.SubMV[part*4+3], ref)
			}
		}
	}
}

// writeBackIntra4x4 marks all 4x4 cells of an intra macroblock as ref=-1 in
// the ref4 cache (no L0 reference).
func writeBackIntra4x4(ref4 []int8, stride4, mbX, mbY int) {
	baseX, baseY := mbX*4, mbY*4
	for y := 0; y < 4; y++ {
		row := (baseY+y)*stride4 + baseX
		for x := 0; x < 4; x++ {
			ref4[row+x] = -1
		}
	}
}

// representativeRightEdgeMV returns the MV/ref from the rightmost partition,
// used as the representative L0 context for the current macroblock in future
// MV predictor lookups.
func representativeRightEdgeMV(mb *syntax.MBInter) (syntax.MotionVector, int8) {
	switch mb.MBType {
	case syntax.PMBTypeP8x16:
		return mb.MV[1], mb.RefIdx[1]
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		return mb.SubMV[4], mb.RefIdx[1]
	default:
		return mb.MV[0], mb.RefIdx[0]
	}
}

// predictSkipMV returns the MV predictor for a P-skip macroblock.
// It uses the 4x4 cache via predictSkipMV4x4; the predMV argument (from the
// skip path) is returned unchanged as a pass-through.
func predictSkipMV(ctx []syntax.MotionVector, refCtx []int8, pred syntax.MotionVector, mbIdx, mbX, mbY, mbWidth int) syntax.MotionVector {
	return pred
}

// predictSkipMV4x4 computes the P-skip MV predictor directly from the 4x4
// cache, matching FFmpeg's pred_pskip_motion / h264_mv_pred_skip path.
func predictSkipMV4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int) syntax.MotionVector {
	const partNotAvailable int8 = -2
	left, leftRef := getMV4(mv4, ref4, stride4, x4-1, y4)
	top, topRef := getMV4(mv4, ref4, stride4, x4, y4-1)
	if leftRef == partNotAvailable || topRef == partNotAvailable {
		return syntax.MotionVector{}
	}
	if (leftRef == 0 && left.X == 0 && left.Y == 0) || (topRef == 0 && top.X == 0 && top.Y == 0) {
		return syntax.MotionVector{}
	}
	c, cRef := getMV4(mv4, ref4, stride4, x4+4, y4-1)
	if cRef == partNotAvailable {
		c, cRef = getMV4(mv4, ref4, stride4, x4-1, y4-1)
	}
	matchCount := 0
	if leftRef == 0 {
		matchCount++
	}
	if topRef == 0 {
		matchCount++
	}
	if cRef == 0 {
		matchCount++
	}
	if matchCount > 1 {
		return syntax.MotionVector{X: median3(left.X, top.X, c.X), Y: median3(left.Y, top.Y, c.Y)}
	}
	if matchCount == 1 {
		if leftRef == 0 {
			return left
		}
		if topRef == 0 {
			return top
		}
		return c
	}
	return syntax.MotionVector{X: median3(left.X, top.X, c.X), Y: median3(left.Y, top.Y, c.Y)}
}

func predictMBMV(ctx []syntax.MotionVector, refCtx []int8, targetRef int8, mbIdx, mbX, mbY, mbWidth int) syntax.MotionVector {
	a, b, c, availA, availB, availC := neighbourMVs(ctx, refCtx, targetRef, mbIdx, mbX, mbY, mbWidth)
	return syntax.PredictMV(a, b, c, availA, availB, availC)
}

func getMV4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int) (syntax.MotionVector, int8) {
	const partNotAvailable int8 = -2
	if x4 < 0 || y4 < 0 || x4 >= stride4 || y4*stride4+x4 >= len(ref4) {
		return syntax.MotionVector{}, partNotAvailable
	}
	idx := y4*stride4 + x4
	return mv4[idx], ref4[idx]
}

func predictMotion4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4, partWidth4 int, targetRef int8) syntax.MotionVector {
	const partNotAvailable int8 = -2
	a, refA := getMV4(mv4, ref4, stride4, x4-1, y4)
	b, refB := getMV4(mv4, ref4, stride4, x4, y4-1)
	c, refC := getMV4(mv4, ref4, stride4, x4+partWidth4, y4-1)
	if refC == partNotAvailable {
		c, refC = getMV4(mv4, ref4, stride4, x4-1, y4-1)
	}
	matchCount := 0
	if refA == targetRef {
		matchCount++
	}
	if refB == targetRef {
		matchCount++
	}
	if refC == targetRef {
		matchCount++
	}
	if matchCount > 1 {
		return syntax.MotionVector{X: median3(a.X, b.X, c.X), Y: median3(a.Y, b.Y, c.Y)}
	}
	if matchCount == 1 {
		if refA == targetRef {
			return a
		}
		if refB == targetRef {
			return b
		}
		return c
	}
	if refB == partNotAvailable && refC == partNotAvailable && refA != partNotAvailable {
		return a
	}
	return syntax.MotionVector{X: median3(a.X, b.X, c.X), Y: median3(a.Y, b.Y, c.Y)}
}

func predict16x8Motion4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int, part int, targetRef int8) syntax.MotionVector {
	if part == 0 {
		b, refB := getMV4(mv4, ref4, stride4, x4, y4-1)
		if refB == targetRef {
			return b
		}
		return predictMotion4x4(mv4, ref4, stride4, x4, y4, 4, targetRef)
	}
	a, refA := getMV4(mv4, ref4, stride4, x4-1, y4+2)
	if refA == targetRef {
		return a
	}
	return predictMotion4x4(mv4, ref4, stride4, x4, y4+2, 4, targetRef)
}

func predict8x16Motion4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int, part int, targetRef int8) syntax.MotionVector {
	if part == 0 {
		a, refA := getMV4(mv4, ref4, stride4, x4-1, y4)
		if refA == targetRef {
			return a
		}
		return predictMotion4x4(mv4, ref4, stride4, x4, y4, 2, targetRef)
	}
	c, refC := getMV4(mv4, ref4, stride4, x4+4, y4-1)
	if refC == -2 {
		c, refC = getMV4(mv4, ref4, stride4, x4-1, y4-1)
	}
	if refC == targetRef {
		return c
	}
	return predictMotion4x4(mv4, ref4, stride4, x4+2, y4, 2, targetRef)
}

func fillMV4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4, w4, h4 int, mv syntax.MotionVector, ref int8) {
	for y := 0; y < h4; y++ {
		row := (y4+y)*stride4 + x4
		for x := 0; x < w4; x++ {
			if row+x >= 0 && row+x < len(ref4) {
				mv4[row+x] = mv
				ref4[row+x] = ref
			}
		}
	}
}

func median3(a, b, c int16) int16 {
	if a > b {
		a, b = b, a
	}
	if b > c {
		b, c = c, b
	}
	if a > b {
		a, b = b, a
	}
	return b
}

func neighbourMVs(ctx []syntax.MotionVector, refCtx []int8, targetRef int8, mbIdx, mbX, mbY, mbWidth int) (a, b, c syntax.MotionVector, availA, availB, availC bool) {
	availA = mbX > 0 && refCtx[mbIdx-1] == targetRef
	availB = mbY > 0 && refCtx[mbIdx-mbWidth] == targetRef
	availC = mbY > 0 && mbX+1 < mbWidth && refCtx[mbIdx-mbWidth+1] == targetRef
	if availA {
		a = ctx[mbIdx-1]
	}
	if availB {
		b = ctx[mbIdx-mbWidth]
	}
	if availC {
		c = ctx[mbIdx-mbWidth+1]
	} else if mbY > 0 && mbX > 0 && refCtx[mbIdx-mbWidth-1] == targetRef {
		c = ctx[mbIdx-mbWidth-1]
		availC = true
	}
	return
}

func addMV(mv *syntax.MotionVector, pred syntax.MotionVector) {
	mv.X += pred.X
	mv.Y += pred.Y
}

func applyMVPredictors(mb *syntax.MBInter, ctx []syntax.MotionVector, refCtx []int8, mv4 []syntax.MotionVector, ref4 []int8, stride4 int, mbIdx, mbX, mbY, mbWidth int) {
	switch mb.MBType {
	case syntax.PMBTypeP16x16:
		addMV(&mb.MV[0], predictMotion4x4(mv4, ref4, stride4, mbX*4, mbY*4, 4, mb.RefIdx[0]))
	case syntax.PMBTypeP16x8:
		pred0 := predict16x8Motion4x4(mv4, ref4, stride4, mbX*4, mbY*4, 0, mb.RefIdx[0])
		pred1 := predict16x8Motion4x4(mv4, ref4, stride4, mbX*4, mbY*4, 1, mb.RefIdx[1])
		addMV(&mb.MV[0], pred0)
		addMV(&mb.MV[1], pred1)
	case syntax.PMBTypeP8x16:
		// Predict the right 8x16 partition against the left partition just decoded,
		// matching the H.264 intra-MB MV cache update order. We can safely write
		// into mv4/ref4 directly: these are current-MB cache positions that will be
		// overwritten with the same final values by the normal write-back path.
		x4, y4 := mbX*4, mbY*4
		pred0 := predict8x16Motion4x4(mv4, ref4, stride4, x4, y4, 0, mb.RefIdx[0])
		addMV(&mb.MV[0], pred0)
		fillMV4(mv4, ref4, stride4, x4, y4, 2, 4, mb.MV[0], mb.RefIdx[0])
		pred1 := predict8x16Motion4x4(mv4, ref4, stride4, x4, y4, 1, mb.RefIdx[1])
		addMV(&mb.MV[1], pred1)
	case syntax.PMBTypeP8x8, syntax.PMBTypeP8x8ref0:
		mbBaseX, mbBaseY := mbX*4, mbY*4
		for part := 0; part < 4; part++ {
			baseX := mbBaseX + (part&1)*2
			baseY := mbBaseY + (part>>1)*2
			ref := mb.RefIdx[part]
			switch mb.SubMBType[part] {
			case 0: // P_L0_8x8
				pred := predictMotion4x4(mv4, ref4, stride4, baseX, baseY, 2, ref)
				addMV(&mb.SubMV[part*4], pred)
				fillMV4(mv4, ref4, stride4, baseX, baseY, 2, 2, mb.SubMV[part*4], ref)
			case 1: // P_L0_8x4
				for j := 0; j < 2; j++ {
					idx := part*4 + j
					y := baseY + j
					pred := predictMotion4x4(mv4, ref4, stride4, baseX, y, 2, ref)
					addMV(&mb.SubMV[idx], pred)
					fillMV4(mv4, ref4, stride4, baseX, y, 2, 1, mb.SubMV[idx], ref)
				}
			case 2: // P_L0_4x8
				for j := 0; j < 2; j++ {
					idx := part*4 + j
					x := baseX + j
					pred := predictMotion4x4(mv4, ref4, stride4, x, baseY, 1, ref)
					addMV(&mb.SubMV[idx], pred)
					fillMV4(mv4, ref4, stride4, x, baseY, 1, 2, mb.SubMV[idx], ref)
				}
			case 3: // P_L0_4x4
				for j := 0; j < 4; j++ {
					idx := part*4 + j
					x := baseX + (j & 1)
					y := baseY + (j >> 1)
					pred := predictMotion4x4(mv4, ref4, stride4, x, y, 1, ref)
					addMV(&mb.SubMV[idx], pred)
					fillMV4(mv4, ref4, stride4, x, y, 1, 1, mb.SubMV[idx], ref)
				}
			}
		}
		mb.MV[0] = mb.SubMV[0]
	}
}

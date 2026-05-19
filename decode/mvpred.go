package decode

// decode/mvpred.go — motion vector prediction helpers and 4x4 MV/ref cache
// write-back. All functions are pure computations on the MV cache slices with
// no dependency on the frame or reconstruction path.

import (
	"fmt"
	"os"

	"github.com/rcarmo/go-264/syntax"
)

// writeBackInter4x4 fills the 4x4 MV/ref cache for an inter macroblock after
// decoding. Each luma4x4BlkIdx cell is written with the partition MV and ref.
func writeBackInter4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, mbX, mbY int, mb *syntax.MBInter) {
	if mb == nil || stride4 <= 0 {
		return
	}
	fill := func(x4, y4, w4, h4 int, mv syntax.MotionVector, ref int8) {
		baseX, baseY := mbX*4+x4, mbY*4+y4
		for y := 0; y < h4; y++ {
			row := (baseY+y)*stride4 + baseX
			for x := 0; x < w4; x++ {
				idx := row + x
				if idx >= 0 && idx < len(mv4) && idx < len(ref4) {
					mv4[idx] = mv
					ref4[idx] = ref
				}
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
	if stride4 <= 0 {
		return
	}
	baseX, baseY := mbX*4, mbY*4
	for y := 0; y < 4; y++ {
		row := (baseY+y)*stride4 + baseX
		for x := 0; x < 4; x++ {
			idx := row + x
			if idx >= 0 && idx < len(ref4) {
				ref4[idx] = -1
			}
		}
	}
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

func getMV4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int) (syntax.MotionVector, int8) {
	const partNotAvailable int8 = -2
	idx := y4*stride4 + x4
	if stride4 <= 0 || x4 < 0 || y4 < 0 || x4 >= stride4 || idx < 0 || idx >= len(ref4) || idx >= len(mv4) {
		return syntax.MotionVector{}, partNotAvailable
	}
	return mv4[idx], ref4[idx]
}

func cabacRefIdxCtx(ref4 []int8, stride4, x4, y4 int) int {
	refAt := func(cx, cy int) int8 {
		idx := cy*stride4 + cx
		if stride4 <= 0 || cx < 0 || cy < 0 || cx >= stride4 || idx < 0 || idx >= len(ref4) {
			return -2
		}
		return ref4[idx]
	}
	ctx := 0
	if refAt(x4-1, y4) > 0 {
		ctx++
	}
	if refAt(x4, y4-1) > 0 {
		ctx += 2
	}
	return ctx
}

func cabacRefIdxCtxsForMB(ref4 []int8, stride4, mbX, mbY int) [4]int {
	x4, y4 := mbX*4, mbY*4
	return [4]int{
		cabacRefIdxCtx(ref4, stride4, x4, y4),     // top-left 8x8 origin
		cabacRefIdxCtx(ref4, stride4, x4+2, y4),   // top-right 8x8 origin
		cabacRefIdxCtx(ref4, stride4, x4, y4+2),   // bottom-left 8x8 origin
		cabacRefIdxCtx(ref4, stride4, x4+2, y4+2), // bottom-right 8x8 origin
	}
}

func cabacMVDAMVD(mvd4 []syntax.MotionVector, stride4, x4, y4 int, component int) int {
	absComponent := func(cx, cy int) int {
		idx := cy*stride4 + cx
		if stride4 <= 0 || cx < 0 || cy < 0 || cx >= stride4 || idx < 0 || idx >= len(mvd4) {
			return 0
		}
		mv := mvd4[idx]
		v := int(mv.X)
		if component == 1 {
			v = int(mv.Y)
		}
		if v < 0 {
			return -v
		}
		return v
	}
	return absComponent(x4-1, y4) + absComponent(x4, y4-1)
}

func fillMVD4(mvd4 []syntax.MotionVector, stride4, x4, y4, w4, h4 int, mvd syntax.MotionVector) {
	if stride4 <= 0 {
		return
	}
	for y := 0; y < h4; y++ {
		row := (y4+y)*stride4 + x4
		for x := 0; x < w4; x++ {
			idx := row + x
			if idx >= 0 && idx < len(mvd4) {
				mvd4[idx] = mvd
			}
		}
	}
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
	var out syntax.MotionVector
	if matchCount > 1 {
		out = syntax.MotionVector{X: median3(a.X, b.X, c.X), Y: median3(a.Y, b.Y, c.Y)}
	} else if matchCount == 1 {
		if refA == targetRef {
			out = a
		} else if refB == targetRef {
			out = b
		} else {
			out = c
		}
	} else if refB == partNotAvailable && refC == partNotAvailable && refA != partNotAvailable {
		out = a
	} else {
		out = syntax.MotionVector{X: median3(a.X, b.X, c.X), Y: median3(a.Y, b.Y, c.Y)}
	}
	if os.Getenv("GO264_B_MVP_TRACE") != "" && stride4 > 0 {
		mb := (y4/4)*(stride4/4) + x4/4
		fmt.Fprintf(os.Stderr, "GOMVP mb=%04d x4=%d y4=%d pw=%d ref=%d A=%d/{%d,%d} B=%d/{%d,%d} C=%d/{%d,%d} out={%d,%d}\n", mb, x4, y4, partWidth4, targetRef, refA, a.X, a.Y, refB, b.X, b.Y, refC, c.X, c.Y, out.X, out.Y)
	}
	return out
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
	if stride4 <= 0 {
		return
	}
	for y := 0; y < h4; y++ {
		row := (y4+y)*stride4 + x4
		for x := 0; x < w4; x++ {
			idx := row + x
			if idx >= 0 && idx < len(mv4) && idx < len(ref4) {
				mv4[idx] = mv
				ref4[idx] = ref
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

func addMV(mv *syntax.MotionVector, pred syntax.MotionVector) {
	mv.X += pred.X
	mv.Y += pred.Y
}

func applyMVPredictors(mb *syntax.MBInter, mv4 []syntax.MotionVector, ref4 []int8, stride4 int, mbX, mbY int) {
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

func writeBackBidiL1Context(mv4 []syntax.MotionVector, ref4 []int8, stride4, mbX, mbY int, mb *syntax.MBBidi) {
	writeBackBidiListContext(mv4, ref4, stride4, mbX, mbY, mb, 1)
}

func writeBackBidiL0Context(mv4 []syntax.MotionVector, ref4 []int8, stride4, mbX, mbY int, mb *syntax.MBBidi) {
	writeBackBidiListContext(mv4, ref4, stride4, mbX, mbY, mb, 0)
}

func writeBackBidiListContext(mv4 []syntax.MotionVector, ref4 []int8, stride4, mbX, mbY int, mb *syntax.MBBidi, list int) {
	if mb == nil || stride4 <= 0 {
		return
	}
	fill := func(x4, y4, w4, h4 int, mv syntax.MotionVector, ref int8) {
		for dy := 0; dy < h4; dy++ {
			row := (y4+dy)*stride4 + x4
			for dx := 0; dx < w4; dx++ {
				idx := row + dx
				if idx >= 0 && idx < len(mv4) && idx < len(ref4) {
					mv4[idx] = mv
					ref4[idx] = ref
				}
			}
		}
	}
	x4, y4 := mbX*4, mbY*4
	if mb.MBType == syntax.BMBTypeDirect16x16 {
		mv, ref := mb.MVL0[0], mb.RefIdxL0[0]
		if list == 1 {
			mv, ref = mb.MVL1[0], mb.RefIdxL1[0]
		}
		fill(x4, y4, 4, 4, mv, ref)
		return
	}
	if mb.MBType == syntax.BMBTypeB8x8 {
		for part := 0; part < 4; part++ {
			t := mb.SubMBType[part]
			usesList := t == 0 || (list == 0 && syntax.BMBSubUsesL0(t)) || (list == 1 && syntax.BMBSubUsesL1(t))
			if !usesList {
				continue
			}
			baseX, baseY := x4+(part&1)*2, y4+(part>>1)*2
			w4, h4 := syntax.BMBSubPartFillDims(t)
			parts := syntax.BMBSubPartCount(t)
			for j := 0; j < parts; j++ {
				var ox4, oy4 int
				switch t {
				case 4, 6, 8:
					ox4, oy4 = 0, j
				case 5, 7, 9:
					ox4, oy4 = j, 0
				default:
					ox4, oy4 = j&1, j>>1
				}
				mv, ref := mb.SubMVL0[part*4+j], mb.RefIdxL0[part]
				if list == 1 {
					mv, ref = mb.SubMVL1[part*4+j], mb.RefIdxL1[part]
				}
				fill(baseX+ox4, baseY+oy4, w4, h4, mv, ref)
			}
		}
		return
	}
	parts := cabacBPartsForType(mb.MBType)
	for part := 0; part < parts; part++ {
		if (list == 0 && !cabacBPartUsesL0(mb.MBType, part)) || (list == 1 && !cabacBPartUsesL1(mb.MBType, part)) {
			continue
		}
		mv, ref := mb.MVL0[part], mb.RefIdxL0[part]
		if list == 1 {
			mv, ref = mb.MVL1[part], mb.RefIdxL1[part]
		}
		w4, h4 := cabacBPartDims(mb.MBType, part)
		fill(x4+cabacBPartX(mb.MBType, part, parts), y4+cabacBPartY(mb.MBType, part, parts), w4, h4, mv, ref)
	}
}

func predictBPartMotion4x4(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int, mbType uint32, part int, targetRef int8) syntax.MotionVector {
	parts := cabacBPartsForType(mbType)
	if parts == 2 {
		switch mbType {
		case syntax.BMBTypeL016x8, syntax.BMBTypeL116x8, syntax.BMBTypeBi16x8:
			return predict16x8Motion4x4(mv4, ref4, stride4, x4, y4, part, targetRef)
		case syntax.BMBTypeL016x8b, syntax.BMBTypeL116x8b, syntax.BMBTypeBi16x8b,
			syntax.BMBTypeL08x16, syntax.BMBTypeL18x16, syntax.BMBTypeBi8x16:
			return predict8x16Motion4x4(mv4, ref4, stride4, x4, y4, part, targetRef)
		}
	}
	bx := x4 + cabacBPartX(mbType, part, parts)
	by := y4 + cabacBPartY(mbType, part, parts)
	pw, _ := cabacBPartDims(mbType, part)
	return predictMotion4x4(mv4, ref4, stride4, bx, by, pw, targetRef)
}

func applyB8x8DirectSpatial(mb *syntax.MBBidi, refL0 int8, mvL0 syntax.MotionVector, refL1 int8, mvL1 syntax.MotionVector) {
	if mb == nil || mb.MBType != syntax.BMBTypeB8x8 {
		return
	}
	for part, t := range mb.SubMBType {
		if t != 0 { // B_Direct_8x8 only; explicit L0/L1/Bi sub-MBs keep decoded MVD/MVP MVs.
			continue
		}
		mb.RefIdxL0[part] = refL0
		mb.RefIdxL1[part] = refL1
		mb.MVL0[part] = mvL0
		mb.MVL1[part] = mvL1
		for j := 0; j < 4; j++ {
			mb.SubMVL0[part*4+j] = mvL0
			mb.SubMVL1[part*4+j] = mvL1
		}
	}
}

func predictBDirectSpatialL0Ref(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int) int8 {
	const partNotAvailable int8 = -2
	_, leftRef := getMV4(mv4, ref4, stride4, x4-1, y4)
	_, topRef := getMV4(mv4, ref4, stride4, x4, y4-1)
	_, cRef := getMV4(mv4, ref4, stride4, x4+4, y4-1)
	if cRef == partNotAvailable {
		_, cRef = getMV4(mv4, ref4, stride4, x4-1, y4-1)
	}
	best := int8(127)
	for _, r := range []int8{leftRef, topRef, cRef} {
		if r >= 0 && r < best {
			best = r
		}
	}
	if best == 127 {
		return -1
	}
	return best
}

func predictBDirectSpatialL0ForSimpleRefs(mv4 []syntax.MotionVector, ref4 []int8, stride4, x4, y4 int) (int8, syntax.MotionVector) {
	const partNotAvailable int8 = -2
	left, leftRef := getMV4(mv4, ref4, stride4, x4-1, y4)
	top, topRef := getMV4(mv4, ref4, stride4, x4, y4-1)
	c, cRef := getMV4(mv4, ref4, stride4, x4+4, y4-1)
	if cRef == partNotAvailable {
		c, cRef = getMV4(mv4, ref4, stride4, x4-1, y4-1)
	}
	best := int8(127)
	for _, r := range []int8{leftRef, topRef, cRef} {
		if r >= 0 && r < best {
			best = r
		}
	}
	if best == 127 {
		return -1, syntax.MotionVector{}
	}
	matches := 0
	if leftRef == best {
		matches++
	}
	if topRef == best {
		matches++
	}
	if cRef == best {
		matches++
	}
	if matches > 1 {
		return best, syntax.MotionVector{X: median3(left.X, top.X, c.X), Y: median3(left.Y, top.Y, c.Y)}
	}
	if leftRef == best {
		return best, left
	}
	if topRef == best {
		return best, top
	}
	return best, c
}

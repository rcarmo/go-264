package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func main() {
	input := flag.String("i", "", "input Annex B H.264 bitstream")
	limit := flag.Int("limit", 64, "maximum macroblocks to trace per slice")
	cabac := flag.Bool("cabac", false, "enable CABAC-mode tracing (currently rejects CABAC streams instead of mis-tracing them)")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: trace264 -i input.h264 [-limit N] [-cabac]")
		os.Exit(2)
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	if err := trace(data, *limit, *cabac); err != nil {
		fmt.Fprintf(os.Stderr, "trace: %v\n", err)
		os.Exit(1)
	}
}

func trace(data []byte, limit int, cabacTrace bool) error {
	units := nal.SplitNALUnits(data)
	spsMap := map[uint32]*nal.SPS{}
	ppsMap := map[uint32]*nal.PPS{}
	for nalIdx, unit := range units {
		switch unit.Type {
		case nal.TypeSPS:
			sps, err := nal.ParseSPS(unit.Payload)
			if err != nil {
				return fmt.Errorf("nal %d SPS: %w", nalIdx, err)
			}
			spsMap[sps.SPSID] = sps
			fmt.Printf("nal=%d type=SPS id=%d size=%dx%d mbs=%dx%d\n", nalIdx, sps.SPSID, sps.Width, sps.Height, sps.PicWidthInMbs, sps.PicHeightInMapUnits)
		case nal.TypePPS:
			pps, err := nal.ParsePPS(unit.Payload)
			if err != nil {
				return fmt.Errorf("nal %d PPS: %w", nalIdx, err)
			}
			ppsMap[pps.PPSID] = pps
			fmt.Printf("nal=%d type=PPS id=%d sps=%d entropy=%d initQP=%d refsL0=%d\n", nalIdx, pps.PPSID, pps.SPSID, pps.EntropyCodingMode, pps.PicInitQP, pps.NumRefIdxL0Active)
		case nal.TypeSliceIDR, nal.TypeSliceNonIDR:
			if err := traceSlice(nalIdx, unit, spsMap, ppsMap, limit, cabacTrace); err != nil {
				return err
			}
		}
	}
	return nil
}

func traceSlice(nalIdx int, unit nal.Unit, spsMap map[uint32]*nal.SPS, ppsMap map[uint32]*nal.PPS, limit int, cabacTrace bool) error {
	peek := nal.NewReader(unit.Payload)
	_ = peek.ReadUE()
	_ = peek.ReadUE()
	ppsID := peek.ReadUE()
	pps := ppsMap[ppsID]
	if pps == nil {
		return fmt.Errorf("nal %d slice: PPS %d not available", nalIdx, ppsID)
	}
	sps := spsMap[pps.SPSID]
	if sps == nil {
		return fmt.Errorf("nal %d slice: SPS %d not available", nalIdx, pps.SPSID)
	}
	hdr, r := syntax.ParseHeaderWithRefIDC(unit.Payload, unit.Type, unit.RefIDC, sps, pps)
	if pps.EntropyCodingMode != 0 {
		if cabacTrace {
			return fmt.Errorf("nal %d slice: CABAC MB-level tracing is not implemented yet; use decode conformance/profiling tools for CABAC streams", nalIdx)
		}
		return fmt.Errorf("nal %d slice: CABAC stream requires -cabac, but MB-level CABAC tracing is not implemented yet", nalIdx)
	}
	mbWidth := int(sps.PicWidthInMbs)
	mbHeight := int(sps.PicHeightInMapUnits)
	maxMBs := mbWidth * mbHeight
	if limit > 0 && maxMBs > int(hdr.FirstMbInSlice)+limit {
		maxMBs = int(hdr.FirstMbInSlice) + limit
	}
	fmt.Printf("nal=%d type=%d slice=%d firstMB=%d frame=%d qp=%d payloadBits=%d\n", nalIdx, unit.Type, hdr.SliceType, hdr.FirstMbInSlice, hdr.FrameNum, hdr.QP(pps.PicInitQP), len(unit.Payload)*8)
	currentQP := int(hdr.QP(pps.PicInitQP))
	nzCtx := make([][16]int, mbWidth*mbHeight)
	chromaNZCtx := make([][2][4]int, mbWidth*mbHeight)
	mv4Stride := mbWidth * 4
	mv4Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	ref4Ctx := make([]int8, mv4Stride*mbHeight*4)
	for i := range ref4Ctx {
		ref4Ctx[i] = -2
	}
	skipRun := 0
	decodeAfterSkipRun := false
	for mbIdx := int(hdr.FirstMbInSlice); mbIdx < maxMBs; mbIdx++ {
		mbX := mbIdx % mbWidth
		mbY := mbIdx / mbWidth
		var leftNZ, topNZ *[16]int
		var leftChromaNZ, topChromaNZ *[2][4]int
		if mbX > 0 {
			leftNZ = &nzCtx[mbIdx-1]
			leftChromaNZ = &chromaNZCtx[mbIdx-1]
		}
		if mbY > 0 {
			topNZ = &nzCtx[mbIdx-mbWidth]
			topChromaNZ = &chromaNZCtx[mbIdx-mbWidth]
		}
		start := r.Position()
		if hdr.IsIntra() {
			mb := syntax.DecodeMBIntra(r, syntax.IntraDecodeOpts{
				SliceQP: int32(currentQP), Transform8x8: pps.Transform8x8Mode,
				LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
			})
			currentQP = updateQP(currentQP, int(mb.QPDelta))
			nzCtx[mbIdx] = mb.TotalCoeff
			chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
			writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=I:%d cbp=%02x chromaMode=%d qpd=%d qp=%d tc=%v\n", mbIdx, mbX, mbY, start, r.Position(), mb.MBType, mb.CodedBlockPattern, mb.ChromaPredMode, mb.QPDelta, currentQP, mb.TotalCoeff)
			if mb.MBType > syntax.MBTypeIPCM || mb.ChromaPredMode > 3 {
				return fmt.Errorf("invalid intra syntax at mb=%d: mb_type=%d chroma_mode=%d nextBit=%d", mbIdx, mb.MBType, mb.ChromaPredMode, r.Position())
			}
			continue
		}
		predMV := predictSkipMV4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX*4, mbY*4)
		if hdr.SliceType == syntax.SliceTypeP && pps.EntropyCodingMode == 0 {
			if skipRun == 0 && !decodeAfterSkipRun {
				skipRun = int(r.ReadUE())
			}
			if skipRun > 0 {
				skipMV := predMV
				fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=P_SKIP remainingSkip=%d qp=%d mv0=(%d,%d) ref0=0\n", mbIdx, mbX, mbY, start, r.Position(), skipRun-1, currentQP, skipMV.X, skipMV.Y)
				mbSkip := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
				mbSkip.MV[0] = skipMV
				writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbSkip)
				skipRun--
				decodeAfterSkipRun = skipRun == 0
				continue
			}
			decodeAfterSkipRun = false
		}
		if hdr.SliceType == syntax.SliceTypeB {
			bStart := r.Position()
			mbB := syntax.DecodeMBBidi(r, int32(currentQP), hdr.NumRefIdxL0Active, hdr.NumRefIdxL1Active)
			currentQP = updateQP(currentQP, int(mbB.QPDelta))
			fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=B:%d cbp=%02x qpd=%d qp=%d refL0=%v refL1=%v mvL0=%v mvL1=%v\n", mbIdx, mbX, mbY, bStart, r.Position(), mbB.MBType, mbB.CBP, mbB.QPDelta, currentQP, mbB.RefIdxL0, mbB.RefIdxL1, mbB.MVL0, mbB.MVL1)
			continue
		}
		mb := syntax.DecodeMBInter(r, syntax.InterDecodeOpts{
			SliceQP: int32(currentQP), NumRefFrames: hdr.NumRefIdxL0Active,
			LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
		})
		if mb.MBType >= syntax.PMBTypeIntra {
			intra := syntax.DecodeMBIntraWithType(r, mb.MBType-syntax.PMBTypeIntra, syntax.IntraDecodeOpts{
				SliceQP: int32(currentQP), Transform8x8: pps.Transform8x8Mode,
				LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
			})
			currentQP = updateQP(currentQP, int(intra.QPDelta))
			nzCtx[mbIdx] = intra.TotalCoeff
			chromaNZCtx[mbIdx] = intra.ChromaTotalCoeff
			writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=P:I:%d cbp=%02x chromaMode=%d qpd=%d qp=%d tc=%v\n", mbIdx, mbX, mbY, start, r.Position(), intra.MBType, intra.CodedBlockPattern, intra.ChromaPredMode, intra.QPDelta, currentQP, intra.TotalCoeff)
			if intra.MBType > syntax.MBTypeIPCM || intra.ChromaPredMode > 3 {
				return fmt.Errorf("invalid P-intra syntax at mb=%d: mb_type=%d chroma_mode=%d nextBit=%d", mbIdx, intra.MBType, intra.ChromaPredMode, r.Position())
			}
			continue
		}
		rawMV0 := mb.MV[0]
		pred0 := predictMotion4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX*4, mbY*4, 4, mb.RefIdx[0])
		applyMVPredictors(&mb, mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY)
		currentQP = updateQP(currentQP, int(mb.QPDelta))
		nzCtx[mbIdx] = mb.TotalCoeff
		chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
		writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, &mb)
		fmt.Printf("  mb=%04d x=%02d y=%02d bits=%d..%d type=P:%d cbp=%02x qpd=%d qp=%d mvd0=(%d,%d) pred0=(%d,%d) mv0=(%d,%d) ref0=%d tc=%v\n", mbIdx, mbX, mbY, start, r.Position(), mb.MBType, mb.CBP, mb.QPDelta, currentQP, rawMV0.X, rawMV0.Y, pred0.X, pred0.Y, mb.MV[0].X, mb.MV[0].Y, mb.RefIdx[0], mb.TotalCoeff)
	}
	return nil
}

func updateQP(current, delta int) int {
	qp := (current + delta) % 52
	if qp < 0 {
		qp += 52
	}
	return qp
}

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
	if x4 < 0 || y4 < 0 || x4 >= stride4 || stride4 <= 0 || y4*stride4+x4 >= len(ref4) || y4*stride4+x4 >= len(mv4) {
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
		// matching the decoder's in-MB MV cache update order.
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
				pos := [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
				for j := 0; j < 4; j++ {
					idx := part*4 + j
					x, y := baseX+pos[j][0], baseY+pos[j][1]
					pred := predictMotion4x4(mv4, ref4, stride4, x, y, 1, ref)
					addMV(&mb.SubMV[idx], pred)
					fillMV4(mv4, ref4, stride4, x, y, 1, 1, mb.SubMV[idx], ref)
				}
			}
		}
		mb.MV[0] = mb.SubMV[0]
	}
}

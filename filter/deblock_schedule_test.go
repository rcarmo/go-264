package filter

// Original edge schedule, kept independent of the whole-MB threshold shortcut.
func deblockOriginalSchedule(
	yPlane []uint8, yStride int,
	uPlane, vPlane []uint8, cStride int,
	mbX, mbY int,
	cur *MBDeblockInfo,
	left, top *MBDeblockInfo,
	ctx DeblockMBContext,
) {
	if ctx.DisableIDC == 1 {
		return
	}

	// §8.7: QP at a boundary is the average of the two MBs.
	// indexA = Clip3(0,51, avgQP + alphaOffset), indexB = Clip3(0,51, avgQP + betaOffset).
	// For internal edges: QP = current MB QP (no averaging needed).
	lumaQP := func(qpA, qpB int) int { return (qpA + qpB + 1) >> 1 }
	indexA := func(qp int) int { return Clip3(0, 51, qp+ctx.AlphaOffset) }
	indexB := func(qp int) int { return Clip3(0, 51, qp+ctx.BetaOffset) }

	chromaIndexA := func(qp int) int { return Clip3(0, 51, qp+ctx.AlphaOffset) }
	chromaIndexB := func(qp int) int { return Clip3(0, 51, qp+ctx.BetaOffset) }

	// ---- Vertical edges (dir=0): filter from left to right ----
	// Edge 0: left MB boundary (if left neighbor exists)
	if left != nil {
		qp := lumaQP(cur.QP, left.QP)
		ia, ib := indexA(qp), indexB(qp)
		bs := bsVertMB(cur, left)
		FilterLumaEdgeV(yPlane, yStride, mbX*16, mbY*16, 16, bs, ia, ib)
		// FFmpeg averages already-mapped chroma QPs across MB boundaries.
		cbs := chromaBSFrom(bs)
		qpu := lumaQP(cur.ChromaQPU, left.ChromaQPU)
		qpv := lumaQP(cur.ChromaQPV, left.ChromaQPV)
		FilterChromaEdgeV(uPlane, cStride, mbX*8, mbY*8, 8, cbs, chromaIndexA(qpu), chromaIndexB(qpu))
		FilterChromaEdgeV(vPlane, cStride, mbX*8, mbY*8, 8, cbs, chromaIndexA(qpv), chromaIndexB(qpv))
	}

	// Internal vertical edges (edges 1-3): 4×4 column boundaries within MB.
	for e := 1; e <= 3; e++ {
		col := mbX*16 + e*4
		bs := bsVertInternal(cur, e)
		if bsAllZero(bs) {
			continue
		}
		qp := cur.QP
		ia, ib := indexA(qp), indexB(qp)
		FilterLumaEdgeV(yPlane, yStride, col, mbY*16, 16, bs, ia, ib)
		// Chroma: filter at even luma edges only (e=2 → chroma col mbX*8+4).
		if e == 2 {
			cbs := chromaBSFrom(bs)
			FilterChromaEdgeV(uPlane, cStride, mbX*8+4, mbY*8, 8, cbs, chromaIndexA(cur.ChromaQPU), chromaIndexB(cur.ChromaQPU))
			FilterChromaEdgeV(vPlane, cStride, mbX*8+4, mbY*8, 8, cbs, chromaIndexA(cur.ChromaQPV), chromaIndexB(cur.ChromaQPV))
		}
	}

	// ---- Horizontal edges (dir=1): filter from top to bottom ----
	// Edge 0: top MB boundary.
	if top != nil {
		qp := lumaQP(cur.QP, top.QP)
		ia, ib := indexA(qp), indexB(qp)
		bs := bsHorizMB(cur, top)
		FilterLumaEdgeH(yPlane, yStride, mbY*16, mbX*16, 16, bs, ia, ib)
		cbs := chromaBSFrom(bs)
		qpu := lumaQP(cur.ChromaQPU, top.ChromaQPU)
		qpv := lumaQP(cur.ChromaQPV, top.ChromaQPV)
		FilterChromaEdgeH(uPlane, cStride, mbY*8, mbX*8, 8, cbs, chromaIndexA(qpu), chromaIndexB(qpu))
		FilterChromaEdgeH(vPlane, cStride, mbY*8, mbX*8, 8, cbs, chromaIndexA(qpv), chromaIndexB(qpv))
	}

	// Internal horizontal edges (edges 1-3).
	for e := 1; e <= 3; e++ {
		row := mbY*16 + e*4
		bs := bsHorizInternal(cur, e)
		if bsAllZero(bs) {
			continue
		}
		qp := cur.QP
		ia, ib := indexA(qp), indexB(qp)
		FilterLumaEdgeH(yPlane, yStride, row, mbX*16, 16, bs, ia, ib)
		if e == 2 {
			cbs := chromaBSFrom(bs)
			FilterChromaEdgeH(uPlane, cStride, mbY*8+4, mbX*8, 8, cbs, chromaIndexA(cur.ChromaQPU), chromaIndexB(cur.ChromaQPU))
			FilterChromaEdgeH(vPlane, cStride, mbY*8+4, mbX*8, 8, cbs, chromaIndexA(cur.ChromaQPV), chromaIndexB(cur.ChromaQPV))
		}
	}
}

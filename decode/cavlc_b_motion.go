package decode

import "github.com/rcarmo/go-264/syntax"

// applyCAVLCBMotion converts CAVLC partition MVDs to final MVs in H.264 syntax
// order. CABAC performs these steps while decoding; CAVLC returns all MVDs and
// must replay the same per-list cache updates before reconstruction.
func applyCAVLCBMotion(c bMotionCache, mb *syntax.MBBidi, mbX, mbY int) {
	if mb == nil || mb.MBType == syntax.BMBTypeDirect16x16 || mb.MBType >= syntax.BMBTypeIntra {
		return
	}
	if mb.MBType == syntax.BMBTypeB8x8 {
		applyCAVLCB8x8Motion(c, mb, mbX, mbY)
		return
	}
	parts := bMacroblockPartCount(mb.MBType)
	x4, y4 := mbX*4, mbY*4
	for list := 0; list < 2; list++ {
		for part := 0; part < parts; part++ {
			uses := syntax.BPartUsesL0(mb.MBType, part)
			if list == 1 {
				uses = syntax.BPartUsesL1(mb.MBType, part)
			}
			if !uses {
				continue
			}
			ref, mvd := mb.RefIdxL0[part], mb.MVL0[part]
			if list == 1 {
				ref, mvd = mb.RefIdxL1[part], mb.MVL1[part]
			}
			mvp := predictBPartMotion4x4(c.mv[list], c.ref[list], c.stride4, x4, y4, mb.MBType, part, ref, c.trace)
			final := syntax.MotionVector{X: mvd.X + mvp.X, Y: mvd.Y + mvp.Y}
			if list == 0 {
				mb.MVDL0[part], mb.MVPL0[part], mb.MVL0[part] = mvd, mvp, final
			} else {
				mb.MVDL1[part], mb.MVPL1[part], mb.MVL1[part] = mvd, mvp, final
			}
			bx, by := x4+cabacBPartX(mb.MBType, part, parts), y4+cabacBPartY(mb.MBType, part, parts)
			w4, h4 := cabacBPartDims(mb.MBType, part)
			fillMV4(c.mv[list], c.ref[list], c.stride4, bx, by, w4, h4, final, ref)
		}
	}
}

func applyCAVLCB8x8Motion(c bMotionCache, mb *syntax.MBBidi, mbX, mbY int) {
	x4, y4 := mbX*4, mbY*4
	// FFmpeg's CAVLC scan8 cache temporarily invalidates internal aliases when
	// Direct and explicit B_8x8 parts are mixed. Replay on private arrays so
	// those transient values cannot overwrite neighbouring frame-wide state.
	work := c
	for list := 0; list < 2; list++ {
		work.mv[list] = append([]syntax.MotionVector(nil), c.mv[list]...)
		work.ref[list] = append([]int8(nil), c.ref[list]...)
	}
	// Syntax stores compact MVDs at part*4+subpart. Expansion of an earlier
	// final MV can cover those slots, so preserve all raw MVDs before replay.
	rawMVDL0, rawMVDL1 := mb.SubMVL0, mb.SubMVL1
	// Direct values were derived before this call. Seed direct and unavailable
	// regions before explicit subpartitions derive predictors.
	for part, t := range mb.SubMBType {
		bx, by := x4+(part&1)*2, y4+(part>>1)*2
		if t == 0 {
			fillMV4(work.mv[0], work.ref[0], work.stride4, bx, by, 2, 2, mb.SubMVL0[part*4], mb.RefIdxL0[part])
			fillMV4(work.mv[1], work.ref[1], work.stride4, bx, by, 2, 2, mb.SubMVL1[part*4], mb.RefIdxL1[part])
		} else {
			// Explicit used/unused list regions are populated in per-list part
			// order below so future parts remain unavailable for C-to-D fallback.
		}
	}
	hasDirect := false
	for _, t := range mb.SubMBType {
		hasDirect = hasDirect || t == 0
	}
	if hasDirect {
		for _, part := range []int{1, 3} {
			x, y := x4+(part&1)*2, y4+(part>>1)*2
			for list := 0; list < 2; list++ {
				i := y*work.stride4 + x
				if i >= 0 && i < len(work.ref[list]) {
					work.ref[list][i], work.mv[list][i] = -2, syntax.MotionVector{}
				}
			}
		}
	}
	for list := 0; list < 2; list++ {
		for part, t := range mb.SubMBType {
			if t == 0 {
				x, y := x4+(part&1)*2, y4+(part>>1)*2
				dst, src := y*work.stride4+x, y*work.stride4+x+1
				if dst >= 0 && src >= 0 && dst < len(work.ref[list]) && src < len(work.ref[list]) {
					work.ref[list][dst], work.mv[list][dst] = work.ref[list][src], work.mv[list][src]
				}
				continue
			}
			uses := syntax.BMBSubUsesL0(t)
			if list == 1 {
				uses = syntax.BMBSubUsesL1(t)
			}
			if !uses {
				bx, by := x4+(part&1)*2, y4+(part>>1)*2
				fillMV4(work.mv[list], work.ref[list], work.stride4, bx, by, 2, 2, syntax.MotionVector{}, -1)
				continue
			}
			ref := mb.RefIdxL0[part]
			if list == 1 {
				ref = mb.RefIdxL1[part]
			}
			bx, by := x4+(part&1)*2, y4+(part>>1)*2
			count := syntax.BMBSubPartCount(t)
			w4, h4 := syntax.BMBSubPartFillDims(t)
			for sub := 0; sub < count; sub++ {
				ox, oy := bSubPartOffset4x4(t, sub)
				sx, sy := bx+ox, by+oy
				mvd := rawMVDL0[part*4+sub]
				if list == 1 {
					mvd = rawMVDL1[part*4+sub]
				}
				mvp := predictMotion4x4(work.mv[list], work.ref[list], work.stride4, sx, sy, w4, ref, work.trace)
				final := syntax.MotionVector{X: mvd.X + mvp.X, Y: mvd.Y + mvp.Y}
				// Keep MBBidi in compact syntax order for reconstruction and
				// write-back; only the neighbour cache is spatially expanded.
				if list == 0 {
					mb.SubMVL0[part*4+sub] = final
				} else {
					mb.SubMVL1[part*4+sub] = final
				}
				fillMV4(work.mv[list], work.ref[list], work.stride4, sx, sy, w4, h4, final, ref)
			}
			mb.MVL0[part], mb.MVL1[part] = mb.SubMVL0[part*4], mb.SubMVL1[part*4]
		}
	}
	for list := 0; list < 2; list++ {
		for y := 0; y < 4; y++ {
			start := (y4+y)*c.stride4 + x4
			copy(c.mv[list][start:start+4], work.mv[list][start:start+4])
			copy(c.ref[list][start:start+4], work.ref[list][start:start+4])
		}
	}
}

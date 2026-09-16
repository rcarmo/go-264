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
	// Syntax stores compact MVDs at part*4+subpart. Expansion of an earlier
	// final MV can cover those slots, so preserve all raw MVDs before replay.
	rawMVDL0, rawMVDL1 := mb.SubMVL0, mb.SubMVL1
	// Direct values were derived before this call. Seed direct and unavailable
	// regions before explicit subpartitions derive predictors.
	for part, t := range mb.SubMBType {
		bx, by := x4+(part&1)*2, y4+(part>>1)*2
		if t == 0 {
			fillMV4(c.mv[0], c.ref[0], c.stride4, bx, by, 2, 2, mb.SubMVL0[part*4], mb.RefIdxL0[part])
			fillMV4(c.mv[1], c.ref[1], c.stride4, bx, by, 2, 2, mb.SubMVL1[part*4], mb.RefIdxL1[part])
		} else {
			// Match CABAC/FFmpeg ordering: mark only unused-list regions now.
			// Used-list references become available as each subpartition is
			// processed; pre-seeding future regions changes MVP fallback rules.
			if !syntax.BMBSubUsesL0(t) {
				fillMV4(c.mv[0], c.ref[0], c.stride4, bx, by, 2, 2, syntax.MotionVector{}, -1)
			}
			if !syntax.BMBSubUsesL1(t) {
				fillMV4(c.mv[1], c.ref[1], c.stride4, bx, by, 2, 2, syntax.MotionVector{}, -1)
			}
		}
	}
	for list := 0; list < 2; list++ {
		for part, t := range mb.SubMBType {
			uses := syntax.BMBSubUsesL0(t)
			if list == 1 {
				uses = syntax.BMBSubUsesL1(t)
			}
			if !uses {
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
				mvp := predictMotion4x4(c.mv[list], c.ref[list], c.stride4, sx, sy, w4, ref, c.trace)
				final := syntax.MotionVector{X: mvd.X + mvp.X, Y: mvd.Y + mvp.Y}
				// Keep MBBidi in compact syntax order for reconstruction and
				// write-back; only the neighbour cache is spatially expanded.
				if list == 0 {
					mb.SubMVL0[part*4+sub] = final
				} else {
					mb.SubMVL1[part*4+sub] = final
				}
				fillMV4(c.mv[list], c.ref[list], c.stride4, sx, sy, w4, h4, final, ref)
			}
			mb.MVL0[part], mb.MVL1[part] = mb.SubMVL0[part*4], mb.SubMVL1[part*4]
		}
	}
}

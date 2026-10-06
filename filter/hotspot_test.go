package filter

import (
	"math/rand"
	"testing"
)

func TestDeblockThresholdShortcutMatchesOriginal(t *testing.T) {
	rng := rand.New(rand.NewSource(264127))
	for trial := 0; trial < 6000; trial++ {
		info := func() MBDeblockInfo {
			v := MBDeblockInfo{QP: rng.Intn(52), ChromaQPU: rng.Intn(52), ChromaQPV: rng.Intn(52), IsIntra: rng.Intn(3) == 0, IsB: rng.Intn(2) == 0, Use8x8: rng.Intn(2) == 0}
			for i := range v.NZC {
				v.NZC[i] = rng.Intn(2)
				v.RefIDL0[i] = rng.Intn(3) - 1
				v.RefIDL1[i] = rng.Intn(3) - 1
				v.MVL0[i] = [2]int16{int16(rng.Intn(17) - 8), int16(rng.Intn(17) - 8)}
				v.MVL1[i] = [2]int16{int16(rng.Intn(17) - 8), int16(rng.Intn(17) - 8)}
			}
			return v
		}
		cur, left, top := info(), info(), info()
		if trial%3 == 0 {
			cur.QP, cur.ChromaQPU, cur.ChromaQPV = trial%16, trial%16, trial%16
			left, top = cur, cur
		}
		ctx := DeblockMBContext{AlphaOffset: rng.Intn(25) - 12, BetaOffset: rng.Intn(25) - 12}
		if trial%7 == 0 {
			ctx.DisableIDC = 1
		}
		var lp, tp *MBDeblockInfo
		if trial%4 != 0 {
			lp = &left
		}
		if trial%5 != 0 {
			tp = &top
		}
		y, u, v := make([]byte, 64*64), make([]byte, 32*32), make([]byte, 32*32)
		for _, p := range [][]byte{y, u, v} {
			for i := range p {
				p[i] = byte(110 + rng.Intn(25))
			}
		}
		ry, ru, rv := append([]byte(nil), y...), append([]byte(nil), u...), append([]byte(nil), v...)
		DeblockMBFrameInfo(y, 64, u, v, 32, 1, 1, &cur, lp, tp, ctx)
		deblockOriginalSchedule(ry, 64, ru, rv, 32, 1, 1, &cur, lp, tp, ctx)
		// Exact decoded H.264 samples are the contract, independent of shortcuts.
		for plane, p := range [][]byte{y, u, v} {
			want := [][]byte{ry, ru, rv}[plane]
			for i, got := range p {
				if got != want[i] {
					t.Fatalf("trial=%d plane=%d pixel=%d got=%d want=%d", trial, plane, i, got, want[i])
				}
			}
		}
	}
}

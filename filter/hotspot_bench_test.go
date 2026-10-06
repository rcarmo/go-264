package filter

import "testing"

func BenchmarkDeblockFrameInfoHotspots(b *testing.B) {
	for _, qp := range []int{12, 30} {
		name := "lowQP"
		if qp == 30 {
			name = "activeQP"
		}
		b.Run(name, func(b *testing.B) {
			y := make([]byte, 64*64)
			u := make([]byte, 32*32)
			v := make([]byte, 32*32)
			for i := range y {
				y[i] = byte(120 + i%13)
			}
			for i := range u {
				u[i] = byte(120 + i%7)
				v[i] = byte(120 + i%5)
			}
			cur := MBDeblockInfo{QP: qp, ChromaQPU: qp, ChromaQPV: qp}
			for i := range cur.NZC {
				cur.NZC[i] = 1
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				DeblockMBFrameInfo(y, 64, u, v, 32, 1, 1, &cur, &cur, &cur, DeblockMBContext{})
			}
		})
	}
}

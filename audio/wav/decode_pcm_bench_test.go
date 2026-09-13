package wav

import (
	"fmt"
	"testing"
)

func BenchmarkDecodePCM(b *testing.B) {
	for _, bits := range []int{8, 16, 24, 32} {
		b.Run(fmt.Sprintf("bits%d", bits), func(b *testing.B) {
			samples := 4096
			src := make([]byte, samples*(bits/8))
			for i := range src {
				src[i] = byte(i*37 + 11)
			}
			dst := make([]float64, samples)
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decodePCM(dst, src, bits)
			}
		})
	}
}

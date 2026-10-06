package resample

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkReadFramesHotspots(b *testing.B) {
	for _, rates := range [][2]int{{48000, 16000}, {44100, 16000}} {
		for _, channels := range []int{1, 2} {
			b.Run(fmt.Sprintf("%d-%d/ch%d", rates[0], rates[1], channels), func(b *testing.B) {
				data := make([]float64, rates[0]*channels)
				for i := range data {
					data[i] = float64(i%31-15) / 16
				}
				r, err := New(&interleavedMem{data: data, rate: rates[0], channels: channels}, rates[1])
				if err != nil {
					b.Fatal(err)
				}
				dst := make([]float64, rates[1]*channels)
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if err := r.SeekFrame(ctx, 0); err != nil {
						b.Fatal(err)
					}
					if n, err := r.ReadFrames(ctx, dst); err != nil || n != rates[1] {
						b.Fatal(n, err)
					}
				}
			})
		}
	}
}

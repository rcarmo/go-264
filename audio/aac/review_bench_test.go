package aac

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkDecodeLayouts(b *testing.B) {
	for _, config := range []int{1, 2, 6, 7} {
		b.Run(fmt.Sprint(config), func(b *testing.B) {
			d, err := NewDecoder(ascFor(3, config))
			if err != nil {
				b.Fatal(err)
			}
			packet := impulsePacket(config, 0)
			dst := make([]float64, 1024*d.OutputChannels())
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := d.Decode(ctx, packet, dst); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

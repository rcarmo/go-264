package decode

import (
	"fmt"
	"testing"
)

func BenchmarkFrameNumGapsHotspots(b *testing.B) {
	for _, distance := range []int{1, 32, 65535} {
		b.Run(fmt.Sprint(distance), func(b *testing.B) {
			refs := shortRefs(0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				staged, next, err := stageFrameNumGaps(refs, 0, distance, 65536, 3, true)
				if err != nil || next != distance-1 || len(staged) == 0 {
					b.Fatal(next, err)
				}
			}
		})
	}
}

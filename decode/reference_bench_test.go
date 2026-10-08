package decode

import (
	"fmt"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/syntax"
)

func BenchmarkPReferenceList(b *testing.B) {
	for _, n := range []int{1, 4, 16} {
		for _, modified := range []bool{false, true} {
			b.Run(fmt.Sprintf("refs%d/modified%t", n, modified), func(b *testing.B) {
				refs := make([]*frame.Frame, n)
				for i := range refs {
					refs[i] = &frame.Frame{FrameNum: i, IsRef: true}
				}
				var mods []syntax.RefPicListModification
				if modified {
					mods = []syntax.RefPicListModification{{Op: 0, Val: uint32(n - 1)}}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					got, err := buildPReferenceList(refs, n, 32, n, mods)
					if err != nil || len(got) != n {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

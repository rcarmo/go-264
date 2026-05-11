package decode

import (
	"os"
	"testing"
)

func BenchmarkDecodeBBBbaseline(b *testing.B) {
	benchmarkDecodeFixture(b, "/workspace/tmp/bbb_baseline.h264")
}

func BenchmarkDecodeTestsrcBaseline(b *testing.B) {
	benchmarkDecodeFixture(b, "/workspace/tmp/testsrc_bl.h264")
}

func BenchmarkDecodeCABACP(b *testing.B) {
	benchmarkDecodeFixture(b, "/workspace/tmp/testsrc_cabac_p.h264")
}

func benchmarkDecodeFixture(b *testing.B, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("fixture unavailable: %v", err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec := NewDecoder()
		frames, err := dec.Decode(data)
		if err != nil {
			b.Fatalf("decode: %v", err)
		}
		if len(frames) == 0 {
			b.Fatal("no frames decoded")
		}
	}
}

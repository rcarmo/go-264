package decode

import "testing"

// Uses a checked-in synthetic fixture; optional larger external corpora remain
// separate benchmarks rather than silently falling back to this small stream.
func BenchmarkDecodeRetainedLowQP(b *testing.B) {
	benchmarkDecodeFixture(b, "testdata/cabac-b-lowqp.h264")
}

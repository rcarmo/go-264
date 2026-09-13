package ac3

import (
	"math"
	"reflect"
	"testing"
)

func TestOverlapAddExact(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 1, -1, math.MaxFloat64 / 4}
	for offset := 0; offset < 3; offset++ {
		for n := 0; n <= 513; n++ {
			current := make([]float64, offset+n+3)
			previous := make([]float64, offset+n+3)
			got := make([]float64, offset+n+3)
			want := make([]float64, offset+n+3)
			for i := 0; i < n; i++ {
				current[offset+i] = values[i%len(values)]
				previous[offset+i] = values[(i*3+1)%len(values)]
			}
			overlapAddScalar(want[offset:offset+n], current[offset:offset+n], previous[offset:offset+n])
			overlapAdd(got[offset:offset+n], current[offset:offset+n], previous[offset:offset+n])
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("offset=%d n=%d", offset, n)
			}
		}
	}
}

func TestOverlapAddAliasedDestination(t *testing.T) {
	for _, alias := range []string{"current", "previous"} {
		current, previous := make([]float64, 257), make([]float64, 257)
		for i := range current {
			current[i], previous[i] = float64(i-128)/257, float64(73-i)/311
		}
		want := make([]float64, len(current))
		overlapAddScalar(want, current, previous)
		switch alias {
		case "current":
			overlapAdd(current, current, previous)
			if !reflect.DeepEqual(current, want) {
				t.Fatal(alias)
			}
		case "previous":
			overlapAdd(previous, current, previous)
			if !reflect.DeepEqual(previous, want) {
				t.Fatal(alias)
			}
		}
	}
}

func BenchmarkOverlapAdd(b *testing.B) {
	current, previous, output := make([]float64, 256), make([]float64, 256), make([]float64, 256)
	for i := range current {
		current[i], previous[i] = float64(i)/256, float64(256-i)/512
	}
	b.ReportAllocs()
	b.Run("dispatch", func(b *testing.B) {
		for b.Loop() {
			overlapAdd(output, current, previous)
		}
	})
	b.Run("scalar", func(b *testing.B) {
		for b.Loop() {
			overlapAddScalar(output, current, previous)
		}
	})
}

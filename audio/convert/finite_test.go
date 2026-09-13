package convert

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

func TestS16NonFinitePayloadsFailBeforeWrite(t *testing.T) {
	badBits := []uint64{
		0x7ff0000000000000, 0xfff0000000000000,
		0x7ff0000000000001, 0x7ff8000000000000,
		0xfff0000000000001, 0xfff8000000000000,
	}
	for _, n := range []int{1, 2, 3, 4, 7, 128, 2048} {
		for _, pos := range []int{0, n / 2, n - 1} {
			for _, bits := range badBits {
				t.Run(fmt.Sprintf("n%d/pos%d/%016x", n, pos, bits), func(t *testing.T) {
					src := make([]float64, n)
					for i := range src {
						src[i] = float64(i%17-8) / 16
					}
					src[pos] = math.Float64frombits(bits)
					dst := make([]int16, n+1)
					for i := range dst {
						dst[i] = int16(1000 + i)
					}
					want := append([]int16(nil), dst...)
					if err := S16(dst[:n], src); err == nil {
						t.Fatal("non-finite input accepted")
					}
					if !slices.Equal(dst, want) {
						t.Fatal("destination changed before validation completed")
					}
				})
			}
		}
	}
}

func TestAllFiniteParity(t *testing.T) {
	special := []uint64{
		0, 1 << 63, 1, 1<<63 | 1,
		0x000fffffffffffff, 0x800fffffffffffff,
		0x0010000000000000, 0x8010000000000000,
		0x7fefffffffffffff, 0xffefffffffffffff,
		0x7ff0000000000000, 0xfff0000000000000,
		0x7ff0000000000001, 0x7ff8000000000000,
		0xfff0000000000001, 0xfff8000000000000,
	}
	rng := rand.New(rand.NewSource(26416))
	for _, n := range []int{0, 1, 2, 3, 7, 128, 2048} {
		for offset := 0; offset < 2; offset++ {
			t.Run(fmt.Sprintf("n%d/off%d", n, offset), func(t *testing.T) {
				src := make([]float64, n+offset)
				for i := range src {
					src[i] = math.Float64frombits(rng.Uint64())
				}
				for i, bits := range special {
					if i < n {
						src[offset+i] = math.Float64frombits(bits)
					}
				}
				got := allFinite(src[offset:])
				want := allFiniteScalar(src[offset:])
				if got != want {
					t.Fatalf("got %t want %t", got, want)
				}
			})
		}
	}
	for _, bits := range special {
		v := []float64{math.Float64frombits(bits)}
		if got, want := allFinite(v), allFiniteScalar(v); got != want {
			t.Fatalf("bits=%016x got %t want %t", bits, got, want)
		}
	}
}

func BenchmarkS16Public(b *testing.B) {
	for _, n := range []int{128, 2048} {
		b.Run(fmt.Sprintf("n%d", n), func(b *testing.B) {
			src, dst := make([]float64, n), make([]int16, n)
			for i := range src {
				src[i] = float64(i%201-100) / 128
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := S16(dst, src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFiniteValidation(b *testing.B) {
	for _, n := range []int{128, 2048} {
		for _, mode := range []string{"scalar", "dispatch"} {
			b.Run(fmt.Sprintf("n%d/%s", n, mode), func(b *testing.B) {
				src := make([]float64, n)
				for i := range src {
					src[i] = float64(i%201-100) / 128
				}
				fn := allFiniteScalar
				if mode == "dispatch" {
					fn = allFinite
				}
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if !fn(src) {
						b.Fatal("finite input rejected")
					}
				}
			})
		}
	}
}

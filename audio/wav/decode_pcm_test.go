package wav

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func checkDecodeBits(t *testing.T, got, want []float64) {
	t.Helper()
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("sample %d got=%016x want=%016x", i, math.Float64bits(got[i]), math.Float64bits(want[i]))
		}
	}
}

func TestDecodePCMAll8And16BitValues(t *testing.T) {
	src8 := make([]byte, 256)
	for i := range src8 {
		src8[i] = byte(i)
	}
	got8, want8 := make([]float64, len(src8)), make([]float64, len(src8))
	decodePCM8(got8, src8)
	decodePCM8Scalar(want8, src8)
	checkDecodeBits(t, got8, want8)

	src16 := make([]byte, 65536*2)
	for i := 0; i < 65536; i++ {
		binary.LittleEndian.PutUint16(src16[i*2:], uint16(i))
	}
	got16, want16 := make([]float64, 65536), make([]float64, 65536)
	decodePCM16(got16, src16)
	decodePCM16Scalar(want16, src16)
	checkDecodeBits(t, got16, want16)
}

func TestDecodePCMKernelParity(t *testing.T) {
	rng := rand.New(rand.NewSource(26417))
	for _, bits := range []int{8, 16, 32} {
		for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 128, 4096} {
			for offset := 0; offset < 2; offset++ {
				t.Run(fmt.Sprintf("bits%d/n%d/off%d", bits, n, offset), func(t *testing.T) {
					bytesPerSample := bits / 8
					src := make([]byte, offset+n*bytesPerSample+8)
					_, _ = rng.Read(src)
					if n > 0 {
						switch bits {
						case 8:
							copy(src[offset:], []byte{0, 127, 128, 255})
						case 16:
							values := []int16{math.MinInt16, -1, 0, math.MaxInt16}
							for i, v := range values {
								if i < n {
									binary.LittleEndian.PutUint16(src[offset+i*2:], uint16(v))
								}
							}
						case 32:
							values := []int32{math.MinInt32, -1, 0, math.MaxInt32}
							for i, v := range values {
								if i < n {
									binary.LittleEndian.PutUint32(src[offset+i*4:], uint32(v))
								}
							}
						}
					}
					got, want := make([]float64, n+2), make([]float64, n+2)
					for i := range got {
						got[i], want[i] = 123, 123
					}
					payload := src[offset : offset+n*bytesPerSample]
					switch bits {
					case 8:
						decodePCM8(got[1:1+n], payload)
						decodePCM8Scalar(want[1:1+n], payload)
					case 16:
						decodePCM16(got[1:1+n], payload)
						decodePCM16Scalar(want[1:1+n], payload)
					case 32:
						decodePCM32(got[1:1+n], payload)
						decodePCM32Scalar(want[1:1+n], payload)
					}
					checkDecodeBits(t, got, want)
				})
			}
		}
	}
}

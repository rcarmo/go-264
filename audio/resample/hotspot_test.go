package resample

import (
	"context"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// Independent direct-index FIR oracle: no streaming ring or coordinate
// recurrence. Preserve the established coefficient/ordered-sum definition.
func directFIR(r *Reader, input []float64, pos int64, channel int) float64 {
	center, err := pcm.ScaleFrames(pos, r.inRate, r.outRate)
	if err != nil {
		panic(err)
	}
	phase := int((pos%int64(r.outRate))*int64(r.inRate)%int64(r.outRate)) / r.phaseStep
	sum := 0.0
	for j := -r.radius; j <= r.radius; j++ {
		idx := center + int64(j)
		if idx >= 0 && idx < r.sourceFrames {
			sum += input[int(idx)*r.channels+channel] * r.coeff[phase*r.taps+j+r.radius]
		}
	}
	return sum
}

func TestReadFramesRationalStereoOracle(t *testing.T) {
	for _, rates := range [][2]int{{48000, 16000}, {44100, 16000}, {192000, 8000}, {8000, 48000}} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%d-%d/ch%d", rates[0], rates[1], channels), func(t *testing.T) {
				input := make([]float64, 5003*channels)
				for i := range input {
					input[i] = 0.47*math.Sin(float64(i)*0.17) + float64(i%19-9)/20
				}
				r, err := New(&interleavedMem{data: input, rate: rates[0], channels: channels}, rates[1])
				if err != nil {
					t.Fatal(err)
				}
				for _, offset := range []int64{0, 1, 127, r.Info().Frames / 2, r.Info().Frames - 1} {
					for _, chunk := range []int{1, 257, 1601} {
						if err := r.SeekFrame(context.Background(), offset); err != nil {
							t.Fatal(err)
						}
						buf := make([]float64, chunk*channels)
						pos := offset
						for {
							n, err := r.ReadFrames(context.Background(), buf)
							for i := 0; i < n; i++ {
								for c := 0; c < channels; c++ {
									got, want := buf[i*channels+c], directFIR(r, input, pos+int64(i), c)
									// The same coefficient/products are accumulated in source order;
									// 1e-14 covers rounding only, far below audio amplitude tolerances.
									if math.IsNaN(got) || math.Abs(got-want) > 1e-14 {
										t.Fatalf("frame=%d ch=%d got=%.17g want=%.17g error=%g", pos+int64(i), c, got, want, math.Abs(got-want))
									}
								}
							}
							pos += int64(n)
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Fatal(err)
							}
						}
						if pos != r.Info().Frames {
							t.Fatal("output length", pos)
						}
					}
				}
			})
		}
	}
}

package filterbank

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// The logged digest is compared between separate default and purego test
// binaries during qualification, including state across shape/sequence changes.
func TestSynthesisStateDigest(t *testing.T) {
	bank := New()
	coeff, dst := make([]float64, coeffCount), make([]float64, outputSamples)
	h := sha256.New()
	var raw [8]byte
	for frame := 0; frame < 64; frame++ {
		for i := range coeff {
			// Exact binary fractions avoid differing transcendental input setup.
			coeff[i] = float64((i*17+frame*29)%251-125) / 128
		}
		if err := bank.Synthesize(coeff, frame%4, (frame/4)%2, dst); err != nil {
			t.Fatal(err)
		}
		for _, data := range [][]float64{dst, bank.overlap[:]} {
			for _, v := range data {
				binary.LittleEndian.PutUint64(raw[:], math.Float64bits(v))
				_, _ = h.Write(raw[:])
			}
		}
		binary.LittleEndian.PutUint64(raw[:], uint64(bank.prevShape))
		_, _ = h.Write(raw[:])
	}
	t.Logf("SYNTHESIS_STATE_SHA256=%x", h.Sum(nil))
}

func TestFiniteInputOverflowRollsBack(t *testing.T) {
	for sequence := 0; sequence < 4; sequence++ {
		t.Run(fmt.Sprint(sequence), func(t *testing.T) {
			b := New()
			coeff, dst := make([]float64, coeffCount), make([]float64, outputSamples+1)
			coeff[7] = 1
			if err := b.Synthesize(coeff, 0, 0, dst); err != nil {
				t.Fatal(err)
			}
			before, output := *b, append([]float64(nil), dst...)
			for i := range coeff {
				coeff[i] = math.MaxFloat64
			}
			if err := b.Synthesize(coeff, sequence, 1, dst); !errors.Is(err, pcm.ErrMalformed) {
				t.Fatalf("want malformed on overflow, got %v", err)
			}
			if *b != before {
				t.Fatal("state changed after overflow")
			}
			checkFloatBits(t, dst, output)
		})
	}
}

package ac3

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

func TestCanonicalLayouts(t *testing.T) {
	if got, want := canonicalLayout(7, true), []Channel{FrontLeft, FrontRight, FrontCenter, LFE, SideLeft, SideRight}; !reflect.DeepEqual(got, want) {
		t.Fatalf("5.1 layout=%v want %v", got, want)
	}
	if got, want := canonicalLayout(5, false), []Channel{FrontLeft, FrontRight, FrontCenter, BackCenter}; !reflect.DeepEqual(got, want) {
		t.Fatalf("3/1 layout=%v want %v", got, want)
	}
}

func TestConfigRejectsInvalidSelections(t *testing.T) {
	for _, config := range []Config{
		{},
		{OutputChannels: 3},
		{OutputChannels: 1, SourceChannels: []int{0, 1}},
		{OutputChannels: 2, SourceChannels: []int{1, 1}},
		{OutputChannels: 1, SourceChannels: []int{6}},
	} {
		if _, err := NewDecoder(config); !errors.Is(err, pcm.ErrMalformed) && !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatalf("config=%+v error=%v", config, err)
		}
	}
}

func TestRenderFivePointOneExtractsCanonicalChannels(t *testing.T) {
	decoder, err := NewDecoder(Config{OutputChannels: 2, SourceChannels: []int{2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	header := header{acmod: 7, lfeon: 1, nfchans: 5}
	layout, _ := canonicalLayoutFixed(header.acmod, true)
	var planar [MaxChannels][FrameSamples]float64
	// Coded order is FL, FC, FR, SL, SR, LFE; selected canonical indices 2/3
	// are therefore centre and LFE.
	for channel := range planar {
		for frame := range planar[channel] {
			planar[channel][frame] = float64(channel + 1)
		}
	}
	output := make([]float64, FrameSamples*2)
	decoder.render(header, layout, &planar, output)
	for frame := 0; frame < FrameSamples; frame++ {
		if output[2*frame] != 2 || output[2*frame+1] != 6 {
			t.Fatalf("frame %d=%v", frame, output[2*frame:2*frame+2])
		}
	}
}

func TestRenderFivePointOneDownmixExcludesLFE(t *testing.T) {
	header := header{acmod: 7, lfeon: 1, nfchans: 5, cmixlev: 0, surmixlev: 0}
	layout, _ := canonicalLayoutFixed(header.acmod, true)
	var planar [MaxChannels][FrameSamples]float64
	// Coded order: FL=1, FC=2, FR=3, SL=4, SR=5, LFE=1000.
	values := [...]float64{1, 2, 3, 4, 5, 1000}
	for channel, value := range values {
		for frame := range planar[channel] {
			planar[channel][frame] = value
		}
	}
	rootHalf := 1 / math.Sqrt2
	scale := 1 / (1 + 2*rootHalf)
	wantLeft := (1 + 2*rootHalf + 4*rootHalf) * scale
	wantRight := (3 + 2*rootHalf + 5*rootHalf) * scale

	stereo, _ := NewDecoder(Config{OutputChannels: 2})
	stereoOutput := make([]float64, FrameSamples*2)
	stereo.render(header, layout, &planar, stereoOutput)
	for frame := 0; frame < FrameSamples; frame++ {
		if math.Abs(stereoOutput[2*frame]-wantLeft) > 1e-15 || math.Abs(stereoOutput[2*frame+1]-wantRight) > 1e-15 {
			t.Fatalf("stereo frame %d=%v want [%g %g]", frame, stereoOutput[2*frame:2*frame+2], wantLeft, wantRight)
		}
	}

	mono, _ := NewDecoder(Config{OutputChannels: 1})
	monoOutput := make([]float64, FrameSamples)
	mono.render(header, layout, &planar, monoOutput)
	wantMono := (wantLeft + wantRight) * 0.5
	for frame, value := range monoOutput {
		if math.Abs(value-wantMono) > 1e-15 {
			t.Fatalf("mono frame %d=%g want %g", frame, value, wantMono)
		}
	}
}

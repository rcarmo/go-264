package lc

import (
	"github.com/rcarmo/go-264/audio/aac/internal/aacbits"
	"math"
	"testing"
)

func TestNoiseFirstDeltaUnsigned(t *testing.T) {
	var w bitWriter
	w.bits(300, 9)
	ch := Channel{NumGroups: 1, MaxSFB: 1}
	ch.Codebook[0][0] = 13
	if e := parseScaleFactorData(aacbits.New(w.finish()), &ch, 100); e != nil {
		t.Fatal(e)
	}
	if ch.Scale[0][0] != 54 {
		t.Fatalf("got%d want54", ch.Scale[0][0])
	}
}
func TestTNSOnePoleBothDirections(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		ch := Channel{Sequence: SequenceOnlyLong, MaxSFB: 1, Offsets: [65]int{0, 4, 1024}, NumOffsets: 3}
		ch.TNS[0] = TNSWindow{Count: 1}
		ch.TNS[0].Filters[0] = TNSFilter{Order: 1, Length: 2, Direction: reverse, Coef: [12]uint8{1}}
		var s [1024]float64
		if reverse {
			s[3] = 1
		} else {
			s[0] = 1
		}
		applyTNS(&s, &ch, 3)
		k := math.Sin(math.Pi / 7)
		for n := 0; n < 4; n++ {
			i := n
			if reverse {
				i = 3 - n
			}
			want := math.Pow(-k, float64(n))
			if math.Abs(s[i]-want) > 1e-14 {
				t.Fatal(reverse, n, s[i], want)
			}
		}
		if s[4] != 0 {
			t.Fatal("tail")
		}
	}
}
func TestReconstructionGainMSIntensity(t *testing.T) {
	f := Frame{Count: 2, CommonWindow: true, MMode: 1}
	for c := 0; c < 2; c++ {
		f.Channels[c] = Channel{Sequence: SequenceOnlyLong, NumGroups: 1, GroupLength: [8]int{1}, MaxSFB: 1, Offsets: [65]int{0, 4, 1024}, NumOffsets: 3}
		f.Channels[c].Codebook[0][0] = 1
		f.Channels[c].Scale[0][0] = 100
	}
	f.Channels[0].Quant[0] = 8
	f.Channels[1].Quant[0] = 1
	f.MS[0][0] = true
	seed := uint32(1)
	s, e := Reconstruct(&f, 48000, &seed)
	if e != nil || s[0][0] != 17 || s[1][0] != 15 {
		t.Fatal(s[0][0], s[1][0], e)
	}
	f.Channels[1].Codebook[0][0] = 15
	f.Channels[1].Scale[0][0] = 4
	s, e = Reconstruct(&f, 48000, &seed)
	if e != nil || s[1][0] != -8 {
		t.Fatal(s[1][0], e)
	}
}

func TestIntensityAllBandMaskInverts(t *testing.T) {
	f := Frame{Count: 2, CommonWindow: true, MMode: 2}
	for c := 0; c < 2; c++ {
		f.Channels[c] = Channel{NumGroups: 1, GroupLength: [8]int{1}, MaxSFB: 1, Offsets: [65]int{0, 4, 1024}, NumOffsets: 3}
		f.Channels[c].Scale[0][0] = 100
	}
	f.Channels[0].Quant[0] = 1
	f.Channels[0].Codebook[0][0] = 1
	f.Channels[1].Codebook[0][0] = 15
	f.Channels[1].Scale[0][0] = 0
	f.MS[0][0] = true
	seed := uint32(1)
	s, e := Reconstruct(&f, 48000, &seed)
	if e != nil || s[1][0] != -1 {
		t.Fatal(s[1][0], e)
	}
}

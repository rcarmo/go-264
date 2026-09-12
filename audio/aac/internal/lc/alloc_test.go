package lc

import (
	"slices"
	"testing"
)

func TestParsedOffsetsOwnedAndZeroAllocCommonFrames(t *testing.T) {
	var short bitWriter
	short.bits(0, 3)
	short.bits(0, 4)
	short.bits(100, 8)
	groups := []int{2, 1, 2, 3}
	writeICSInfoShort(&short, 1, 2, groups)
	writeZeroSectionsShort(&short, len(groups), 2)
	short.bit(false)
	short.bit(false)
	short.bit(false)
	short.bits(7, 3)
	for name, payload := range map[string][]byte{
		"mono-long":   monoZeroLongPayload(),
		"stereo-long": stereoCommonZeroLongPayload(),
		"mono-short":  short.finish(),
	} {
		t.Run(name, func(t *testing.T) {
			channels := 1
			if name == "stereo-long" {
				channels = 2
			}
			first, err := Parse(payload, 48000, channels)
			if err != nil {
				t.Fatal(err)
			}
			before := first.Channels[0].Offsets
			cloned := first // The value copy must own its offset array as well.
			cloned.Channels[0].Offsets[1] = -100
			if first.Channels[0].Offsets != before {
				t.Fatal("channel copy aliases offset storage")
			}
			if channels == 2 {
				first.Channels[0].Offsets[1] = -200
				if first.Channels[1].Offsets[1] != before[1] {
					t.Fatal("common window channels alias offsets")
				}
			}
			var parseErr error
			allocs := testing.AllocsPerRun(100, func() {
				var f Frame
				f, parseErr = Parse(payload, 48000, channels)
				if f.Count != channels {
					panic("frame count changed")
				}
			})
			if parseErr != nil || allocs != 0 {
				t.Fatalf("common frame parse allocated: %g, err=%v", allocs, parseErr)
			}
			fresh, err := Parse(payload, 48000, channels)
			if err != nil || fresh.Channels[0].Offsets != before {
				t.Fatal("previous parse mutated shared tables", err)
			}
		})
	}
}

func TestScaledGroupOffsetsCallerStorage(t *testing.T) {
	input := []int{0, 4, 8, 12}
	out := []int{99, 99, 99, 99, 123}
	scaledGroupOffsets(out[:4], input, 3, 8)
	if !slices.Equal(out, []int{0, 32, 64, 96, 123}) {
		t.Fatal(out)
	}
}

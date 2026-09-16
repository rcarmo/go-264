package mp4

import (
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"testing"
)

func TestTimingPlans(t *testing.T) {
	base := Track{SampleRate: 48000, MediaTimescale: 48000, MovieTimescale: 1000, MediaDuration: 49024}
	p, e := base.TimingPlan(49152)
	if e != nil || p.PaddingFrames != 128 || p.PrimingFrames != 0 || p.OutputFrames != 49024 {
		t.Fatal(p, e)
	}
	base.EditList = []Edit{{SegmentDuration: 1000, MediaTime: 1024, MediaRateInteger: 1}}
	p, e = base.TimingPlan(49152)
	if e != nil || p.PrimingFrames != 1024 || p.MediaFrames != 48000 || p.PaddingFrames != 128 || p.OutputFrames != 48000 {
		t.Fatal(p, e)
	}
	base.EditList = append([]Edit{{SegmentDuration: 200, MediaTime: -1, MediaRateInteger: 1}}, base.EditList...)
	p, e = base.TimingPlan(49152)
	if e != nil || p.LeadingSilenceFrames != 9600 || p.OutputFrames != 57600 {
		t.Fatal(p, e)
	}
}
func TestTimingRejects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		track  Track
		frames int64
		want   error
	}{{"fractional", Track{SampleRate: 44100, MediaTimescale: 44100, MovieTimescale: 1000, MediaDuration: 5000, EditList: []Edit{{SegmentDuration: 1, MediaTime: 0, MediaRateInteger: 1}}}, 5120, pcm.ErrUnsupported}, {"short", Track{SampleRate: 48000, MediaTimescale: 48000, MovieTimescale: 1000, MediaDuration: 48000}, 47999, pcm.ErrMalformed}, {"rate", Track{SampleRate: 48000, MediaTimescale: 48000, MovieTimescale: 1000, MediaDuration: 48000, EditList: []Edit{{SegmentDuration: 1000, MediaTime: 0, MediaRateInteger: 2}}}, 48000, pcm.ErrUnsupported}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := tc.track.TimingPlan(tc.frames); !errors.Is(e, tc.want) {
				t.Fatal(e)
			}
		})
	}
}

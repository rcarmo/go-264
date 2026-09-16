package mp4

import (
	"fmt"
	"github.com/rcarmo/go-264/audio/pcm"
	"math"
)

// Timing is an exact source-rate edit/trim plan for one progressive audio track.
// It does not decode PCM. LeadingSilenceFrames represents an initial empty edit
// and must be emitted as silence to retain the movie timeline. Source frames
// before PrimingFrames and after MediaFrames are discarded. Movie time zero is
// output frame zero; output media frame f maps to source f+PrimingFrames after
// subtracting LeadingSilenceFrames. No iTunSMPB or implicit encoder delay guess.
type Timing struct {
	DecodedFrames        int64
	LeadingSilenceFrames int64
	PrimingFrames        int64
	PaddingFrames        int64
	MediaFrames          int64
	OutputFrames         int64
	HasEdit              bool
}

// TimingPlan supports no edits, one unit-rate media edit, or one leading empty
// edit followed by one media edit. All conversions must be sample-integral.
// Fractional-sample edit durations, repeats, rate changes and unknown delay
// metadata are not approximated. Caller supplies actual decoded frame count.
func (t Track) TimingPlan(decodedFrames int64) (Timing, error) {
	var p Timing
	if decodedFrames < 0 || t.SampleRate <= 0 || t.MediaTimescale == 0 || t.MovieTimescale == 0 {
		return p, fmt.Errorf("%w: timing dimensions", pcm.ErrMalformed)
	}
	p.DecodedFrames = decodedFrames
	media, e := exactFrames(t.MediaDuration, t.MediaTimescale, t.SampleRate)
	if e != nil {
		return p, e
	}
	p.MediaFrames = media
	if len(t.EditList) > 0 {
		p.HasEdit = true
		edits := t.EditList
		if len(edits) > 2 {
			return p, fmt.Errorf("%w: multiple media edits", pcm.ErrUnsupported)
		}
		for _, ed := range edits {
			if ed.MediaRateInteger != 1 || ed.MediaRateFraction != 0 {
				return p, fmt.Errorf("%w: non-unit edit rate", pcm.ErrUnsupported)
			}
		}
		if edits[0].MediaTime == -1 {
			if len(edits) != 2 {
				return p, fmt.Errorf("%w: empty edit without media", pcm.ErrUnsupported)
			}
			p.LeadingSilenceFrames, e = exactFrames(edits[0].SegmentDuration, t.MovieTimescale, t.SampleRate)
			if e != nil {
				return p, e
			}
			edits = edits[1:]
		}
		if len(edits) != 1 || edits[0].MediaTime < 0 {
			return p, fmt.Errorf("%w: edit sequence", pcm.ErrUnsupported)
		}
		p.PrimingFrames, e = exactFrames(uint64(edits[0].MediaTime), t.MediaTimescale, t.SampleRate)
		if e != nil {
			return p, e
		}
		p.MediaFrames, e = exactFrames(edits[0].SegmentDuration, t.MovieTimescale, t.SampleRate)
		if e != nil {
			return p, e
		}
		if p.PrimingFrames > media || p.MediaFrames > media-p.PrimingFrames {
			return p, fmt.Errorf("%w: edit outside media duration", pcm.ErrMalformed)
		}
	}
	if p.PrimingFrames > decodedFrames || p.MediaFrames > decodedFrames-p.PrimingFrames {
		return p, fmt.Errorf("%w: decoded frames too short for timing", pcm.ErrMalformed)
	}
	p.PaddingFrames = decodedFrames - p.PrimingFrames - p.MediaFrames
	if p.LeadingSilenceFrames > math.MaxInt64-p.MediaFrames {
		return p, fmt.Errorf("%w: output timing overflow", pcm.ErrLimit)
	}
	p.OutputFrames = p.LeadingSilenceFrames + p.MediaFrames
	return p, nil
}
func exactFrames(ticks uint64, scale uint32, rate int) (int64, error) {
	if scale == 0 || rate <= 0 || ticks > math.MaxInt64 {
		return 0, fmt.Errorf("%w: time ratio", pcm.ErrLimit)
	}
	rem := ticks % uint64(scale)
	if rem > math.MaxUint64/uint64(rate) {
		return 0, fmt.Errorf("%w: time fraction overflow", pcm.ErrLimit)
	}
	if rem*uint64(rate)%uint64(scale) != 0 {
		return 0, fmt.Errorf("%w: fractional-sample edit/timing", pcm.ErrUnsupported)
	}
	return pcm.ScaleFrames(int64(ticks), rate, int(scale))
}

package decode

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
)

type outputLimits struct {
	capacity, buffering, reorder int
	width, height                uint32
}

// Annex A, Table A-1 and E.2.1. These checks bound the output DPB; they do
// not validate bitrate, processing rate, or every other level constraint.
func pictureOutputLimits(s *nal.SPS) (outputLimits, error) {
	level := s.LevelIDC
	if level == 11 && s.ConstraintFlags&0x10 != 0 && (s.ProfileIDC == 66 || s.ProfileIDC == 77 || s.ProfileIDC == 88) {
		level = 9 // Level 1b uses the Level 1 picture and DPB limits.
	}
	var maxFS, maxDPB uint64
	switch level {
	case 9, 10:
		maxFS, maxDPB = 99, 396
	case 11:
		maxFS, maxDPB = 396, 900
	case 12, 13, 20:
		maxFS, maxDPB = 396, 2376
	case 21:
		maxFS, maxDPB = 792, 4752
	case 22, 30:
		maxFS, maxDPB = 1620, 8100
	case 31:
		maxFS, maxDPB = 3600, 18000
	case 32:
		maxFS, maxDPB = 5120, 20480
	case 40, 41:
		maxFS, maxDPB = 8192, 32768
	case 42:
		maxFS, maxDPB = 8704, 34816
	case 50:
		maxFS, maxDPB = 22080, 110400
	case 51, 52:
		maxFS, maxDPB = 36864, 184320
	case 60, 61, 62:
		maxFS, maxDPB = 139264, 696320
	default:
		return outputLimits{}, fmt.Errorf("unsupported output-order level_idc %d", s.LevelIDC)
	}
	w, h := uint64(s.PicWidthInMbs), uint64(s.PicHeightInMapUnits)
	if !s.FrameMbsOnlyFlag || w == 0 || h == 0 || w*h > maxFS || w*w > maxFS*8 || h*h > maxFS*8 {
		return outputLimits{}, fmt.Errorf("%w: picture exceeds progressive level size limits", nal.ErrInvalidSyntax)
	}
	limit := outputLimits{capacity: int(min(maxDPB/(w*h), 16)), width: s.PicWidthInMbs, height: s.PicHeightInMapUnits}
	reorder, buffering := uint32(limit.capacity), uint32(limit.capacity)
	if s.BitstreamRestriction {
		reorder, buffering = s.MaxNumReorderFrames, s.MaxDecFrameBuffering
	} else if s.ConstraintFlags&0x10 != 0 {
		switch s.ProfileIDC {
		case 44, 86, 100, 110, 122, 244:
			reorder, buffering = 0, 0
		}
	}
	if reorder > buffering || buffering > uint32(limit.capacity) || buffering < s.MaxNumRefFrames {
		return outputLimits{}, fmt.Errorf("%w: inconsistent output DPB restrictions", nal.ErrInvalidSyntax)
	}
	limit.buffering, limit.reorder = int(buffering), int(reorder)
	return limit, nil
}

// outputBuffer uses the progressive-frame output-order DPB in C.4, releasing
// pictures earlier when the E.2.1 reorder bound guarantees their output order.
// Only pictures still needed for output are stored here. Reference storage is
// shared with Decoder.DPB, including pictures already output and gap placeholders.
type outputBuffer struct {
	pending []*frame.Frame
	limits  outputLimits
	output  func(*frame.Frame) error
}

// Reference marking copies Frame metadata but shares immutable coded pixels.
// Compare the coded-plane backing store, not a Frame pointer or wrapped POC.
func sameOutputPicture(a, b *frame.Frame) bool {
	return !a.NonExisting && !b.NonExisting && &a.Y[0] == &b.Y[0]
}

func (q *outputBuffer) fullness(refs []*frame.Frame) int {
	n := len(refs)
	for _, f := range q.pending {
		if !slices.ContainsFunc(refs, func(ref *frame.Frame) bool {
			return sameOutputPicture(f, ref)
		}) {
			n++
		}
	}
	return n
}

func (q *outputBuffer) first() *frame.Frame {
	return slices.MinFunc(q.pending, func(a, b *frame.Frame) int {
		return cmp.Compare(a.FullPOC, b.FullPOC)
	})
}

func (q *outputBuffer) emit(f *frame.Frame) error {
	view, err := f.OutputView()
	if err != nil {
		return err
	}
	return q.output(ownedOutput(view))
}

func (q *outputBuffer) bump() error {
	if len(q.pending) == 0 {
		return fmt.Errorf("%w: output DPB is full of references", nal.ErrInvalidSyntax)
	}
	f := q.first()
	i := slices.Index(q.pending, f)
	q.pending = slices.Delete(q.pending, i, i+1)
	return q.emit(f)
}

func (q *outputBuffer) flush() error {
	for len(q.pending) != 0 {
		if err := q.bump(); err != nil {
			return err
		}
	}
	return nil
}

// C.4.2 runs after each inferred gap's sliding marking, before inserting it.
// Outputting a reference does not free its slot; keep bumping until one is free.
func (q *outputBuffer) gap(refs []*frame.Frame) error {
	for q.fullness(refs) >= q.limits.capacity {
		if err := q.bump(); err != nil {
			return err
		}
	}
	return nil
}

func (q *outputBuffer) add(f *frame.Frame, sps *nal.SPS, markedRefs []*frame.Frame) error {
	limits, err := pictureOutputLimits(sps)
	if err != nil {
		return err
	}
	if f.IsIDR {
		// C.4.4 also infers discard when coded geometry or VUI buffering
		// changes. Compare the SPS belonging to the preceding picture.
		changed := q.limits.capacity != 0 && (q.limits.width != limits.width || q.limits.height != limits.height || q.limits.buffering != limits.buffering)
		if f.NoOutputOfPriorPics || changed {
			q.pending = nil
		} else if err := q.flush(); err != nil {
			return err
		}
	} else if f.ResetsPictureOrder {
		if err := q.flush(); err != nil {
			return err
		}
	}
	q.limits = limits
	// Marking has succeeded, but C.4.5 makes room before inserting current.
	refs := make([]*frame.Frame, 0, len(markedRefs))
	for _, ref := range markedRefs {
		if !sameOutputPicture(f, ref) {
			refs = append(refs, ref)
		}
	}
	for q.fullness(refs) >= q.limits.capacity {
		// A non-reference picture need not enter a full DPB if it precedes
		// every pending output (including the case with no pending output).
		if !f.IsRef && (len(q.pending) == 0 || f.FullPOC < q.first().FullPOC) {
			return q.emit(f)
		}
		if err := q.bump(); err != nil {
			return err
		}
	}
	q.pending = append(q.pending, f)
	// E.2.1 bounds pictures decoded before a future picture but output after
	// it. With more than reorder pictures pending, no future picture can
	// precede their minimum POC in output order without violating that bound.
	// Only pending output counts: delivering a picture does not release its
	// reference storage.
	for len(q.pending) > q.limits.reorder {
		if err := q.bump(); err != nil {
			return err
		}
	}
	return nil
}

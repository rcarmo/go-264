package decode

import (
	"errors"
	"fmt"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
)

const DefaultMaxNALBytes = 8 << 20

// ErrWaitingForIDR means prediction cannot resume until a complete IDR picture
// arrives. A receiver can use this error to request a new keyframe.
var ErrWaitingForIDR = errors.New("waiting for an IDR picture")

type StreamConfig struct {
	// MaxNALBytes bounds one encoded NAL, including its header but excluding
	// Annex B framing. Zero uses DefaultMaxNALBytes.
	MaxNALBytes int
	// MaxFrameMacroblocks has the same meaning as Decoder.MaxFrameMacroblocks.
	MaxFrameMacroblocks int
}

// StreamDecoder accepts incremental Annex B input without retaining an output
// history. Outputs are delivered synchronously in decoding order, not display
// order; FullPOC is available to consumers that reorder B pictures. Each output
// owns its pixels and metadata and may be modified or retained by the consumer.
//
// Retained storage is bounded by MaxNALBytes, the coded-picture budget and the
// SPS reference count (at most 16). Consumer-retained outputs are not included.
// Methods and the output callback must not call this decoder concurrently or
// reentrantly.
type StreamDecoder struct {
	d      *Decoder
	config StreamConfig
	output func(*frame.Frame) error
	// pending includes a synthetic three-byte Annex B prefix. Zero runs stay
	// separate until they are known to be payload rather than framing/padding.
	pending      []byte
	zeros        int
	zeroOverflow bool
	waitingIDR   bool
}

func NewStreamDecoder(config StreamConfig, output func(*frame.Frame) error) (*StreamDecoder, error) {
	if config.MaxNALBytes < 0 || config.MaxFrameMacroblocks < 0 {
		return nil, fmt.Errorf("negative stream resource limit")
	}
	if output == nil {
		return nil, fmt.Errorf("nil stream output callback")
	}
	if config.MaxNALBytes == 0 {
		config.MaxNALBytes = DefaultMaxNALBytes
	}
	s := &StreamDecoder{config: config, output: output}
	s.Reset()
	return s, nil
}

// Reset drops all input, parameter sets and reference state. Configuration and
// the output callback are preserved. The next sequence must provide SPS/PPS/IDR.
func (s *StreamDecoder) Reset() {
	s.d = NewDecoder()
	s.d.MaxFrameMacroblocks = s.config.MaxFrameMacroblocks
	s.pending, s.zeros, s.zeroOverflow = nil, 0, false
	s.waitingIDR = true
}

// Discontinuity reports transport loss or an application-level seek. It drops
// partial input and references but retains parsed SPS/PPS, then waits for IDR.
// Input errors and callback errors invoke this automatically. The remainder of
// a failed Push call is discarded; the next Push must begin at a start code.
// Complete pictures delivered before an error remain valid.
func (s *StreamDecoder) Discontinuity() {
	sps, pps := s.d.SPS, s.d.PPS
	s.Reset()
	s.d.SPS, s.d.PPS = sps, pps
}

func (s *StreamDecoder) WaitingForIDR() bool { return s.waitingIDR }

// Push accepts arbitrarily split Annex B bytes, including split start codes.
// A NAL is consumed when the next start code arrives; Drain consumes the last
// NAL. Push does not retain the supplied slice after it returns.
func (s *StreamDecoder) Push(data []byte) (err error) {
	defer func() {
		if err != nil {
			s.Discontinuity()
		}
	}()
	for _, b := range data {
		if b == 0 {
			if s.zeros < s.config.MaxNALBytes {
				s.zeros++
			} else {
				s.zeroOverflow = true
			}
			continue
		}
		if b == 1 && (s.zeros >= 2 || s.zeroOverflow) {
			if len(s.pending) != 0 {
				if err := s.consumeNAL(); err != nil {
					return err
				}
			}
			s.pending = append(s.pending[:0], 0, 0, 1)
			s.zeros, s.zeroOverflow = 0, false
			continue
		}
		if len(s.pending) == 0 {
			return fmt.Errorf("%w: data before Annex B start code", nal.ErrInvalidSyntax)
		}
		used := len(s.pending) - 3
		if s.zeroOverflow || used >= s.config.MaxNALBytes || s.zeros > s.config.MaxNALBytes-used-1 {
			return fmt.Errorf("NAL exceeds stream limit of %d bytes", s.config.MaxNALBytes)
		}
		for ; s.zeros > 0; s.zeros-- {
			s.pending = append(s.pending, 0)
		}
		s.pending = append(s.pending, b)
	}
	return nil
}

// Drain finishes the last NAL and picture, or reports a truncated picture. It
// retains SPS/PPS and references, so the next Annex B segment may continue the
// sequence. It does not emit reference-gap placeholders or retain an output
// queue; a second Drain without more input is a no-op.
func (s *StreamDecoder) Drain() (err error) {
	defer func() {
		if err != nil {
			s.Discontinuity()
		}
	}()
	if len(s.pending) != 0 {
		if err := s.consumeNAL(); err != nil {
			return err
		}
	} else if s.zeros != 0 || s.zeroOverflow {
		return fmt.Errorf("%w: missing Annex B start code", nal.ErrInvalidSyntax)
	}
	s.pending, s.zeros, s.zeroOverflow = nil, 0, false
	return s.publish()
}

func (s *StreamDecoder) consumeNAL() error {
	units, err := nal.SplitNALUnitsChecked(s.pending)
	if err != nil {
		return err
	}
	u := units[0] // pending contains exactly one scanner-delimited NAL.
	// Prefix NALs (14) can occur between base-layer slices; ignore them
	// without forcing picture completion.
	switch u.Type {
	case nal.TypeSEI, nal.TypeSPS, nal.TypePPS, nal.TypeAUD, nal.TypeEndSeq, nal.TypeEndStream, 15, 16, 17, 18:
		if err := s.publish(); err != nil {
			return err
		}
	}
	switch u.Type {
	case nal.TypeSPS:
		ps, err := nal.ParseSPS(u.Payload)
		if err != nil {
			return fmt.Errorf("SPS: %w", err)
		}
		s.d.SPS[ps.SPSID] = ps
	case nal.TypePPS:
		ps, err := nal.ParsePPS(u.Payload)
		if err != nil {
			return fmt.Errorf("PPS: %w", err)
		}
		s.d.PPS[ps.PPSID] = ps
	case nal.TypeEndSeq, nal.TypeEndStream:
		// End markers terminate prediction continuity, unlike Drain between
		// caller-supplied segments of the same coded sequence.
		s.Discontinuity()
	case nal.TypeSliceIDR, nal.TypeSliceNonIDR:
		slice, err := s.d.parseSlice(u)
		if err != nil {
			return fmt.Errorf("slice: %w", err)
		}
		if s.d.picture != nil && s.d.picture.identity != identifyPicture(slice) {
			if err := s.publish(); err != nil {
				return err
			}
		}
		if s.waitingIDR && u.Type != nal.TypeSliceIDR {
			return ErrWaitingForIDR
		}
		if err := s.d.addSlice(slice); err != nil {
			return fmt.Errorf("slice: %w", err)
		}
		// Headers/metadata are needed until picture completion, encoded bytes
		// are not. Releasing them also allows the scanner buffer to be reused.
		slice.reader, slice.unit.Payload = nil, nil
	case nal.TypeSlicePartA, nal.TypeSlicePartB, nal.TypeSlicePartC:
		return fmt.Errorf("unsupported coded slice NAL type %d", u.Type)
	default:
		// Other non-VCL contents are ignored, as in the batch decoder.
	}
	return nil
}

func (s *StreamDecoder) publish() error {
	p := s.d.picture
	if p == nil {
		return nil
	}
	if _, err := s.d.finishPicture(); err != nil {
		return err
	}
	if err := s.d.commitPictureReferences(p); err != nil {
		return err
	}
	s.d.commitPicturePOC(p)
	view, err := p.frame.OutputView()
	if err != nil {
		return err
	}
	s.d.picture, s.d.slice, s.d.activeL0Refs, s.d.intraModes = nil, nil, nil, nil
	s.waitingIDR = false
	s.d.traceFrameIndex++
	return s.output(ownedOutput(view))
}

func ownedOutput(f *frame.Frame) *frame.Frame {
	out := *f
	copyPlane := func(src []byte, stride, width, height int) []byte {
		dst := make([]byte, width*height)
		for y := 0; y < height; y++ {
			copy(dst[y*width:(y+1)*width], src[y*stride:y*stride+width])
		}
		return dst
	}
	out.Y = copyPlane(f.Y, f.StrideY, f.Width, f.Height)
	out.U = copyPlane(f.U, f.StrideC, f.Width/2, f.Height/2)
	out.V = copyPlane(f.V, f.StrideC, f.Width/2, f.Height/2)
	out.StrideY, out.StrideC = f.Width, f.Width/2
	out.MotionL0 = append([][2]int16(nil), f.MotionL0...)
	out.MotionL1 = append([][2]int16(nil), f.MotionL1...)
	out.RefIdxL0 = append([]int8(nil), f.RefIdxL0...)
	out.TemporalRefIdxL0 = append([]int8(nil), f.TemporalRefIdxL0...)
	out.RefIdxL1 = append([]int8(nil), f.RefIdxL1...)
	out.MBType = append([]uint32(nil), f.MBType...)
	out.RefListL0POC = append([]int(nil), f.RefListL0POC...)
	out.RefListL0Num = append([]int(nil), f.RefListL0Num...)
	return &out
}

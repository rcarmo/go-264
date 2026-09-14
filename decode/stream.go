package decode

import (
	"bytes"
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
	// OutputOrder enables presentation-order delivery for progressive frames.
	// Pictures are released when pending output exceeds the SPS reorder bound
	// or DPB storage fills. A zero bound delivers each completed picture
	// immediately; an absent bound is inferred and can retain up to the level's
	// DPB capacity (at most 16). Reference pictures remain available after
	// delivery. Drain ends the sequence and requires a new IDR; false preserves
	// the original decoding-order/segment-drain API.
	OutputOrder bool
}

// StreamDecoder accepts incremental Annex B input without retaining an output
// history. Outputs are delivered synchronously in decoding order unless
// StreamConfig.OutputOrder is enabled. I/P pictures can also need reordering. Each output
// owns its pixels and metadata and may be modified or retained by the consumer.
//
// Retained storage is bounded by MaxNALBytes, the coded-picture budget and the
// SPS reference count (at most 16). In output-order mode, the reference/output
// union is bounded by the level's DPB capacity, plus one coded picture for
// reconstruction or reuse.
// Consumer-retained outputs are not included.
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
	order        *outputBuffer
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
	// Keep diagnostics stable throughout a streamed sequence, including pictures
	// split across Push calls. Reset starts a new configuration snapshot.
	s.d.trace = snapshotTraceConfig()
	s.d.traceRefList = s.d.trace.enabled(traceRefList)
	s.d.MaxFrameMacroblocks = s.config.MaxFrameMacroblocks
	s.d.pictureBuffers = &pictureBufferPool{}
	if s.config.OutputOrder {
		s.order = &outputBuffer{output: s.output}
		s.d.outputOrder = s.order
	}
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

// DecodeAccessUnit accepts one complete Annex B picture, possibly in multiple
// slices. Parameter-set or filler-only input is also allowed and emits nothing.
// Each output carries its picture's tag, even when later input releases it;
// tags on input without a picture are ignored. Zero is a valid tag.
// MaxNALBytes bounds each NAL, not the whole input. Callers must bound data;
// temporary parsing storage scales with the number of NALs in the buffer.
//
// Completing an access unit preserves prediction state and any delayed output.
// Explicit end markers still end the sequence. Input is not retained after
// return. Decode errors discard prediction state as with Push; previously
// delivered pictures remain valid.
// Pending incremental input must first be finished with Drain or discarded
// with Discontinuity. Calling this method while it is pending returns an error
// without discarding that input.
func (s *StreamDecoder) DecodeAccessUnit(data []byte, tag uint64) (err error) {
	if len(s.pending) != 0 || s.zeros != 0 || s.zeroOverflow || s.d.picture != nil {
		return fmt.Errorf("cannot decode a complete access unit with incremental input pending")
	}
	defer func() {
		if err != nil {
			s.Discontinuity()
		}
	}()
	units, err := nal.SplitNALUnitsChecked(data)
	if err != nil {
		return err
	}
	var end *nal.Unit
	for i, u := range units {
		if len(u.Payload)+1 > s.config.MaxNALBytes {
			return fmt.Errorf("NAL exceeds stream limit of %d bytes", s.config.MaxNALBytes)
		}
		if end != nil && (end.Type == nal.TypeEndStream || u.IsSlice()) {
			return fmt.Errorf("%w: NAL after access-unit end marker", nal.ErrInvalidSyntax)
		}
		// End markers belong after the picture. Defer their reset until all
		// input has been checked and the current picture has been published.
		if u.Type == nal.TypeEndSeq || u.Type == nal.TypeEndStream {
			end = &units[i]
			continue
		}
		if err := s.consumeUnit(u, true); err != nil {
			return err
		}
	}
	if s.d.picture != nil {
		// Attach the token before reference marking or output can copy the
		// picture header. It must never come from the call releasing an older
		// picture, nor leak from a filler-only call to the following picture.
		s.d.picture.frame.Tag = tag
	}
	if err := s.publish(); err != nil {
		return err
	}
	if end != nil {
		return s.consumeUnit(*end, false)
	}
	return nil
}

// Push accepts arbitrarily split Annex B bytes, including split start codes.
// A NAL is consumed when the next start code arrives; Drain consumes the last
// NAL. Push does not retain the supplied slice after it returns. Pictures
// decoded through Push have tag zero.
func (s *StreamDecoder) Push(data []byte) (err error) {
	defer func() {
		if err != nil {
			s.Discontinuity()
		}
	}()
	for len(data) != 0 {
		// A nonzero run cannot contain an Annex B start code. Append it as a
		// block; zero runs still use the framing path below, including when a
		// prefix or emulation-prevention sequence crosses Push calls.
		if len(data) > 1 && s.zeros == 0 && data[0] != 0 && len(s.pending) != 0 {
			n := bytes.IndexByte(data, 0)
			if n < 0 {
				n = len(data)
			}
			if n > s.config.MaxNALBytes-(len(s.pending)-3) {
				return fmt.Errorf("NAL exceeds stream limit of %d bytes", s.config.MaxNALBytes)
			}
			s.pending = append(s.pending, data[:n]...)
			data = data[n:]
			continue
		}
		b := data[0]
		data = data[1:]
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
// retains SPS/PPS and, in decoding-order mode, references for the next segment.
// In output-order mode it flushes pending pictures in presentation order and
// ends the sequence: SPS/PPS survive, but a new IDR is required. Do not drain
// between arbitrary chunks of an output-order sequence; use Push instead.
// Reference-gap placeholders are never output. Repeated Drain is a no-op.
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
	if err := s.publish(); err != nil {
		return err
	}
	if s.order != nil {
		if err := s.order.flush(); err != nil {
			return err
		}
		s.Discontinuity()
	}
	return nil
}

func (s *StreamDecoder) consumeNAL() error {
	units, err := nal.SplitNALUnitsChecked(s.pending)
	if err != nil {
		return err
	}
	u := units[0] // pending contains exactly one scanner-delimited NAL.
	return s.consumeUnit(u, false)
}

// consumeUnit shares syntax and reconstruction between framed and incremental
// input. Framed input must stay in one picture until its caller publishes it;
// incremental input discovers picture boundaries from the NALs themselves.
func (s *StreamDecoder) consumeUnit(u nal.Unit, framed bool) error {
	// Prefix NALs (14) can occur between base-layer slices; ignore them
	// without forcing picture completion.
	switch u.Type {
	case nal.TypeSEI, nal.TypeSPS, nal.TypePPS, nal.TypeAUD, nal.TypeEndSeq, nal.TypeEndStream, 15, 16, 17, 18:
		if framed && s.d.picture != nil {
			// These NALs start another access unit after VCL (7.4.1.2.3).
			// DecodeAccessUnit handles its trailing end markers separately.
			return fmt.Errorf("%w: access-unit delimiter after picture", nal.ErrInvalidSyntax)
		}
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
		if s.order != nil {
			if err := s.order.flush(); err != nil {
				return err
			}
		}
		s.Discontinuity()
	case nal.TypeSliceIDR, nal.TypeSliceNonIDR:
		slice, err := s.d.parseSlice(u)
		if err != nil {
			return fmt.Errorf("slice: %w", err)
		}
		if s.d.picture != nil && s.d.picture.identity != identifyPicture(slice) {
			if framed {
				return fmt.Errorf("%w: multiple pictures in one access unit", nal.ErrInvalidSyntax)
			}
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
	var pending []*frame.Frame
	if s.order != nil {
		err = s.order.add(p.frame, p.sps, s.d.DPB.Frames)
		pending = s.order.pending
	} else {
		err = s.output(ownedOutput(view))
	}
	if err != nil {
		return err
	}
	// Marking and output can release several pictures at once (IDR/MMCO5).
	// Prune now, rather than retaining the old peak until another picture arrives.
	s.d.pictureBuffers.prune(int(p.sps.PicWidthInMbs), int(p.sps.PicHeightInMapUnits), s.d.DPB.Frames, pending)
	return nil
}

func ownedOutput(f *frame.Frame) *frame.Frame {
	out := *f
	out.Y, out.U, out.V = makePicturePlanes(f.Width*f.Height, (f.Width/2)*(f.Height/2))
	copyPlane := func(dst, src []byte, stride, width, height int) {
		if stride == width {
			copy(dst, src[:len(dst)])
			return
		}
		for y := 0; y < height; y++ {
			copy(dst[y*width:(y+1)*width], src[y*stride:y*stride+width])
		}
	}
	copyPlane(out.Y, f.Y, f.StrideY, f.Width, f.Height)
	copyPlane(out.U, f.U, f.StrideC, f.Width/2, f.Height/2)
	copyPlane(out.V, f.V, f.StrideC, f.Width/2, f.Height/2)
	out.StrideY, out.StrideC = f.Width, f.Width/2
	out.MotionL0, out.MotionL1, _ = ownedMetadataSlices(f.MotionL0, f.MotionL1, nil)
	out.RefIdxL0, out.TemporalRefIdxL0, out.RefIdxL1 = ownedMetadataSlices(f.RefIdxL0, f.TemporalRefIdxL0, f.RefIdxL1)
	out.MBType = append([]uint32(nil), f.MBType...)
	out.RefListL0POC, out.RefListL0Num, _ = ownedMetadataSlices(f.RefListL0POC, f.RefListL0Num, nil)
	return &out
}

// ownedMetadataSlices copies up to three same-typed metadata arrays into one
// backing allocation. Empty components stay nil, and full-slice capacity limits
// preserve their independence when a consumer grows or replaces an array.
func ownedMetadataSlices[T any](a, b, c []T) (outA, outB, outC []T) {
	if len(a)+len(b)+len(c) == 0 {
		return nil, nil, nil
	}
	buf := make([]T, len(a)+len(b)+len(c))
	clone := func(src []T) []T {
		if len(src) == 0 {
			return nil
		}
		dst := buf[:len(src):len(src)]
		copy(dst, src)
		buf = buf[len(src):]
		return dst
	}
	return clone(a), clone(b), clone(c)
}

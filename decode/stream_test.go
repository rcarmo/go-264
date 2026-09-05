package decode

import (
	"bytes"
	"errors"
	"image"
	"reflect"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

func assemblyStream(t *testing.T, width, height int, output func(*frame.Frame) error) *StreamDecoder {
	t.Helper()
	s, err := NewStreamDecoder(StreamConfig{MaxFrameMacroblocks: width * height}, output)
	if err != nil {
		t.Fatal(err)
	}
	d := assemblyDecoder(width, height)
	s.d.SPS, s.d.PPS = d.SPS, d.PPS
	return s
}

func pushAndDrain(t *testing.T, s *StreamDecoder, input []byte) {
	t.Helper()
	if err := s.Push(input); err != nil {
		t.Fatal(err)
	}
	if err := s.Drain(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamAccessUnitTagsAndOwnedOutput(t *testing.T) {
	var outputs []*frame.Frame
	s := assemblyStream(t, 2, 1, func(f *frame.Frame) error {
		outputs = append(outputs, f)
		return nil
	})
	const firstTag = uint64(0xfedcba9876543210)
	// Two slices make one picture. No following start code or Drain is needed.
	input := assemblyInput(pcmAssemblySlice(0, 81), pcmAssemblySlice(1, 149))
	if err := s.DecodeAccessUnit(input, firstTag); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Tag != firstTag || outputs[0].PixelY(16, 0) != 149 {
		t.Fatal("complete picture was delayed, mis-tagged or lost a slice")
	}
	// Neither retained output nor caller input may alias future prediction.
	outputs[0].Y[0], outputs[0].Tag = 1, 2
	clear(input)
	if err := s.DecodeAccessUnit(assemblyInput(nal.Unit{Type: nal.TypeFiller, Payload: []byte{0xff, 0x80}}), 99); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 {
		t.Fatal("filler emitted or repeated a picture")
	}
	// Switching back to incremental input must not inherit the nonzero tag.
	pushAndDrain(t, s, assemblyInput(referenceSkipSlice(1, nil, nil, 2)))
	if len(outputs) != 2 || outputs[1].Tag != 0 || outputs[1].Y[0] != 81 || outputs[1].PixelY(16, 0) != 149 {
		t.Fatal("filler/caller metadata leaked, or owned output corrupted prediction")
	}
	// Zero is also a valid explicit tag when switching to framed input again.
	if err := s.DecodeAccessUnit(assemblyInput(referenceSkipSlice(2, nil, nil, 2)), 0); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 3 || outputs[2].Tag != 0 {
		t.Fatal("explicit zero tag was not preserved")
	}
}

func TestStreamAccessUnitParameterOnlyInput(t *testing.T) {
	for _, vector := range decoderSyntaxVectors {
		t.Run(vector.name, func(t *testing.T) {
			prefix, slice := firstSyntaxTestSlice(t, vector.name)
			var outputs []*frame.Frame
			s, err := NewStreamDecoder(StreamConfig{}, func(f *frame.Frame) error { outputs = append(outputs, f); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DecodeAccessUnit(prefix, 99); err != nil || len(outputs) != 0 {
				t.Fatalf("parameter-only input: %v, %d outputs", err, len(outputs))
			}
			if err := s.DecodeAccessUnit(assemblyInput(slice), 7); err != nil {
				t.Fatal(err)
			}
			if len(outputs) != 1 || outputs[0].Tag != 7 || outputs[0].Width != 32 || outputs[0].Height != 16 {
				t.Fatal("parameter-only input lost configuration or assigned its tag to the picture")
			}
		})
	}
}

func TestStreamAccessUnitRejectsIncompleteAndMultiplePictures(t *testing.T) {
	complete := pcmAssemblySlice(0, 81, 149)
	for _, tt := range []struct {
		name  string
		units []nal.Unit
	}{
		{"missing slice", []nal.Unit{pcmAssemblySlice(0, 81)}},
		{"second picture", []nal.Unit{complete, referenceSkipSlice(1, nil, nil)}},
		{"AUD between slices", []nal.Unit{pcmAssemblySlice(0, 81), {Type: nal.TypeAUD, Payload: []byte{0x10}}, pcmAssemblySlice(1, 149)}},
		{"next AU prefix", []nal.Unit{complete, {Type: nal.TypeSEI, Payload: []byte{0x80}}}},
		{"slice after end sequence", []nal.Unit{complete, {Type: nal.TypeEndSeq}, referenceSkipSlice(1, nil, nil)}},
		{"data after end stream", []nal.Unit{complete, {Type: nal.TypeEndStream}, {Type: nal.TypeFiller, Payload: []byte{0x80}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var outputs []*frame.Frame
			s := assemblyStream(t, 2, 1, func(f *frame.Frame) error { outputs = append(outputs, f); return nil })
			if err := s.DecodeAccessUnit(assemblyInput(tt.units...), 13); err == nil {
				t.Fatal("invalid complete-picture input accepted")
			}
			if len(outputs) != 0 || !s.WaitingForIDR() || len(s.d.DPB.Frames) != 0 {
				t.Fatal("invalid input published a picture or retained damaged references")
			}
			if err := s.DecodeAccessUnit(assemblyInput(complete), 17); err != nil {
				t.Fatal(err)
			}
			if len(outputs) != 1 || outputs[0].Tag != 17 {
				t.Fatal("recovery inherited the failed input's tag")
			}
		})
	}
}

func TestStreamAccessUnitPreservesPendingIncrementalInput(t *testing.T) {
	var outputs []*frame.Frame
	s := assemblyStream(t, 1, 1, func(f *frame.Frame) error { outputs = append(outputs, f); return nil })
	input := assemblyInput(pcmAssemblySlice(0, 91))
	if err := s.Push(input[:len(input)/2]); err != nil {
		t.Fatal(err)
	}
	if err := s.DecodeAccessUnit(input, 42); err == nil {
		t.Fatal("framed input was appended to an unfinished NAL")
	}
	pushAndDrain(t, s, input[len(input)/2:])
	if len(outputs) != 1 || outputs[0].Tag != 0 || outputs[0].Y[0] != 91 {
		t.Fatal("API-usage error lost or relabeled incremental input")
	}
	if err := s.DecodeAccessUnit(assemblyInput(referenceSkipSlice(1, nil, nil)), 42); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 || outputs[1].Tag != 42 {
		t.Fatal("could not switch APIs after completing incremental input")
	}
}

func TestStreamAccessUnitEndMarkersAndCallbackFailure(t *testing.T) {
	var outputs []*frame.Frame
	want := errors.New("consumer stopped")
	fail := false
	s := assemblyStream(t, 1, 1, func(f *frame.Frame) error {
		if fail {
			return want
		}
		outputs = append(outputs, f)
		return nil
	})
	// End-sequence may precede filler; end-stream must be last (7.4.1.2.3).
	input := assemblyInput(pcmAssemblySlice(0, 91), nal.Unit{Type: nal.TypeEndSeq},
		nal.Unit{Type: nal.TypeFiller, Payload: []byte{0xff, 0x80}}, nal.Unit{Type: nal.TypeEndStream})
	if err := s.DecodeAccessUnit(input, 23); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Tag != 23 || !s.WaitingForIDR() {
		t.Fatal("end marker lost the tagged picture or retained prediction continuity")
	}
	if err := s.DecodeAccessUnit(assemblyInput(referenceSkipSlice(1, nil, nil)), 24); !errors.Is(err, ErrWaitingForIDR) {
		t.Fatalf("predicted picture after end marker: %v", err)
	}
	fail = true
	if err := s.DecodeAccessUnit(assemblyInput(pcmAssemblySlice(0, 92)), 25); !errors.Is(err, want) {
		t.Fatalf("callback error: %v", err)
	}
	if !s.WaitingForIDR() || len(s.d.DPB.Frames) != 0 {
		t.Fatal("callback failure retained references")
	}
}

func TestStreamAllChunkBoundaries(t *testing.T) {
	input := assemblyInput(pcmAssemblySlice(0, 81), pcmAssemblySlice(1, 149), nal.Unit{Type: nal.TypeAUD, Payload: []byte{0x10}})
	for split := 0; split <= len(input); split++ {
		var outputs []*frame.Frame
		s := assemblyStream(t, 2, 1, func(f *frame.Frame) error { outputs = append(outputs, f); return nil })
		if err := s.Push(input[:split]); err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		pushAndDrain(t, s, input[split:])
		if len(outputs) != 1 || outputs[0].PixelY(0, 0) != 81 || outputs[0].PixelY(16, 0) != 149 {
			t.Fatalf("split %d lost/repeated a slice or picture", split)
		}
		if err := s.Drain(); err != nil || len(outputs) != 1 {
			t.Fatalf("second drain repeated output: %v", err)
		}
	}
}

func TestStreamBytewiseInputAndOwnedOutput(t *testing.T) {
	var outputs []*frame.Frame
	s := assemblyStream(t, 1, 1, func(f *frame.Frame) error {
		outputs = append(outputs, f)
		if len(outputs) == 1 {
			f.Y[0], f.U[0], f.V[0] = 1, 2, 3
			f.FrameNum = 12
			f.MotionL0[0] = [2]int16{99, 99}
			f.RefIdxL0[0], f.MBType[0] = 9, 999
		}
		return nil
	})
	input := assemblyInput(pcmAssemblySlice(0, 91), referenceSkipSlice(1, nil, nil), nal.Unit{Type: nal.TypeAUD, Payload: []byte{0x10}})
	for _, b := range input {
		if err := s.Push([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Drain(); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 || outputs[1].Y[0] != 91 || outputs[1].U[0] != 91 || outputs[1].V[0] != 91 {
		t.Fatal("caller mutation corrupted prediction")
	}
	if ref := s.d.DPB.Frames[0]; ref.FrameNum != 0 || ref.MotionL0[0] != [2]int16{} || ref.MBType[0] == 999 {
		t.Fatal("caller metadata mutation reached DPB")
	}
	if len(s.d.Frames) != 0 || s.d.picture != nil || s.d.slice != nil || s.d.activeL0Refs != nil {
		t.Fatal("stream retains output history or finished picture state")
	}
}

func TestOwnedOutputCopiesCroppedPlanesAndMotion(t *testing.T) {
	f := frame.NewFrame(32, 32)
	f.CropRect = image.Rect(2, 2, 28, 30)
	for i := range f.Y {
		f.Y[i] = byte(i)
	}
	f.RefListL0POC, f.RefListL0Num = []int{9}, []int{4}
	f.TemporalRefIdxL0 = []int8{0}
	f.MotionL1, f.RefIdxL1 = [][2]int16{{1, 2}}, []int8{1}
	v, err := f.OutputView()
	if err != nil {
		t.Fatal(err)
	}
	out := ownedOutput(v)
	if out.StrideY != 26 || out.StrideC != 13 || len(out.Y) != 26*28 || len(out.U) != 13*14 {
		t.Fatal("owned output retained coded padding")
	}
	for y := 0; y < out.Height; y++ {
		if !bytes.Equal(out.Y[y*out.StrideY:(y+1)*out.StrideY], v.Y[y*v.StrideY:y*v.StrideY+v.Width]) {
			t.Fatal("crop copied wrong rows")
		}
	}
	out.RefListL0POC[0], out.RefListL0Num[0], out.RefIdxL1[0] = 0, 0, 0
	out.MotionL1[0] = [2]int16{}
	out.TemporalRefIdxL0[0] = 1
	if f.RefListL0POC[0] != 9 || f.RefListL0Num[0] != 4 || f.RefIdxL1[0] != 1 || f.MotionL1[0] != [2]int16{1, 2} || f.TemporalRefIdxL0[0] != 0 {
		t.Fatal("owned output aliases metadata")
	}
}

func TestStreamLossRecoveryRequiresCompleteIDR(t *testing.T) {
	count := 0
	s := assemblyStream(t, 2, 1, func(*frame.Frame) error { count++; return nil })
	pushAndDrain(t, s, assemblyInput(pcmAssemblySlice(0, 81, 149)))
	if s.WaitingForIDR() {
		t.Fatal("complete IDR did not establish references")
	}
	if err := s.Push(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
		t.Fatal(err)
	}
	if err := s.Drain(); err == nil || !strings.Contains(err.Error(), "incomplete picture") {
		t.Fatalf("incomplete IDR: %v", err)
	}
	if count != 1 || !s.WaitingForIDR() || len(s.d.DPB.Frames) != 0 || len(s.d.SPS) != 1 {
		t.Fatal("failed IDR left damaged prediction state or lost SPS")
	}
	if err := s.Push(assemblyInput(referenceSkipSlice(1, nil, nil))); err != nil {
		t.Fatal(err)
	}
	if err := s.Drain(); !errors.Is(err, ErrWaitingForIDR) {
		t.Fatalf("predicted picture after loss: %v", err)
	}
	pushAndDrain(t, s, assemblyInput(pcmAssemblySlice(0, 102, 103)))
	if count != 2 || s.WaitingForIDR() {
		t.Fatal("fresh IDR failed to recover")
	}
	s.Discontinuity()
	if len(s.d.DPB.Frames) != 0 || !s.WaitingForIDR() || len(s.d.PPS) != 1 {
		t.Fatal("explicit loss retained references or discarded parameter sets")
	}
	s.Reset()
	if len(s.d.PPS) != 0 || len(s.d.SPS) != 0 {
		t.Fatal("Reset retained the old sequence")
	}
}

func TestStreamCallbackErrorDropsReferences(t *testing.T) {
	want := errors.New("consumer stopped")
	s := assemblyStream(t, 1, 1, func(*frame.Frame) error { return want })
	if err := s.Push(assemblyInput(pcmAssemblySlice(0, 91))); err != nil {
		t.Fatal(err)
	}
	if err := s.Drain(); !errors.Is(err, want) {
		t.Fatalf("callback error: %v", err)
	}
	if !s.WaitingForIDR() || len(s.d.DPB.Frames) != 0 || len(s.pending) != 0 {
		t.Fatal("callback failure retained in-flight sequence")
	}
}

func TestStreamEndMarkerRequiresNewIDR(t *testing.T) {
	for _, typ := range []uint8{nal.TypeEndSeq, nal.TypeEndStream} {
		count := 0
		s := assemblyStream(t, 1, 1, func(*frame.Frame) error { count++; return nil })
		pushAndDrain(t, s, assemblyInput(pcmAssemblySlice(0, 91), nal.Unit{Type: typ, Payload: []byte{0x80}}))
		if count != 1 || !s.WaitingForIDR() || len(s.d.DPB.Frames) != 0 {
			t.Fatalf("end marker %d retained prediction continuity", typ)
		}
		pushAndDrain(t, s, assemblyInput(pcmAssemblySlice(0, 92)))
		if count != 2 || s.WaitingForIDR() {
			t.Fatalf("IDR after end marker %d failed to restart", typ)
		}
	}
}

func TestStreamIgnoresNonDelimitingNALs(t *testing.T) {
	for _, typ := range []uint8{0, 12, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31} {
		var outputs []*frame.Frame
		s := assemblyStream(t, 2, 1, func(f *frame.Frame) error { outputs = append(outputs, f); return nil })
		ignored := nal.Unit{Type: typ, Payload: []byte{0x80}}
		units := []nal.Unit{pcmAssemblySlice(0, 81), ignored, pcmAssemblySlice(1, 149)}
		if typ == 19 {
			// Auxiliary slices follow the complete primary picture.
			units[1], units[2] = units[2], units[1]
		}
		units = append(units, nal.Unit{Type: nal.TypeFiller, Payload: []byte{0xff, 0x80}})
		for _, b := range assemblyInput(units...) {
			if err := s.Push([]byte{b}); err != nil {
				t.Fatalf("type %d: %v", typ, err)
			}
		}
		if len(outputs) != 0 {
			t.Fatalf("type %d prematurely delimited the picture", typ)
		}
		if err := s.Drain(); err != nil {
			t.Fatalf("type %d drain: %v", typ, err)
		}
		if len(outputs) != 1 || outputs[0].PixelY(0, 0) != 81 || outputs[0].PixelY(16, 0) != 149 {
			t.Fatalf("type %d lost or repeated slice samples", typ)
		}
	}
}

func TestStreamExtensionNALStartsAccessUnit(t *testing.T) {
	for _, typ := range []uint8{15, 16, 17, 18} {
		count := 0
		s := assemblyStream(t, 1, 1, func(*frame.Frame) error { count++; return nil })
		data := assemblyInput(pcmAssemblySlice(0, 91), nal.Unit{Type: typ, RefIDC: 1, Payload: []byte{0x80}}, referenceSkipSlice(1, nil, nil))
		if err := s.Push(data); err != nil {
			t.Fatalf("type %d: %v", typ, err)
		}
		if count != 1 {
			t.Fatalf("type %d did not delimit the first access unit", typ)
		}
		if err := s.Drain(); err != nil || count != 2 {
			t.Fatalf("type %d lost following picture: count=%d err=%v", typ, count, err)
		}
	}
}

func checkStreamSVCPrefixes(t *testing.T, config StreamConfig) {
	t.Helper()
	input := prefixAssemblyInput(true)
	for _, chunkSize := range []int{1, 7, 128, len(input)} {
		var outputs []*frame.Frame
		s, err := NewStreamDecoder(config, func(f *frame.Frame) error { outputs = append(outputs, f); return nil })
		if err != nil {
			t.Fatal(err)
		}
		d := assemblyDecoder(2, 1)
		d.SPS[0].LevelIDC, d.SPS[0].ConstraintFlags = 10, 0xc0
		s.d.SPS, s.d.PPS = d.SPS, d.PPS
		for offset := 0; offset < len(input); offset += chunkSize {
			if err := s.Push(input[offset:min(offset+chunkSize, len(input))]); err != nil {
				t.Fatalf("chunk size %d: %v", chunkSize, err)
			}
		}
		if err := s.Drain(); err != nil {
			t.Fatalf("chunk size %d drain: %v", chunkSize, err)
		}
		checkPrefixPictures(t, outputs)
	}
}

func TestStreamSVCPrefixes(t *testing.T) {
	checkStreamSVCPrefixes(t, StreamConfig{})
}

func TestStreamOutputOrderSVCPrefixes(t *testing.T) {
	checkStreamSVCPrefixes(t, StreamConfig{OutputOrder: true})
}

func TestStreamNALBudgetAndPadding(t *testing.T) {
	s, err := NewStreamDecoder(StreamConfig{MaxNALBytes: 32}, func(*frame.Frame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// Start codes and arbitrarily long trailing-zero padding do not consume
	// NAL storage. A zero run followed by non-framing data does consume it.
	input := append([]byte{0, 0, 1, nal.TypeFiller}, bytes.Repeat([]byte{0xff}, 30)...)
	input = append(input, 0x80)
	if err := s.Push(input); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(make([]byte, 1<<20)); err != nil || len(s.pending) != 35 {
		t.Fatalf("padding allocated payload: %v", err)
	}
	if err := s.Drain(); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(append(input, 0x80)); err == nil || !strings.Contains(err.Error(), "NAL exceeds") {
		t.Fatalf("oversized NAL: %v", err)
	}
	if err := s.Push([]byte{0, 0, 1, nal.TypeFiller}); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.Push([]byte{3}); err == nil {
		t.Fatal("payload zero run evaded NAL budget")
	}
	if err := s.Push([]byte{0, 0, 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drain(); err == nil {
		t.Fatal("missing NAL header accepted")
	}
	// The framed entry point uses the same NAL budget, excluding Annex B
	// delimiters and trailing zero bytes, without buffering through Push.
	if err := s.DecodeAccessUnit(append(append([]byte(nil), input...), make([]byte, 1024)...), 7); err != nil {
		t.Fatalf("framed NAL exactly at limit with padding: %v", err)
	}
	if err := s.DecodeAccessUnit(append(input, 0x80), 8); err == nil || !strings.Contains(err.Error(), "NAL exceeds") {
		t.Fatalf("oversized framed NAL: %v", err)
	}
}

func TestStreamLongRunningRetainedState(t *testing.T) {
	count := 0
	s := assemblyStream(t, 1, 1, func(*frame.Frame) error { count++; return nil })
	for i := 0; i < 2000; i++ {
		u := referenceSkipSlice(uint32(i%16), nil, nil)
		if i%64 == 0 {
			u = pcmAssemblySlice(0, 91)
		}
		pushAndDrain(t, s, assemblyInput(u))
		if len(s.d.Frames) != 0 || len(s.d.DPB.Frames) > 4 || s.d.picture != nil || s.d.activeL0Refs != nil || s.pending != nil {
			t.Fatalf("retained state grew at frame %d", i)
		}
	}
	if count != 2000 {
		t.Fatalf("delivered %d pictures", count)
	}
}

func FuzzStream(f *testing.F) {
	for _, vector := range decoderSyntaxVectors {
		f.Add(syntaxTestInput(f, vector.name), uint8(7))
	}
	f.Add([]byte{0, 0, 1, 0x67}, uint8(1))
	f.Add([]byte{}, uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, chunk uint8) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		count := 0
		s, err := NewStreamDecoder(StreamConfig{MaxNALBytes: 64 << 10, MaxFrameMacroblocks: 64}, func(*frame.Frame) error {
			count++
			if count == 2 {
				return errors.New("fuzz output limit")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		step := int(chunk) + 1
		for offset := 0; offset < len(data) && err == nil; offset += step {
			err = s.Push(data[offset:min(offset+step, len(data))])
		}
		if err == nil {
			err = s.Drain()
		}
		if err != nil && strings.Contains(err.Error(), "decode panic:") {
			t.Fatalf("unchecked malformed input: %v", err)
		}
	})
}

func FuzzStreamAccessUnit(f *testing.F) {
	for _, vector := range decoderSyntaxVectors {
		f.Add(syntaxTestInput(f, vector.name), uint64(7))
	}
	f.Add([]byte{0, 0, 1, 0x67}, uint64(0))
	f.Fuzz(func(t *testing.T, data []byte, tag uint64) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		s, err := NewStreamDecoder(StreamConfig{MaxNALBytes: 64 << 10, MaxFrameMacroblocks: 64}, func(out *frame.Frame) error {
			if out.Tag != tag {
				t.Fatalf("output tag = %d, want %d", out.Tag, tag)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DecodeAccessUnit(data, tag); err != nil && strings.Contains(err.Error(), "decode panic:") {
			t.Fatalf("unchecked malformed access unit: %v", err)
		}
	})
}

// A tiny self-contained CB stream; no external encoder or fixture is needed.
func outputOrderParameters(reorder ...uint32) []byte {
	w := &assemblyBits{}
	w.uint(66, 8)
	w.uint(0xc0, 8)
	w.uint(10, 8)
	w.ue(0) // SPS id
	w.ue(0) // four-bit frame_num
	w.ue(0) // POC type 0
	w.ue(4) // eight-bit POC LSB
	w.ue(4) // max references
	w.bit(0)
	w.ue(0)
	w.ue(0) // one macroblock
	w.bit(1)
	w.bit(1)
	w.bit(0) // no crop
	if len(reorder) == 0 {
		w.bit(0) // no VUI; infer level DPB capacity, not zero reorder
	} else {
		w.bit(1) // VUI present
		for i := 0; i < 8; i++ {
			w.bit(0) // aspect ratio through pic_struct_present_flag
		}
		w.bit(1) // bitstream restrictions
		w.bit(1) // motion vectors over picture boundaries
		w.ue(0)  // no coded-picture byte limit (fixtures use I_PCM)
		w.ue(0)  // no macroblock bit limit
		w.ue(15) // horizontal MV bound
		w.ue(15) // vertical MV bound
		w.ue(reorder[0])
		w.ue(4) // references still need storage, even with zero reordering
	}
	w.bit(1)
	w.align()
	sps := nal.Unit{Type: nal.TypeSPS, RefIDC: 3, Payload: w.bytes()}
	w = &assemblyBits{}
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.bit(0)
	w.ue(0)
	w.ue(0)
	w.ue(0)
	w.bit(0)
	w.uint(0, 2)
	w.ue(0)
	w.ue(0)
	w.ue(0)
	w.bit(1) // deblocking control
	w.bit(0)
	w.bit(0)
	w.bit(1)
	w.align()
	return assemblyInput(sps, nal.Unit{Type: nal.TypePPS, RefIDC: 3, Payload: w.bytes()})
}

func outputOrderSlice(number, poc uint32, reference, idr, discard, mmco5 bool, value byte) nal.Unit {
	w := &assemblyBits{}
	w.ue(0)
	if idr {
		w.ue(syntax.SliceTypeI)
	} else {
		w.ue(syntax.SliceTypeP)
	}
	w.ue(0)
	w.uint(number, 4)
	if idr {
		w.ue(0)
	}
	w.uint(poc, 8)
	if !idr {
		w.bit(0) // active reference count
		w.bit(0) // reference list modifications
	}
	if reference {
		if idr {
			if discard {
				w.bit(1)
			} else {
				w.bit(0)
			}
			w.bit(0)
		} else if mmco5 {
			w.bit(1)
			w.ue(5)
			w.ue(0)
		} else {
			w.bit(0)
		}
	}
	w.ue(0)
	w.ue(1) // filter off
	if idr {
		w.ue(25)
	} else {
		w.ue(0)
		w.ue(30)
	} // skip_run 0, P I_PCM
	w.align()
	for i := 0; i < 384; i++ {
		w.uint(uint32(value), 8)
	}
	w.bit(1)
	w.align()
	u := nal.Unit{Type: nal.TypeSliceNonIDR, Payload: w.bytes()}
	if idr {
		u.Type = nal.TypeSliceIDR
	}
	if reference {
		u.RefIDC = 1
	}
	return u
}

// One skipped macroblock copies the latest reference in the type-0 POC stream.
func outputOrderSkipSlice(number, poc uint32) nal.Unit {
	w := &assemblyBits{}
	w.ue(0)
	w.ue(syntax.SliceTypeP)
	w.ue(0)
	w.uint(number, 4)
	w.uint(poc, 8)
	w.bit(0) // default one active reference
	w.bit(0) // no reference list modification
	w.bit(0) // sliding reference marking
	w.ue(0)  // QP delta
	w.ue(1)  // filter off
	w.ue(1)  // mb_skip_run
	w.bit(1)
	w.align()
	return nal.Unit{Type: nal.TypeSliceNonIDR, RefIDC: 1, Payload: w.bytes()}
}

func reorderedStreamInput() []byte {
	return append(outputOrderParameters(), assemblyInput(
		outputOrderSlice(0, 0, true, true, false, false, 81),
		outputOrderSlice(1, 4, true, false, false, false, 149),
		outputOrderSlice(2, 2, false, false, false, false, 113),
	)...)
}

func TestStreamOutputOrderAndDecodeOrder(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		var pocs []int
		var samples []byte
		s, err := NewStreamDecoder(StreamConfig{OutputOrder: ordered}, func(f *frame.Frame) error {
			pocs = append(pocs, f.FullPOC)
			samples = append(samples, f.Y[0])
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range reorderedStreamInput() {
			if err := s.Push([]byte{b}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Drain(); err != nil {
			t.Fatal(err)
		}
		wantPOC, wantSamples := []int{0, 4, 2}, []byte{81, 149, 113}
		if ordered {
			wantPOC, wantSamples = []int{0, 2, 4}, []byte{81, 113, 149}
		}
		if !reflect.DeepEqual(pocs, wantPOC) || !bytes.Equal(samples, wantSamples) {
			t.Fatalf("ordered=%v: POCs %v, samples %v", ordered, pocs, samples)
		}
		if err := s.Drain(); err != nil || len(pocs) != 3 {
			t.Fatalf("repeat drain: %v", err)
		}
		if s.WaitingForIDR() != ordered {
			t.Fatal("drain did not preserve mode-specific continuity")
		}
		if ordered {
			if err := s.Push(assemblyInput(outputOrderSlice(2, 6, true, false, false, false, 120))); err != nil {
				t.Fatal(err)
			}
			if err := s.Drain(); !errors.Is(err, ErrWaitingForIDR) {
				t.Fatalf("post-drain continuation: %v", err)
			}
			pushAndDrain(t, s, assemblyInput(outputOrderSlice(0, 0, true, true, false, false, 121)))
			if len(pocs) != 4 {
				t.Fatal("post-drain IDR failed")
			}
		}
	}
}

func TestStreamAccessUnitZeroReorderRetainsReferences(t *testing.T) {
	var outputs []*frame.Frame
	s, err := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(f *frame.Frame) error {
		outputs = append(outputs, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecodeAccessUnit(outputOrderParameters(0), 99); err != nil {
		t.Fatal(err)
	}
	for i, u := range []nal.Unit{
		outputOrderSlice(0, 0, true, true, false, false, 81),
		outputOrderSkipSlice(1, 2),
		outputOrderSlice(2, 4, true, false, false, false, 149),
		outputOrderSkipSlice(3, 6),
	} {
		tag := uint64(i + 1)
		if err := s.DecodeAccessUnit(assemblyInput(u), tag); err != nil {
			t.Fatal(err)
		}
		if len(outputs) != i+1 {
			t.Fatalf("picture %d: delivered %d pictures before drain, want %d", i, len(outputs), i+1)
		}
		got, sample := outputs[i], []byte{81, 81, 149, 149}[i]
		if got.Tag != tag || got.FullPOC != i*2 || got.Y[0] != sample || got.U[0] != sample || got.V[0] != sample {
			t.Fatalf("picture %d: tag/POC/YUV = %d/%d/%d,%d,%d, want %d/%d/%d,%d,%d",
				i, got.Tag, got.FullPOC, got.Y[0], got.U[0], got.V[0], tag, i*2, sample, sample, sample)
		}
		// Delivery must not evict the prediction reference or expose its pixels:
		// the next skipped picture still copies the original Y, U and V values.
		got.Y[0], got.U[0], got.V[0] = 0, 0, 0
	}
}

func TestStreamAccessUnitTagsFollowOutputOrder(t *testing.T) {
	type picture struct {
		tag    uint64
		poc    int
		sample byte
	}
	for _, tc := range []struct {
		name    string
		reorder []uint32
		discard bool
	}{
		{"inferred bound/IDR flush", nil, false},
		{"inferred bound/IDR discard", nil, true},
		{"one reordered picture/IDR flush", []uint32{1}, false},
		{"one reordered picture/IDR discard", []uint32{1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var outputs []picture
			s, err := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(f *frame.Frame) error {
				outputs = append(outputs, picture{f.Tag, f.FullPOC, f.Y[0]})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DecodeAccessUnit(outputOrderParameters(tc.reorder...), 99); err != nil {
				t.Fatal(err)
			}
			// Decode A, B, C, but display A, C, B. Finishing each complete
			// picture must not drain the queue or terminate prediction.
			a, b, c := picture{0xa, 0, 81}, picture{0xb, 4, 149}, picture{0xc, 2, 113}
			var want []picture
			for i, p := range []picture{a, b, c} {
				u := outputOrderSlice(uint32(i), uint32(p.poc), i != 2, i == 0, false, false, p.sample)
				if err := s.DecodeAccessUnit(assemblyInput(u), p.tag); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					// A is waiting for output. A filler-only call must neither
					// release A nor replace A's tag with the filler's token.
					filler := nal.Unit{Type: nal.TypeFiller, Payload: []byte{0xff, 0x80}}
					if err := s.DecodeAccessUnit(assemblyInput(filler), 0); err != nil {
						t.Fatal(err)
					}
				}
				if len(tc.reorder) != 0 && i > 0 {
					// Depth one releases A when B arrives, then C when C arrives;
					// B is still waiting despite all three input calls completing.
					want = append(want, []picture{a, c}[i-1])
				}
				if !reflect.DeepEqual(outputs, want) {
					t.Fatalf("picture %d or filler: output = %v, want %v", i, outputs, want)
				}
			}
			// This call carries D's tag, but an IDR without the discard flag
			// first releases the older pictures with their own tags and pixels.
			d := picture{0xd, 0, 200}
			if err := s.DecodeAccessUnit(assemblyInput(outputOrderSlice(0, 0, true, true, tc.discard, false, d.sample)), d.tag); err != nil {
				t.Fatal(err)
			}
			if !tc.discard {
				want = []picture{a, c, b}
			}
			if !reflect.DeepEqual(outputs, want) {
				t.Fatalf("IDR output = %v, want %v", outputs, want)
			}
			if err := s.Drain(); err != nil {
				t.Fatal(err)
			}
			want = append(want, d)
			if !reflect.DeepEqual(outputs, want) || !s.WaitingForIDR() {
				t.Fatalf("final drain = %v, waiting for IDR %v; want %v and ended sequence", outputs, s.WaitingForIDR(), want)
			}
		})
	}
}

func TestStreamOutputOrderResets(t *testing.T) {
	for _, tc := range []struct {
		name           string
		discard, mmco5 bool
		want           []byte
	}{
		{"IDR flush", false, false, []byte{81, 113, 149, 200}},
		{"IDR discard", true, false, []byte{200}},
		{"MMCO5 flush", false, true, []byte{81, 113, 149, 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var samples []byte
			s, _ := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(f *frame.Frame) error { samples = append(samples, f.Y[0]); return nil })
			last := outputOrderSlice(0, 0, true, true, tc.discard, false, 200)
			if tc.mmco5 {
				last = outputOrderSlice(2, 6, true, false, false, true, 200)
			}
			pushAndDrain(t, s, append(reorderedStreamInput(), assemblyInput(last)...))
			if !bytes.Equal(samples, tc.want) {
				t.Fatalf("samples %v, want %v", samples, tc.want)
			}
		})
	}
}

func TestStreamOutputOrderEndAndLoss(t *testing.T) {
	for _, end := range []uint8{nal.TypeEndSeq, nal.TypeEndStream} {
		var pocs []int
		s, _ := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(f *frame.Frame) error { pocs = append(pocs, f.FullPOC); return nil })
		input := append(reorderedStreamInput(), assemblyInput(nal.Unit{Type: end, Payload: []byte{0x80}}, nal.Unit{Type: nal.TypeAUD, Payload: []byte{0x10}})...)
		if err := s.Push(input); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pocs, []int{0, 2, 4}) || !s.WaitingForIDR() {
			t.Fatalf("end=%d: %v", end, pocs)
		}
		if err := s.Drain(); err != nil || len(pocs) != 3 {
			t.Fatal("end marker repeated output")
		}
	}
	count := 0
	s, _ := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(*frame.Frame) error { count++; return nil })
	if err := s.Push(append(reorderedStreamInput(), assemblyInput(nal.Unit{Type: nal.TypeAUD, Payload: []byte{0x10}})...)); err != nil {
		t.Fatal(err)
	}
	s.Discontinuity()
	if err := s.Drain(); err != nil || count != 0 || len(s.order.pending) != 0 {
		t.Fatal("loss output stale queued pictures")
	}
}

func TestStreamOutputOrderBoundedAndCallbackFailure(t *testing.T) {
	count := 0
	stop := errors.New("output stopped")
	s, _ := NewStreamDecoder(StreamConfig{OutputOrder: true}, func(f *frame.Frame) error {
		count++
		if count == 20 {
			return stop
		}
		// Owned output remains safe even when this picture is still a reference.
		f.Y[0], f.FrameNum = 0, 99
		return nil
	})
	if err := s.Push(outputOrderParameters()); err != nil {
		t.Fatal(err)
	}
	var got error
	for n := 0; n < 50; n++ {
		got = s.Push(assemblyInput(outputOrderSlice(uint32(n%16), uint32(n*2), true, n == 0, false, false, 91), nal.Unit{Type: nal.TypeAUD, Payload: []byte{0x10}}))
		if got != nil {
			break
		}
		if len(s.order.pending) > 16 || s.order.fullness(s.d.DPB.Frames) > 16 || len(s.d.Frames) != 0 {
			t.Fatal("unbounded retained pictures")
		}
	}
	if !errors.Is(got, stop) || !s.WaitingForIDR() || len(s.order.pending) != 0 || len(s.d.DPB.Frames) != 0 {
		t.Fatalf("callback failure: count=%d, err=%v", count, got)
	}
}

func TestStreamSnapshotsDeblockingSettingUntilReset(t *testing.T) {
	t.Setenv("GO264_DISABLE_DEBLOCK", "1")
	var samples []byte
	s := assemblyStream(t, 2, 1, func(f *frame.Frame) error {
		samples = append(samples, f.PixelY(14, 0))
		return nil
	})
	// The P slice from TestCAVLCPSkipPreservesQPForDeblocking uses QP 36,
	// a one-pixel motion vector in MB 0 and a skip in MB 1. With this PCM
	// reference, filtering changes the sample at x=14 from 100 to 102.
	input := assemblyInput(pcmAssemblySlice(0, 100, 104), nal.Unit{
		Type: nal.TypeSliceNonIDR, RefIDC: 1,
		Payload: []byte{0xe2, 0x02, 0x9f, 0x11, 0xa8},
	})
	// Changing the environment after construction must not change the
	// stream's configuration between input chunks or pictures.
	t.Setenv("GO264_DISABLE_DEBLOCK", "")
	pushAndDrain(t, s, input)
	if len(samples) != 2 || samples[1] != 100 {
		t.Fatalf("stream did not retain disabled deblocking: samples=%v", samples)
	}
	// Discontinuity resets the decoder while keeping SPS/PPS. The new
	// sequence must pick up the now-enabled filter and produce filtered pixels.
	s.Discontinuity()
	pushAndDrain(t, s, input)
	if len(samples) != 4 || samples[3] != 102 {
		t.Fatalf("reset did not refresh deblocking configuration: samples=%v", samples)
	}
}

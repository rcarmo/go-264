package decode

import (
	"bytes"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
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

package wav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// All fixtures in this file are generated synthetically from literal PCM sample
// values. No external recordings or third-party assets are used.

type chunkSpec struct {
	id           string
	payload      []byte
	declaredSize *uint32
	addPad       *bool
}

func TestOpenReadBitDepths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		bits       int
		channels   int
		sampleRate int
		samples    []int32
		withJunk   bool
		fmtExtra   []byte
		want       []float64
	}{
		{
			name:       "pcm8_mono",
			bits:       8,
			channels:   1,
			sampleRate: 8000,
			samples:    []int32{0, 64, 128},
			withJunk:   true,
			want:       []float64{-1, -0.5, 0},
		},
		{
			name:       "pcm16_stereo_fmt20",
			bits:       16,
			channels:   2,
			sampleRate: 44100,
			samples:    []int32{-32768, 32767, -16384, 16384, 0, 0},
			fmtExtra:   []byte{0, 0},
			want:       []float64{-1, 32767.0 / 32768, -0.5, 0.5, 0, 0},
		},
		{
			name:       "pcm24_mono",
			bits:       24,
			channels:   1,
			sampleRate: 48000,
			samples:    []int32{-8388608, -4194304, 4194304},
			want:       []float64{-1, -0.5, 0.5},
		},
		{
			name:       "pcm32_stereo",
			bits:       32,
			channels:   2,
			sampleRate: 192000,
			samples:    []int32{-2147483648, 2147483647, -1073741824, 1073741824},
			want:       []float64{-1, 2147483647.0 / 2147483648, -0.5, 0.5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := makePCMFixture(1, tc.channels, tc.sampleRate, tc.bits, tc.samples, tc.fmtExtra, tc.withJunk)
			r, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), pcm.Limits{})
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			info := r.Info()
			if info.SampleRate != tc.sampleRate || info.Channels != tc.channels || info.BitsPerSample != tc.bits {
				t.Fatalf("Info() = %+v", info)
			}
			if info.Frames != int64(len(tc.samples)/tc.channels) {
				t.Fatalf("Info().Frames = %d, want %d", info.Frames, len(tc.samples)/tc.channels)
			}

			var got []float64
			buf := make([]float64, tc.channels)
			for {
				n, err := r.ReadFrames(context.Background(), buf)
				if n > 0 {
					got = append(got, buf[:n*tc.channels]...)
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("ReadFrames() error = %v", err)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("decoded samples = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("sample[%d] = %.12f, want %.12f", i, got[i], tc.want[i])
				}
			}

			if n, err := r.ReadFrames(context.Background(), buf); n != 0 || err != io.EOF {
				t.Fatalf("post-EOF ReadFrames() = (%d, %v), want (0, EOF)", n, err)
			}
		})
	}
}

func TestSeekFrameAndEOF(t *testing.T) {
	t.Parallel()

	data := makePCMFixture(1, 2, 48000, 24, []int32{-8388608, 8388607, -4194304, 4194304, 0, 0}, nil, false)
	r, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), pcm.Limits{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := r.SeekFrame(context.Background(), 1); err != nil {
		t.Fatalf("SeekFrame() error = %v", err)
	}
	buf := make([]float64, 6)
	n, err := r.ReadFrames(context.Background(), buf)
	if n != 2 || err != io.EOF {
		t.Fatalf("ReadFrames() = (%d, %v), want (2, EOF)", n, err)
	}
	want := []float64{-0.5, 0.5, 0, 0}
	for i := range want {
		if buf[i] != want[i] {
			t.Fatalf("seeked sample[%d] = %.12f, want %.12f", i, buf[i], want[i])
		}
	}
	if err := r.SeekFrame(context.Background(), r.Info().Frames); err != nil {
		t.Fatalf("SeekFrame(end) error = %v", err)
	}
	if n, err := r.ReadFrames(context.Background(), buf[:2]); n != 0 || err != io.EOF {
		t.Fatalf("ReadFrames(end) = (%d, %v), want (0, EOF)", n, err)
	}
	if err := r.SeekFrame(context.Background(), -1); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("SeekFrame(-1) error = %v, want ErrMalformed", err)
	}
	if err := r.SeekFrame(context.Background(), r.Info().Frames+1); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("SeekFrame(too far) error = %v, want ErrMalformed", err)
	}
	if n, err := r.ReadFrames(context.Background(), make([]float64, 3)); n != 0 || !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("ReadFrames(odd dst) = (%d, %v), want ErrMalformed", n, err)
	}
	if n, err := r.ReadFrames(context.Background(), nil); n != 0 || err != nil {
		t.Fatalf("ReadFrames(nil) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestShortReaderAtAndPartialReads(t *testing.T) {
	t.Parallel()

	data := makePCMFixture(1, 2, 44100, 16, []int32{-32768, 32767, -16384, 16384}, nil, false)
	src := &chunkedReaderAt{data: data, max: 3}
	r, err := Open(context.Background(), src, int64(len(data)), pcm.Limits{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	buf := make([]float64, 4)
	n, err := r.ReadFrames(context.Background(), buf)
	if n != 2 || err != nil {
		t.Fatalf("ReadFrames() = (%d, %v), want (2, nil)", n, err)
	}
	want := []float64{-1, 32767.0 / 32768, -0.5, 0.5}
	for i := range want {
		if buf[i] != want[i] {
			t.Fatalf("sample[%d] = %.12f, want %.12f", i, buf[i], want[i])
		}
	}
}

func TestReadS16FramesDirectContract(t *testing.T) {
	t.Parallel()
	samples := make([]int32, 5000*2)
	want := make([]int16, len(samples))
	for i := range samples {
		samples[i] = int32((i*7919)%65536 - 32768)
		want[i] = int16(samples[i])
	}
	data := makePCMFixture(1, 2, 48000, 16, samples, nil, false)
	r, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), pcm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int16, len(want)+3)
	for i := range got {
		got[i] = 1234
	}
	n, err := r.ReadS16Frames(context.Background(), got[:len(want)])
	if n != len(want)/2 || err != nil || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("direct read=(%d,%v), data mismatch=%t", n, err, !slices.Equal(got[:len(want)], want))
	}
	if !slices.Equal(got[len(want):], []int16{1234, 1234, 1234}) {
		t.Fatal("destination tail changed")
	}
	if n, err = r.ReadS16Frames(context.Background(), got[:2]); n != 0 || err != io.EOF {
		t.Fatalf("direct EOF=(%d,%v)", n, err)
	}
	if err = r.SeekFrame(context.Background(), 4999); err != nil {
		t.Fatal(err)
	}
	got[0], got[1], got[2] = 0, 0, 1234
	if n, err = r.ReadS16Frames(context.Background(), got[:2]); n != 1 || err != nil || got[0] != want[len(want)-2] || got[1] != want[len(want)-1] || got[2] != 1234 {
		t.Fatalf("direct seek=(%d,%v) samples=%v", n, err, got[:3])
	}
	if n, err = r.ReadS16Frames(context.Background(), got[:4]); n != 0 || err != io.EOF {
		t.Fatalf("direct post-seek EOF=(%d,%v)", n, err)
	}

	non16 := makePCMFixture(1, 1, 48000, 24, []int32{1}, nil, false)
	r24, err := Open(context.Background(), bytes.NewReader(non16), int64(len(non16)), pcm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if n, err = r24.ReadS16Frames(context.Background(), got[:1]); n != 0 || !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatalf("24-bit direct=(%d,%v)", n, err)
	}
}

func TestReadS16FramesCancellationPreservesPartialOutput(t *testing.T) {
	samples := make([]int32, 5000*2)
	for i := range samples {
		samples[i] = int32(i%65536 - 32768)
	}
	data := makePCMFixture(1, 2, 48000, 16, samples, nil, false)
	ctx, cancel := context.WithCancel(context.Background())
	src := &chunkedReaderAt{data: data}
	r, err := Open(context.Background(), src, int64(len(data)), pcm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	src.calls = 0
	src.cancel, src.cancelAfter = cancel, 1
	dst := make([]int16, len(samples))
	n, err := r.ReadS16Frames(ctx, dst)
	if n <= 0 || n >= len(samples)/2 || !errors.Is(err, context.Canceled) {
		t.Fatalf("direct canceled=(%d,%v)", n, err)
	}
	for i := 0; i < n*2; i++ {
		if dst[i] != int16(samples[i]) {
			t.Fatalf("partial sample %d=%d want %d", i, dst[i], int16(samples[i]))
		}
	}
	fresh := make([]int16, len(samples)-n*2)
	m, err := r.ReadS16Frames(context.Background(), fresh)
	if m != len(fresh)/2 || err != nil {
		t.Fatalf("continued direct=(%d,%v)", m, err)
	}
	for i := range fresh {
		if fresh[i] != int16(samples[n*2+i]) {
			t.Fatalf("continued sample %d", i)
		}
	}
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	valid := makePCMFixture(1, 1, 8000, 8, []int32{0, 128}, nil, false)
	ctxOpen, cancelOpen := context.WithCancel(context.Background())
	cancelOpen()
	if _, err := Open(ctxOpen, bytes.NewReader(valid), int64(len(valid)), pcm.Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open(canceled) error = %v, want context.Canceled", err)
	}

	longSamples := make([]int32, 5000*2)
	for i := range longSamples {
		if i%2 == 0 {
			longSamples[i] = -32768
		} else {
			longSamples[i] = 32767
		}
	}
	longData := makePCMFixture(1, 2, 44100, 16, longSamples, nil, false)
	ctxRead, cancelRead := context.WithCancel(context.Background())
	src := &chunkedReaderAt{data: longData}
	r, err := Open(context.Background(), src, int64(len(longData)), pcm.Limits{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	src.calls = 0
	src.cancel = cancelRead
	src.cancelAfter = 1
	buf := make([]float64, len(longSamples))
	n, err := r.ReadFrames(ctxRead, buf)
	if n <= 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadFrames(canceled) = (%d, %v), want partial frames and context.Canceled", n, err)
	}

	ctxSeek, cancelSeek := context.WithCancel(context.Background())
	cancelSeek()
	if err := r.SeekFrame(ctxSeek, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("SeekFrame(canceled) error = %v, want context.Canceled", err)
	}
}

func TestOpenRejectsMalformedAndUnsupported(t *testing.T) {
	t.Parallel()

	fmt16 := makeFmtChunk(1, 1, 8000, 16, 2, 16000, nil)
	data1 := chunkSpec{id: "data", payload: []byte{0, 0}}
	base := makeRIFF([]chunkSpec{{id: "fmt ", payload: fmt16}, data1})

	badRiffSize := append([]byte(nil), base...)
	binary.LittleEndian.PutUint32(badRiffSize[4:8], binary.LittleEndian.Uint32(badRiffSize[4:8])-1)

	fmtMismatch := makeFmtChunk(1, 1, 8000, 16, 2, 16000, []byte{9, 9})
	binary.LittleEndian.PutUint16(fmtMismatch[16:18], 1)

	hugeChunk := makeRIFF([]chunkSpec{{id: "JUNK", payload: nil, declaredSize: uint32Ptr(0xFFFFFFF0)}})

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{
			name: "duplicate_fmt",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: fmt16}, {id: "fmt ", payload: fmt16}, data1}),
			want: pcm.ErrMalformed,
		},
		{
			name: "duplicate_data",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: fmt16}, data1, data1}),
			want: pcm.ErrMalformed,
		},
		{
			name: "data_before_fmt",
			data: makeRIFF([]chunkSpec{data1, {id: "fmt ", payload: fmt16}}),
			want: pcm.ErrMalformed,
		},
		{
			name: "float_tag",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(3, 1, 8000, 32, 4, 32000, nil)}, {id: "data", payload: []byte{0, 0, 0, 0}}}),
			want: pcm.ErrUnsupported,
		},
		{
			name: "extensible_tag",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(0xFFFE, 2, 48000, 16, 4, 192000, make([]byte, 22))}, {id: "data", payload: []byte{0, 0, 0, 0}}}),
			want: pcm.ErrUnsupported,
		},
		{
			name: "bad_channels",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(1, 3, 8000, 16, 6, 48000, nil)}, {id: "data", payload: []byte{0, 0, 0, 0, 0, 0}}}),
			want: pcm.ErrUnsupported,
		},
		{
			name: "bad_sample_rate",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(1, 1, 4000, 16, 2, 8000, nil)}, data1}),
			want: pcm.ErrUnsupported,
		},
		{
			name: "bad_bits",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(1, 1, 8000, 12, 1, 8000, nil)}, {id: "data", payload: []byte{0}}}),
			want: pcm.ErrUnsupported,
		},
		{
			name: "bad_block_align",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(1, 1, 8000, 16, 1, 8000, nil)}, data1}),
			want: pcm.ErrMalformed,
		},
		{
			name: "bad_byte_rate",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(1, 1, 8000, 16, 2, 1, nil)}, data1}),
			want: pcm.ErrMalformed,
		},
		{
			name: "data_not_aligned",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: fmt16}, {id: "data", payload: []byte{0, 0, 0}}}),
			want: pcm.ErrMalformed,
		},
		{
			name: "missing_pad_byte",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: makeFmtChunk(1, 1, 8000, 8, 1, 8000, nil)}, {id: "data", payload: []byte{0, 1, 2}, addPad: boolPtr(false)}}),
			want: pcm.ErrMalformed,
		},
		{
			name: "riff_size_mismatch",
			data: badRiffSize,
			want: pcm.ErrMalformed,
		},
		{
			name: "missing_fmt",
			data: makeRIFF([]chunkSpec{data1}),
			want: pcm.ErrMalformed,
		},
		{
			name: "missing_data",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: fmt16}}),
			want: pcm.ErrMalformed,
		},
		{
			name: "fmt_extension_mismatch",
			data: makeRIFF([]chunkSpec{{id: "fmt ", payload: fmtMismatch}, {id: "data", payload: []byte{0, 0}}}),
			want: pcm.ErrMalformed,
		},
		{
			name: "huge_declared_chunk",
			data: hugeChunk,
			want: pcm.ErrMalformed,
		},
		{
			name: "rf64_header",
			data: append([]byte("RF64\x04\x00\x00\x00WAVE"), []byte{}...),
			want: pcm.ErrUnsupported,
		},
		{
			name: "rifx_header",
			data: append([]byte("RIFX\x04\x00\x00\x00WAVE"), []byte{}...),
			want: pcm.ErrUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Open(context.Background(), bytes.NewReader(tc.data), int64(len(tc.data)), pcm.Limits{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Open() error = %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()

	data := makePCMFixture(1, 1, 8000, 8, []int32{0, 1, 2, 3}, nil, false)
	panicSrc := &panicReaderAt{}
	if _, err := Open(context.Background(), panicSrc, int64(len(data)), pcm.Limits{MaxBytes: int64(len(data)) - 1}); !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("Open(size limit) error = %v, want ErrLimit", err)
	}
	if panicSrc.called {
		t.Fatalf("Open(size limit) should not read source")
	}

	chunky := makePCMFixture(1, 1, 8000, 8, []int32{0}, nil, true)
	if _, err := Open(context.Background(), bytes.NewReader(chunky), int64(len(chunky)), pcm.Limits{MaxChunks: 2}); !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("Open(chunk limit) error = %v, want ErrLimit", err)
	}

	longSamples := make([]int32, 8001)
	long := makePCMFixture(1, 1, 8000, 8, longSamples, nil, false)
	if _, err := Open(context.Background(), bytes.NewReader(long), int64(len(long)), pcm.Limits{MaxDurationSeconds: 1}); !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("Open(duration limit) error = %v, want ErrLimit", err)
	}

	if _, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), pcm.Limits{MaxChunks: -1}); !errors.Is(err, pcm.ErrLimit) {
		t.Fatalf("Open(negative limit) error = %v, want ErrLimit", err)
	}
}

func FuzzOpenAndRead(f *testing.F) {
	f.Add(makePCMFixture(1, 1, 8000, 8, []int32{0, 128, 255}, nil, false))
	f.Add(makePCMFixture(1, 2, 44100, 16, []int32{-32768, 32767, 0, 0}, nil, false))
	f.Add(makePCMFixture(1, 1, 48000, 24, []int32{-8388608, 0, 8388607}, nil, false))
	f.Add(makePCMFixture(1, 2, 192000, 32, []int32{-2147483648, 2147483647}, nil, false))
	f.Add([]byte("RIFF\x04\x00\x00\x00WAVE"))

	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), pcm.Limits{})
		if err != nil {
			return
		}
		info := r.Info()
		if info.Channels != 1 && info.Channels != 2 {
			t.Fatalf("channels = %d", info.Channels)
		}
		if info.Frames < 0 {
			t.Fatalf("frames = %d", info.Frames)
		}

		buf := make([]float64, info.Channels*3)
		var total int64
		for {
			n, err := r.ReadFrames(context.Background(), buf)
			total += int64(n)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("ReadFrames() error = %v", err)
			}
		}
		if total != info.Frames {
			t.Fatalf("decoded frames = %d, want %d", total, info.Frames)
		}
		if err := r.SeekFrame(context.Background(), info.Frames); err != nil {
			t.Fatalf("SeekFrame(end) error = %v", err)
		}
		if n, err := r.ReadFrames(context.Background(), buf[:info.Channels]); n != 0 || err != io.EOF {
			t.Fatalf("ReadFrames(end) = (%d, %v), want (0, EOF)", n, err)
		}
	})
}

func makePCMFixture(tag uint16, channels, sampleRate, bits int, samples []int32, fmtExtra []byte, withJunk bool) []byte {
	blockAlign := uint16(channels * (bits / 8))
	byteRate := uint32(sampleRate) * uint32(blockAlign)
	chunks := make([]chunkSpec, 0, 3)
	if withJunk {
		chunks = append(chunks, chunkSpec{id: "JUNK", payload: []byte{1, 2, 3}})
	}
	chunks = append(chunks,
		chunkSpec{id: "fmt ", payload: makeFmtChunk(tag, channels, sampleRate, bits, blockAlign, byteRate, fmtExtra)},
		chunkSpec{id: "data", payload: encodePCM(bits, samples)},
	)
	return makeRIFF(chunks)
}

func makeFmtChunk(tag uint16, channels, sampleRate, bits int, blockAlign uint16, byteRate uint32, extra []byte) []byte {
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint16(buf[0:2], tag)
	binary.LittleEndian.PutUint16(buf[2:4], uint16(channels))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[8:12], byteRate)
	binary.LittleEndian.PutUint16(buf[12:14], blockAlign)
	binary.LittleEndian.PutUint16(buf[14:16], uint16(bits))
	if extra == nil {
		return buf
	}
	withExtra := make([]byte, 18+len(extra))
	copy(withExtra, buf)
	binary.LittleEndian.PutUint16(withExtra[16:18], uint16(len(extra)))
	copy(withExtra[18:], extra)
	return withExtra
}

func makeRIFF(chunks []chunkSpec) []byte {
	buf := make([]byte, 12)
	copy(buf[0:4], []byte("RIFF"))
	copy(buf[8:12], []byte("WAVE"))
	for _, chunk := range chunks {
		payload := chunk.payload
		declaredSize := uint32(len(payload))
		if chunk.declaredSize != nil {
			declaredSize = *chunk.declaredSize
		}
		buf = append(buf, []byte(chunk.id)...)
		var sizeBytes [4]byte
		binary.LittleEndian.PutUint32(sizeBytes[:], declaredSize)
		buf = append(buf, sizeBytes[:]...)
		buf = append(buf, payload...)
		addPad := true
		if chunk.addPad != nil {
			addPad = *chunk.addPad
		}
		if declaredSize&1 != 0 && addPad {
			buf = append(buf, 0)
		}
	}
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(buf)-8))
	return buf
}

func encodePCM(bits int, samples []int32) []byte {
	bytesPerSample := bits / 8
	buf := make([]byte, len(samples)*bytesPerSample)
	for i, sample := range samples {
		off := i * bytesPerSample
		switch bits {
		case 8:
			buf[off] = byte(sample)
		case 16:
			binary.LittleEndian.PutUint16(buf[off:off+2], uint16(int16(sample)))
		case 24:
			buf[off] = byte(sample)
			buf[off+1] = byte(sample >> 8)
			buf[off+2] = byte(sample >> 16)
		case 32:
			binary.LittleEndian.PutUint32(buf[off:off+4], uint32(int32(sample)))
		default:
			panic("unsupported bit depth in test fixture")
		}
	}
	return buf
}

type chunkedReaderAt struct {
	data        []byte
	max         int
	calls       int
	cancel      func()
	cancelAfter int
}

func (r *chunkedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, io.EOF
	}
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	if r.max > 0 && len(p) > r.max {
		p = p[:r.max]
	}
	n := copy(p, r.data[off:])
	r.calls++
	if r.cancel != nil && r.cancelAfter > 0 && r.calls == r.cancelAfter {
		r.cancel()
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

type panicReaderAt struct{ called bool }

func (r *panicReaderAt) ReadAt(_ []byte, _ int64) (int, error) {
	r.called = true
	return 0, errors.New("unexpected read")
}

func boolPtr(v bool) *bool       { return &v }
func uint32Ptr(v uint32) *uint32 { return &v }

func TestNormalisationIsFinite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		bits    int
		samples []int32
	}{
		{8, []int32{0, 255}},
		{16, []int32{math.MinInt16, math.MaxInt16}},
		{24, []int32{-8388608, 8388607}},
		{32, []int32{math.MinInt32, math.MaxInt32}},
	}
	for _, tc := range cases {
		buf := encodePCM(tc.bits, tc.samples)
		got := make([]float64, len(tc.samples))
		decodePCM(got, buf, tc.bits)
		for i, v := range got {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("bits=%d sample[%d]=%v", tc.bits, i, v)
			}
		}
	}
}

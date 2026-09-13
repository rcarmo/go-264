package audio_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/rcarmo/go-264/audio"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
	"os"
	"reflect"
	"testing"
)

// Synthetic PCM fixture authored for this test; no recording or third-party data.
func fixture(ch, rate int, samples []int16) []byte {
	b := make([]byte, 44+2*len(samples))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(ch))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*ch*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(ch*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(samples)*2))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}
func TestRoundtripAndOwnership(t *testing.T) {
	ctx := context.Background()
	x := []int16{-32768, -1, 0, 1, 32767}
	b := fixture(1, 16000, x)
	d, e := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{})
	if e != nil {
		t.Fatal(e)
	}
	if d.Metadata().Output.Frames != 5 {
		t.Fatal(d.Metadata())
	}
	dst := make([]int16, 9)
	n, span, e := d.ReadPCM(ctx, dst)
	if n != 5 || e != io.EOF || span.Frames != 5 || span.StartFrame != 0 || !reflect.DeepEqual(dst[:n], x) {
		t.Fatal(n, span, e, dst)
	}
	if e = d.Seek(ctx, 1); e != nil {
		t.Fatal(e)
	}
	n, span, e = d.ReadPCM(ctx, dst[:2])
	if n != 2 || e != nil || span.StartFrame != 1 || span.SourceStartFrame != 1 {
		t.Fatal(n, span, e)
	}
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	if _, _, e = d.ReadPCM(ctx, dst); !errors.Is(e, pcm.ErrClosed) {
		t.Fatal(e)
	}
	if e = d.Seek(ctx, 0); !errors.Is(e, pcm.ErrClosed) {
		t.Fatal(e)
	}
}
func TestDirectPCM16ExactLayoutChunkSeekAndOwnership(t *testing.T) {
	ctx := context.Background()
	samples := make([]int16, 5003*2)
	for i := range samples {
		samples[i] = int16((i*7919)%65536 - 32768)
	}
	data := fixture(2, 48000, samples)
	d, err := audio.Open(ctx, bytes.NewReader(data), int64(len(data)), audio.Options{TargetRate: 48000, TargetChannels: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Metadata().Output != d.Metadata().Source || d.Metadata().Output.BitsPerSample != 16 {
		t.Fatalf("metadata=%+v", d.Metadata())
	}
	got := make([]int16, len(samples)+4)
	for i := range got {
		got[i] = 1234
	}
	n, span, err := d.ReadPCM(ctx, got[:len(samples)])
	if n != len(samples) || err != nil || span.StartFrame != 0 || span.SourceStartFrame != 0 || span.Frames != 5003 || !reflect.DeepEqual(got[:n], samples) {
		t.Fatalf("direct read=(%d,%+v,%v)", n, span, err)
	}
	for _, v := range got[n:] {
		if v != 1234 {
			t.Fatal("caller tail changed")
		}
	}
	if err = d.Seek(ctx, 4095); err != nil {
		t.Fatal(err)
	}
	buf := make([]int16, 7*2)
	n, span, err = d.ReadPCM(ctx, buf)
	if n != len(buf) || err != nil || span.StartFrame != 4095 || span.SourceStartFrame != 4095 || span.Frames != 7 || !reflect.DeepEqual(buf, samples[4095*2:4102*2]) {
		t.Fatalf("direct seek=(%d,%+v,%v)", n, span, err)
	}
	// Caller source remains owned by the caller and readable after decoder close.
	src := bytes.NewReader(data)
	d2, err := audio.Open(ctx, src, int64(len(data)), audio.Options{TargetRate: 48000, TargetChannels: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err = d2.Close(); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err = src.ReadAt(b[:], 0); err != nil || b[0] != 'R' {
		t.Fatalf("caller source unavailable after close: %v", err)
	}
}

func TestPCM16DirectPathIsExactLayoutOnly(t *testing.T) {
	data := fixture(2, 48000, []int16{1000, -1000, 2000, 4000})
	for _, opts := range []audio.Options{
		{TargetRate: 16000, TargetChannels: 2},
		{TargetRate: 48000, TargetChannels: 1},
	} {
		d, err := audio.Open(context.Background(), bytes.NewReader(data), int64(len(data)), opts)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]int16, int(d.Metadata().Output.Frames)*opts.TargetChannels)
		n, _, err := d.ReadPCM(context.Background(), out)
		if n == 0 || err != nil {
			t.Fatalf("fallback read=(%d,%v)", n, err)
		}
		_ = d.Close()
	}
}

func TestMixing(t *testing.T) {
	b := fixture(2, 16000, []int16{1000, -1000, 2000, 4000})
	d, e := audio.Open(context.Background(), bytes.NewReader(b), int64(len(b)), audio.Options{})
	if e != nil {
		t.Fatal(e)
	}
	dst := make([]int16, 2)
	n, _, e := d.ReadPCM(context.Background(), dst)
	if n != 2 || e != nil || !reflect.DeepEqual(dst, []int16{0, 3000}) {
		t.Fatal(n, e, dst)
	}
}
func TestLimitsAndRejection(t *testing.T) {
	ctx := context.Background()
	b := fixture(1, 16000, []int16{1})
	if _, e := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{Limits: pcm.Limits{MaxBytes: 12}}); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
	if _, e := audio.Open(ctx, bytes.NewReader(make([]byte, 20)), 20, audio.Options{}); !errors.Is(e, pcm.ErrUnsupported) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestStreamAndPreciseReadContract(t *testing.T) {
	ctx := context.Background()
	samples := []int16{1, 2, 3, 4, 5, 6}
	data := fixture(2, 16000, samples)
	dir := t.TempDir()
	d, e := audio.OpenStream(ctx, bytes.NewBuffer(data), dir, audio.Options{TargetChannels: 2})
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	dst := []int16{9, 9, 9, 9, 9, 9, 9, 9}
	if n, span, e := d.ReadPCM(ctx, dst[:1]); n != 0 || !errors.Is(e, pcm.ErrMalformed) || span.StartFrame != 0 {
		t.Fatal(n, span, e)
	}
	n, span, e := d.ReadPCM(ctx, dst[:6])
	if n != 6 || e != nil || span.Frames != 3 {
		t.Fatal(n, span, e)
	}
	if n, _, e = d.ReadPCM(ctx, nil); n != 0 || e != nil {
		t.Fatal(n, e)
	}
	if n, _, e = d.ReadPCM(ctx, dst); n != 0 || e != io.EOF {
		t.Fatal(n, e)
	}
	if e = d.Seek(ctx, 2); e != nil {
		t.Fatal(e)
	}
	for i := range dst {
		dst[i] = 9
	}
	n, span, e = d.ReadPCM(ctx, dst)
	if n != 2 || span.StartFrame != 2 || span.Frames != 1 || e != io.EOF {
		t.Fatal(n, span, e)
	}
	for _, v := range dst[n:] {
		if v != 9 {
			t.Fatal("tail")
		}
	}
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 0 {
		t.Fatal(files, e)
	}
}
func TestRationalSpanSeek(t *testing.T) {
	ctx := context.Background()
	x := make([]int16, 4500)
	for i := range x {
		x[i] = int16(i * 31)
	}
	b := fixture(1, 44100, x)
	d, e := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	total := int(d.Metadata().Output.Frames)
	all := make([]int16, total)
	n, _, e := d.ReadPCM(ctx, all)
	if n != total || e != nil {
		t.Fatal(n, e)
	}
	for _, off := range []int64{1, 37, 1000, int64(total - 1), int64(total)} {
		if e = d.Seek(ctx, off); e != nil {
			t.Fatal(e)
		}
		buf := make([]int16, total-int(off))
		n, s, e := d.ReadPCM(ctx, buf)
		if n != len(buf) || e != nil || s.StartFrame != off || s.SourceStartFrame != off*44100/16000 || !reflect.DeepEqual(buf, all[off:]) {
			t.Fatal(n, s, e)
		}
	}
}

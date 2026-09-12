package mp4pcm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/rcarmo/go-264/audio/pcm"
	"io"
	"testing"
)

func box(kind string, p []byte) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b, uint32(8+len(p)))
	copy(b[4:], kind)
	return append(b, p...)
}
func u32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func join(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}
func table(kind string, n uint32, p []byte) []byte {
	return box(kind, join(make([]byte, 4), u32(n), p))
}
func descriptor(tag byte, p []byte) []byte { return append([]byte{tag, byte(len(p))}, p...) }
func zeroAU() []byte { // SCE/tag0, global_gain100, long max_sfb0, no tools, END.
	var out []byte
	pos := 0
	put := func(v uint32, n int) {
		for i := n - 1; i >= 0; i-- {
			if pos%8 == 0 {
				out = append(out, 0)
			}
			out[pos/8] |= byte(v>>i&1) << uint(7-pos%8)
			pos++
		}
	}
	put(0, 3)
	put(0, 4)
	put(100, 8)
	put(0, 1)
	put(0, 2)
	put(0, 1)
	put(0, 6)
	put(0, 1)
	put(0, 3)
	put(7, 3)
	return out
}
func fixture(leading bool) []byte {
	au := zeroAU()
	mdat := box("mdat", join(au, au))
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 48000)
	binary.BigEndian.PutUint32(mvhd[16:], 1500)
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[12:], 1)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], 48000)
	binary.BigEndian.PutUint32(mdhd[16:], 2048)
	hdlr := make([]byte, 24)
	copy(hdlr[8:], "soun")
	decoder := join([]byte{0x40, 0x15}, make([]byte, 11), descriptor(5, []byte{0x11, 0x88}))
	esds := box("esds", join(make([]byte, 4), descriptor(3, join([]byte{0, 1, 0}, descriptor(4, decoder), descriptor(6, []byte{2})))))
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[16:], 1)
	binary.BigEndian.PutUint16(entry[18:], 16)
	binary.BigEndian.PutUint32(entry[24:], 48000<<16)
	stsd := table("stsd", 1, box("mp4a", join(entry, esds)))
	stbl := box("stbl", join(stsd, table("stts", 1, join(u32(2), u32(1024))), table("stsc", 1, join(u32(1), u32(2), u32(1))), box("stsz", join(make([]byte, 4), u32(uint32(len(au))), u32(2))), table("stco", 1, u32(8))))
	dref := table("dref", 1, box("url ", []byte{0, 0, 0, 1}))
	minf := box("minf", join(box("dinf", dref), stbl))
	mediaEdit := join(u32(1000), u32(1024), []byte{0, 1, 0, 0})
	count := uint32(1)
	if leading {
		mediaEdit = join(u32(500), u32(0xffffffff), []byte{0, 1, 0, 0}, mediaEdit)
		count = 2
	}
	trak := box("trak", join(box("tkhd", tkhd), box("edts", table("elst", count, mediaEdit)), box("mdia", join(box("mdhd", mdhd), box("hdlr", hdlr), minf))))
	return join(mdat, box("moov", join(box("mvhd", mvhd), trak)))
}
func TestTrimLeadingSilenceSeek(t *testing.T) {
	ctx := context.Background()
	for _, leading := range []bool{false, true} {
		b := fixture(leading)
		r, e := Open(ctx, bytes.NewReader(b), int64(len(b)), pcm.Limits{})
		if e != nil {
			t.Fatal(e)
		}
		want := 1000
		if leading {
			want += 500
		}
		meta := r.Metadata()
		if meta.Output.Frames != int64(want) || meta.PrimingFrames != 1024 || meta.PaddingFrames != 24 {
			t.Fatal(meta)
		}
		dst := make([]float64, want+1)
		for i := range dst {
			dst[i] = 7
		}
		n, e := r.ReadFrames(ctx, dst)
		if n != want || e != io.EOF || dst[n] != 7 {
			t.Fatal(n, e)
		}
		for _, v := range dst[:n] {
			if v != 0 {
				t.Fatal(v)
			}
		}
		for _, off := range []int64{0, 499, 999, int64(want)} {
			if e = r.SeekFrame(ctx, off); e != nil {
				t.Fatal(e)
			}
			n, e = r.ReadFrames(ctx, dst[:1])
			if off == int64(want) {
				if n != 0 || e != io.EOF {
					t.Fatal(n, e)
				}
			} else if n != 1 || e != nil {
				t.Fatal(n, e)
			}
		}
	}
}
func TestCancelledReplayAndSourceTruncation(t *testing.T) {
	ctx := context.Background()
	b := fixture(false)
	r, e := Open(ctx, bytes.NewReader(b), int64(len(b)), pcm.Limits{})
	if e != nil {
		t.Fatal(e)
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	out := []float64{7}
	if n, e := r.ReadFrames(c, out); n != 0 || !errors.Is(e, context.Canceled) || out[0] != 7 {
		t.Fatal(n, e)
	}
	if n, e := r.ReadFrames(ctx, out); n != 1 || e != nil {
		t.Fatal(n, e)
	}
	if e = r.SeekFrame(ctx, 0); e != nil {
		t.Fatal(e)
	}
	if n, e := r.ReadFrames(ctx, out); n != 1 || e != nil {
		t.Fatal(n, e)
	}
}

func TestTruncatedPayloadIsNotEOF(t *testing.T) {
	ctx := context.Background()
	b := fixture(false)
	src := &mutableSource{data: b}
	r, e := Open(ctx, src, int64(len(b)), pcm.Limits{})
	if e != nil {
		t.Fatal(e)
	}
	src.cut = 8 + len(zeroAU()) + 1
	dst := []float64{9, 9}
	n, e := r.ReadFrames(ctx, dst)
	if n != 0 || !errors.Is(e, pcm.ErrMalformed) || errors.Is(e, io.EOF) || dst[0] != 9 {
		t.Fatal(n, e, dst)
	}
}

type mutableSource struct {
	data    []byte
	cut     int
	cancel  context.CancelFunc
	calls   int
	trigger int
}

func (s *mutableSource) ReadAt(b []byte, off int64) (int, error) {
	s.calls++
	if s.cancel != nil && s.calls == s.trigger {
		s.cancel()
	}
	data := s.data
	if s.cut > 0 {
		data = data[:s.cut]
	}
	return bytes.NewReader(data).ReadAt(b, off)
}
func TestMidReplayCancellationAndRetry(t *testing.T) {
	ctx := context.Background()
	b := fixture(false)
	src := &mutableSource{data: b}
	r, e := Open(ctx, src, int64(len(b)), pcm.Limits{})
	if e != nil {
		t.Fatal(e)
	}
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	src.cancel = cancel
	src.trigger = src.calls + 2
	out := []float64{9}
	n, e := r.ReadFrames(c, out)
	if n != 0 || !errors.Is(e, context.Canceled) || out[0] != 9 {
		t.Fatal(n, e, out)
	}
	src.cancel = nil
	if n, e = r.ReadFrames(ctx, out); n != 1 || e != nil || out[0] != 0 {
		t.Fatal(n, e, out)
	}
}
func TestFullyTrimmedTailStillValidated(t *testing.T) {
	ctx := context.Background()
	b := fixture(false)
	elst := bytes.Index(b, []byte("elst"))
	if elst < 0 {
		t.Fatal("fixture")
	}
	binary.BigEndian.PutUint32(b[elst+12:], 1000)
	binary.BigEndian.PutUint32(b[elst+16:], 0)
	b[8+len(zeroAU())] = 0xff
	r, e := Open(ctx, bytes.NewReader(b), int64(len(b)), pcm.Limits{})
	if e != nil {
		t.Fatal(e)
	}
	dst := make([]float64, 1000)
	n, e := r.ReadFrames(ctx, dst)
	if n != 1000 || e == nil || e == io.EOF {
		t.Fatal(n, e)
	}
}

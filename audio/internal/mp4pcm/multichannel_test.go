package mp4pcm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// aacBits is an MSB-first bit writer for hand-built AAC-LC access units.
type aacBits struct {
	out []byte
	pos int
}

func (w *aacBits) put(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.pos%8 == 0 {
			w.out = append(w.out, 0)
		}
		w.out[w.pos/8] |= byte(v>>i&1) << uint(7-w.pos%8)
		w.pos++
	}
}

// channelBody writes global_gain 100, long ics_info max_sfb 0 and no tools.
func (w *aacBits) channelBody(icsInfo bool) {
	w.put(100, 8)
	if icsInfo {
		w.put(0, 11)
	}
	w.put(0, 3)
}

// zeroMultichannelAU builds an all-zero raw_data_block from element ids
// (0 SCE, 1 CPE, 3 LFE) with per-type instance tags, terminated by END.
func zeroMultichannelAU(elements ...int) []byte {
	var w aacBits
	tags := map[int]uint32{}
	for _, id := range elements {
		w.put(uint32(id), 3)
		w.put(tags[id], 4)
		tags[id]++
		if id == 1 {
			w.put(1, 1)  // common_window
			w.put(0, 11) // shared ics_info, max_sfb 0
			w.put(0, 2)  // ms_mask_present
			w.channelBody(false)
			w.channelBody(false)
			continue
		}
		w.channelBody(true)
	}
	w.put(7, 3)
	return w.out
}

// aacFixture is a minimal self-contained MP4 with one mp4a track, no edit list
// and n identical access units.
func aacFixture(asc []byte, entryChannels uint16, au []byte, n int) []byte {
	var payload []byte
	for i := 0; i < n; i++ {
		payload = append(payload, au...)
	}
	mdat := box("mdat", payload)
	frames := uint32(n * 1024)
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 48000)
	binary.BigEndian.PutUint32(mvhd[16:], frames)
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[12:], 1)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], 48000)
	binary.BigEndian.PutUint32(mdhd[16:], frames)
	hdlr := make([]byte, 24)
	copy(hdlr[8:], "soun")
	decoder := join([]byte{0x40, 0x15}, make([]byte, 11), descriptor(5, asc))
	esds := box("esds", join(make([]byte, 4), descriptor(3, join([]byte{0, 1, 0}, descriptor(4, decoder), descriptor(6, []byte{2})))))
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[16:], entryChannels)
	binary.BigEndian.PutUint16(entry[18:], 16)
	binary.BigEndian.PutUint32(entry[24:], 48000<<16)
	stsd := table("stsd", 1, box("mp4a", join(entry, esds)))
	stbl := box("stbl", join(stsd, table("stts", 1, join(u32(uint32(n)), u32(1024))), table("stsc", 1, join(u32(1), u32(uint32(n)), u32(1))), box("stsz", join(make([]byte, 4), u32(uint32(len(au))), u32(uint32(n)))), table("stco", 1, u32(8))))
	dref := table("dref", 1, box("url ", []byte{0, 0, 0, 1}))
	minf := box("minf", join(box("dinf", dref), stbl))
	trak := box("trak", join(box("tkhd", tkhd), box("mdia", join(box("mdhd", mdhd), box("hdlr", hdlr), minf))))
	return join(mdat, box("moov", join(box("mvhd", mvhd), trak)))
}

func surround51Fixture() []byte {
	return aacFixture([]byte{0x11, 0xB0}, 2, zeroMultichannelAU(0, 1, 1, 3), 3)
}

func readAllZero(t *testing.T, r *Reader, channels int, frames int) {
	t.Helper()
	ctx := context.Background()
	dst := make([]float64, (frames+1)*channels)
	for i := range dst {
		dst[i] = 7
	}
	n, err := r.ReadFrames(ctx, dst)
	if n != frames || err != io.EOF {
		t.Fatal(n, err)
	}
	for i, v := range dst[:n*channels] {
		if v != 0 {
			t.Fatal(i, v)
		}
	}
	for _, v := range dst[n*channels:] {
		if v != 7 {
			t.Fatal("wrote past returned frames")
		}
	}
}

func TestMultichannelAACOutputLayouts(t *testing.T) {
	ctx := context.Background()
	b := surround51Fixture()
	for _, tc := range []struct{ output, want int }{{0, 6}, {6, 6}, {2, 2}, {1, 1}} {
		r, err := Open(ctx, bytes.NewReader(b), int64(len(b)), pcm.Limits{}, Options{OutputChannels: tc.output})
		if err != nil {
			t.Fatal(tc.output, err)
		}
		if info := r.Info(); info.Channels != tc.want || info.SampleRate != 48000 || info.Frames != 3*1024 {
			t.Fatal(tc.output, info)
		}
		if meta := r.Metadata(); meta.Source.Channels != 6 || meta.Output.Channels != tc.want || meta.Source.Frames != 3*1024 {
			t.Fatal(tc.output, meta)
		}
		readAllZero(t, r, tc.want, 3*1024)
		if err = r.SeekFrame(ctx, 1500); err != nil {
			t.Fatal(err)
		}
		one := make([]float64, tc.want)
		if n, err := r.ReadFrames(ctx, one); n != 1 || err != nil {
			t.Fatal(n, err)
		}
	}
}

func TestMultichannelAACOtherOutputCountsStayNative(t *testing.T) {
	// Only 1 and 2 select the AAC downmix; other counts leave the layout native
	// and audio/convert rejects the unsupported conversion.
	ctx := context.Background()
	b := surround51Fixture()
	for _, output := range []int{3, 4, 5, 8} {
		r, err := Open(ctx, bytes.NewReader(b), int64(len(b)), pcm.Limits{}, Options{OutputChannels: output})
		if err != nil {
			t.Fatal(output, err)
		}
		if r.Info().Channels != 6 {
			t.Fatal(output, r.Info())
		}
	}
}

func TestMultichannelAACRejectsSourceChannelSelection(t *testing.T) {
	b := surround51Fixture()
	_, err := Open(context.Background(), bytes.NewReader(b), int64(len(b)), pcm.Limits{}, Options{SourceChannels: []int{0}, OutputChannels: 1})
	if !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestMultichannelAACConfigurationsNative(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		asc      []byte
		elements []int
		channels int
	}{
		{[]byte{0x11, 0x98}, []int{0, 1}, 3},
		{[]byte{0x11, 0xB0}, []int{0, 1, 1, 3}, 6},
		{[]byte{0x11, 0xB8}, []int{0, 1, 1, 1, 3}, 8},
	} {
		b := aacFixture(tc.asc, 2, zeroMultichannelAU(tc.elements...), 2)
		r, err := Open(ctx, bytes.NewReader(b), int64(len(b)), pcm.Limits{})
		if err != nil {
			t.Fatal(tc.channels, err)
		}
		if r.Info().Channels != tc.channels || r.Metadata().Source.Channels != tc.channels {
			t.Fatal(tc.channels, r.Metadata())
		}
		readAllZero(t, r, tc.channels, 2*1024)
	}
}

func TestMultichannelAACElementMismatchIsMalformed(t *testing.T) {
	// A 5.1 configuration carrying a stereo-only access unit fails on read.
	b := aacFixture([]byte{0x11, 0xB0}, 2, zeroMultichannelAU(1), 1)
	r, err := Open(context.Background(), bytes.NewReader(b), int64(len(b)), pcm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]float64, 6)
	if n, err := r.ReadFrames(context.Background(), dst); n != 0 || !errors.Is(err, pcm.ErrMalformed) {
		t.Fatal(n, err)
	}
}

func TestStereoAACKeepsNativeLayoutForMonoRequest(t *testing.T) {
	b := aacFixture([]byte{0x11, 0x90}, 2, zeroMultichannelAU(1), 2)
	r, err := Open(context.Background(), bytes.NewReader(b), int64(len(b)), pcm.Limits{}, Options{OutputChannels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Info().Channels != 2 || r.Metadata().Source.Channels != 2 {
		t.Fatal(r.Metadata())
	}
	readAllZero(t, r, 2, 2*1024)
}

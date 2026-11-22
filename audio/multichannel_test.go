package audio_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/rcarmo/go-264/audio"
	"github.com/rcarmo/go-264/audio/pcm"
)

func mcBox(kind string, p []byte) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b, uint32(8+len(p)))
	copy(b[4:], kind)
	return append(b, p...)
}
func mcU32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func mcJoin(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}
func mcTable(kind string, n uint32, p []byte) []byte {
	return mcBox(kind, mcJoin(make([]byte, 4), mcU32(n), p))
}
func mcDescriptor(tag byte, p []byte) []byte { return append([]byte{tag, byte(len(p))}, p...) }

// mcZero51AU is an all-zero 5.1 raw_data_block: SCE, CPE, CPE, LFE, END, each
// channel with global_gain 100, long ics_info, max_sfb 0 and no tools.
func mcZero51AU() []byte {
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
	body := func(icsInfo bool) {
		put(100, 8)
		if icsInfo {
			put(0, 11)
		}
		put(0, 3)
	}
	put(0, 3)
	put(0, 4)
	body(true)
	for tag := uint32(0); tag < 2; tag++ {
		put(1, 3)
		put(tag, 4)
		put(1, 1)
		put(0, 11)
		put(0, 2)
		body(false)
		body(false)
	}
	put(3, 3)
	put(0, 4)
	body(true)
	put(7, 3)
	return out
}

// Synthetic 48 kHz 5.1 AAC-LC MP4 authored for this test: n silent access
// units, sample-entry channelcount 2 and ASC channel configuration 6.
func mcSurround51MP4(n int) []byte {
	au := mcZero51AU()
	var payload []byte
	for i := 0; i < n; i++ {
		payload = append(payload, au...)
	}
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
	decoder := mcJoin([]byte{0x40, 0x15}, make([]byte, 11), mcDescriptor(5, []byte{0x11, 0xB0}))
	esds := mcBox("esds", mcJoin(make([]byte, 4), mcDescriptor(3, mcJoin([]byte{0, 1, 0}, mcDescriptor(4, decoder), mcDescriptor(6, []byte{2})))))
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[16:], 2)
	binary.BigEndian.PutUint16(entry[18:], 16)
	binary.BigEndian.PutUint32(entry[24:], 48000<<16)
	stsd := mcTable("stsd", 1, mcBox("mp4a", mcJoin(entry, esds)))
	stbl := mcBox("stbl", mcJoin(stsd, mcTable("stts", 1, mcJoin(mcU32(uint32(n)), mcU32(1024))), mcTable("stsc", 1, mcJoin(mcU32(1), mcU32(uint32(n)), mcU32(1))), mcBox("stsz", mcJoin(make([]byte, 4), mcU32(uint32(len(au))), mcU32(uint32(n)))), mcTable("stco", 1, mcU32(8))))
	dref := mcTable("dref", 1, mcBox("url ", []byte{0, 0, 0, 1}))
	minf := mcBox("minf", mcJoin(mcBox("dinf", dref), stbl))
	trak := mcBox("trak", mcJoin(mcBox("tkhd", tkhd), mcBox("mdia", mcJoin(mcBox("mdhd", mdhd), mcBox("hdlr", hdlr), minf))))
	return mcJoin(mcBox("mdat", payload), mcBox("moov", mcJoin(mcBox("mvhd", mvhd), trak)))
}

func TestMultichannelAACPublicLayouts(t *testing.T) {
	ctx := context.Background()
	b := mcSurround51MP4(3)
	for _, tc := range []struct{ rate, channels, wantRate, wantChannels int }{
		{48000, 6, 48000, 6},
		{48000, 2, 48000, 2},
		{16000, 6, 16000, 6},
		{16000, 2, 16000, 2},
		{0, 0, 16000, 1},
	} {
		d, err := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{TargetRate: tc.rate, TargetChannels: tc.channels})
		if err != nil {
			t.Fatal(tc, err)
		}
		meta := d.Metadata()
		if meta.Output.Channels != tc.wantChannels || meta.Output.SampleRate != tc.wantRate || meta.Source.Channels != 6 || meta.Source.SampleRate != 48000 {
			t.Fatal(tc, meta)
		}
		samples := readAllPCM(t, d, 6*700)
		if int64(len(samples)) != meta.Output.Frames*int64(tc.wantChannels) || len(samples) == 0 {
			t.Fatal(tc, len(samples), meta.Output.Frames)
		}
		if tc.wantRate == 48000 && meta.Output.Frames != 3*1024 {
			t.Fatal(tc, meta.Output.Frames)
		}
		for i, v := range samples {
			if v != 0 {
				t.Fatal(tc, i, v)
			}
		}
		if err = d.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMultichannelAACUnsupportedTargets(t *testing.T) {
	ctx := context.Background()
	b := mcSurround51MP4(1)
	for _, channels := range []int{3, 4, 5, 7, 8, 9, pcm.MaxChannels + 1} {
		if _, err := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{TargetRate: 48000, TargetChannels: channels}); !errors.Is(err, pcm.ErrUnsupported) {
			t.Fatal(channels, err)
		}
	}
	if _, err := audio.Open(ctx, bytes.NewReader(b), int64(len(b)), audio.Options{TargetRate: 48000, TargetChannels: 1, SourceChannels: []int{0}}); !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatal(err)
	}
	wav := fixture(1, 16000, []int16{0, 1, -1, 2})
	if _, err := audio.Open(ctx, bytes.NewReader(wav), int64(len(wav)), audio.Options{TargetChannels: 6}); !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestMultichannelAACProbeMetadata(t *testing.T) {
	b := mcSurround51MP4(2)
	meta, err := audio.ProbeMetadata(context.Background(), bytes.NewReader(b), int64(len(b)), pcm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Output.Channels != 6 || meta.Source.Channels != 6 || meta.Output.SampleRate != 48000 || meta.Output.Frames != 2*1024 {
		t.Fatal(meta)
	}
	info, err := audio.Probe(context.Background(), bytes.NewReader(b), int64(len(b)), pcm.Limits{})
	if err != nil || info.Channels != 6 {
		t.Fatal(info, err)
	}
}

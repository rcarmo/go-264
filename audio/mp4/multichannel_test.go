package mp4

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

// zeroAACBits is a minimal MSB-first bit writer for hand-built AAC packets.
type zeroAACBits struct {
	buf  []byte
	nbit int
}

func (w *zeroAACBits) put(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.nbit%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if v>>uint(i)&1 != 0 {
			w.buf[len(w.buf)-1] |= 0x80 >> uint(w.nbit%8)
		}
		w.nbit++
	}
}

// zeroICS writes global_gain, a long-window ics_info with max_sfb=0 when
// withInfo is set, and the pulse/tns/gain-control flags.
func (w *zeroAACBits) zeroICS(withInfo bool) {
	w.put(100, 8)
	if withInfo {
		w.zeroICSInfo()
	}
	w.put(0, 3)
}

func (w *zeroAACBits) zeroICSInfo() {
	w.put(0, 1) // reserved
	w.put(0, 2) // ONLY_LONG_SEQUENCE
	w.put(0, 1) // window_shape
	w.put(0, 6) // max_sfb
	w.put(0, 1) // predictor_data_present
}

// zeroAACPacket builds an all-zero raw_data_block for an ASC channel
// configuration in 3, 6 or 7.
func zeroAACPacket(t *testing.T, channelConfig int) []byte {
	t.Helper()
	var w zeroAACBits
	sce := func(id uint32, tag uint32) {
		w.put(id, 3)
		w.put(tag, 4)
		w.zeroICS(true)
	}
	cpe := func(tag uint32) {
		w.put(1, 3)
		w.put(tag, 4)
		w.put(1, 1) // common_window
		w.zeroICSInfo()
		w.put(0, 2) // ms_mask_present
		w.zeroICS(false)
		w.zeroICS(false)
	}
	switch channelConfig {
	case 3:
		sce(0, 0)
		cpe(0)
	case 6:
		sce(0, 0)
		cpe(0)
		cpe(1)
		sce(3, 0)
	case 7:
		sce(0, 0)
		cpe(0)
		cpe(1)
		cpe(2)
		sce(3, 0)
	default:
		t.Fatalf("no zero packet recipe for channel config %d", channelConfig)
	}
	w.put(7, 3) // END
	return w.buf
}

func multichannelFixture(t *testing.T, asc []byte, entryChannels, channelConfig int) audioFixture {
	t.Helper()
	packet := zeroAACPacket(t, channelConfig)
	return audioFixture{
		mediaTimescale:    48000,
		sampleRate:        48000,
		channels:          entryChannels,
		asc:               asc,
		samples:           [][]byte{packet, packet},
		chunkSamples:      []int{2},
		drefSelfContained: true,
	}
}

func TestOpenAcceptsMultichannelAACConfig(t *testing.T) {
	for _, tc := range []struct {
		name          string
		asc           []byte
		entryChannels int
		channelConfig int
		want          int
	}{
		{name: "5.1-template-channelcount", asc: []byte{0x11, 0xb0}, entryChannels: 2, channelConfig: 6, want: 6},
		{name: "5.1-exact-channelcount", asc: []byte{0x11, 0xb0}, entryChannels: 6, channelConfig: 6, want: 6},
		{name: "7.1-template-channelcount", asc: []byte{0x11, 0xb8}, entryChannels: 2, channelConfig: 7, want: 8},
		{name: "7.1-exact-channelcount", asc: []byte{0x11, 0xb8}, entryChannels: 8, channelConfig: 7, want: 8},
		{name: "3.0-template-channelcount", asc: []byte{0x11, 0x98}, entryChannels: 2, channelConfig: 3, want: 3},
		{name: "3.0-exact-channelcount", asc: []byte{0x11, 0x98}, entryChannels: 3, channelConfig: 3, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := openFixture(t, multichannelFixture(t, tc.asc, tc.entryChannels, tc.channelConfig), Limits{})
			track := r.Track()
			if !track.Accepted || track.Codec != "mp4a" || track.Channels != tc.want || track.SampleRate != 48000 {
				t.Fatalf("track = %+v", track)
			}
			if track.AACConfig.Channels != tc.want || track.AACConfig.ChannelConfig != tc.channelConfig || track.AACConfig.SampleRate != 48000 || track.AACConfig.FrameSamples != 1024 {
				t.Fatalf("aac config = %+v", track.AACConfig)
			}
			if !bytes.Equal(track.AudioSpecificConfig, tc.asc) {
				t.Fatalf("asc = %x want %x", track.AudioSpecificConfig, tc.asc)
			}
		})
	}
}

func TestParseMP4AMultichannelChannelCount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sampleRate    int
		entryChannels int
		asc           []byte
		want          int
		wantErr       error
	}{
		{name: "mono", sampleRate: 48000, entryChannels: 1, asc: []byte{0x11, 0x88}, want: 1},
		{name: "stereo", sampleRate: 44100, entryChannels: 2, asc: []byte{0x12, 0x10}, want: 2},
		{name: "5.1-template", sampleRate: 48000, entryChannels: 2, asc: []byte{0x11, 0xb0}, want: 6},
		{name: "5.1-mono-entry", sampleRate: 48000, entryChannels: 1, asc: []byte{0x11, 0xb0}, want: 6},
		{name: "7.1-exact", sampleRate: 48000, entryChannels: 8, asc: []byte{0x11, 0xb8}, want: 8},
		{name: "stereo-asc-6ch-entry", sampleRate: 44100, entryChannels: 6, asc: []byte{0x12, 0x10}, wantErr: pcm.ErrUnsupported},
		{name: "mono-asc-3ch-entry", sampleRate: 48000, entryChannels: 3, asc: []byte{0x11, 0x88}, wantErr: pcm.ErrUnsupported},
		{name: "5.1-zero-channelcount", sampleRate: 48000, entryChannels: 0, asc: []byte{0x11, 0xb0}, wantErr: pcm.ErrUnsupported},
		{name: "5.1-nine-channelcount", sampleRate: 48000, entryChannels: 9, asc: []byte{0x11, 0xb0}, wantErr: pcm.ErrUnsupported},
		{name: "pce-channel-config", sampleRate: 48000, entryChannels: 2, asc: []byte{0x11, 0x80}, wantErr: pcm.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desc, err := parseMP4A(makeMP4AEntry(tc.sampleRate, tc.entryChannels, 16, 1, tc.asc)[8:])
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("parseMP4A() error = %v want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !desc.accepted || desc.channels != tc.want || desc.aacConfig.Channels != tc.want || desc.sampleRate != tc.sampleRate {
				t.Fatalf("desc=%+v config=%+v", desc, desc.aacConfig)
			}
		})
	}
}

func TestOpenStereoAACWithMultichannelEntryStillUnsupported(t *testing.T) {
	spec := audioFixture{
		channels:          6,
		samples:           [][]byte{{0xaa, 0xbb}, {0xcc, 0xdd}},
		chunkSamples:      []int{2},
		drefSelfContained: true,
	}
	data := makeFile(spec)
	if _, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{}); !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatalf("Open() error = %v want %v", err, pcm.ErrUnsupported)
	}
	if _, err := OpenTrack(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{}, 0); !errors.Is(err, pcm.ErrUnsupported) {
		t.Fatalf("OpenTrack() error = %v want %v", err, pcm.ErrUnsupported)
	}

	// The unsupported entry is reported, not accepted, when another track wins.
	r, _ := openFixture(t, audioFixture{
		trackID:           2,
		samples:           [][]byte{{0xaa, 0xbb}, {0xcc, 0xdd}},
		chunkSamples:      []int{2},
		drefSelfContained: true,
	}, Limits{}, makeAudioTrack(spec, []int64{0}))
	tracks := r.Tracks()
	if len(tracks) != 2 || tracks[0].Accepted || !tracks[1].Accepted || tracks[1].Channels != 2 {
		t.Fatalf("tracks = %+v", tracks)
	}
	if track := r.Track(); track.Index != 1 || track.Channels != 2 {
		t.Fatalf("selected track = %+v", track)
	}
}

// TestOpenDefaultPrefersFormerlyAcceptedTracks pins the default selection:
// mono/stereo AAC-LC (then AC-3) still wins over a newly accepted multichannel
// AAC-LC trak, which remains reachable through OpenTrack.
func TestOpenDefaultPrefersFormerlyAcceptedTracks(t *testing.T) {
	surround := multichannelFixture(t, []byte{0x11, 0xb0}, 2, 6)
	surround.trackID = 1
	stereo := surround
	stereo.trackID = 2
	stereo.channels = 2
	stereo.asc = []byte{0x11, 0x90}

	// The leading 5.1 trak shares the stereo trak's mdat bytes. Its stco
	// width does not depend on the offset values, so the first build fixes
	// the moov size and the second patches the real payload offset in.
	sized := makeFile(stereo, makeAudioTrack(surround, []int64{0}))
	mdatPayloadStart := int64(len(sized) - len(samplePayload(stereo.samples)))
	data := makeFile(stereo, makeAudioTrack(surround, []int64{mdatPayloadStart}))
	if len(data) != len(sized) {
		t.Fatalf("file size changed: %d != %d", len(data), len(sized))
	}

	r, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if track := r.Track(); track.Index != 1 || track.ID != 2 || track.Channels != 2 {
		t.Fatalf("default track = %+v", track)
	}
	tracks := r.Tracks()
	if len(tracks) != 2 || tracks[0].Channels != 6 || tracks[1].Channels != 2 {
		t.Fatalf("tracks = %+v", tracks)
	}
	r, err = OpenTrack(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{}, 0)
	if err != nil {
		t.Fatalf("OpenTrack() error = %v", err)
	}
	if track := r.Track(); track.Index != 0 || track.ID != 1 || track.Channels != 6 || track.AACConfig.ChannelConfig != 6 {
		t.Fatalf("selected track = %+v", track)
	}
	buf := make([]byte, 64)
	n, info, err := r.ReadPacket(context.Background(), 0, buf)
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if info.TrackIndex != 0 || info.Offset != mdatPayloadStart || !bytes.Equal(buf[:n], surround.samples[0]) {
		t.Fatalf("packet = %x info=%+v want %x", buf[:n], info, surround.samples[0])
	}
}

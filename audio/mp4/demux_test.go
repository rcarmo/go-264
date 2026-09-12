package mp4

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/rcarmo/go-264/audio/pcm"
)

type timeRun struct {
	count uint32
	delta uint32
}

type cttsRun struct {
	count  uint32
	offset int32
}

type audioFixture struct {
	tailMoov             bool
	movieTimescale       uint32
	movieDuration        uint64
	trackID              uint32
	tkhdVersion          byte
	tkhdFlags            uint32
	mediaTimescale       uint32
	mediaDuration        uint64
	mdhdVersion          byte
	mdhdFlags            uint32
	editVersion          byte
	edits                []Edit
	sampleRate           int
	channels             int
	sampleSize           int
	asc                  []byte
	dataRefIndex         uint16
	drefKind             string
	drefSelfContained    bool
	useCo64              bool
	sizeTable            string
	samples              [][]byte
	chunkSamples         []int
	chunkOffsetsOverride []int64
	stts                 []timeRun
	cttsVersion          byte
	ctts                 []cttsRun
}

func be16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func be64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func join(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func fullBox(version byte, flags uint32, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	out[0] = version
	out[1] = byte(flags >> 16)
	out[2] = byte(flags >> 8)
	out[3] = byte(flags)
	copy(out[4:], payload)
	return out
}

func descriptor(tag byte, payload []byte) []byte {
	if len(payload) >= 0x80 {
		panic("descriptor payload too large for test helper")
	}
	return append([]byte{tag, byte(len(payload))}, payload...)
}

func esdsPayload(asc []byte) []byte {
	ascDesc := descriptor(0x05, asc)
	decCfg := descriptor(0x04, join(
		[]byte{0x40, 0x15, 0x00, 0x00, 0x00},
		[]byte{0x00, 0x00, 0x00, 0x00},
		[]byte{0x00, 0x00, 0x00, 0x00},
		ascDesc,
	))
	esDesc := descriptor(0x03, join(be16(1), []byte{0x00}, decCfg))
	return fullBox(0, 0, esDesc)
}

func testESDS(version, objectType byte, extra ...[]byte) []byte {
	parts := [][]byte{
		[]byte{objectType, 0x15, 0x00, 0x00, 0x00},
		[]byte{0x00, 0x00, 0x00, 0x00},
		[]byte{0x00, 0x00, 0x00, 0x00},
	}
	parts = append(parts, extra...)
	return fullBox(version, 0, descriptor(0x03, join(be16(1), []byte{0x00}, descriptor(0x04, join(parts...)))))
}

func testESDSWithOptionalESFields() []byte {
	return fullBox(0, 0, descriptor(0x03, join(
		be16(1),
		[]byte{0xe0, 0x00, 0x00, 0x02, 'o', 'k', 0x00, 0x00},
		descriptor(0x04, join(
			[]byte{0x40, 0x15, 0x00, 0x00, 0x00},
			[]byte{0x00, 0x00, 0x00, 0x00},
			[]byte{0x00, 0x00, 0x00, 0x00},
			descriptor(0x05, []byte{0x12, 0x10}),
		)),
	)))
}

func makeMP4AEntry(sampleRate, channels, sampleSize int, dataRefIndex uint16, asc []byte) []byte {
	base := make([]byte, 28)
	binary.BigEndian.PutUint16(base[6:8], dataRefIndex)
	binary.BigEndian.PutUint16(base[16:18], uint16(channels))
	binary.BigEndian.PutUint16(base[18:20], uint16(sampleSize))
	binary.BigEndian.PutUint32(base[24:28], uint32(sampleRate)<<16)
	return box("mp4a", join(base, box("esds", esdsPayload(asc), false)), false)
}

func makeMVHD(version byte, flags uint32, timescale uint32, duration uint64) []byte {
	switch version {
	case 0:
		body := make([]byte, 16)
		binary.BigEndian.PutUint32(body[8:12], timescale)
		binary.BigEndian.PutUint32(body[12:16], uint32(duration))
		return box("mvhd", fullBox(version, flags, body), false)
	case 1:
		body := make([]byte, 28)
		binary.BigEndian.PutUint32(body[16:20], timescale)
		binary.BigEndian.PutUint64(body[20:28], duration)
		return box("mvhd", fullBox(version, flags, body), false)
	default:
		panic("unsupported mvhd version in test helper")
	}
}

func makeTKHD(version byte, flags uint32, trackID uint32) []byte {
	switch version {
	case 0:
		body := make([]byte, 16)
		binary.BigEndian.PutUint32(body[8:12], trackID)
		return box("tkhd", fullBox(version, flags, body), false)
	case 1:
		body := make([]byte, 28)
		binary.BigEndian.PutUint32(body[16:20], trackID)
		return box("tkhd", fullBox(version, flags, body), false)
	default:
		panic("unsupported tkhd version in test helper")
	}
}

func makeMDHD(version byte, flags uint32, timescale uint32, duration uint64) []byte {
	switch version {
	case 0:
		body := make([]byte, 20)
		binary.BigEndian.PutUint32(body[8:12], timescale)
		binary.BigEndian.PutUint32(body[12:16], uint32(duration))
		return box("mdhd", fullBox(version, flags, body), false)
	case 1:
		body := make([]byte, 32)
		binary.BigEndian.PutUint32(body[16:20], timescale)
		binary.BigEndian.PutUint64(body[20:28], duration)
		return box("mdhd", fullBox(version, flags, body), false)
	default:
		panic("unsupported mdhd version in test helper")
	}
}

func makeHDLR(handler string) []byte {
	body := make([]byte, 12)
	copy(body[4:8], []byte(handler))
	return box("hdlr", fullBox(0, 0, body), false)
}

func makeELST(version byte, edits []Edit) []byte {
	if len(edits) == 0 {
		return nil
	}
	entries := make([]byte, 0, len(edits)*20)
	switch version {
	case 0:
		for _, e := range edits {
			ent := make([]byte, 12)
			binary.BigEndian.PutUint32(ent[0:4], uint32(e.SegmentDuration))
			binary.BigEndian.PutUint32(ent[4:8], uint32(int32(e.MediaTime)))
			binary.BigEndian.PutUint16(ent[8:10], uint16(e.MediaRateInteger))
			binary.BigEndian.PutUint16(ent[10:12], uint16(e.MediaRateFraction))
			entries = append(entries, ent...)
		}
	case 1:
		for _, e := range edits {
			ent := make([]byte, 20)
			binary.BigEndian.PutUint64(ent[0:8], e.SegmentDuration)
			binary.BigEndian.PutUint64(ent[8:16], uint64(e.MediaTime))
			binary.BigEndian.PutUint16(ent[16:18], uint16(e.MediaRateInteger))
			binary.BigEndian.PutUint16(ent[18:20], uint16(e.MediaRateFraction))
			entries = append(entries, ent...)
		}
	default:
		panic("unsupported elst version in test helper")
	}
	return box("edts", box("elst", fullBox(version, 0, join(be32(uint32(len(edits))), entries)), false), false)
}

func makeDREF(kind string, selfContained bool) []byte {
	if kind == "" {
		kind = "url "
	}
	flags := uint32(0)
	if selfContained {
		flags = 1
	}
	entry := box(kind, fullBox(0, flags, nil), false)
	return box("dref", fullBox(0, 0, join(be32(1), entry)), false)
}

func makeSTSD(entries ...[]byte) []byte {
	return box("stsd", fullBox(0, 0, join(be32(uint32(len(entries))), join(entries...))), false)
}

func makeSTTS(runs []timeRun) []byte {
	entries := make([]byte, 0, len(runs)*8)
	for _, r := range runs {
		entries = append(entries, be32(r.count)...)
		entries = append(entries, be32(r.delta)...)
	}
	return box("stts", fullBox(0, 0, join(be32(uint32(len(runs))), entries)), false)
}

func makeCTTS(version byte, runs []cttsRun) []byte {
	entries := make([]byte, 0, len(runs)*8)
	for _, r := range runs {
		entries = append(entries, be32(r.count)...)
		entries = append(entries, be32(uint32(r.offset))...)
	}
	return box("ctts", fullBox(version, 0, join(be32(uint32(len(runs))), entries)), false)
}

func makeSTSC(chunkSamples []int) []byte {
	if len(chunkSamples) == 0 {
		panic("empty chunk map in test helper")
	}
	var entries []stscEntry
	for i, n := range chunkSamples {
		if i == 0 || n != chunkSamples[i-1] {
			entries = append(entries, stscEntry{firstChunk: uint32(i + 1), samplesPerChunk: uint32(n), descIndex: 1})
		}
	}
	body := make([]byte, 0, len(entries)*12)
	for _, e := range entries {
		body = append(body, be32(e.firstChunk)...)
		body = append(body, be32(e.samplesPerChunk)...)
		body = append(body, be32(e.descIndex)...)
	}
	return box("stsc", fullBox(0, 0, join(be32(uint32(len(entries))), body)), false)
}

func sampleSizes(samples [][]byte) []uint32 {
	out := make([]uint32, len(samples))
	for i, s := range samples {
		out[i] = uint32(len(s))
	}
	return out
}

func makeSTSZ(mode string, sizes []uint32) []byte {
	switch mode {
	case "", "stsz-var":
		body := make([]byte, 0, 8+len(sizes)*4)
		body = append(body, be32(0)...)
		body = append(body, be32(uint32(len(sizes)))...)
		for _, sz := range sizes {
			body = append(body, be32(sz)...)
		}
		return box("stsz", fullBox(0, 0, body), false)
	case "stsz-fixed":
		fixed := sizes[0]
		for _, sz := range sizes {
			if sz != fixed {
				panic("fixed stsz requires equal sample sizes")
			}
		}
		body := join(be32(fixed), be32(uint32(len(sizes))))
		return box("stsz", fullBox(0, 0, body), false)
	default:
		panic("unsupported stsz mode")
	}
}

func makeSTZ2(fieldSize int, sizes []uint32) []byte {
	payload := make([]byte, 12)
	payload[7] = byte(fieldSize)
	binary.BigEndian.PutUint32(payload[8:12], uint32(len(sizes)))
	switch fieldSize {
	case 4:
		for i := 0; i < len(sizes); i += 2 {
			if sizes[i] > 0x0f {
				panic("stz2/4 size too large")
			}
			v := byte(sizes[i] << 4)
			if i+1 < len(sizes) {
				if sizes[i+1] > 0x0f {
					panic("stz2/4 size too large")
				}
				v |= byte(sizes[i+1])
			}
			payload = append(payload, v)
		}
	case 8:
		for _, sz := range sizes {
			if sz > math.MaxUint8 {
				panic("stz2/8 size too large")
			}
			payload = append(payload, byte(sz))
		}
	case 16:
		for _, sz := range sizes {
			if sz > math.MaxUint16 {
				panic("stz2/16 size too large")
			}
			payload = append(payload, be16(uint16(sz))...)
		}
	default:
		panic("unsupported stz2 field size")
	}
	return box("stz2", payload, false)
}

func makeChunkOffsets(useCo64 bool, offsets []int64) []byte {
	body := make([]byte, 0, 4+len(offsets)*8)
	body = append(body, be32(uint32(len(offsets)))...)
	if useCo64 {
		for _, off := range offsets {
			body = append(body, be64(uint64(off))...)
		}
		return box("co64", fullBox(0, 0, body), false)
	}
	for _, off := range offsets {
		body = append(body, be32(uint32(off))...)
	}
	return box("stco", fullBox(0, 0, body), false)
}

func samplePayload(samples [][]byte) []byte { return join(samples...) }

func chunkOffsetsFromSamples(mdatPayloadStart int64, chunkSamples []int, samples [][]byte) []int64 {
	offsets := make([]int64, len(chunkSamples))
	sampleIndex := 0
	off := mdatPayloadStart
	for i, n := range chunkSamples {
		offsets[i] = off
		for j := 0; j < n; j++ {
			off += int64(len(samples[sampleIndex]))
			sampleIndex++
		}
	}
	if sampleIndex != len(samples) {
		panic("chunk/sample count mismatch in test helper")
	}
	return offsets
}

func normalizeFixture(spec audioFixture) audioFixture {
	if spec.movieTimescale == 0 {
		spec.movieTimescale = 1000
	}
	if spec.movieDuration == 0 {
		spec.movieDuration = 9000
	}
	if spec.trackID == 0 {
		spec.trackID = 1
	}
	if spec.mediaTimescale == 0 {
		spec.mediaTimescale = 44100
	}
	if spec.sampleRate == 0 {
		spec.sampleRate = 44100
	}
	if spec.channels == 0 {
		spec.channels = 2
	}
	if spec.sampleSize == 0 {
		spec.sampleSize = 16
	}
	if spec.asc == nil {
		spec.asc = []byte{0x12, 0x10}
	}
	if spec.dataRefIndex == 0 {
		spec.dataRefIndex = 1
	}
	if spec.drefKind == "" {
		spec.drefKind = "url "
	}
	if len(spec.chunkSamples) == 0 {
		spec.chunkSamples = []int{len(spec.samples)}
	}
	if len(spec.stts) == 0 {
		spec.stts = []timeRun{{count: uint32(len(spec.samples)), delta: 1024}}
	}
	if spec.mediaDuration == 0 {
		for _, r := range spec.stts {
			spec.mediaDuration += uint64(r.count) * uint64(r.delta)
		}
	}
	return spec
}

func makeAudioTrack(spec audioFixture, offsets []int64) []byte {
	spec = normalizeFixture(spec)
	entry := makeMP4AEntry(spec.sampleRate, spec.channels, spec.sampleSize, spec.dataRefIndex, spec.asc)
	var sizeBox []byte
	switch spec.sizeTable {
	case "", "stsz-var", "stsz-fixed":
		sizeBox = makeSTSZ(spec.sizeTable, sampleSizes(spec.samples))
	case "stz2-4":
		sizeBox = makeSTZ2(4, sampleSizes(spec.samples))
	case "stz2-8":
		sizeBox = makeSTZ2(8, sampleSizes(spec.samples))
	case "stz2-16":
		sizeBox = makeSTZ2(16, sampleSizes(spec.samples))
	default:
		panic("unsupported size table in test helper")
	}
	stblChildren := [][]byte{
		makeSTSD(entry),
		makeSTTS(spec.stts),
		makeSTSC(spec.chunkSamples),
		sizeBox,
		makeChunkOffsets(spec.useCo64, offsets),
	}
	if len(spec.ctts) > 0 {
		stblChildren = append(stblChildren, makeCTTS(spec.cttsVersion, spec.ctts))
	}
	stbl := box("stbl", join(stblChildren...), false)
	minf := box("minf", join(box("dinf", makeDREF(spec.drefKind, spec.drefSelfContained), false), stbl), false)
	mdia := box("mdia", join(makeMDHD(spec.mdhdVersion, spec.mdhdFlags, spec.mediaTimescale, spec.mediaDuration), makeHDLR("soun"), minf), false)
	return box("trak", join(makeTKHD(spec.tkhdVersion, spec.tkhdFlags, spec.trackID), makeELST(spec.editVersion, spec.edits), mdia), false)
}

func makeSidecarTrack(trackID uint32, handler string) []byte {
	mdia := box("mdia", join(makeMDHD(0, 0, 1000, 1), makeHDLR(handler)), false)
	return box("trak", join(makeTKHD(0, 7, trackID), mdia), false)
}

func makeFile(spec audioFixture, extraTracks ...[]byte) []byte {
	spec = normalizeFixture(spec)
	payload := samplePayload(spec.samples)
	placeholder := makeAudioTrack(spec, make([]int64, len(spec.chunkSamples)))
	tracks := append(append([][]byte{}, extraTracks...), placeholder)
	moov := box("moov", join(append([][]byte{makeMVHD(spec.mdhdVersion, 0, spec.movieTimescale, spec.movieDuration)}, tracks...)...), false)
	mdat := box("mdat", payload, false)
	mdatPayloadStart := int64(8)
	if !spec.tailMoov {
		mdatPayloadStart = int64(len(moov) + 8)
	}
	offsets := spec.chunkOffsetsOverride
	if offsets == nil {
		offsets = chunkOffsetsFromSamples(mdatPayloadStart, spec.chunkSamples, spec.samples)
	}
	track := makeAudioTrack(spec, offsets)
	tracks[len(tracks)-1] = track
	finalMoov := box("moov", join(append([][]byte{makeMVHD(spec.mdhdVersion, 0, spec.movieTimescale, spec.movieDuration)}, tracks...)...), false)
	if len(finalMoov) != len(moov) {
		panic("moov size changed after offset patch")
	}
	if spec.tailMoov {
		return join(mdat, finalMoov)
	}
	return join(finalMoov, mdat)
}

func expectedPackets(spec audioFixture, file []byte) []PacketInfo {
	spec = normalizeFixture(spec)
	sizes := sampleSizes(spec.samples)
	mdatPayloadStart := int64(8)
	if !spec.tailMoov {
		moov := box("moov", join(makeMVHD(spec.mdhdVersion, 0, spec.movieTimescale, spec.movieDuration), makeAudioTrack(spec, make([]int64, len(spec.chunkSamples)))), false)
		mdatPayloadStart = int64(len(moov) + 8)
	}
	offsets := spec.chunkOffsetsOverride
	if offsets == nil {
		offsets = chunkOffsetsFromSamples(mdatPayloadStart, spec.chunkSamples, spec.samples)
	}
	out := make([]PacketInfo, 0, len(spec.samples))
	sampleIndex := 0
	decodeTime := uint64(0)
	ctts := make([]int64, len(spec.samples))
	k := 0
	for _, run := range spec.ctts {
		for j := uint32(0); j < run.count; j++ {
			ctts[k] = int64(run.offset)
			k++
		}
	}
	runIndex := 0
	runRemain := uint32(0)
	delta := uint32(0)
	for _, chunkCount := range spec.chunkSamples {
		off := offsets[runIndex]
		runIndex++
		for j := 0; j < chunkCount; j++ {
			if runRemain == 0 {
				delta = spec.stts[0].delta
				consumed := sampleIndex
				acc := 0
				for _, r := range spec.stts {
					if consumed < acc+int(r.count) {
						delta = r.delta
						runRemain = uint32(acc + int(r.count) - consumed)
						break
					}
					acc += int(r.count)
				}
			}
			sz := sizes[sampleIndex]
			pts := int64(decodeTime)
			if len(ctts) > 0 {
				pts += ctts[sampleIndex]
			}
			out = append(out, PacketInfo{TrackIndex: 0, SampleIndex: sampleIndex, Offset: off, Size: int(sz), DecodeTime: decodeTime, Duration: delta, CompositionOffset: ctts[sampleIndex], PresentationTime: pts})
			off += int64(sz)
			decodeTime += uint64(delta)
			runRemain--
			sampleIndex++
		}
	}
	_ = file
	return out
}

func openFixture(t *testing.T, spec audioFixture, limits Limits, extraTracks ...[]byte) (*Reader, []byte) {
	t.Helper()
	data := makeFile(spec, extraTracks...)
	r, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), limits)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return r, data
}

func TestOpenSyntheticAudioTablesAndPackets(t *testing.T) {
	tests := []struct {
		name string
		spec audioFixture
	}{
		{
			name: "front-stsz-var-stco-v0",
			spec: audioFixture{
				movieTimescale:    1000,
				movieDuration:     9000,
				trackID:           11,
				tkhdVersion:       0,
				tkhdFlags:         7,
				mediaTimescale:    44100,
				mdhdVersion:       0,
				mdhdFlags:         1,
				editVersion:       0,
				edits:             []Edit{{SegmentDuration: 3000, MediaTime: 0, MediaRateInteger: 1}, {SegmentDuration: 6000, MediaTime: 1024, MediaRateInteger: 1}},
				sizeTable:         "stsz-var",
				samples:           [][]byte{{0x10, 0x11, 0x12}, {0x21, 0x22, 0x23, 0x24}, {0x30, 0x31}},
				chunkSamples:      []int{2, 1},
				stts:              []timeRun{{count: 1, delta: 1024}, {count: 2, delta: 2048}},
				drefSelfContained: true,
			},
		},
		{
			name: "tail-stsz-fixed-co64-v1-ctts0",
			spec: audioFixture{
				tailMoov:          true,
				movieTimescale:    48000,
				movieDuration:     12288,
				trackID:           22,
				tkhdVersion:       1,
				tkhdFlags:         7,
				mediaTimescale:    48000,
				mediaDuration:     3072,
				mdhdVersion:       1,
				mdhdFlags:         3,
				editVersion:       1,
				edits:             []Edit{{SegmentDuration: 12288, MediaTime: -1, MediaRateInteger: 1}},
				useCo64:           true,
				sizeTable:         "stsz-fixed",
				samples:           [][]byte{{0x40, 0x41, 0x42, 0x43}, {0x50, 0x51, 0x52, 0x53}, {0x60, 0x61, 0x62, 0x63}},
				chunkSamples:      []int{1, 2},
				stts:              []timeRun{{count: 3, delta: 1024}},
				cttsVersion:       0,
				ctts:              []cttsRun{{count: 1, offset: 10}, {count: 2, offset: 20}},
				drefSelfContained: true,
			},
		},
		{
			name: "front-stz2-4-ctts1",
			spec: audioFixture{
				trackID:           33,
				sizeTable:         "stz2-4",
				samples:           [][]byte{{0x01}, bytes.Repeat([]byte{0x02}, 10), bytes.Repeat([]byte{0x03}, 15)},
				chunkSamples:      []int{1, 2},
				stts:              []timeRun{{count: 3, delta: 10}},
				cttsVersion:       1,
				ctts:              []cttsRun{{count: 1, offset: 0}, {count: 1, offset: -2}, {count: 1, offset: 4}},
				drefSelfContained: true,
			},
		},
		{
			name: "front-stz2-8",
			spec: audioFixture{
				trackID:           44,
				sizeTable:         "stz2-8",
				samples:           [][]byte{bytes.Repeat([]byte{0x7a}, 17), bytes.Repeat([]byte{0x7b}, 31)},
				chunkSamples:      []int{2},
				stts:              []timeRun{{count: 2, delta: 512}},
				drefSelfContained: true,
			},
		},
		{
			name: "front-stz2-16",
			spec: audioFixture{
				trackID:           55,
				sizeTable:         "stz2-16",
				samples:           [][]byte{bytes.Repeat([]byte{0x33}, 257), bytes.Repeat([]byte{0x44}, 258)},
				chunkSamples:      []int{2},
				stts:              []timeRun{{count: 2, delta: 512}},
				drefSelfContained: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, data := openFixture(t, tt.spec, Limits{})
			track := r.Track()
			if !track.Accepted {
				t.Fatal("selected track not accepted")
			}
			if track.ID != normalizeFixture(tt.spec).trackID {
				t.Fatalf("track id = %d", track.ID)
			}
			if track.Codec != "mp4a" {
				t.Fatalf("codec = %q", track.Codec)
			}
			if track.Channels != 2 || track.SampleRate != 44100 || track.SampleSize != 16 {
				t.Fatalf("track params = %+v", track)
			}
			if !bytes.Equal(track.AudioSpecificConfig, []byte{0x12, 0x10}) {
				t.Fatalf("asc = %x", track.AudioSpecificConfig)
			}
			if track.AACConfig.SampleRate != 44100 || track.AACConfig.Channels != 2 || track.AACConfig.FrameSamples != 1024 {
				t.Fatalf("aac config = %+v", track.AACConfig)
			}
			if track.MovieTimescale != normalizeFixture(tt.spec).movieTimescale || track.MovieDuration != normalizeFixture(tt.spec).movieDuration {
				t.Fatalf("movie metadata = %+v", track)
			}
			if track.MediaTimescale != normalizeFixture(tt.spec).mediaTimescale || track.MediaDuration != normalizeFixture(tt.spec).mediaDuration {
				t.Fatalf("media metadata = %+v", track)
			}
			if track.SampleCount != len(tt.spec.samples) || track.SampleDescriptionIndex != 1 {
				t.Fatalf("sample metadata = %+v", track)
			}
			if len(track.EditList) != len(tt.spec.edits) {
				t.Fatalf("edit count = %d", len(track.EditList))
			}
			for i := range tt.spec.edits {
				if track.EditList[i] != tt.spec.edits[i] {
					t.Fatalf("edit %d = %+v want %+v", i, track.EditList[i], tt.spec.edits[i])
				}
			}
			expected := expectedPackets(tt.spec, data)
			for i, want := range expected {
				buf := make([]byte, want.Size)
				n, got, err := r.ReadPacket(context.Background(), i, buf)
				if err != nil {
					t.Fatalf("ReadPacket(%d) error = %v", i, err)
				}
				if n != want.Size {
					t.Fatalf("ReadPacket(%d) size = %d want %d", i, n, want.Size)
				}
				want.TrackIndex = got.TrackIndex
				if got != want {
					t.Fatalf("packet %d = %+v want %+v", i, got, want)
				}
				if !bytes.Equal(buf[:n], tt.spec.samples[i]) {
					t.Fatalf("packet %d bytes = %x want %x", i, buf[:n], tt.spec.samples[i])
				}
			}
		})
	}
}

func TestOpenSkipsUnsupportedSidecarTrack(t *testing.T) {
	spec := audioFixture{
		trackID:           77,
		samples:           [][]byte{{0xaa, 0xbb}, {0xcc, 0xdd}},
		chunkSamples:      []int{2},
		drefSelfContained: true,
	}
	r, _ := openFixture(t, spec, Limits{}, makeSidecarTrack(1, "vide"))
	tracks := r.Tracks()
	if len(tracks) != 2 {
		t.Fatalf("track count = %d", len(tracks))
	}
	if tracks[0].Handler != "vide" || tracks[0].Accepted {
		t.Fatalf("sidecar track = %+v", tracks[0])
	}
	if tracks[1].Handler != "soun" || !tracks[1].Accepted {
		t.Fatalf("audio track = %+v", tracks[1])
	}
}

func TestOpenRejectsExternalAndInvalidDataReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec audioFixture
		want error
	}{
		{
			name: "external-url-reference",
			spec: audioFixture{samples: [][]byte{{0x01, 0x02}}, chunkSamples: []int{1}, drefSelfContained: false},
			want: pcm.ErrUnsupported,
		},
		{
			name: "sample-entry-reference-out-of-range",
			spec: audioFixture{samples: [][]byte{{0x01, 0x02}}, chunkSamples: []int{1}, dataRefIndex: 2, drefSelfContained: true},
			want: pcm.ErrMalformed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := makeFile(tc.spec)
			_, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Open() error = %v want %v", err, tc.want)
			}
		})
	}
}

func TestOpenRejectsPacketOutsideMdatBudgetAndTrackLimit(t *testing.T) {
	t.Run("packet-outside-mdat", func(t *testing.T) {
		spec := audioFixture{
			samples:              [][]byte{{0x01, 0x02, 0x03}},
			chunkSamples:         []int{1},
			chunkOffsetsOverride: []int64{9},
			drefSelfContained:    true,
		}
		data := makeFile(spec)
		_, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{})
		if !errors.Is(err, pcm.ErrMalformed) {
			t.Fatalf("Open() error = %v", err)
		}
	})

	t.Run("table-budget", func(t *testing.T) {
		spec := audioFixture{
			samples:           [][]byte{{0x01}, {0x02}, {0x03}},
			chunkSamples:      []int{1, 1, 1},
			sizeTable:         "stsz-var",
			drefSelfContained: true,
		}
		data := makeFile(spec)
		_, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{MaxTableBytes: 100})
		if !errors.Is(err, pcm.ErrLimit) {
			t.Fatalf("Open() error = %v", err)
		}
	})

	t.Run("track-limit", func(t *testing.T) {
		spec := audioFixture{samples: [][]byte{{0x01}}, chunkSamples: []int{1}, drefSelfContained: true}
		data := makeFile(spec, makeSidecarTrack(1, "vide"))
		_, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{MaxTracks: 1})
		if !errors.Is(err, pcm.ErrLimit) {
			t.Fatalf("Open() error = %v", err)
		}
	})
}

func TestReadPacketErrors(t *testing.T) {
	r, _ := openFixture(t, audioFixture{samples: [][]byte{{0x10, 0x11}}, chunkSamples: []int{1}, drefSelfContained: true}, Limits{})
	if _, _, err := r.ReadPacket(nil, 0, nil); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("nil context error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.ReadPacket(ctx, 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	if _, _, err := r.ReadPacket(context.Background(), -1, nil); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("negative index error = %v", err)
	}
	if _, info, err := r.ReadPacket(context.Background(), 0, make([]byte, 1)); !errors.Is(err, io.ErrShortBuffer) || info.Size != 2 {
		t.Fatalf("short buffer = %v info=%+v", err, info)
	}
	if _, _, err := r.ReadPacket(context.Background(), 1, nil); !errors.Is(err, io.EOF) {
		t.Fatalf("eof error = %v", err)
	}
}

func TestOpenRejectsNegativePresentationTime(t *testing.T) {
	spec := audioFixture{
		samples:           [][]byte{{0x01, 0x02}},
		chunkSamples:      []int{1},
		stts:              []timeRun{{count: 1, delta: 10}},
		cttsVersion:       1,
		ctts:              []cttsRun{{count: 1, offset: -1}},
		drefSelfContained: true,
	}
	data := makeFile(spec)
	if _, err := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{}); !errors.Is(err, pcm.ErrMalformed) {
		t.Fatal(err)
	}
}

func TestParseESDSBoundsAndObjects(t *testing.T) {
	asc, err := parseESDS(testESDSWithOptionalESFields())
	if err != nil {
		t.Fatalf("parseESDS(good) error = %v", err)
	}
	if !bytes.Equal(asc, []byte{0x12, 0x10}) {
		t.Fatalf("asc = %x", asc)
	}

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{
			name: "nonzero-version",
			data: testESDS(1, 0x40, descriptor(0x05, []byte{0x12, 0x10})),
			want: pcm.ErrUnsupported,
		},
		{
			name: "wrong-object-type",
			data: testESDS(0, 0x66, descriptor(0x05, []byte{0x12, 0x10})),
			want: pcm.ErrUnsupported,
		},
		{
			name: "truncated-descriptor-length",
			data: append(fullBox(0, 0, nil), 0x03, 0x81),
			want: pcm.ErrMalformed,
		},
		{
			name: "duplicate-asc",
			data: testESDS(0, 0x40, descriptor(0x05, []byte{0x12, 0x10}), descriptor(0x05, []byte{0x12, 0x10})),
			want: pcm.ErrMalformed,
		},
		{
			name: "missing-decoder-config",
			data: fullBox(0, 0, descriptor(0x03, join(be16(1), []byte{0x00}, descriptor(0x05, []byte{0x12, 0x10})))),
			want: pcm.ErrMalformed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseESDS(tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("parseESDS() error = %v want %v", err, tc.want)
			}
		})
	}
}

func TestParseChunkOffsetsCo64Overflow(t *testing.T) {
	payload := fullBox(0, 0, join(be32(1), be64(uint64(math.MaxInt64)+1)))
	data := box("co64", payload, false)
	budget := allocBudget{remain: 1 << 20}
	_, err := parseChunkOffsets(context.Background(), bytes.NewReader(data), Limits{MaxSamples: 4, MaxTableBytes: 1 << 20}, &budget, Box{Type: "co64", Offset: 0, Size: int64(len(data)), HeaderSize: 8})
	if !errors.Is(err, pcm.ErrMalformed) {
		t.Fatalf("parseChunkOffsets() error = %v", err)
	}
}

func TestPacketPartialIOCountsAndDuration(t *testing.T) {
	spec := audioFixture{samples: [][]byte{{1, 2, 3, 4}}, chunkSamples: []int{1}, stts: []timeRun{{count: 1, delta: 48000 * 2}}, drefSelfContained: true}
	data := makeFile(spec)
	if _, e := Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{MaxDurationSeconds: 1}); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
	r, _ := openFixture(t, spec, Limits{})
	r.src = bytes.NewReader(data[:r.packets[0].offset+2])
	buf := []byte{9, 9, 9, 9, 9}
	n, _, e := r.ReadPacket(context.Background(), 0, buf)
	if n != 2 || !errors.Is(e, pcm.ErrMalformed) || buf[0] != 1 || buf[1] != 2 || buf[2] != 9 || buf[4] != 9 {
		t.Fatal(n, e, buf)
	}
}

func TestAggregateTreeBudget(t *testing.T) {
	spec := audioFixture{samples: [][]byte{{1, 2}}, chunkSamples: []int{1}, drefSelfContained: true}
	data := makeFile(spec)
	lim, e := (Limits{MaxTableBytes: 32768}).validated()
	if e != nil {
		t.Fatal(e)
	}
	lim.budget = &allocBudget{remain: lim.MaxTableBytes}
	_, e = collectTree(context.Background(), bytes.NewReader(data), int64(len(data)), lim)
	if e != nil {
		t.Fatal(e)
	}
	if lim.budget.remain == lim.MaxTableBytes {
		t.Fatal("tree not charged")
	}
	for i := 0; i < 400; i++ {
		data = append(data, box("free", nil, false)...)
	}
	if _, e = Open(context.Background(), bytes.NewReader(data), int64(len(data)), Limits{MaxTableBytes: 32768}); !errors.Is(e, pcm.ErrLimit) {
		t.Fatal(e)
	}
}

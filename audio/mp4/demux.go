package mp4

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/rcarmo/go-264/audio/aac"
	"github.com/rcarmo/go-264/audio/pcm"
)

// Edit describes one edit-list entry. Durations use the movie timescale.
type Edit struct {
	SegmentDuration   uint64
	MediaTime         int64
	MediaRateInteger  int16
	MediaRateFraction int16
}

// Track describes one trak in file order. Accepted tracks are usable with
// ReadPacket and carry validated AAC-LC metadata and packet counts.
type Track struct {
	Index                  int
	ID                     uint32
	Accepted               bool
	Handler                string
	Codec                  string
	Channels               int
	SampleRate             int
	SampleSize             int
	SampleCount            int
	SampleDescriptionIndex int
	AudioSpecificConfig    []byte
	AACConfig              aac.Config
	MovieTimescale         uint32
	MovieDuration          uint64
	MediaTimescale         uint32
	MediaDuration          uint64
	EditList               []Edit
}

// PacketInfo describes one selected-track packet in media timescale units.
// Edit lists are preserved on Track metadata and are not applied here.
type PacketInfo struct {
	TrackIndex        int
	SampleIndex       int
	Offset            int64
	Size              int
	DecodeTime        uint64
	Duration          uint32
	CompositionOffset int64
	PresentationTime  int64
}

// Reader validates a progressive MP4/M4A and exposes one selected AAC track.
// The source is caller-owned and must remain readable for future packet reads.
type Reader struct {
	src            io.ReaderAt
	tracks         []Track
	selected       int
	movieTimescale uint32
	movieDuration  uint64
	packets        []packetEntry
}

type packetEntry struct {
	offset            int64
	size              uint32
	decodeTime        uint64
	duration          uint32
	compositionOffset int64
}

type extent struct {
	start int64
	end   int64
}

type node struct {
	box      Box
	children []*node
}

type sampleDesc struct {
	typ          string
	dataRefIndex uint16
	channels     int
	sampleRate   int
	sampleSize   int
	asc          []byte
	cfg          aac.Config
	accepted     bool
}

type dataRefEntry struct{ selfContained bool }

type stscEntry struct {
	firstChunk      uint32
	samplesPerChunk uint32
	descIndex       uint32
}

type allocBudget struct{ remain int64 }

const packetEntryBudgetBytes = 40

func (b *allocBudget) take(n int64) error {
	if n < 0 {
		return fmt.Errorf("%w: negative MP4 allocation", pcm.ErrLimit)
	}
	if n > b.remain {
		return fmt.Errorf("%w: MP4 table memory", pcm.ErrLimit)
	}
	b.remain -= n
	return nil
}

// Open validates a non-fragmented MP4 and selects the first accepted AAC-LC
// mono/stereo mp4a/esds track in file order. Unsupported sidecars are skipped.
func Open(ctx context.Context, src io.ReaderAt, size int64, limits Limits) (*Reader, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if src == nil || size < 8 {
		return nil, fmt.Errorf("%w: MP4 source/header", pcm.ErrMalformed)
	}
	lim, err := limits.validated()
	if err != nil {
		return nil, err
	}
	if size > lim.MaxBytes {
		return nil, fmt.Errorf("%w: MP4 size", pcm.ErrLimit)
	}
	root, err := collectTree(ctx, src, size, lim)
	if err != nil {
		return nil, err
	}
	moovs := childrenByType(root, "moov")
	if len(moovs) != 1 {
		return nil, fmt.Errorf("%w: MP4 moov count", pcm.ErrMalformed)
	}
	mdats := childrenByType(root, "mdat")
	if len(mdats) == 0 {
		return nil, fmt.Errorf("%w: MP4 missing mdat", pcm.ErrMalformed)
	}
	exts := make([]extent, 0, len(mdats))
	for _, mdat := range mdats {
		exts = append(exts, extent{start: mdat.box.PayloadOffset(), end: mdat.box.Offset + mdat.box.Size})
	}
	moov := moovs[0]
	mvhd, err := uniqueChild(moov, "mvhd", true)
	if err != nil {
		return nil, err
	}
	movieTimescale, movieDuration, err := parseMVHD(ctx, src, lim, mvhd.box)
	if err != nil {
		return nil, err
	}
	r := &Reader{src: src, selected: -1, movieTimescale: movieTimescale, movieDuration: movieDuration}
	traks := childrenByType(moov, "trak")
	if len(traks) > lim.MaxTracks {
		return nil, fmt.Errorf("%w: MP4 track count", pcm.ErrLimit)
	}
	for i, trak := range traks {
		meta, stbl, descs, err := parseTrackHeader(ctx, src, lim, trak, i, movieTimescale, movieDuration)
		if err != nil {
			return nil, err
		}
		r.tracks = append(r.tracks, meta)
		if r.selected >= 0 || meta.Handler != "soun" {
			continue
		}
		candidate := false
		for _, d := range descs {
			candidate = candidate || d.accepted
		}
		if !candidate {
			continue
		}
		packets, usedDesc, err := buildPackets(ctx, src, lim, exts, stbl, descs)
		if err != nil {
			if errors.Is(err, pcm.ErrUnsupported) {
				continue
			}
			return nil, err
		}
		last := packets[len(packets)-1]
		duration := last.decodeTime + uint64(last.duration)
		if duration > math.MaxInt64 || uint64(lim.MaxDurationSeconds) > math.MaxUint64/uint64(meta.MediaTimescale) {
			return nil, fmt.Errorf("%w: track duration overflow", pcm.ErrLimit)
		}
		if duration > uint64(lim.MaxDurationSeconds)*uint64(meta.MediaTimescale) {
			return nil, fmt.Errorf("%w: track duration", pcm.ErrLimit)
		}
		if meta.MediaDuration != duration {
			return nil, fmt.Errorf("%w: mdhd/stts duration mismatch", pcm.ErrMalformed)
		}
		for _, p := range packets {
			if _, err := packetPTS(p); err != nil {
				return nil, err
			}
		}
		used := descs[usedDesc-1]
		selected := &r.tracks[len(r.tracks)-1]
		selected.Accepted = true
		selected.Codec = used.typ
		selected.Channels = used.cfg.Channels
		selected.SampleRate = used.cfg.SampleRate
		selected.SampleSize = used.sampleSize
		selected.SampleCount = len(packets)
		selected.SampleDescriptionIndex = usedDesc
		selected.AudioSpecificConfig = append([]byte(nil), used.asc...)
		selected.AACConfig = used.cfg
		r.selected = len(r.tracks) - 1
		r.packets = packets
	}
	if r.selected < 0 {
		return nil, fmt.Errorf("%w: no supported AAC-LC track", pcm.ErrUnsupported)
	}
	return r, nil
}

// MovieTimescale returns the mvhd timescale copied at open time.
func (r *Reader) MovieTimescale() uint32 {
	if r == nil {
		return 0
	}
	return r.movieTimescale
}

// MovieDuration returns the mvhd duration copied at open time.
func (r *Reader) MovieDuration() uint64 {
	if r == nil {
		return 0
	}
	return r.movieDuration
}

// Track returns the selected track metadata.
func (r *Reader) Track() Track {
	if r == nil || r.selected < 0 || r.selected >= len(r.tracks) {
		return Track{}
	}
	return cloneTrack(r.tracks[r.selected])
}

// Tracks returns all track metadata in file order.
func (r *Reader) Tracks() []Track {
	if r == nil {
		return nil
	}
	out := make([]Track, len(r.tracks))
	for i := range r.tracks {
		out[i] = cloneTrack(r.tracks[i])
	}
	return out
}

// ReadPacket reads one selected-track packet into dst. The caller owns dst.
// info is returned even when dst is too small. On non-EOF I/O error, n counts
// bytes actually written to dst and dst[n:] is untouched; discard that partial
// packet. Each call is positional; retry requires no mutable decoder rollback.
func (r *Reader) ReadPacket(ctx context.Context, index int, dst []byte) (n int, info PacketInfo, err error) {
	if r == nil {
		return 0, info, fmt.Errorf("%w: nil reader", pcm.ErrMalformed)
	}
	if r.selected < 0 {
		return 0, info, fmt.Errorf("%w: no selected track", pcm.ErrMalformed)
	}
	if ctx == nil {
		return 0, info, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return 0, info, err
	}
	if index < 0 {
		return 0, info, fmt.Errorf("%w: negative packet index", pcm.ErrMalformed)
	}
	if index >= len(r.packets) {
		return 0, info, io.EOF
	}
	p := r.packets[index]
	pts, err := packetPTS(p)
	if err != nil {
		return 0, info, err
	}
	info = PacketInfo{
		TrackIndex:        r.selected,
		SampleIndex:       index,
		Offset:            p.offset,
		Size:              int(p.size),
		DecodeTime:        p.decodeTime,
		Duration:          p.duration,
		CompositionOffset: p.compositionOffset,
		PresentationTime:  pts,
	}
	if len(dst) < int(p.size) {
		return 0, info, io.ErrShortBuffer
	}
	if p.size == 0 {
		return 0, info, nil
	}
	n, readErr := io.ReadFull(io.NewSectionReader(r.src, p.offset, int64(p.size)), dst[:int(p.size)])
	if readErr != nil {
		return n, info, fmt.Errorf("%w: packet data: %v", pcm.ErrMalformed, readErr)
	}
	if err := ctx.Err(); err != nil {
		return n, info, err
	}
	return n, info, nil
}

func cloneTrack(t Track) Track {
	out := t
	out.AudioSpecificConfig = append([]byte(nil), t.AudioSpecificConfig...)
	out.EditList = append([]Edit(nil), t.EditList...)
	return out
}

func packetPTS(p packetEntry) (int64, error) {
	if p.decodeTime > math.MaxInt64 {
		return 0, fmt.Errorf("%w: packet decode time overflow", pcm.ErrLimit)
	}
	base := int64(p.decodeTime)
	if p.compositionOffset >= 0 {
		if base > math.MaxInt64-p.compositionOffset {
			return 0, fmt.Errorf("%w: packet presentation time overflow", pcm.ErrLimit)
		}
		return base + p.compositionOffset, nil
	}
	if p.compositionOffset < -base {
		return 0, fmt.Errorf("%w: negative packet presentation time", pcm.ErrMalformed)
	}
	return base + p.compositionOffset, nil
}

func collectTree(ctx context.Context, src io.ReaderAt, size int64, limits Limits) (*node, error) {
	root := &node{box: Box{Type: "root", Offset: 0, Size: size, HeaderSize: 0, Depth: -1}}
	stack := []*node{root}
	err := Walk(ctx, src, size, limits, func(b Box) error {
		for len(stack) > b.Depth+1 {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			return fmt.Errorf("%w: MP4 traversal stack", pcm.ErrMalformed)
		}
		n := &node{box: b}
		stack[len(stack)-1].children = append(stack[len(stack)-1].children, n)
		if isContainer(b.Type) {
			stack = append(stack, n)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return root, nil
}

func childrenByType(n *node, typ string) []*node {
	if n == nil {
		return nil
	}
	var out []*node
	for _, c := range n.children {
		if c.box.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

func uniqueChild(n *node, typ string, required bool) (*node, error) {
	kids := childrenByType(n, typ)
	if len(kids) > 1 {
		return nil, fmt.Errorf("%w: duplicate MP4 %s", pcm.ErrMalformed, typ)
	}
	if len(kids) == 0 {
		if required {
			return nil, fmt.Errorf("%w: missing MP4 %s", pcm.ErrMalformed, typ)
		}
		return nil, nil
	}
	return kids[0], nil
}

func parseTrackHeader(ctx context.Context, src io.ReaderAt, limits Limits, trak *node, index int, movieTimescale uint32, movieDuration uint64) (Track, *node, []sampleDesc, error) {
	meta := Track{Index: index, MovieTimescale: movieTimescale, MovieDuration: movieDuration}
	if _, err := uniqueChild(trak, "tkhd", false); err != nil {
		return meta, nil, nil, err
	}
	if tkhd, _ := uniqueChild(trak, "tkhd", false); tkhd != nil {
		id, err := parseTKHD(ctx, src, limits, tkhd.box)
		if err != nil {
			return meta, nil, nil, err
		}
		meta.ID = id
	}
	mdia, err := uniqueChild(trak, "mdia", true)
	if err != nil {
		return meta, nil, nil, err
	}
	mdhd, err := uniqueChild(mdia, "mdhd", true)
	if err != nil {
		return meta, nil, nil, err
	}
	mediaTimescale, mediaDuration, err := parseMDHD(ctx, src, limits, mdhd.box)
	if err != nil {
		return meta, nil, nil, err
	}
	meta.MediaTimescale = mediaTimescale
	meta.MediaDuration = mediaDuration
	hdlr, err := uniqueChild(mdia, "hdlr", true)
	if err != nil {
		return meta, nil, nil, err
	}
	handler, err := parseHDLR(ctx, src, limits, hdlr.box)
	if err != nil {
		return meta, nil, nil, err
	}
	meta.Handler = handler
	if edts, err := uniqueChild(trak, "edts", false); err != nil {
		return meta, nil, nil, err
	} else if edts != nil {
		if elst, err := uniqueChild(edts, "elst", false); err != nil {
			return meta, nil, nil, err
		} else if elst != nil {
			edits, err := parseELST(ctx, src, limits, elst.box)
			if err != nil {
				return meta, nil, nil, err
			}
			meta.EditList = edits
		}
	}
	if meta.Handler != "soun" {
		return meta, nil, nil, nil
	}
	minf, err := uniqueChild(mdia, "minf", true)
	if err != nil {
		return meta, nil, nil, err
	}
	dinf, err := uniqueChild(minf, "dinf", true)
	if err != nil {
		return meta, nil, nil, err
	}
	dref, err := uniqueChild(dinf, "dref", true)
	if err != nil {
		return meta, nil, nil, err
	}
	refs, err := parseDREF(ctx, src, limits, dref.box)
	if err != nil {
		return meta, nil, nil, err
	}
	stbl, err := uniqueChild(minf, "stbl", true)
	if err != nil {
		return meta, nil, nil, err
	}
	stsd, err := uniqueChild(stbl, "stsd", true)
	if err != nil {
		return meta, nil, nil, err
	}
	descs, err := parseSTSD(ctx, src, limits, stsd.box)
	if err != nil {
		return meta, nil, nil, err
	}
	for i := range descs {
		idx := int(descs[i].dataRefIndex)
		if idx < 1 || idx > len(refs) {
			return meta, nil, nil, fmt.Errorf("%w: sample entry data reference", pcm.ErrMalformed)
		}
		if !refs[idx-1].selfContained {
			descs[i].accepted = false
		}
	}
	if len(descs) > 0 {
		meta.Codec = descs[0].typ
		meta.Channels = descs[0].channels
		meta.SampleRate = descs[0].sampleRate
		meta.SampleSize = descs[0].sampleSize
	}
	return meta, stbl, descs, nil
}

func buildPackets(ctx context.Context, src io.ReaderAt, limits Limits, mdats []extent, stbl *node, descs []sampleDesc) ([]packetEntry, int, error) {
	if len(descs) == 0 {
		return nil, 0, fmt.Errorf("%w: missing sample description", pcm.ErrUnsupported)
	}
	for _, c := range stbl.children {
		if c.box.Type == "senc" {
			return nil, 0, fmt.Errorf("%w: protected samples", pcm.ErrUnsupported)
		}
	}
	stts, err := uniqueChild(stbl, "stts", true)
	if err != nil {
		return nil, 0, err
	}
	stsc, err := uniqueChild(stbl, "stsc", true)
	if err != nil {
		return nil, 0, err
	}
	ctts, err := uniqueChild(stbl, "ctts", false)
	if err != nil {
		return nil, 0, err
	}
	var sizeBox *node
	switch {
	case len(childrenByType(stbl, "stsz")) > 1 || len(childrenByType(stbl, "stz2")) > 1:
		return nil, 0, fmt.Errorf("%w: duplicate MP4 size table", pcm.ErrMalformed)
	case len(childrenByType(stbl, "stsz")) == 1 && len(childrenByType(stbl, "stz2")) == 1:
		return nil, 0, fmt.Errorf("%w: conflicting MP4 size tables", pcm.ErrMalformed)
	case len(childrenByType(stbl, "stsz")) == 1:
		sizeBox = childrenByType(stbl, "stsz")[0]
	case len(childrenByType(stbl, "stz2")) == 1:
		sizeBox = childrenByType(stbl, "stz2")[0]
	default:
		return nil, 0, fmt.Errorf("%w: missing MP4 size table", pcm.ErrMalformed)
	}
	var offBox *node
	switch {
	case len(childrenByType(stbl, "stco")) > 1 || len(childrenByType(stbl, "co64")) > 1:
		return nil, 0, fmt.Errorf("%w: duplicate MP4 chunk offsets", pcm.ErrMalformed)
	case len(childrenByType(stbl, "stco")) == 1 && len(childrenByType(stbl, "co64")) == 1:
		return nil, 0, fmt.Errorf("%w: conflicting MP4 chunk offsets", pcm.ErrMalformed)
	case len(childrenByType(stbl, "stco")) == 1:
		offBox = childrenByType(stbl, "stco")[0]
	case len(childrenByType(stbl, "co64")) == 1:
		offBox = childrenByType(stbl, "co64")[0]
	default:
		return nil, 0, fmt.Errorf("%w: missing MP4 chunk offsets", pcm.ErrMalformed)
	}
	budget := allocBudget{remain: limits.MaxTableBytes}
	sizes, err := parseSizeTable(ctx, src, limits, &budget, sizeBox.box)
	if err != nil {
		return nil, 0, err
	}
	if len(sizes) == 0 {
		return nil, 0, fmt.Errorf("%w: empty audio track", pcm.ErrUnsupported)
	}
	if len(sizes) > limits.MaxSamples {
		return nil, 0, fmt.Errorf("%w: MP4 sample count", pcm.ErrLimit)
	}
	chunks, err := parseChunkOffsets(ctx, src, limits, &budget, offBox.box)
	if err != nil {
		return nil, 0, err
	}
	maps, err := parseSTSC(ctx, src, limits, &budget, stsc.box)
	if err != nil {
		return nil, 0, err
	}
	usedDesc, err := chooseDescription(descs, maps, len(chunks))
	if err != nil {
		return nil, 0, err
	}
	if err := budget.take(int64(len(sizes)) * packetEntryBudgetBytes); err != nil {
		return nil, 0, err
	}
	packets := make([]packetEntry, len(sizes))
	if err := parseSTTS(ctx, src, limits, &budget, stts.box, packets); err != nil {
		return nil, 0, err
	}
	if ctts != nil {
		if err := parseCTTS(ctx, src, limits, &budget, ctts.box, packets); err != nil {
			return nil, 0, err
		}
	}
	if err := assignOffsets(mdats, limits.MaxPacketBytes, descs, maps, chunks, sizes, packets); err != nil {
		return nil, 0, err
	}
	return packets, usedDesc, nil
}

func chooseDescription(descs []sampleDesc, maps []stscEntry, chunkCount int) (int, error) {
	used := 0
	for i, m := range maps {
		nextFirst := chunkCount + 1
		if i+1 < len(maps) {
			nextFirst = int(maps[i+1].firstChunk)
		}
		if nextFirst <= int(m.firstChunk) {
			return 0, fmt.Errorf("%w: stsc order", pcm.ErrMalformed)
		}
		for chunk := int(m.firstChunk); chunk < nextFirst; chunk++ {
			if chunk < 1 || chunk > chunkCount {
				return 0, fmt.Errorf("%w: stsc chunk index", pcm.ErrMalformed)
			}
			if int(m.descIndex) < 1 || int(m.descIndex) > len(descs) {
				return 0, fmt.Errorf("%w: stsc sample description index", pcm.ErrMalformed)
			}
			if used == 0 {
				used = int(m.descIndex)
			} else if used != int(m.descIndex) {
				return 0, fmt.Errorf("%w: multiple sample descriptions", pcm.ErrUnsupported)
			}
		}
	}
	if used == 0 {
		return 0, fmt.Errorf("%w: empty chunk map", pcm.ErrUnsupported)
	}
	if !descs[used-1].accepted {
		return 0, fmt.Errorf("%w: unsupported sample description", pcm.ErrUnsupported)
	}
	return used, nil
}

func assignOffsets(mdats []extent, maxPacketBytes int, descs []sampleDesc, maps []stscEntry, chunks []int64, sizes []uint32, packets []packetEntry) error {
	if len(sizes) != len(packets) {
		return fmt.Errorf("%w: sample table mismatch", pcm.ErrMalformed)
	}
	sample := 0
	for i, m := range maps {
		nextFirst := len(chunks) + 1
		if i+1 < len(maps) {
			nextFirst = int(maps[i+1].firstChunk)
		}
		if int(m.descIndex) < 1 || int(m.descIndex) > len(descs) {
			return fmt.Errorf("%w: stsc sample description index", pcm.ErrMalformed)
		}
		for chunk := int(m.firstChunk); chunk < nextFirst; chunk++ {
			if chunk < 1 || chunk > len(chunks) {
				return fmt.Errorf("%w: stsc chunk index", pcm.ErrMalformed)
			}
			off := chunks[chunk-1]
			for j := 0; j < int(m.samplesPerChunk); j++ {
				if sample >= len(sizes) {
					return fmt.Errorf("%w: stsc sample count", pcm.ErrMalformed)
				}
				sz := sizes[sample]
				if sz == 0 {
					return fmt.Errorf("%w: zero packet size", pcm.ErrMalformed)
				}
				if maxPacketBytes > 0 && sz > uint32(maxPacketBytes) {
					return fmt.Errorf("%w: packet size", pcm.ErrLimit)
				}
				end, ok := addInt64(off, int64(sz))
				if !ok {
					return fmt.Errorf("%w: packet offset overflow", pcm.ErrMalformed)
				}
				if !withinExtents(mdats, off, end) {
					return fmt.Errorf("%w: packet extent outside mdat", pcm.ErrMalformed)
				}
				packets[sample].offset = off
				packets[sample].size = sz
				off = end
				sample++
			}
		}
	}
	if sample != len(sizes) {
		return fmt.Errorf("%w: unassigned samples", pcm.ErrMalformed)
	}
	return nil
}

func withinExtents(exts []extent, start, end int64) bool {
	if start < 0 || end < start {
		return false
	}
	for _, ext := range exts {
		if start >= ext.start && end <= ext.end {
			return true
		}
	}
	return false
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func parseMVHD(ctx context.Context, src io.ReaderAt, limits Limits, b Box) (uint32, uint64, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return 0, 0, err
	}
	if len(data) < 4 {
		return 0, 0, fmt.Errorf("%w: mvhd", pcm.ErrMalformed)
	}
	version := data[0]
	switch version {
	case 0:
		if len(data) < 20 {
			return 0, 0, fmt.Errorf("%w: mvhd", pcm.ErrMalformed)
		}
		ts := binary.BigEndian.Uint32(data[12:16])
		if ts == 0 {
			return 0, 0, fmt.Errorf("%w: mvhd timescale", pcm.ErrMalformed)
		}
		return ts, uint64(binary.BigEndian.Uint32(data[16:20])), nil
	case 1:
		if len(data) < 32 {
			return 0, 0, fmt.Errorf("%w: mvhd", pcm.ErrMalformed)
		}
		ts := binary.BigEndian.Uint32(data[20:24])
		if ts == 0 {
			return 0, 0, fmt.Errorf("%w: mvhd timescale", pcm.ErrMalformed)
		}
		return ts, binary.BigEndian.Uint64(data[24:32]), nil
	default:
		return 0, 0, fmt.Errorf("%w: mvhd version %d", pcm.ErrUnsupported, version)
	}
}

func parseTKHD(ctx context.Context, src io.ReaderAt, limits Limits, b Box) (uint32, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return 0, err
	}
	if len(data) < 4 {
		return 0, fmt.Errorf("%w: tkhd", pcm.ErrMalformed)
	}
	switch data[0] {
	case 0:
		if len(data) < 20 {
			return 0, fmt.Errorf("%w: tkhd", pcm.ErrMalformed)
		}
		id := binary.BigEndian.Uint32(data[12:16])
		if id == 0 {
			return 0, fmt.Errorf("%w: tkhd track id", pcm.ErrMalformed)
		}
		return id, nil
	case 1:
		if len(data) < 32 {
			return 0, fmt.Errorf("%w: tkhd", pcm.ErrMalformed)
		}
		id := binary.BigEndian.Uint32(data[20:24])
		if id == 0 {
			return 0, fmt.Errorf("%w: tkhd track id", pcm.ErrMalformed)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("%w: tkhd version %d", pcm.ErrUnsupported, data[0])
	}
}

func parseMDHD(ctx context.Context, src io.ReaderAt, limits Limits, b Box) (uint32, uint64, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return 0, 0, err
	}
	if len(data) < 4 {
		return 0, 0, fmt.Errorf("%w: mdhd", pcm.ErrMalformed)
	}
	switch data[0] {
	case 0:
		if len(data) < 24 {
			return 0, 0, fmt.Errorf("%w: mdhd", pcm.ErrMalformed)
		}
		ts := binary.BigEndian.Uint32(data[12:16])
		if ts == 0 {
			return 0, 0, fmt.Errorf("%w: mdhd timescale", pcm.ErrMalformed)
		}
		return ts, uint64(binary.BigEndian.Uint32(data[16:20])), nil
	case 1:
		if len(data) < 36 {
			return 0, 0, fmt.Errorf("%w: mdhd", pcm.ErrMalformed)
		}
		ts := binary.BigEndian.Uint32(data[20:24])
		if ts == 0 {
			return 0, 0, fmt.Errorf("%w: mdhd timescale", pcm.ErrMalformed)
		}
		return ts, binary.BigEndian.Uint64(data[24:32]), nil
	default:
		return 0, 0, fmt.Errorf("%w: mdhd version %d", pcm.ErrUnsupported, data[0])
	}
}

func parseHDLR(ctx context.Context, src io.ReaderAt, limits Limits, b Box) (string, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return "", err
	}
	if len(data) < 12 {
		return "", fmt.Errorf("%w: hdlr", pcm.ErrMalformed)
	}
	return string(data[8:12]), nil
}

func parseELST(ctx context.Context, src io.ReaderAt, limits Limits, b Box) ([]Edit, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: elst", pcm.ErrMalformed)
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count > uint32(limits.MaxSamples) {
		return nil, fmt.Errorf("%w: elst entry count", pcm.ErrLimit)
	}
	// Validate encoded size and expanded memory before allocation.
	width := 0
	switch data[0] {
	case 0:
		width = 12
	case 1:
		width = 20
	default:
		return nil, fmt.Errorf("%w: elst version %d", pcm.ErrUnsupported, data[0])
	}
	if uint64(count)*uint64(width) != uint64(len(data)-8) {
		return nil, fmt.Errorf("%w: elst size", pcm.ErrMalformed)
	}
	if int64(count)*24 > limits.MaxTableBytes-int64(len(data)) {
		return nil, fmt.Errorf("%w: edit table memory", pcm.ErrLimit)
	}
	edits := make([]Edit, int(count))
	p := data[8:]
	switch data[0] {
	case 0:
		if len(p) != int(count)*12 {
			return nil, fmt.Errorf("%w: elst size", pcm.ErrMalformed)
		}
		for i := range edits {
			edits[i] = Edit{
				SegmentDuration:   uint64(binary.BigEndian.Uint32(p[:4])),
				MediaTime:         int64(int32(binary.BigEndian.Uint32(p[4:8]))),
				MediaRateInteger:  int16(binary.BigEndian.Uint16(p[8:10])),
				MediaRateFraction: int16(binary.BigEndian.Uint16(p[10:12])),
			}
			p = p[12:]
		}
	case 1:
		if len(p) != int(count)*20 {
			return nil, fmt.Errorf("%w: elst size", pcm.ErrMalformed)
		}
		for i := range edits {
			edits[i] = Edit{
				SegmentDuration:   binary.BigEndian.Uint64(p[:8]),
				MediaTime:         int64(binary.BigEndian.Uint64(p[8:16])),
				MediaRateInteger:  int16(binary.BigEndian.Uint16(p[16:18])),
				MediaRateFraction: int16(binary.BigEndian.Uint16(p[18:20])),
			}
			p = p[20:]
		}
	default:
		return nil, fmt.Errorf("%w: elst version %d", pcm.ErrUnsupported, data[0])
	}
	return edits, nil
}

func parseSTSD(ctx context.Context, src io.ReaderAt, limits Limits, b Box) ([]sampleDesc, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: stsd", pcm.ErrMalformed)
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count > uint32(limits.MaxTracks) {
		return nil, fmt.Errorf("%w: stsd entry count", pcm.ErrLimit)
	}
	rem := data[8:]
	out := make([]sampleDesc, 0, int(count))
	for i := 0; i < int(count); i++ {
		kind, payload, rest, err := nextInlineBox(rem)
		if err != nil {
			return nil, err
		}
		if len(payload) < 8 {
			return nil, fmt.Errorf("%w: sample entry", pcm.ErrMalformed)
		}
		desc := sampleDesc{typ: kind, dataRefIndex: binary.BigEndian.Uint16(payload[6:8])}
		switch kind {
		case "mp4a":
			desc, err = parseMP4A(payload)
			if err != nil {
				if errors.Is(err, pcm.ErrUnsupported) {
					desc.typ = kind
					desc.dataRefIndex = binary.BigEndian.Uint16(payload[6:8])
				} else {
					return nil, err
				}
			}
		case "enca", "encv":
			// Explicit protected sample entries are not accepted.
		}
		out = append(out, desc)
		rem = rest
	}
	if len(rem) != 0 {
		return nil, fmt.Errorf("%w: trailing stsd data", pcm.ErrMalformed)
	}
	return out, nil
}

func parseMP4A(data []byte) (sampleDesc, error) {
	desc := sampleDesc{typ: "mp4a"}
	if len(data) < 28 {
		return desc, fmt.Errorf("%w: mp4a sample entry", pcm.ErrMalformed)
	}
	if binary.BigEndian.Uint16(data[8:10]) != 0 {
		return desc, fmt.Errorf("%w: QuickTime audio entry version", pcm.ErrUnsupported)
	}
	desc.dataRefIndex = binary.BigEndian.Uint16(data[6:8])
	desc.channels = int(binary.BigEndian.Uint16(data[16:18]))
	desc.sampleSize = int(binary.BigEndian.Uint16(data[18:20]))
	rate := binary.BigEndian.Uint32(data[24:28])
	if rate&0xffff != 0 {
		return desc, fmt.Errorf("%w: mp4a sample rate fraction", pcm.ErrUnsupported)
	}
	desc.sampleRate = int(rate >> 16)
	if desc.channels != 1 && desc.channels != 2 {
		return desc, fmt.Errorf("%w: mp4a channel count %d", pcm.ErrUnsupported, desc.channels)
	}
	esdsCount := 0
	for rem := data[28:]; len(rem) > 0; {
		kind, payload, rest, err := nextInlineBox(rem)
		if err != nil {
			return desc, err
		}
		switch kind {
		case "esds":
			esdsCount++
			if esdsCount > 1 {
				return desc, fmt.Errorf("%w: duplicate esds", pcm.ErrMalformed)
			}
			asc, err := parseESDS(payload)
			if err != nil {
				return desc, err
			}
			cfg, err := aac.ParseConfig(asc)
			if err != nil {
				return desc, err
			}
			desc.asc = append([]byte(nil), asc...)
			desc.cfg = cfg
			desc.accepted = true
		case "sinf":
			return desc, fmt.Errorf("%w: protected sample entry", pcm.ErrUnsupported)
		}
		rem = rest
	}
	if esdsCount == 0 {
		return desc, fmt.Errorf("%w: missing esds", pcm.ErrUnsupported)
	}
	if !desc.accepted {
		return desc, fmt.Errorf("%w: unsupported mp4a config", pcm.ErrUnsupported)
	}
	if desc.sampleRate != 0 && desc.sampleRate != desc.cfg.SampleRate {
		return desc, fmt.Errorf("%w: mp4a/esds sample rate mismatch", pcm.ErrUnsupported)
	}
	return desc, nil
}

func parseESDS(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("%w: esds", pcm.ErrMalformed)
	}
	if data[0] != 0 {
		return nil, fmt.Errorf("%w: esds version %d", pcm.ErrUnsupported, data[0])
	}
	var asc []byte
	gotDecoder := false
	count := 0
	var walk func([]byte, int) error
	walk = func(buf []byte, depth int) error {
		if depth > 8 {
			return fmt.Errorf("%w: esds descriptor depth", pcm.ErrLimit)
		}
		for len(buf) > 0 {
			count++
			if count > 64 {
				return fmt.Errorf("%w: esds descriptor count", pcm.ErrLimit)
			}
			tag, payload, rest, err := nextDescriptor(buf)
			if err != nil {
				return err
			}
			switch tag {
			case 0x03:
				if depth != 0 {
					return fmt.Errorf("%w: nested ES descriptor", pcm.ErrLimit)
				}
				if len(payload) < 3 {
					return fmt.Errorf("%w: esds ES_Descriptor", pcm.ErrMalformed)
				}
				flags := payload[2]
				off := 3
				if flags&0x80 != 0 {
					off += 2
				}
				if flags&0x40 != 0 {
					if off >= len(payload) {
						return fmt.Errorf("%w: esds URL descriptor", pcm.ErrMalformed)
					}
					urlLen := int(payload[off])
					off++
					if off+urlLen > len(payload) {
						return fmt.Errorf("%w: esds URL descriptor", pcm.ErrMalformed)
					}
					off += urlLen
				}
				if flags&0x20 != 0 {
					off += 2
				}
				if off > len(payload) {
					return fmt.Errorf("%w: esds optional fields", pcm.ErrMalformed)
				}
				if err := walk(payload[off:], depth+1); err != nil {
					return err
				}
			case 0x04:
				if len(payload) < 13 {
					return fmt.Errorf("%w: esds DecoderConfigDescriptor", pcm.ErrMalformed)
				}
				if payload[0] != 0x40 {
					return fmt.Errorf("%w: esds object type 0x%02x", pcm.ErrUnsupported, payload[0])
				}
				if gotDecoder {
					return fmt.Errorf("%w: duplicate decoder config", pcm.ErrMalformed)
				}
				if payload[1]>>2 != 5 {
					return fmt.Errorf("%w: esds non-audio stream", pcm.ErrUnsupported)
				}
				gotDecoder = true
				if err := walk(payload[13:], depth+1); err != nil {
					return err
				}
			case 0x05:
				if depth != 2 || !gotDecoder {
					return fmt.Errorf("%w: misplaced ASC", pcm.ErrMalformed)
				}
				if asc != nil {
					return fmt.Errorf("%w: duplicate AudioSpecificConfig", pcm.ErrMalformed)
				}
				if len(payload) > 64 {
					return fmt.Errorf("%w: ASC size", pcm.ErrLimit)
				}
				asc = append([]byte(nil), payload...)
			}
			buf = rest
		}
		return nil
	}
	if err := walk(data[4:], 0); err != nil {
		return nil, err
	}
	if !gotDecoder {
		return nil, fmt.Errorf("%w: missing DecoderConfigDescriptor", pcm.ErrUnsupported)
	}
	if asc == nil {
		return nil, fmt.Errorf("%w: missing AudioSpecificConfig", pcm.ErrUnsupported)
	}
	return asc, nil
}

func nextDescriptor(data []byte) (tag byte, payload, rest []byte, err error) {
	if len(data) < 2 {
		return 0, nil, nil, fmt.Errorf("%w: descriptor header", pcm.ErrMalformed)
	}
	tag = data[0]
	length := 0
	i := 1
	for n := 0; n < 4; n++ {
		if i >= len(data) {
			return 0, nil, nil, fmt.Errorf("%w: truncated descriptor length", pcm.ErrMalformed)
		}
		b := data[i]
		i++
		if length > (math.MaxInt32 >> 7) {
			return 0, nil, nil, fmt.Errorf("%w: descriptor length overflow", pcm.ErrMalformed)
		}
		length = (length << 7) | int(b&0x7f)
		if b&0x80 == 0 {
			if i+length > len(data) {
				return 0, nil, nil, fmt.Errorf("%w: descriptor payload", pcm.ErrMalformed)
			}
			return tag, data[i : i+length], data[i+length:], nil
		}
	}
	return 0, nil, nil, fmt.Errorf("%w: descriptor length encoding", pcm.ErrMalformed)
}

func nextInlineBox(data []byte) (kind string, payload, rest []byte, err error) {
	if len(data) < 8 {
		return "", nil, nil, fmt.Errorf("%w: inline MP4 box header", pcm.ErrMalformed)
	}
	size := uint64(binary.BigEndian.Uint32(data[:4]))
	header := 8
	if size == 1 {
		if len(data) < 16 {
			return "", nil, nil, fmt.Errorf("%w: inline large MP4 box header", pcm.ErrMalformed)
		}
		size = binary.BigEndian.Uint64(data[8:16])
		header = 16
	} else if size == 0 {
		size = uint64(len(data))
	}
	kind = string(data[4:8])
	if kind == "uuid" {
		header += 16
	}
	if size > uint64(len(data)) || int(size) < header {
		return "", nil, nil, fmt.Errorf("%w: inline MP4 %q size", pcm.ErrMalformed, kind)
	}
	return kind, data[header:int(size)], data[int(size):], nil
}

func parseDREF(ctx context.Context, src io.ReaderAt, limits Limits, b Box) ([]dataRefEntry, error) {
	data, err := readPayload(ctx, src, b, limits.MaxTableBytes)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: dref", pcm.ErrMalformed)
	}
	if data[0] != 0 {
		return nil, fmt.Errorf("%w: dref version %d", pcm.ErrUnsupported, data[0])
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count > uint32(limits.MaxTracks) {
		return nil, fmt.Errorf("%w: dref entry count", pcm.ErrLimit)
	}
	rem := data[8:]
	out := make([]dataRefEntry, 0, int(count))
	for i := 0; i < int(count); i++ {
		kind, payload, rest, err := nextInlineBox(rem)
		if err != nil {
			return nil, err
		}
		if len(payload) < 4 {
			return nil, fmt.Errorf("%w: data reference %s", pcm.ErrMalformed, kind)
		}
		if payload[0] != 0 {
			return nil, fmt.Errorf("%w: data reference version %d", pcm.ErrUnsupported, payload[0])
		}
		selfContained := false
		switch kind {
		case "url ", "urn ":
			selfContained = payload[3]&1 != 0
		}
		out = append(out, dataRefEntry{selfContained: selfContained})
		rem = rest
	}
	if len(rem) != 0 {
		return nil, fmt.Errorf("%w: trailing dref data", pcm.ErrMalformed)
	}
	return out, nil
}

func parseSizeTable(ctx context.Context, src io.ReaderAt, limits Limits, budget *allocBudget, b Box) ([]uint32, error) {
	data, err := readPayloadBudgeted(ctx, src, b, budget)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 {
		return nil, fmt.Errorf("%w: %s", pcm.ErrMalformed, b.Type)
	}
	sampleCount := binary.BigEndian.Uint32(data[8:12])
	if sampleCount > uint32(limits.MaxSamples) {
		return nil, fmt.Errorf("%w: MP4 sample count", pcm.ErrLimit)
	}
	if err := budget.take(int64(sampleCount) * 4); err != nil {
		return nil, err
	}
	sizes := make([]uint32, int(sampleCount))
	switch b.Type {
	case "stsz":
		fixed := binary.BigEndian.Uint32(data[4:8])
		if fixed != 0 {
			if len(data) != 12 {
				return nil, fmt.Errorf("%w: stsz fixed-size payload", pcm.ErrMalformed)
			}
			if fixed == 0 {
				return nil, fmt.Errorf("%w: zero packet size", pcm.ErrMalformed)
			}
			for i := range sizes {
				sizes[i] = fixed
			}
			return sizes, nil
		}
		if len(data) != 12+len(sizes)*4 {
			return nil, fmt.Errorf("%w: stsz payload", pcm.ErrMalformed)
		}
		p := data[12:]
		for i := range sizes {
			sizes[i] = binary.BigEndian.Uint32(p[:4])
			p = p[4:]
		}
		return sizes, nil
	case "stz2":
		fieldSize := int(data[7])
		p := data[12:]
		switch fieldSize {
		case 4:
			if len(p) != (len(sizes)+1)/2 {
				return nil, fmt.Errorf("%w: stz2 payload", pcm.ErrMalformed)
			}
			for i := 0; i < len(sizes); i += 2 {
				v := p[i/2]
				sizes[i] = uint32(v >> 4)
				if i+1 < len(sizes) {
					sizes[i+1] = uint32(v & 0x0f)
				}
			}
		case 8:
			if len(p) != len(sizes) {
				return nil, fmt.Errorf("%w: stz2 payload", pcm.ErrMalformed)
			}
			for i := range sizes {
				sizes[i] = uint32(p[i])
			}
		case 16:
			if len(p) != len(sizes)*2 {
				return nil, fmt.Errorf("%w: stz2 payload", pcm.ErrMalformed)
			}
			for i := range sizes {
				sizes[i] = uint32(binary.BigEndian.Uint16(p[:2]))
				p = p[2:]
			}
		default:
			return nil, fmt.Errorf("%w: stz2 field size %d", pcm.ErrUnsupported, fieldSize)
		}
		return sizes, nil
	default:
		return nil, fmt.Errorf("%w: size table %s", pcm.ErrMalformed, b.Type)
	}
}

func parseChunkOffsets(ctx context.Context, src io.ReaderAt, limits Limits, budget *allocBudget, b Box) ([]int64, error) {
	data, err := readPayloadBudgeted(ctx, src, b, budget)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: %s", pcm.ErrMalformed, b.Type)
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count > uint32(limits.MaxSamples) {
		return nil, fmt.Errorf("%w: chunk count", pcm.ErrLimit)
	}
	if err := budget.take(int64(count) * 8); err != nil {
		return nil, err
	}
	out := make([]int64, int(count))
	switch b.Type {
	case "stco":
		if len(data) != 8+len(out)*4 {
			return nil, fmt.Errorf("%w: stco payload", pcm.ErrMalformed)
		}
		p := data[8:]
		for i := range out {
			out[i] = int64(binary.BigEndian.Uint32(p[:4]))
			p = p[4:]
		}
	case "co64":
		if len(data) != 8+len(out)*8 {
			return nil, fmt.Errorf("%w: co64 payload", pcm.ErrMalformed)
		}
		p := data[8:]
		for i := range out {
			v := binary.BigEndian.Uint64(p[:8])
			if v > math.MaxInt64 {
				return nil, fmt.Errorf("%w: co64 offset overflow", pcm.ErrMalformed)
			}
			out[i] = int64(v)
			p = p[8:]
		}
	default:
		return nil, fmt.Errorf("%w: chunk table %s", pcm.ErrMalformed, b.Type)
	}
	return out, nil
}

func parseSTSC(ctx context.Context, src io.ReaderAt, limits Limits, budget *allocBudget, b Box) ([]stscEntry, error) {
	data, err := readPayloadBudgeted(ctx, src, b, budget)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("%w: stsc", pcm.ErrMalformed)
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count == 0 {
		return nil, fmt.Errorf("%w: empty stsc", pcm.ErrMalformed)
	}
	if count > uint32(limits.MaxSamples) {
		return nil, fmt.Errorf("%w: stsc entry count", pcm.ErrLimit)
	}
	if len(data) != 8+int(count)*12 {
		return nil, fmt.Errorf("%w: stsc payload", pcm.ErrMalformed)
	}
	if err := budget.take(int64(count) * 12); err != nil {
		return nil, err
	}
	out := make([]stscEntry, int(count))
	p := data[8:]
	prev := uint32(0)
	for i := range out {
		out[i] = stscEntry{
			firstChunk:      binary.BigEndian.Uint32(p[:4]),
			samplesPerChunk: binary.BigEndian.Uint32(p[4:8]),
			descIndex:       binary.BigEndian.Uint32(p[8:12]),
		}
		if out[i].firstChunk == 0 || out[i].samplesPerChunk == 0 || out[i].descIndex == 0 {
			return nil, fmt.Errorf("%w: stsc entry", pcm.ErrMalformed)
		}
		if i == 0 && out[i].firstChunk != 1 {
			return nil, fmt.Errorf("%w: stsc first chunk", pcm.ErrMalformed)
		}
		if out[i].firstChunk <= prev {
			return nil, fmt.Errorf("%w: stsc order", pcm.ErrMalformed)
		}
		prev = out[i].firstChunk
		p = p[12:]
	}
	return out, nil
}

func parseSTTS(ctx context.Context, src io.ReaderAt, limits Limits, budget *allocBudget, b Box, packets []packetEntry) error {
	data, err := readPayloadBudgeted(ctx, src, b, budget)
	if err != nil {
		return err
	}
	if len(data) < 8 {
		return fmt.Errorf("%w: stts", pcm.ErrMalformed)
	}
	if data[0] != 0 {
		return fmt.Errorf("%w: stts version %d", pcm.ErrUnsupported, data[0])
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count > uint32(limits.MaxSamples) {
		return fmt.Errorf("%w: stts entry count", pcm.ErrLimit)
	}
	if len(data) != 8+int(count)*8 {
		return fmt.Errorf("%w: stts payload", pcm.ErrMalformed)
	}
	p := data[8:]
	sample := 0
	var dts uint64
	for i := 0; i < int(count); i++ {
		n := binary.BigEndian.Uint32(p[:4])
		delta := binary.BigEndian.Uint32(p[4:8])
		if n == 0 || delta == 0 {
			return fmt.Errorf("%w: stts entry", pcm.ErrMalformed)
		}
		for j := uint32(0); j < n; j++ {
			if sample >= len(packets) {
				return fmt.Errorf("%w: stts sample count", pcm.ErrMalformed)
			}
			packets[sample].decodeTime = dts
			packets[sample].duration = delta
			if math.MaxUint64-dts < uint64(delta) {
				return fmt.Errorf("%w: decode time overflow", pcm.ErrLimit)
			}
			dts += uint64(delta)
			sample++
		}
		p = p[8:]
	}
	if sample != len(packets) {
		return fmt.Errorf("%w: stts sample count", pcm.ErrMalformed)
	}
	return nil
}

func parseCTTS(ctx context.Context, src io.ReaderAt, limits Limits, budget *allocBudget, b Box, packets []packetEntry) error {
	data, err := readPayloadBudgeted(ctx, src, b, budget)
	if err != nil {
		return err
	}
	if len(data) < 8 {
		return fmt.Errorf("%w: ctts", pcm.ErrMalformed)
	}
	version := data[0]
	if version != 0 && version != 1 {
		return fmt.Errorf("%w: ctts version %d", pcm.ErrUnsupported, version)
	}
	count := binary.BigEndian.Uint32(data[4:8])
	if count > uint32(limits.MaxSamples) {
		return fmt.Errorf("%w: ctts entry count", pcm.ErrLimit)
	}
	if len(data) != 8+int(count)*8 {
		return fmt.Errorf("%w: ctts payload", pcm.ErrMalformed)
	}
	p := data[8:]
	sample := 0
	for i := 0; i < int(count); i++ {
		n := binary.BigEndian.Uint32(p[:4])
		if n == 0 {
			return fmt.Errorf("%w: ctts entry", pcm.ErrMalformed)
		}
		var off int64
		if version == 0 {
			off = int64(binary.BigEndian.Uint32(p[4:8]))
		} else {
			off = int64(int32(binary.BigEndian.Uint32(p[4:8])))
		}
		for j := uint32(0); j < n; j++ {
			if sample >= len(packets) {
				return fmt.Errorf("%w: ctts sample count", pcm.ErrMalformed)
			}
			packets[sample].compositionOffset = off
			sample++
		}
		p = p[8:]
	}
	if sample != len(packets) {
		return fmt.Errorf("%w: ctts sample count", pcm.ErrMalformed)
	}
	return nil
}

func readPayload(ctx context.Context, src io.ReaderAt, b Box, max int64) ([]byte, error) {
	bufBudget := allocBudget{remain: max}
	return readPayloadBudgeted(ctx, src, b, &bufBudget)
}

func readPayloadBudgeted(ctx context.Context, src io.ReaderAt, b Box, budget *allocBudget) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := b.PayloadSize()
	if n < 0 {
		return nil, fmt.Errorf("%w: negative MP4 payload size", pcm.ErrMalformed)
	}
	if err := budget.take(n); err != nil {
		return nil, err
	}
	if n > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("%w: MP4 payload too large", pcm.ErrLimit)
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(io.NewSectionReader(src, b.PayloadOffset(), n), buf); err != nil {
		return nil, fmt.Errorf("%w: MP4 %s payload: %v", pcm.ErrMalformed, b.Type, err)
	}
	return buf, nil
}

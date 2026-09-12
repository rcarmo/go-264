// Package mp4 implements bounded ISO BMFF traversal and progressive AAC packet
// demux for a narrow audio-only subset. It validates structure and packet
// extents, but does not decode AAC frames to PCM.
package mp4

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/rcarmo/go-264/audio/pcm"
)

// Box describes immutable on-disk bounds. Size includes the header. ReaderAt
// operations on Payload must remain within [Offset+HeaderSize,Offset+Size).
type Box struct {
	Type       string
	Offset     int64
	Size       int64
	HeaderSize int64
	Depth      int
}

func (b Box) PayloadOffset() int64 { return b.Offset + b.HeaderSize }
func (b Box) PayloadSize() int64   { return b.Size - b.HeaderSize }

// Limits bound traversal and selected-track table expansion. Zero selects
// conservative defaults; negative values fail.
type Limits struct {
	MaxBytes           int64
	MaxBoxes           int
	MaxDepth           int
	MaxTracks          int
	MaxSamples         int
	MaxTableBytes      int64
	MaxPacketBytes     int
	MaxDurationSeconds int64        // zero selects four hours for the selected media timeline
	budget             *allocBudget // Open-owned aggregate budget, never shared between opens
}

func (l Limits) validated() (Limits, error) {
	if l.MaxBytes < 0 || l.MaxBoxes < 0 || l.MaxDepth < 0 || l.MaxTracks < 0 || l.MaxSamples < 0 || l.MaxTableBytes < 0 || l.MaxPacketBytes < 0 || l.MaxDurationSeconds < 0 {
		return l, fmt.Errorf("%w: negative MP4 limits", pcm.ErrLimit)
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = 512 << 20
	}
	if l.MaxBoxes == 0 {
		l.MaxBoxes = 65536
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = 16
	}
	if l.MaxTracks == 0 {
		l.MaxTracks = 256
	}
	if l.MaxSamples == 0 {
		l.MaxSamples = 1 << 20
	}
	if l.MaxTableBytes == 0 {
		l.MaxTableBytes = 64 << 20
	}
	if l.MaxPacketBytes == 0 {
		l.MaxPacketBytes = 1 << 20
	}
	if l.MaxDurationSeconds == 0 {
		l.MaxDurationSeconds = 14400
	}
	// Keep count conversions and memory arithmetic safe on 32-bit hosts too.
	if l.MaxSamples > 1<<24 || l.MaxBoxes > 1<<20 || l.MaxPacketBytes > 64<<20 {
		return l, fmt.Errorf("%w: MP4 hard count/packet cap", pcm.ErrLimit)
	}
	if l.MaxDepth > 64 {
		return l, fmt.Errorf("%w: MP4 maximum nesting64", pcm.ErrLimit)
	}
	if l.MaxTracks > 4096 {
		return l, fmt.Errorf("%w: MP4 track count limit", pcm.ErrLimit)
	}
	return l, nil
}

// Walk visits boxes in file order without reading mdat payloads. It descends
// only standard track containers. Unknown boxes are skipped by validated size.
// The callback must not retain mutable parser state or alter the input source.
// Fragmentation is rejected explicitly. Sample entries and metadata containers
// require their own payload-aware parsing, rather than guessed child offsets.
func Walk(ctx context.Context, src io.ReaderAt, size int64, limits Limits, visit func(Box) error) error {
	if ctx == nil || src == nil || size < 8 || visit == nil {
		return fmt.Errorf("%w: MP4 source/callback", pcm.ErrMalformed)
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	l, e := limits.validated()
	if e != nil {
		return e
	}
	if size > l.MaxBytes {
		return fmt.Errorf("%w: MP4 size", pcm.ErrLimit)
	}
	count := 0
	var walk func(int64, int64, int) error
	walk = func(start, end int64, depth int) error {
		if depth > l.MaxDepth {
			return fmt.Errorf("%w: MP4 depth", pcm.ErrLimit)
		}
		for off := start; off < end; {
			if e := ctx.Err(); e != nil {
				return e
			}
			if count >= l.MaxBoxes {
				return fmt.Errorf("%w: MP4 box count", pcm.ErrLimit)
			}
			count++
			b, e := readBox(src, off, end, depth)
			if e != nil {
				return e
			}
			if b.Type == "moof" || b.Type == "traf" || b.Type == "mvex" {
				return fmt.Errorf("%w: fragmented MP4 (%s)", pcm.ErrUnsupported, b.Type)
			}
			if e = visit(b); e != nil {
				return e
			}
			if isContainer(b.Type) {
				if e = walk(b.PayloadOffset(), off+b.Size, depth+1); e != nil {
					return e
				}
			}
			off += b.Size
		}
		return nil
	}
	return walk(0, size, 0)
}

func isContainer(kind string) bool {
	switch kind {
	case "moov", "trak", "mdia", "minf", "stbl", "edts", "dinf":
		return true
	default:
		return false
	}
}

func readBox(src io.ReaderAt, off, end int64, depth int) (Box, error) {
	var b Box
	if off < 0 || end < off || end-off < 8 {
		return b, fmt.Errorf("%w: truncated MP4 header", pcm.ErrMalformed)
	}
	var h [32]byte
	if _, e := io.ReadFull(io.NewSectionReader(src, off, 8), h[:8]); e != nil {
		return b, fmt.Errorf("%w: box header: %w", pcm.ErrMalformed, e)
	}
	n := uint64(binary.BigEndian.Uint32(h[:4]))
	head := int64(8)
	if n == 1 {
		if end-off < 16 {
			return b, fmt.Errorf("%w: truncated large MP4 header", pcm.ErrMalformed)
		}
		if _, e := io.ReadFull(io.NewSectionReader(src, off+8, 8), h[8:16]); e != nil {
			return b, fmt.Errorf("%w: large box header: %w", pcm.ErrMalformed, e)
		}
		n = binary.BigEndian.Uint64(h[8:16])
		head = 16
	} else if n == 0 {
		n = uint64(end - off)
	}
	kind := string(h[4:8])
	if kind == "uuid" {
		head += 16
	}
	if n > math.MaxInt64 || int64(n) < head || int64(n) > end-off {
		return b, fmt.Errorf("%w: MP4 %q size", pcm.ErrMalformed, kind)
	}
	return Box{Type: kind, Offset: off, Size: int64(n), HeaderSize: head, Depth: depth}, nil
}

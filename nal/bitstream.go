package nal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// ErrInvalidSyntax denotes a value outside the H.264 syntax range.
var ErrInvalidSyntax = errors.New("invalid H.264 syntax")

// Bitstream reader for H.264 exp-Golomb and fixed-length codes.
// All H.264 syntax elements are read through this interface.

// Reader reads bits from a byte slice with H.264 emulation prevention.
type Reader struct {
	data   []byte
	pos    int  // byte position
	bit    int  // bit position within current byte (7 = MSB, 0 = LSB)
	hasEPB bool // payload contains at least one 0x00 0x00 0x03 sequence
	err    error

	// Cached raw byte span without emulation-prevention bytes. Small VLC reads
	// can share one scan instead of rechecking every overlapping byte window.
	rawStart, rawEnd int
}

// Err returns the first consumed-read or syntax error. Seeking never clears it.
func (r *Reader) Err() error { return r.err }

// Fail latches the first error without changing the bit position.
func (r *Reader) Fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// ReadUEBounded checks a syntax value before it can control a loop or allocation.
func (r *Reader) ReadUEBounded(max uint32) uint32 {
	v := r.ReadUE()
	if v > max {
		r.Fail(fmt.Errorf("%w: ue(v) %d exceeds %d", ErrInvalidSyntax, v, max))
		return 0
	}
	return v
}

// ReadSEBounded checks a signed syntax value before arithmetic is performed.
func (r *Reader) ReadSEBounded(min, max int32) int32 {
	v := r.ReadSE()
	if v < min || v > max {
		r.Fail(fmt.Errorf("%w: se(v) %d outside [%d,%d]", ErrInvalidSyntax, v, min, max))
		return 0
	}
	return v
}

// NewReader creates a bitstream reader over raw NAL unit payload (after start code + header).
func NewReader(data []byte) *Reader {
	end := nextEmulationPreventionByte(data, 0)
	return &Reader{data: data, bit: 7, hasEPB: end < len(data), rawEnd: end}
}

func containsEmulationPreventionByte(data []byte) bool {
	return nextEmulationPreventionByte(data, 0) < len(data)
}

// nextEmulationPreventionByte finds the next raw 0x03 whose two predecessors
// are zero, including predecessors before start when resuming after a seek.
func nextEmulationPreventionByte(data []byte, start int) int {
	from := max(0, start-2)
	if from >= len(data) {
		return len(data)
	}
	i := bytes.Index(data[from:], []byte{0, 0, 3})
	if i < 0 {
		return len(data)
	}
	return from + i + 2
}

// rawWindow reports whether the requested bytes can be read without de-escaping.
// Seek and speculative reads need no cache repair: a position outside the cached
// span rescans from that position before using the span again.
func (r *Reader) rawWindow(bytesNeeded int) bool {
	return r.pos >= r.rawStart && r.pos+bytesNeeded <= r.rawEnd || r.scanRawWindow(bytesNeeded)
}

func (r *Reader) scanRawWindow(bytesNeeded int) bool {
	if r.pos+bytesNeeded > len(r.data) {
		return false
	}
	r.rawStart = r.pos
	r.rawEnd = len(r.data)
	if r.hasEPB {
		r.rawEnd = nextEmulationPreventionByte(r.data, r.pos)
	}
	return r.pos+bytesNeeded <= r.rawEnd
}

// ReadBit reads a single bit.
func (r *Reader) ReadBit() uint32 {
	if r.pos >= len(r.data) {
		r.Fail(io.ErrUnexpectedEOF)
		return 0
	}
	v := uint32((r.data[r.pos] >> uint(r.bit)) & 1)
	r.bit--
	if r.bit < 0 {
		r.bit = 7
		r.pos++
		// Emulation prevention: skip 0x03 in 0x00 0x00 0x03
		if r.hasEPB && r.pos >= 2 && r.pos < len(r.data) &&
			r.data[r.pos-2] == 0 && r.data[r.pos-1] == 0 && r.data[r.pos] == 3 {
			r.pos++
		}
	}
	return v
}

// readByte reads one rbsp byte at a byte-aligned position and advances past
// any emulation-prevention byte (0x00 0x00 0x03). It mirrors the byte-boundary
// transition logic in ReadBit.
func (r *Reader) readByte() uint32 {
	if r.pos >= len(r.data) {
		r.Fail(io.ErrUnexpectedEOF)
		return 0
	}
	v := uint32(r.data[r.pos])
	r.pos++
	if r.hasEPB && r.pos >= 2 && r.pos < len(r.data) &&
		r.data[r.pos-2] == 0 && r.data[r.pos-1] == 0 && r.data[r.pos] == 3 {
		r.pos++
	}
	return v
}

// ReadBits reads n bits (up to 32) as a uint32. Out-of-contract lengths are
// clamped defensively so malformed callers cannot trigger oversized shifts or
// negative loop behavior.
func (r *Reader) ReadBits(n int) uint32 {
	if n <= 0 {
		return 0
	}
	if n > 32 {
		n = 32
	}
	bytesNeeded := (7 - r.bit + n + 7) >> 3
	if r.rawWindow(bytesNeeded) {
		v := r.peekBitsRaw(n)
		r.advanceRawBits(n)
		r.skipEmulationPreventionByte()
		return v
	}
	var v uint32
	for n >= 8 && r.bit == 7 {
		v = (v << 8) | r.readByte()
		n -= 8
	}
	for i := 0; i < n; i++ {
		v = (v << 1) | r.ReadBit()
	}
	return v
}

// SkipBits consumes n bits with the same bounds and EOF behavior as ReadBits,
// without assembling a value. VLC lookups already know the matched code value.
func (r *Reader) SkipBits(n int) {
	if n <= 0 {
		return
	}
	n = min(n, 32)
	if r.rawWindow((7 - r.bit + n + 7) >> 3) {
		r.advanceRawBits(n)
		r.skipEmulationPreventionByte()
		return
	}
	r.ReadBits(n)
}

func (r *Reader) skipEmulationPreventionByte() {
	// A raw read can finish exactly before an EPB even though none of its
	// consumed bytes needed de-escaping. Match ReadBit's boundary transition.
	if r.hasEPB && r.bit == 7 && r.pos >= 2 && r.pos < len(r.data) &&
		r.data[r.pos-2] == 0 && r.data[r.pos-1] == 0 && r.data[r.pos] == 3 {
		r.pos++
	}
}

// ReadBytes reads len(dst) logical bytes, removing emulation-prevention bytes.
// It has the same position and error behavior as repeated ReadBits(8), including
// unaligned reads and zero-filled missing bits on truncation. dst must not
// overlap the reader's input. Aligned raw samples can be copied as a whole.
func (r *Reader) ReadBytes(dst []byte) {
	if r.bit != 7 {
		for i := range dst {
			dst[i] = byte(r.ReadBits(8))
		}
		return
	}
	if r.hasEPB {
		for i := range dst {
			dst[i] = byte(r.readByte())
		}
		return
	}
	n := 0
	if r.pos < len(r.data) {
		n = copy(dst, r.data[r.pos:])
	}
	r.pos += n
	if n < len(dst) {
		clear(dst[n:])
		r.Fail(io.ErrUnexpectedEOF)
	}
}

// ReadUE reads an unsigned exp-Golomb coded value.
// Format: leading zeros, 1, suffix bits.
// 0 → 0, 010 → 1, 011 → 2, 00100 → 3, etc.
func (r *Reader) ReadUE() uint32 {
	// Most syntax values fit in a short prefix/suffix pair. Count the prefix
	// in one word; use the consumed-read path for long codes and truncated tails.
	if r.err == nil && r.rawWindow(8) {
		word := binary.BigEndian.Uint64(r.data[r.pos:]) << uint(7-r.bit)
		zeros := bits.LeadingZeros64(word)
		if zeros < 16 {
			n := 2*zeros + 1
			v := uint32(word>>uint(64-n)) - 1
			r.advanceRawBits(n)
			r.skipEmulationPreventionByte()
			return v
		}
	}
	zeros := 0
	for r.ReadBit() == 0 {
		if r.err != nil {
			return 0
		}
		zeros++
		if zeros > 32 {
			r.Fail(fmt.Errorf("%w: Exp-Golomb overflow", ErrInvalidSyntax))
			return 0
		}
	}
	if zeros == 0 {
		return 0
	}
	v := (uint64(1) << uint(zeros)) - 1 + uint64(r.ReadBits(zeros))
	if v > uint64(^uint32(0)) {
		r.Fail(fmt.Errorf("%w: Exp-Golomb overflow", ErrInvalidSyntax))
		return 0
	}
	return uint32(v)
}

// ReadSE reads a signed exp-Golomb coded value.
// Mapping: 0→0, 1→1, 2→-1, 3→2, 4→-2, etc.
func (r *Reader) ReadSE() int32 {
	v := r.ReadUE()
	if v%2 == 0 {
		return -int32(v / 2)
	}
	value := (uint64(v) + 1) / 2
	if value > 1<<31-1 {
		r.Fail(fmt.Errorf("%w: signed Exp-Golomb overflow", ErrInvalidSyntax))
		return 0
	}
	return int32(value)
}

// ReadBool reads a single bit as a boolean (u(1)).
func (r *Reader) ReadBool() bool {
	return r.ReadBit() != 0
}

// ReadU8 reads an 8-bit unsigned integer.
func (r *Reader) ReadU8() uint8 {
	return uint8(r.ReadBits(8))
}

// EOF returns true if the reader has consumed all data.
func (r *Reader) EOF() bool {
	return r.pos >= len(r.data)
}

// ByteAligned returns true if the current position is byte-aligned.
func (r *Reader) ByteAligned() bool {
	return r.bit == 7
}

// ByteAlign skips bits until the next byte boundary.
func (r *Reader) ByteAlign() {
	if r.bit != 7 {
		r.bit = 7
		r.pos++
		if r.hasEPB && r.pos >= 2 && r.pos < len(r.data) &&
			r.data[r.pos-2] == 0 && r.data[r.pos-1] == 0 && r.data[r.pos] == 3 {
			r.pos++
		}
	}
}

// Position returns the current bit position (byte*8 + bits consumed in current byte).
func (r *Reader) Position() int {
	return r.pos*8 + (7 - r.bit)
}

// RBSPBytePosition returns the byte-aligned position after excluding emulation-
// prevention bytes. It is the offset FFmpeg uses inside its de-escaped RBSP.
func (r *Reader) RBSPBytePosition() int {
	if r == nil {
		return 0
	}
	pos := r.pos
	if r.bit != 7 {
		pos++
	}
	rbspPos := pos
	if r.hasEPB {
		for i := 2; i < pos && i < len(r.data); i++ {
			if r.data[i-2] == 0 && r.data[i-1] == 0 && r.data[i] == 3 {
				rbspPos--
			}
		}
	}
	return rbspPos
}

// BitsLeft returns the number of bits remaining in the stream.
func (r *Reader) BitsLeft() int {
	left := (len(r.data)-r.pos)*8 - (7 - r.bit)
	if left < 0 {
		return 0
	}
	return left
}

// PeekBits reads n bits without advancing the position.
func (r *Reader) PeekBits(n int) uint32 {
	// Most lookups stay inside a previously checked raw span. A full word
	// here avoids rescanning emulation prevention or assembling short bytes.
	if uint(n-1) < 32 && r.pos >= r.rawStart && r.pos+8 <= r.rawEnd {
		word := binary.BigEndian.Uint64(r.data[r.pos:])
		return uint32(word << uint(7-r.bit) >> uint(64-n))
	}
	return r.peekBitsSlow(n)
}

func (r *Reader) peekBitsSlow(n int) uint32 {
	if n <= 0 {
		return 0
	}
	if n > 32 {
		n = 32
	}
	if r.pos >= len(r.data) {
		return 0
	}
	// Fast path: if the requested window contains no emulation-prevention byte,
	// read directly from the backing bytes without mutating reader state. This is
	// the common CAVLC VLC lookup path and avoids ReadBits save/restore overhead.
	bytesNeeded := (7 - r.bit + n + 7) >> 3
	if r.rawWindow(bytesNeeded) {
		return r.peekBitsRaw(n)
	}
	// VLC table lookups intentionally peek past the last byte and consume only
	// the matched code's length. Preserve both position and error for lookahead.
	savePos, saveBit, saveErr := r.pos, r.bit, r.err
	v := r.ReadBits(n)
	r.pos, r.bit, r.err = savePos, saveBit, saveErr
	return v
}

// ReadRBSPTrailingBits consumes the mandatory stop bit and byte alignment.
// Parameter sets and CAVLC slices must not silently accept a truncated tail.
func (r *Reader) ReadRBSPTrailingBits() error {
	if r.ReadBit() != 1 {
		r.Fail(fmt.Errorf("%w: missing rbsp_stop_one_bit", ErrInvalidSyntax))
	}
	for !r.ByteAligned() {
		if r.ReadBit() != 0 {
			r.Fail(fmt.Errorf("%w: nonzero rbsp_alignment_zero_bit", ErrInvalidSyntax))
		}
	}
	if !r.EOF() {
		r.Fail(fmt.Errorf("%w: data after RBSP trailing bits", ErrInvalidSyntax))
	}
	return r.Err()
}

func (r *Reader) peekBitsRaw(n int) uint32 {
	if len(r.data)-r.pos >= 8 {
		word := binary.BigEndian.Uint64(r.data[r.pos:])
		return uint32(word << uint(7-r.bit) >> uint(64-n))
	}
	bytesNeeded := (7 - r.bit + n + 7) >> 3
	var acc uint64
	for i := 0; i < bytesNeeded; i++ {
		acc = (acc << 8) | uint64(r.data[r.pos+i])
	}
	shift := uint(bytesNeeded*8 - (7 - r.bit) - n)
	return uint32((acc >> shift) & ((uint64(1) << uint(n)) - 1))
}

func (r *Reader) advanceRawBits(n int) {
	consumed := uint(7 - r.bit + n)
	r.pos += int(consumed >> 3)
	r.bit = 7 - int(consumed&7)
}

// Seek moves to an absolute raw bit position. It is primarily intended for
// restoring a previously saved Position(); callers should not use it to skip
// forward across emulation-prevention bytes. Out-of-range positions are clamped
// to the valid byte span so malformed callers cannot create invalid bit state.
func (r *Reader) Seek(bitPos int) {
	if bitPos <= 0 {
		r.pos = 0
		r.bit = 7
		return
	}
	maxBits := len(r.data) * 8
	if bitPos >= maxBits {
		r.pos = len(r.data)
		r.bit = 7
		return
	}
	r.pos = bitPos / 8
	r.bit = 7 - (bitPos % 8)
}

// RemainingBytes returns all remaining bytes from the current position.
// Assumes the reader is byte-aligned (call ByteAlign first).
// EPB bytes (0x00 0x00 0x03) are removed from the output.
func (r *Reader) RemainingBytes() []byte {
	if r == nil || r.pos >= len(r.data) {
		return nil
	}
	// If not byte-aligned, advance to next byte
	if r.bit != 7 {
		r.bit = 7
		r.pos++
	}
	var out []byte
	for i := r.pos; i < len(r.data); i++ {
		if r.hasEPB && i >= 2 && r.data[i-2] == 0 && r.data[i-1] == 0 && r.data[i] == 3 {
			continue // skip EPB byte
		}
		out = append(out, r.data[i])
	}
	return out
}

// PeekRawWord exposes a non-consuming lookahead only when all eight
// backing bytes exist and contain no emulation-prevention byte. The returned
// bits are left-aligned; count excludes already consumed bits in the first byte.
// A failed speculative decode must leave Position and Err unchanged. Success
// commits through SkipBits so the normal EPB boundary transition is preserved.
func (r *Reader) PeekRawWord() (word uint64, count int) {
	if r == nil || r.err != nil || !r.rawWindow(8) {
		return 0, 0
	}
	offset := uint(7 - r.bit)
	return binary.BigEndian.Uint64(r.data[r.pos:]) << offset, 64 - int(offset)
}

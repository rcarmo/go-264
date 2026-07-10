package nal

// Bitstream reader for H.264 exp-Golomb and fixed-length codes.
// All H.264 syntax elements are read through this interface.

// Reader reads bits from a byte slice with H.264 emulation prevention.
type Reader struct {
	data   []byte
	pos    int  // byte position
	bit    int  // bit position within current byte (7 = MSB, 0 = LSB)
	hasEPB bool // payload contains at least one 0x00 0x00 0x03 sequence
}

// NewReader creates a bitstream reader over raw NAL unit payload (after start code + header).
func NewReader(data []byte) *Reader {
	return &Reader{data: data, pos: 0, bit: 7, hasEPB: containsEmulationPreventionByte(data)}
}

func containsEmulationPreventionByte(data []byte) bool {
	for i := 2; i < len(data); i++ {
		if data[i-2] == 0 && data[i-1] == 0 && data[i] == 3 {
			return true
		}
	}
	return false
}

// ReadBit reads a single bit.
func (r *Reader) ReadBit() uint32 {
	if r.pos >= len(r.data) {
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
	if !r.hasEPB && r.pos+bytesNeeded <= len(r.data) {
		v := r.peekBitsRaw(n)
		r.advanceRawBits(n)
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

// ReadUE reads an unsigned exp-Golomb coded value.
// Format: leading zeros, 1, suffix bits.
// 0 → 0, 010 → 1, 011 → 2, 00100 → 3, etc.
func (r *Reader) ReadUE() uint32 {
	zeros := 0
	for r.ReadBit() == 0 {
		zeros++
		if zeros > 31 {
			return 0 // overflow protection
		}
	}
	if zeros == 0 {
		return 0
	}
	return (1 << uint(zeros)) - 1 + r.ReadBits(zeros)
}

// ReadSE reads a signed exp-Golomb coded value.
// Mapping: 0→0, 1→1, 2→-1, 3→2, 4→-2, etc.
func (r *Reader) ReadSE() int32 {
	v := r.ReadUE()
	if v%2 == 0 {
		return -int32(v / 2)
	}
	return int32((v + 1) / 2)
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
	if r.pos+bytesNeeded <= len(r.data) && !r.hasEmulationPreventionInWindow(bytesNeeded) {
		return r.peekBitsRaw(n)
	}
	savePos, saveBit := r.pos, r.bit
	v := r.ReadBits(n)
	r.pos, r.bit = savePos, saveBit
	return v
}

func (r *Reader) peekBitsRaw(n int) uint32 {
	bytesNeeded := (7 - r.bit + n + 7) >> 3
	var acc uint64
	for i := 0; i < bytesNeeded; i++ {
		acc = (acc << 8) | uint64(r.data[r.pos+i])
	}
	shift := uint(bytesNeeded*8 - (7 - r.bit) - n)
	return uint32((acc >> shift) & ((uint64(1) << uint(n)) - 1))
}

func (r *Reader) advanceRawBits(n int) {
	bitPos := r.pos*8 + (7 - r.bit) + n
	r.pos = bitPos / 8
	r.bit = 7 - (bitPos % 8)
}

func (r *Reader) hasEmulationPreventionInWindow(bytesNeeded int) bool {
	if !r.hasEPB {
		return false
	}
	end := r.pos + bytesNeeded
	if end > len(r.data) {
		end = len(r.data)
	}
	for i := r.pos; i < end; i++ {
		if i >= 2 && r.data[i-2] == 0 && r.data[i-1] == 0 && r.data[i] == 3 {
			return true
		}
	}
	return false
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

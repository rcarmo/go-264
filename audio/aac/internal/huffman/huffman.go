// Package huffman provides the bounded AAC-LC Huffman primitives needed by a
// future scalar parser: spectrum codebooks 1..11, the scalefactor codebook,
// sign-bit handling, and codebook-11 escape decoding.
//
// The static tables are a mechanical port of the MIT-licensed OxideAV AAC
// reference pinned at:
//
//	tmp/go264-mit/oxideav-aac
//	commit 7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885
//
// Scope is intentionally narrow. This package does not parse sections,
// scalefactor bands, RVLC, MPEG tools, or PCM decode stages.
//
//go:generate go run ../../../../scripts/gen_audio_huffman.go
package huffman

import (
	"fmt"
	"math/bits"

	"github.com/rcarmo/go-264/audio/aac/internal/aacbits"
	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	// ScalefactorDeltaMin is the smallest Table 4.A.1 DPCM delta.
	ScalefactorDeltaMin = -60
	// ScalefactorDeltaMax is the largest Table 4.A.1 DPCM delta.
	ScalefactorDeltaMax = 60
	// SpectralEscapeValue is the in-band codebook-11 escape flag.
	SpectralEscapeValue = 16
	// MaxSpectralMagnitude is the AAC-LC maximum quantised spectral magnitude.
	MaxSpectralMagnitude = 8191
)

const scalefactorIndexOffset = 60

type tableEntry struct {
	bits uint8
	code uint32
}

// Codeword is one Huffman table entry.
type Codeword struct {
	Bits uint8
	Code uint32
}

// Tuple is one decoded spectrum tuple. Only the first Count values are valid.
type Tuple struct {
	Count  int
	Values [4]int16
}

// SpectralBook describes one AAC spectral Huffman codebook.
type SpectralBook struct {
	Number    int
	Dimension int
	Unsigned  bool
	LAV       int
	MaxBits   int
	Entries   int
	Escape    bool
}

type spectralSpec struct {
	SpectralBook
	table []tableEntry
}

var spectralSpecs = [...]spectralSpec{
	{},
	{SpectralBook: SpectralBook{Number: 1, Dimension: 4, Unsigned: false, LAV: 1, MaxBits: 11, Entries: len(spectralHCOD1), Escape: false}, table: spectralHCOD1[:]},
	{SpectralBook: SpectralBook{Number: 2, Dimension: 4, Unsigned: false, LAV: 1, MaxBits: 9, Entries: len(spectralHCOD2), Escape: false}, table: spectralHCOD2[:]},
	{SpectralBook: SpectralBook{Number: 3, Dimension: 4, Unsigned: true, LAV: 2, MaxBits: 16, Entries: len(spectralHCOD3), Escape: false}, table: spectralHCOD3[:]},
	{SpectralBook: SpectralBook{Number: 4, Dimension: 4, Unsigned: true, LAV: 2, MaxBits: 12, Entries: len(spectralHCOD4), Escape: false}, table: spectralHCOD4[:]},
	{SpectralBook: SpectralBook{Number: 5, Dimension: 2, Unsigned: false, LAV: 4, MaxBits: 13, Entries: len(spectralHCOD5), Escape: false}, table: spectralHCOD5[:]},
	{SpectralBook: SpectralBook{Number: 6, Dimension: 2, Unsigned: false, LAV: 4, MaxBits: 11, Entries: len(spectralHCOD6), Escape: false}, table: spectralHCOD6[:]},
	{SpectralBook: SpectralBook{Number: 7, Dimension: 2, Unsigned: true, LAV: 7, MaxBits: 12, Entries: len(spectralHCOD7), Escape: false}, table: spectralHCOD7[:]},
	{SpectralBook: SpectralBook{Number: 8, Dimension: 2, Unsigned: true, LAV: 7, MaxBits: 10, Entries: len(spectralHCOD8), Escape: false}, table: spectralHCOD8[:]},
	{SpectralBook: SpectralBook{Number: 9, Dimension: 2, Unsigned: true, LAV: 12, MaxBits: 15, Entries: len(spectralHCOD9), Escape: false}, table: spectralHCOD9[:]},
	{SpectralBook: SpectralBook{Number: 10, Dimension: 2, Unsigned: true, LAV: 12, MaxBits: 12, Entries: len(spectralHCOD10), Escape: false}, table: spectralHCOD10[:]},
	{SpectralBook: SpectralBook{Number: 11, Dimension: 2, Unsigned: true, LAV: 16, MaxBits: 12, Entries: len(spectralHCOD11), Escape: true}, table: spectralHCOD11[:]},
}

// Book returns metadata for spectral codebooks 1..11.
func Book(number int) (SpectralBook, error) {
	spec, err := spectralSpecFor(number)
	if err != nil {
		return SpectralBook{}, err
	}
	return spec.SpectralBook, nil
}

// SpectralCodeword returns the raw Huffman codeword for a spectral index.
func SpectralCodeword(book, idx int) (Codeword, error) {
	spec, err := spectralSpecFor(book)
	if err != nil {
		return Codeword{}, err
	}
	if idx < 0 || idx >= len(spec.table) {
		return Codeword{}, fmt.Errorf("%w: AAC spectral codebook %d index %d", pcm.ErrMalformed, book, idx)
	}
	entry := spec.table[idx]
	return Codeword{Bits: entry.bits, Code: entry.code}, nil
}

// DecodeSpectralIndex translates a spectral codebook index to its AAC tuple.
//
// Signed books return signed coefficients directly. Unsigned books return the
// non-negative magnitude tuple before sign-bit and escape processing.
func DecodeSpectralIndex(book, idx int) (Tuple, error) {
	spec, err := spectralSpecFor(book)
	if err != nil {
		return Tuple{}, err
	}
	if idx < 0 || idx >= len(spec.table) {
		return Tuple{}, fmt.Errorf("%w: AAC spectral codebook %d index %d", pcm.ErrMalformed, book, idx)
	}
	modulus := spec.LAV + 1
	offset := 0
	if !spec.Unsigned {
		modulus = 2*spec.LAV + 1
		offset = spec.LAV
	}
	out := Tuple{Count: spec.Dimension}
	remaining := idx
	if spec.Dimension == 4 {
		m2 := modulus * modulus
		m3 := m2 * modulus
		w := remaining/m3 - offset
		remaining -= (w + offset) * m3
		x := remaining/m2 - offset
		remaining -= (x + offset) * m2
		y := remaining/modulus - offset
		remaining -= (y + offset) * modulus
		z := remaining - offset
		out.Values[0] = int16(w)
		out.Values[1] = int16(x)
		out.Values[2] = int16(y)
		out.Values[3] = int16(z)
		return out, nil
	}
	y := remaining/modulus - offset
	remaining -= (y + offset) * modulus
	z := remaining - offset
	out.Values[0] = int16(y)
	out.Values[1] = int16(z)
	return out, nil
}

// EncodeSpectralIndex is the inverse of DecodeSpectralIndex.
//
// For unsigned books values must be the in-band magnitude tuple. Codebook-11
// escaped magnitudes therefore must be clamped to the in-band escape flag 16
// before calling this helper.
func EncodeSpectralIndex(book int, values []int16) (int, error) {
	spec, err := spectralSpecFor(book)
	if err != nil {
		return 0, err
	}
	if len(values) < spec.Dimension {
		return 0, fmt.Errorf("%w: AAC spectral codebook %d tuple length %d", pcm.ErrMalformed, book, len(values))
	}
	modulus := spec.LAV + 1
	offset := 0
	if !spec.Unsigned {
		modulus = 2*spec.LAV + 1
		offset = spec.LAV
	}
	acc := 0
	for i := 0; i < spec.Dimension; i++ {
		v := int(values[i])
		if spec.Unsigned {
			if v < 0 || v > spec.LAV {
				return 0, fmt.Errorf("%w: AAC spectral codebook %d tuple value %d", pcm.ErrMalformed, book, v)
			}
		} else if v < -spec.LAV || v > spec.LAV {
			return 0, fmt.Errorf("%w: AAC spectral codebook %d tuple value %d", pcm.ErrMalformed, book, v)
		}
		acc = acc*modulus + v + offset
	}
	return acc, nil
}

// ApplySignBits applies AAC unsigned-book sign bits to a decoded magnitude
// tuple. A sign bit value of true makes the coefficient negative.
func ApplySignBits(book int, tuple Tuple, signs []bool) (Tuple, error) {
	spec, err := spectralSpecFor(book)
	if err != nil {
		return Tuple{}, err
	}
	if tuple.Count != spec.Dimension {
		return Tuple{}, fmt.Errorf("%w: AAC spectral codebook %d tuple dimension %d", pcm.ErrMalformed, book, tuple.Count)
	}
	if !spec.Unsigned {
		if len(signs) != 0 {
			return Tuple{}, fmt.Errorf("%w: AAC spectral codebook %d unexpected sign bits", pcm.ErrMalformed, book)
		}
		return tuple, nil
	}
	nonzero := 0
	for i := 0; i < spec.Dimension; i++ {
		v := int(tuple.Values[i])
		if v < 0 || v > spec.LAV {
			return Tuple{}, fmt.Errorf("%w: AAC spectral codebook %d magnitude %d", pcm.ErrMalformed, book, v)
		}
		if v != 0 {
			nonzero++
		}
	}
	if len(signs) != nonzero {
		return Tuple{}, fmt.Errorf("%w: AAC spectral codebook %d sign count %d want %d", pcm.ErrMalformed, book, len(signs), nonzero)
	}
	out := tuple
	si := 0
	for i := 0; i < spec.Dimension; i++ {
		if out.Values[i] == 0 {
			continue
		}
		if signs[si] {
			out.Values[i] = -out.Values[i]
		}
		si++
	}
	return out, nil
}

// DeriveSignBits returns the sign-bit sequence for an AAC unsigned-book tuple.
// Signed books return an empty slice.
func DeriveSignBits(book int, values []int16) ([]bool, error) {
	spec, err := spectralSpecFor(book)
	if err != nil {
		return nil, err
	}
	if len(values) < spec.Dimension {
		return nil, fmt.Errorf("%w: AAC spectral codebook %d tuple length %d", pcm.ErrMalformed, book, len(values))
	}
	if !spec.Unsigned {
		return nil, nil
	}
	out := make([]bool, 0, spec.Dimension)
	for i := 0; i < spec.Dimension; i++ {
		v := int(values[i])
		mag := v
		if mag < 0 {
			mag = -mag
		}
		limit := spec.LAV
		if spec.Escape {
			limit = MaxSpectralMagnitude
		}
		if mag > limit {
			return nil, fmt.Errorf("%w: AAC spectral codebook %d magnitude %d", pcm.ErrLimit, book, mag)
		}
		if v != 0 {
			out = append(out, v < 0)
		}
	}
	return out, nil
}

// DecodeEscape reconstructs one codebook-11 escaped magnitude from its parsed
// prefix length and escape word.
func DecodeEscape(prefixLen int, word uint32) (int, error) {
	if prefixLen < 0 || prefixLen > 8 {
		return 0, fmt.Errorf("%w: AAC escape prefix %d", pcm.ErrLimit, prefixLen)
	}
	wordBits := prefixLen + 4
	if word >= uint32(1<<wordBits) {
		return 0, fmt.Errorf("%w: AAC escape word 0x%x/%d", pcm.ErrMalformed, word, wordBits)
	}
	value := (1 << wordBits) + int(word)
	if value < SpectralEscapeValue || value > MaxSpectralMagnitude {
		return 0, fmt.Errorf("%w: AAC escape magnitude %d", pcm.ErrLimit, value)
	}
	return value, nil
}

// EncodeEscape is the inverse of DecodeEscape for AAC-LC codebook 11.
func EncodeEscape(value int) (prefixLen int, word uint32, err error) {
	if value < SpectralEscapeValue || value > MaxSpectralMagnitude {
		return 0, 0, fmt.Errorf("%w: AAC escape magnitude %d", pcm.ErrLimit, value)
	}
	log := bits.Len32(uint32(value)) - 1
	prefixLen = log - 4
	word = uint32(value - (1 << (prefixLen + 4)))
	return prefixLen, word, nil
}

// ReadEscape reads one codebook-11 escape sequence from r.
func ReadEscape(r *aacbits.Reader) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("%w: nil AAC bit reader", pcm.ErrMalformed)
	}
	prefixLen := 0
	for {
		bit, err := r.Read(1)
		if err != nil {
			return 0, err
		}
		if bit == 0 {
			break
		}
		prefixLen++
		if prefixLen > 8 {
			return 0, fmt.Errorf("%w: AAC escape prefix %d", pcm.ErrLimit, prefixLen)
		}
	}
	word, err := r.Read(prefixLen + 4)
	if err != nil {
		return 0, err
	}
	return DecodeEscape(prefixLen, word)
}

// ReadSpectral reads one full AAC spectral tuple from r using codebook 1..11.
//
// Unsigned-book sign bits are applied automatically. For codebook 11, escape
// sequences are also consumed and expanded, with AAC-LC magnitudes capped at
// MaxSpectralMagnitude.
func ReadSpectral(r *aacbits.Reader, book int) (Tuple, error) {
	spec, err := spectralSpecFor(book)
	if err != nil {
		return Tuple{}, err
	}
	idx, err := decodeIndex(r, spec.table, spec.MaxBits)
	if err != nil {
		return Tuple{}, err
	}
	tuple, err := DecodeSpectralIndex(book, idx)
	if err != nil {
		return Tuple{}, err
	}
	if spec.Unsigned {
		var signs [4]bool
		n := 0
		for i := 0; i < spec.Dimension; i++ {
			if tuple.Values[i] == 0 {
				continue
			}
			bit, err := r.Read(1)
			if err != nil {
				return Tuple{}, err
			}
			signs[n] = bit != 0
			n++
		}
		tuple, err = ApplySignBits(book, tuple, signs[:n])
		if err != nil {
			return Tuple{}, err
		}
	}
	if spec.Escape {
		for i := 0; i < spec.Dimension; i++ {
			if abs16(tuple.Values[i]) != SpectralEscapeValue {
				continue
			}
			mag, err := ReadEscape(r)
			if err != nil {
				return Tuple{}, err
			}
			if tuple.Values[i] < 0 {
				tuple.Values[i] = int16(-mag)
			} else {
				tuple.Values[i] = int16(mag)
			}
		}
	}
	return tuple, nil
}

// ScalefactorCodeword returns the raw Table 4.A.1 codeword for one DPCM delta.
func ScalefactorCodeword(delta int8) (Codeword, error) {
	idx := int(delta) + scalefactorIndexOffset
	if idx < 0 || idx >= len(scalefactorHCOD) {
		return Codeword{}, fmt.Errorf("%w: AAC scalefactor delta %d", pcm.ErrMalformed, delta)
	}
	entry := scalefactorHCOD[idx]
	return Codeword{Bits: entry.bits, Code: entry.code}, nil
}

// ReadScalefactor reads one Table 4.A.1 scalefactor DPCM codeword.
func ReadScalefactor(r *aacbits.Reader) (int8, error) {
	idx, err := decodeIndex(r, scalefactorHCOD[:], 19)
	if err != nil {
		return 0, err
	}
	return int8(idx - scalefactorIndexOffset), nil
}

func spectralSpecFor(book int) (spectralSpec, error) {
	if book < 1 || book >= len(spectralSpecs) {
		return spectralSpec{}, fmt.Errorf("%w: AAC spectral codebook %d", pcm.ErrUnsupported, book)
	}
	return spectralSpecs[book], nil
}

func decodeIndex(r *aacbits.Reader, table []tableEntry, maxBits int) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("%w: nil AAC bit reader", pcm.ErrMalformed)
	}
	var acc uint32
	for n := 1; n <= maxBits; n++ {
		bit, err := r.Read(1)
		if err != nil {
			return 0, err
		}
		acc = (acc << 1) | bit
		for idx, entry := range table {
			if int(entry.bits) == n && entry.code == acc {
				return idx, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: invalid AAC Huffman codeword", pcm.ErrMalformed)
}

func abs16(v int16) int {
	if v < 0 {
		return int(-v)
	}
	return int(v)
}

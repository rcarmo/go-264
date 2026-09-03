package nal

import "fmt"

// NAL unit types (ITU-T H.264 Table 7-1)
const (
	TypeSliceNonIDR = 1  // Coded slice of a non-IDR picture
	TypeSlicePartA  = 2  // Coded slice data partition A
	TypeSlicePartB  = 3  // Coded slice data partition B
	TypeSlicePartC  = 4  // Coded slice data partition C
	TypeSliceIDR    = 5  // Coded slice of an IDR picture
	TypeSEI         = 6  // Supplemental enhancement information
	TypeSPS         = 7  // Sequence parameter set
	TypePPS         = 8  // Picture parameter set
	TypeAUD         = 9  // Access unit delimiter
	TypeEndSeq      = 10 // End of sequence
	TypeEndStream   = 11 // End of stream
	TypeFiller      = 12 // Filler data
)

// Unit is a parsed NAL unit.
type Unit struct {
	RefIDC  uint8  // nal_ref_idc (2 bits): reference priority
	Type    uint8  // nal_unit_type (5 bits)
	Payload []byte // RBSP payload (after header, before next start code)
}

// IsSlice returns true if this NAL contains slice data.
func (u *Unit) IsSlice() bool {
	return u.Type >= 1 && u.Type <= 5
}

// TypeName returns a human-readable name for the NAL type.
func (u *Unit) TypeName() string {
	switch u.Type {
	case TypeSliceNonIDR:
		return "Slice"
	case TypeSliceIDR:
		return "IDR"
	case TypeSPS:
		return "SPS"
	case TypePPS:
		return "PPS"
	case TypeSEI:
		return "SEI"
	case TypeAUD:
		return "AUD"
	default:
		return fmt.Sprintf("NAL(%d)", u.Type)
	}
}

// SplitNALUnits splits an Annex B bitstream into NAL units.
// Annex B format: [0x00 0x00 0x01 | 0x00 0x00 0x00 0x01] <NAL bytes>
func SplitNALUnits(data []byte) []Unit {
	units, _ := SplitNALUnitsChecked(data)
	return units
}

// SplitNALUnitsChecked validates Annex B framing and NAL headers. The legacy
// splitter remains convenient for inspection; decoding must check this error.
func SplitNALUnitsChecked(data []byte) ([]Unit, error) {
	if len(data) == 0 {
		return nil, nil
	}
	findStart := func(from int) (int, int) {
		for i := from; i+2 < len(data); i++ {
			if data[i] == 0 && data[i+1] == 0 {
				if data[i+2] == 1 {
					return i, i + 3
				}
				if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
					return i, i + 4
				}
			}
		}
		return -1, -1
	}
	start, headerAt := findStart(0)
	if start < 0 {
		return nil, fmt.Errorf("%w: missing Annex B start code", ErrInvalidSyntax)
	}
	for _, b := range data[:start] {
		if b != 0 {
			return nil, fmt.Errorf("%w: data before first start code", ErrInvalidSyntax)
		}
	}
	var units []Unit
	for {
		if headerAt >= len(data) {
			return nil, fmt.Errorf("%w: missing NAL header", ErrInvalidSyntax)
		}
		header := data[headerAt]
		typ, ref := header&31, (header>>5)&3
		if header&0x80 != 0 {
			return nil, fmt.Errorf("%w: invalid NAL header %#x", ErrInvalidSyntax, header)
		}
		if ((typ == TypeSliceIDR || typ == TypeSPS || typ == TypePPS) && ref == 0) ||
			((typ == TypeSEI || typ == TypeAUD || typ == TypeEndSeq || typ == TypeEndStream || typ == TypeFiller) && ref != 0) {
			return nil, fmt.Errorf("%w: invalid nal_ref_idc", ErrInvalidSyntax)
		}
		next, nextHeader := findStart(headerAt + 1)
		end := next
		if end < 0 {
			end = len(data)
		}
		for end > headerAt+1 && data[end-1] == 0 {
			end--
		}
		units = append(units, Unit{Type: typ, RefIDC: ref, Payload: data[headerAt+1 : end]})
		if next < 0 {
			return units, nil
		}
		headerAt = nextHeader
	}
}

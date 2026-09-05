package nal

import "fmt"

// This is a parser work bound, not a negotiated level or a decoder allocation
// budget. It exceeds the largest picture size in the H.264 level tables.
const maxSyntaxMacroblocks = 1 << 20

// SPS (Sequence Parameter Set) — defines stream-level parameters.
// ITU-T H.264 §7.3.2.1

type SPS struct {
	ProfileIDC                               uint8
	ConstraintFlags                          uint8 // constraint_set0..5_flag packed
	LevelIDC                                 uint8
	SPSID                                    uint32
	ChromaFormatIDC                          uint32 // 0=mono, 1=4:2:0, 2=4:2:2, 3=4:4:4
	BitDepthLuma                             uint32
	BitDepthChroma                           uint32
	Log2MaxFrameNum                          uint32
	PicOrderCntType                          uint32
	Log2MaxPocLsb                            uint32
	DeltaPicOrderAlwaysZero                  bool
	OffsetForNonRefPic                       int32
	OffsetForTopToBottomField                int32
	NumRefFramesInPicOrderCntCycle           uint32
	OffsetForRefFrame                        [255]int32
	MaxNumRefFrames                          uint32
	GapsInFrameNumValueAllowedFlag           bool
	PicWidthInMbs                            uint32 // width = PicWidthInMbs * 16
	PicHeightInMapUnits                      uint32
	FrameMbsOnlyFlag                         bool
	Direct8x8Inference                       bool
	FrameCropping                            bool
	CropLeft, CropRight, CropTop, CropBottom uint32

	// Derived
	Width  int
	Height int
}

// ParseSPS parses a Sequence Parameter Set from NAL payload.
func ParseSPS(payload []byte) (*SPS, error) {
	r := NewReader(payload)
	s := &SPS{}

	s.ProfileIDC = r.ReadU8()
	s.ConstraintFlags = r.ReadU8() & 0xfc // reserved_zero_2bits are ignored (§7.4.2.1.1)
	s.LevelIDC = r.ReadU8()
	s.SPSID = r.ReadUEBounded(31)

	// High profile extensions
	if s.ProfileIDC == 100 || s.ProfileIDC == 110 || s.ProfileIDC == 122 ||
		s.ProfileIDC == 244 || s.ProfileIDC == 44 || s.ProfileIDC == 83 ||
		s.ProfileIDC == 86 || s.ProfileIDC == 118 || s.ProfileIDC == 128 {
		s.ChromaFormatIDC = r.ReadUEBounded(3)
		if s.ChromaFormatIDC == 3 {
			r.ReadBit() // separate_colour_plane_flag
		}
		s.BitDepthLuma = r.ReadUEBounded(6) + 8
		s.BitDepthChroma = r.ReadUEBounded(6) + 8
		r.ReadBit()       // qpprime_y_zero_transform_bypass_flag
		if r.ReadBool() { // seq_scaling_matrix_present_flag
			n := 8
			if s.ChromaFormatIDC == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				if r.ReadBool() { // seq_scaling_list_present_flag
					skipScalingList(r, i < 6)
				}
			}
		}
	} else {
		s.ChromaFormatIDC = 1
		s.BitDepthLuma = 8
		s.BitDepthChroma = 8
	}

	s.Log2MaxFrameNum = r.ReadUEBounded(12) + 4
	s.PicOrderCntType = r.ReadUEBounded(2)

	if s.PicOrderCntType == 0 {
		s.Log2MaxPocLsb = r.ReadUEBounded(12) + 4
	} else if s.PicOrderCntType == 1 {
		s.DeltaPicOrderAlwaysZero = r.ReadBool()
		s.OffsetForNonRefPic = r.ReadSE()
		s.OffsetForTopToBottomField = r.ReadSE()
		s.NumRefFramesInPicOrderCntCycle = r.ReadUEBounded(255)
		for i := uint32(0); i < s.NumRefFramesInPicOrderCntCycle && r.Err() == nil; i++ {
			s.OffsetForRefFrame[i] = r.ReadSE()
		}
	}

	s.MaxNumRefFrames = r.ReadUEBounded(16)
	s.GapsInFrameNumValueAllowedFlag = r.ReadBool()

	s.PicWidthInMbs = r.ReadUEBounded(maxSyntaxMacroblocks-1) + 1
	s.PicHeightInMapUnits = r.ReadUEBounded(maxSyntaxMacroblocks-1) + 1
	s.FrameMbsOnlyFlag = r.ReadBool()

	if !s.FrameMbsOnlyFlag {
		r.ReadBit() // mb_adaptive_frame_field_flag
	}
	s.Direct8x8Inference = r.ReadBool()

	s.FrameCropping = r.ReadBool()
	if s.FrameCropping {
		s.CropLeft = r.ReadUE()
		s.CropRight = r.ReadUE()
		s.CropTop = r.ReadUE()
		s.CropBottom = r.ReadUE()
	}

	if r.ReadBool() { // vui_parameters_present_flag
		parseVUI(r)
	}
	if err := r.ReadRBSPTrailingBits(); err != nil {
		return nil, err
	}
	mbs := uint64(s.PicWidthInMbs) * uint64(s.PicHeightInMapUnits)
	if !s.FrameMbsOnlyFlag {
		mbs *= 2
	}
	if mbs > maxSyntaxMacroblocks {
		return nil, fmt.Errorf("%w: invalid coded picture size", ErrInvalidSyntax)
	}
	deriveSPSDimensions(s)
	if s.Width == 0 || s.Height == 0 {
		return nil, fmt.Errorf("%w: crop removes coded picture", ErrInvalidSyntax)
	}
	return s, nil
}

func deriveSPSDimensions(s *SPS) {
	if s == nil {
		return
	}
	width := uint64(s.PicWidthInMbs) * 16
	height := uint64(s.PicHeightInMapUnits) * 16
	if !s.FrameMbsOnlyFlag {
		height *= 2
	}
	if s.FrameCropping {
		cropUnitX := uint64(1)
		cropUnitY := uint64(1)
		if s.ChromaFormatIDC == 1 {
			cropUnitX = 2
			cropUnitY = 2
		} else if s.ChromaFormatIDC == 2 {
			cropUnitX = 2
		}
		if !s.FrameMbsOnlyFlag {
			cropUnitY *= 2
		}
		cropX := (uint64(s.CropLeft) + uint64(s.CropRight)) * cropUnitX
		cropY := (uint64(s.CropTop) + uint64(s.CropBottom)) * cropUnitY
		if cropX >= width {
			width = 0
		} else {
			width -= cropX
		}
		if cropY >= height {
			height = 0
		} else {
			height -= cropY
		}
	}
	s.Width = clampUint64ToInt(width)
	s.Height = clampUint64ToInt(height)
}

func clampUint64ToInt(v uint64) int {
	maxInt := uint64(^uint(0) >> 1)
	if v > maxInt {
		return int(maxInt)
	}
	return int(v)
}

func parseSliceGroupMap(r *Reader, numSliceGroups uint32) (uint32, uint32) {
	if r == nil || numSliceGroups <= 1 {
		return 0, 0
	}
	sliceGroupMapType := r.ReadUEBounded(6)
	sliceGroupChangeRate := uint32(0)
	switch sliceGroupMapType {
	case 0:
		for i := uint32(0); i < numSliceGroups && r.Err() == nil; i++ {
			r.ReadUEBounded(maxSyntaxMacroblocks - 1) // run_length_minus1[i]
		}
	case 2:
		for i := uint32(0); i+1 < numSliceGroups && r.Err() == nil; i++ {
			r.ReadUEBounded(maxSyntaxMacroblocks - 1) // top_left[i]
			r.ReadUEBounded(maxSyntaxMacroblocks - 1) // bottom_right[i]
		}
	case 3, 4, 5:
		r.ReadBit() // slice_group_change_direction_flag
		sliceGroupChangeRate = r.ReadUEBounded(maxSyntaxMacroblocks-1) + 1
	case 6:
		picSizeInMapUnits := r.ReadUEBounded(maxSyntaxMacroblocks-1) + 1
		bitsPerID := 0
		for maxID := numSliceGroups - 1; maxID > 0; maxID >>= 1 {
			bitsPerID++
		}
		for i := uint32(0); i < picSizeInMapUnits && r.Err() == nil; i++ {
			if r.ReadBits(bitsPerID) >= numSliceGroups {
				r.Fail(fmt.Errorf("%w: invalid slice_group_id", ErrInvalidSyntax))
			}
		}
	}
	return sliceGroupMapType, sliceGroupChangeRate
}

func wrapScale256(v int32) int32 {
	v %= 256
	if v < 0 {
		v += 256
	}
	return v
}

func skipScalingList(r *Reader, is4x4 bool) {
	size := 16
	if !is4x4 {
		size = 64
	}
	lastScale := int32(8)
	nextScale := int32(8)
	for j := 0; j < size; j++ {
		if nextScale != 0 {
			delta := r.ReadSEBounded(-128, 127)
			nextScale = wrapScale256(lastScale + delta)
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
}

// PPS (Picture Parameter Set) — defines picture-level parameters.
// ITU-T H.264 §7.3.2.2

type PPS struct {
	PPSID                      uint32
	SPSID                      uint32
	EntropyCodingMode          uint32 // 0=CAVLC, 1=CABAC
	BottomFieldPicOrderInFrame bool
	NumSliceGroups             uint32
	SliceGroupMapType          uint32
	SliceGroupChangeRate       uint32
	NumRefIdxL0Active          uint32
	NumRefIdxL1Active          uint32
	WeightedPred               bool
	WeightedBipredIDC          uint32
	PicInitQP                  int32
	PicInitQS                  int32
	ChromaQPIndexOffset        int32
	DeblockingFilterControl    bool
	ConstrainedIntraPred       bool
	RedundantPicCntPresent     bool

	// High profile
	Transform8x8Mode          bool
	SecondChromaQPIndexOffset int32
}

// ParsePPS parses a Picture Parameter Set from NAL payload.
func ParsePPS(payload []byte) (*PPS, error) {
	r := NewReader(payload)
	p := &PPS{}

	p.PPSID = r.ReadUEBounded(255)
	p.SPSID = r.ReadUEBounded(31)
	p.EntropyCodingMode = r.ReadBits(1)
	p.BottomFieldPicOrderInFrame = r.ReadBool()
	p.NumSliceGroups = r.ReadUEBounded(7) + 1

	if p.NumSliceGroups > 1 {
		p.SliceGroupMapType, p.SliceGroupChangeRate = parseSliceGroupMap(r, p.NumSliceGroups)
	}

	p.NumRefIdxL0Active = r.ReadUEBounded(31) + 1
	p.NumRefIdxL1Active = r.ReadUEBounded(31) + 1
	p.WeightedPred = r.ReadBool()
	p.WeightedBipredIDC = r.ReadBits(2)
	if p.WeightedBipredIDC == 3 {
		r.Fail(fmt.Errorf("%w: reserved weighted_bipred_idc", ErrInvalidSyntax))
	}
	// The PPS may precede its SPS. Admit the full 8..14-bit syntax range here;
	// activation checks PicInitQP against the actual SPS bit depth.
	p.PicInitQP = r.ReadSEBounded(-62, 25) + 26
	p.PicInitQS = r.ReadSEBounded(-26, 25) + 26
	p.ChromaQPIndexOffset = r.ReadSEBounded(-12, 12)
	p.DeblockingFilterControl = r.ReadBool()
	p.ConstrainedIntraPred = r.ReadBool()
	p.RedundantPicCntPresent = r.ReadBool()

	// High profile extensions — only present for High profile and above.
	// The spec says more_rbsp_data() but we need the SPS to know the profile.
	// We use the fact that Baseline/Main don't have these fields.
	// We need the SPS profile to gate this. Since we don't have it here,
	// check if there's more than just the RBSP stop bit remaining.
	if moreRBSPData(r) {
		p.Transform8x8Mode = r.ReadBool()
		if r.ReadBool() { // pic_scaling_matrix_present_flag
			n := 6
			if p.Transform8x8Mode {
				n = 8
			}
			for i := 0; i < n; i++ {
				if r.ReadBool() {
					skipScalingList(r, i < 6)
				}
			}
		}
		p.SecondChromaQPIndexOffset = r.ReadSEBounded(-12, 12)
	} else {
		p.SecondChromaQPIndexOffset = p.ChromaQPIndexOffset
	}

	if err := r.ReadRBSPTrailingBits(); err != nil {
		return nil, err
	}
	return p, nil
}

// VUI does not affect this decoder's reconstruction, but still has to be read
// with bounded HRD loops so a truncated SPS cannot masquerade as a valid one.
func parseVUI(r *Reader) {
	if r.ReadBool() { // aspect_ratio_info_present_flag
		if r.ReadU8() == 255 {
			r.ReadBits(16)
			r.ReadBits(16)
		}
	}
	if r.ReadBool() {
		r.ReadBit()
	} // overscan_info_present_flag
	if r.ReadBool() { // video_signal_type_present_flag
		r.ReadBits(3)
		r.ReadBit()
		if r.ReadBool() {
			r.ReadBits(24)
		}
	}
	if r.ReadBool() { // chroma_loc_info_present_flag
		r.ReadUEBounded(5)
		r.ReadUEBounded(5)
	}
	if r.ReadBool() { // timing_info_present_flag
		r.ReadBits(32)
		r.ReadBits(32)
		r.ReadBit()
	}
	nalHRD := r.ReadBool()
	if nalHRD {
		parseHRD(r)
	}
	vclHRD := r.ReadBool()
	if vclHRD {
		parseHRD(r)
	}
	if nalHRD || vclHRD {
		r.ReadBit()
	}
	r.ReadBit()       // pic_struct_present_flag
	if r.ReadBool() { // bitstream_restriction_flag
		r.ReadBit()
		r.ReadUEBounded(16)
		r.ReadUEBounded(16)
		r.ReadUEBounded(15) // log2_max_mv_length_horizontal
		r.ReadUEBounded(15) // log2_max_mv_length_vertical
		r.ReadUEBounded(16)
		r.ReadUEBounded(16)
	}
}

func parseHRD(r *Reader) {
	n := r.ReadUEBounded(31) + 1
	r.ReadBits(8)
	for i := uint32(0); i < n && r.Err() == nil; i++ {
		r.ReadUEBounded(1<<32 - 2) // bit_rate_value_minus1
		r.ReadUEBounded(1<<32 - 2) // cpb_size_value_minus1
		r.ReadBit()
	}
	r.ReadBits(20)
}

// moreRBSPData checks if there's more than the RBSP trailing bits remaining.
// The RBSP stop bit is a 1 followed by zero-fill to byte alignment.
// Returns true if there's real data beyond the stop bit.
func moreRBSPData(r *Reader) bool {
	if r.EOF() {
		return false
	}
	// Save position, peek at remaining bits
	pos := r.Position()
	remaining := r.BitsLeft()
	if remaining <= 0 {
		return false
	}
	// If remaining <= 8 bits, check if it's just the stop bit pattern (1 followed by 0s)
	if remaining <= 8 {
		bits := r.PeekBits(int(remaining))
		// Stop bit pattern: 1 followed by (remaining-1) zeros
		// E.g. remaining=1: bits=1; remaining=3: bits=100=4
		stopBit := uint32(1) << uint(remaining-1)
		r.Seek(pos) // restore position
		return bits != stopBit
	}
	return true
}

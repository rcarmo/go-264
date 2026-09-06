package syntax

import (
	"fmt"
	"io"
	"os"

	"github.com/rcarmo/go-264/nal"
)

const (
	SliceTypeP  = 0
	SliceTypeB  = 1
	SliceTypeI  = 2
	SliceTypeSP = 3
	SliceTypeSI = 4
)

type Header struct {
	FirstMbInSlice           uint32
	SliceType                uint32
	PPSID                    uint32
	FrameNum                 uint32
	FieldPicFlag             bool
	BottomFieldFlag          bool
	IdrPicID                 uint32
	PicOrderCntLsb           uint32
	DeltaPicOrderCntBottom   int32
	DeltaPicOrderCnt         [2]int32
	RedundantPicCnt          uint32
	DirectSpatialMvPred      bool
	NumRefIdxL0Active        uint32
	NumRefIdxL1Active        uint32
	CabacInitIDC             uint32
	SliceQPDelta             int32
	SPForSwitchFlag          bool
	SliceQSDelta             int32
	DisableDeblocking        int32
	SliceAlphaC0Offset       int32
	SliceBetaOffset          int32
	LumaLog2WeightDenom      uint32
	ChromaLog2WeightDenom    uint32
	LumaWeightL0             [32]int32
	LumaOffsetL0             [32]int32
	LumaWeightL1             [32]int32
	LumaOffsetL1             [32]int32
	ChromaWeightL0           [32][2]int32
	ChromaOffsetL0           [32][2]int32
	ChromaWeightL1           [32][2]int32
	ChromaOffsetL1           [32][2]int32
	WeightedTablePresent     bool
	RefModifications         [2][]RefPicListModification
	AdaptiveRefPicMarking    bool
	LongTermReference        bool // IDR long_term_reference_flag
	NoOutputOfPriorPics      bool // IDR no_output_of_prior_pics_flag
	MemoryManagementControls []MemoryManagementControl
}

type RefPicListModification struct {
	Op  uint32
	Val uint32
}

// MemoryManagementControl stores one dec_ref_pic_marking operation. The
// operands retain their syntax names so the decoded-picture buffer can apply
// the operation after reconstructing the current reference picture.
type MemoryManagementControl struct {
	Op                        uint32
	DifferenceOfPicNumsMinus1 uint32
	LongTermPicNum            uint32
	LongTermFrameIdx          uint32
	MaxLongTermFrameIdxPlus1  uint32
}

func skipPredWeightTable(r *nal.Reader, h *Header, sps *nal.SPS) { parsePredWeightTable(r, h, sps) }

func parsePredWeightTable(r *nal.Reader, h *Header, sps *nal.SPS) {
	if r == nil || h == nil || sps == nil {
		return
	}
	h.WeightedTablePresent = true
	h.LumaLog2WeightDenom = r.ReadUEBounded(7)
	defaultLumaWeight := int32(1 << h.LumaLog2WeightDenom)
	for i := range h.LumaWeightL0 {
		h.LumaWeightL0[i], h.LumaWeightL1[i] = defaultLumaWeight, defaultLumaWeight
	}
	chromaPresent := sps.ChromaFormatIDC != 0
	defaultChromaWeight := int32(1)
	if chromaPresent {
		h.ChromaLog2WeightDenom = r.ReadUEBounded(7)
		defaultChromaWeight <<= h.ChromaLog2WeightDenom
	}
	for i := range h.ChromaWeightL0 {
		for comp := 0; comp < 2; comp++ {
			h.ChromaWeightL0[i][comp] = defaultChromaWeight
			h.ChromaWeightL1[i][comp] = defaultChromaWeight
		}
	}
	parseList := func(refs uint32, weights, offsets *[32]int32, chromaWeights, chromaOffsets *[32][2]int32) {
		for i := uint32(0); i < refs && i < 32; i++ {
			if r.ReadBool() { // luma_weight_lX_flag
				weights[i] = r.ReadSEBounded(-128, 127)
				offsets[i] = r.ReadSEBounded(-128, 127)
			}
			if chromaPresent && r.ReadBool() { // chroma_weight_lX_flag
				for comp := 0; comp < 2; comp++ {
					chromaWeights[i][comp] = r.ReadSEBounded(-128, 127)
					chromaOffsets[i][comp] = r.ReadSEBounded(-128, 127)
				}
			}
		}
		for i := refs; i < 32; i++ {
			weights[i] = defaultLumaWeight
		}
	}
	parseList(h.NumRefIdxL0Active, &h.LumaWeightL0, &h.LumaOffsetL0, &h.ChromaWeightL0, &h.ChromaOffsetL0)
	if h.SliceType == SliceTypeB {
		parseList(h.NumRefIdxL1Active, &h.LumaWeightL1, &h.LumaOffsetL1, &h.ChromaWeightL1, &h.ChromaOffsetL1)
	}
}

func skipRefPicListModification(r *nal.Reader) { parseRefPicListModification(r, nil, 0) }

func parseRefPicListModification(r *nal.Reader, h *Header, list int) {
	if r == nil || !r.ReadBool() {
		return
	}
	for count := 0; count <= 32 && r.Err() == nil; count++ {
		op := r.ReadUE()
		if count == 32 && op != 3 {
			r.Fail(fmt.Errorf("%w: too many reference list modifications", nal.ErrInvalidSyntax))
			return
		}
		switch op {
		case 0, 1:
			val := r.ReadUE() // abs_diff_pic_num_minus1
			if h != nil && list >= 0 && list < len(h.RefModifications) {
				h.RefModifications[list] = append(h.RefModifications[list], RefPicListModification{Op: op, Val: val})
			}
		case 2:
			val := r.ReadUE() // long_term_pic_num
			if h != nil && list >= 0 && list < len(h.RefModifications) {
				h.RefModifications[list] = append(h.RefModifications[list], RefPicListModification{Op: op, Val: val})
			}
		case 3:
			return
		default:
			r.Fail(fmt.Errorf("%w: ref_pic_list_modification operation %d", nal.ErrInvalidSyntax, op))
			return
		}
	}
	r.Fail(fmt.Errorf("%w: unterminated reference list modification", nal.ErrInvalidSyntax))
}

func parseDecRefPicMarking(r *nal.Reader, h *Header, nalType uint8) {
	if r == nil {
		return
	}
	if nalType == nal.TypeSliceIDR {
		noOutput := r.ReadBool()
		longTerm := r.ReadBool()
		if h != nil {
			h.LongTermReference = longTerm
			h.NoOutputOfPriorPics = noOutput
		}
		return
	}
	adaptive := r.ReadBool()
	if h != nil {
		h.AdaptiveRefPicMarking = adaptive
	}
	if !adaptive {
		return
	}
	// This work bound is generous for the supported progressive decoder's
	// maximum of 16 reference frames; it is not a general bound for field syntax.
	for count := 0; count < 64 && r.Err() == nil; count++ {
		op := r.ReadUE()
		if op == 0 {
			return
		}
		mmco := MemoryManagementControl{Op: op}
		switch op {
		case 1:
			mmco.DifferenceOfPicNumsMinus1 = r.ReadUE()
		case 2:
			mmco.LongTermPicNum = r.ReadUEBounded(31)
		case 3:
			mmco.DifferenceOfPicNumsMinus1 = r.ReadUE()
			mmco.LongTermFrameIdx = r.ReadUEBounded(15)
		case 4:
			mmco.MaxLongTermFrameIdxPlus1 = r.ReadUEBounded(16)
		case 5:
			// memory_management_control_operation 5 has no operands.
		case 6:
			mmco.LongTermFrameIdx = r.ReadUEBounded(15)
		default:
			r.Fail(fmt.Errorf("%w: MMCO operation %d", nal.ErrInvalidSyntax, op))
			return
		}
		if h != nil {
			h.MemoryManagementControls = append(h.MemoryManagementControls, mmco)
		}
	}
	r.Fail(fmt.Errorf("%w: unterminated memory management operations", nal.ErrInvalidSyntax))
}

func skipDecRefPicMarking(r *nal.Reader, nalType uint8) {
	parseDecRefPicMarking(r, nil, nalType)
}

// ParseHeader parses a slice header assuming nal_ref_idc is nonzero.
// sps and pps must be nonnil active parameter sets. Callers must check the
// returned reader's Err before using the header or decoding slice data.
func ParseHeader(payload []byte, nalType uint8, sps *nal.SPS, pps *nal.PPS) (*Header, *nal.Reader) {
	return ParseHeaderWithRefIDC(payload, nalType, 1, sps, pps)
}

// ParseHeaderWithRefIDC parses a slice header using the NAL reference indicator.
// sps and pps must be nonnil active parameter sets. Callers must check the
// returned reader's Err before using the header or decoding slice data.
func ParseHeaderWithRefIDC(payload []byte, nalType uint8, nalRefIDC uint8, sps *nal.SPS, pps *nal.PPS) (*Header, *nal.Reader) {
	r := nal.NewReader(payload)
	h := &Header{}

	h.FirstMbInSlice = r.ReadUE()
	h.SliceType = r.ReadUEBounded(9)
	if h.SliceType >= 5 {
		h.SliceType -= 5
	}
	if nalType == nal.TypeSliceIDR && h.SliceType != SliceTypeI && h.SliceType != SliceTypeSI {
		r.Fail(fmt.Errorf("%w: IDR must be an I or SI slice", nal.ErrInvalidSyntax))
	}
	h.PPSID = r.ReadUEBounded(255)
	h.FrameNum = r.ReadBits(int(sps.Log2MaxFrameNum))

	if !sps.FrameMbsOnlyFlag {
		h.FieldPicFlag = r.ReadBool()
		if h.FieldPicFlag {
			h.BottomFieldFlag = r.ReadBool()
		}
	}
	if nalType == nal.TypeSliceIDR {
		h.IdrPicID = r.ReadUEBounded(65535)
	}
	if sps.PicOrderCntType == 0 {
		h.PicOrderCntLsb = r.ReadBits(int(sps.Log2MaxPocLsb))
		if pps.BottomFieldPicOrderInFrame && !h.FieldPicFlag {
			h.DeltaPicOrderCntBottom = r.ReadSE()
		}
	} else if sps.PicOrderCntType == 1 && !sps.DeltaPicOrderAlwaysZero {
		h.DeltaPicOrderCnt[0] = r.ReadSE()
		if pps.BottomFieldPicOrderInFrame && !h.FieldPicFlag {
			h.DeltaPicOrderCnt[1] = r.ReadSE()
		}
	}
	if pps.RedundantPicCntPresent {
		h.RedundantPicCnt = r.ReadUEBounded(127)
	}

	if h.SliceType == SliceTypeB {
		h.DirectSpatialMvPred = r.ReadBool()
	}

	if h.SliceType == SliceTypeP || h.SliceType == SliceTypeB || h.SliceType == SliceTypeSP {
		if r.ReadBool() { // num_ref_idx_active_override_flag
			h.NumRefIdxL0Active = r.ReadUEBounded(31) + 1
			if h.SliceType == SliceTypeB {
				h.NumRefIdxL1Active = r.ReadUEBounded(31) + 1
			}
		} else {
			h.NumRefIdxL0Active = pps.NumRefIdxL0Active
			h.NumRefIdxL1Active = pps.NumRefIdxL1Active
		}
	}

	// ref_pic_list_modification follows num_ref_idx_active_override_flag in the
	// slice header. Reading it earlier shifts P-slice headers whenever the
	// override flag is present.
	if h.SliceType != SliceTypeI && h.SliceType != SliceTypeSI {
		parseRefPicListModification(r, h, 0) // list 0
		if h.SliceType == SliceTypeB {
			parseRefPicListModification(r, h, 1) // list 1
		}
	}

	// pred_weight_table is present before dec_ref_pic_marking when weighted
	// prediction is enabled. The decoder still does not apply weighted samples,
	// but the header parser must consume the table or CABAC init/QP/deblock fields
	// are read from the wrong bit position on Main/High weighted streams.
	if (pps.WeightedPred && (h.SliceType == SliceTypeP || h.SliceType == SliceTypeSP)) ||
		(pps.WeightedBipredIDC == 1 && h.SliceType == SliceTypeB) {
		if os.Getenv("GO264_HEADER_TRACE") != "" {
			fmt.Fprintf(os.Stderr, "GOHEADER_PRE_WEIGHT pos=%d numL0=%d slice_type=%d\n", r.Position(), h.NumRefIdxL0Active, h.SliceType)
		}
		parsePredWeightTable(r, h, sps)
		if os.Getenv("GO264_HEADER_TRACE") != "" {
			fmt.Fprintf(os.Stderr, "GOHEADER_POST_WEIGHT pos=%d\n", r.Position())
		}
	}

	// dec_ref_pic_marking is present only for reference slices (nal_ref_idc != 0).
	if nalRefIDC != 0 {
		parseDecRefPicMarking(r, h, nalType)
	}

	if pps.EntropyCodingMode == 1 && h.SliceType != SliceTypeI && h.SliceType != SliceTypeSI {
		h.CabacInitIDC = r.ReadUEBounded(2)
	}
	h.SliceQPDelta = r.ReadSE()
	if h.SliceType == SliceTypeSP {
		h.SPForSwitchFlag = r.ReadBool()
	}
	if h.SliceType == SliceTypeSP || h.SliceType == SliceTypeSI {
		h.SliceQSDelta = r.ReadSE()
	}

	if pps.DeblockingFilterControl {
		disableDeblocking := r.ReadUEBounded(2)
		h.DisableDeblocking = int32(disableDeblocking)
		if h.DisableDeblocking != 1 {
			h.SliceAlphaC0Offset = r.ReadSEBounded(-6, 6) * 2
			h.SliceBetaOffset = r.ReadSEBounded(-6, 6) * 2
		}
	}

	if pps.NumSliceGroups > 1 && pps.SliceGroupMapType >= 3 && pps.SliceGroupMapType <= 5 {
		r.ReadBits(sliceGroupChangeCycleBits(sps, pps))
	}

	qp := int64(pps.PicInitQP) + int64(h.SliceQPDelta)
	minQP := -6 * (int64(sps.BitDepthLuma) - 8)
	if qp < minQP || qp > 51 {
		r.Fail(fmt.Errorf("%w: invalid slice QP", nal.ErrInvalidSyntax))
	}
	if r.EOF() {
		r.Fail(io.ErrUnexpectedEOF) // no slice_data after the header
	}
	return h, r
}

func sliceGroupChangeCycleBits(sps *nal.SPS, pps *nal.PPS) int {
	if sps == nil || pps == nil || pps.SliceGroupChangeRate == 0 {
		return 0
	}
	if sps.PicWidthInMbs != 0 && sps.PicHeightInMapUnits > ^uint32(0)/sps.PicWidthInMbs {
		return 0
	}
	picSizeInMapUnits := sps.PicWidthInMbs * sps.PicHeightInMapUnits
	if picSizeInMapUnits == 0 {
		return 0
	}
	v := picSizeInMapUnits/pps.SliceGroupChangeRate + 1
	bits := 0
	for x := v - 1; x > 0; x >>= 1 {
		bits++
	}
	return bits
}

func (h *Header) IsIntra() bool        { return h.SliceType == SliceTypeI || h.SliceType == SliceTypeSI }
func (h *Header) QP(ppsQP int32) int32 { return ppsQP + h.SliceQPDelta }

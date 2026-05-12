package syntax

import "github.com/rcarmo/go-264/nal"

const (
	SliceTypeP  = 0
	SliceTypeB  = 1
	SliceTypeI  = 2
	SliceTypeSP = 3
	SliceTypeSI = 4
)

type Header struct {
	FirstMbInSlice      uint32
	SliceType           uint32
	PPSID               uint32
	FrameNum            uint32
	FieldPicFlag        bool
	BottomFieldFlag     bool
	IdrPicID            uint32
	PicOrderCntLsb      uint32
	RedundantPicCnt     uint32
	DirectSpatialMvPred bool
	NumRefIdxL0Active   uint32
	NumRefIdxL1Active   uint32
	CabacInitIDC        uint32
	SliceQPDelta        int32
	DisableDeblocking   int32
	SliceAlphaC0Offset  int32
	SliceBetaOffset     int32
}

func skipPredWeightTable(r *nal.Reader, h *Header, sps *nal.SPS) {
	if r == nil || h == nil || sps == nil {
		return
	}
	r.ReadUE() // luma_log2_weight_denom
	chromaPresent := sps.ChromaFormatIDC != 0
	if chromaPresent {
		r.ReadUE() // chroma_log2_weight_denom
	}
	skipList := func(refs uint32) {
		for i := uint32(0); i < refs; i++ {
			if r.ReadBool() { // luma_weight_lX_flag
				r.ReadSE() // luma_weight_lX[i]
				r.ReadSE() // luma_offset_lX[i]
			}
			if chromaPresent && r.ReadBool() { // chroma_weight_lX_flag
				for j := 0; j < 2; j++ {
					r.ReadSE() // chroma_weight_lX[i][j]
					r.ReadSE() // chroma_offset_lX[i][j]
				}
			}
		}
	}
	skipList(h.NumRefIdxL0Active)
	if h.SliceType == SliceTypeB {
		skipList(h.NumRefIdxL1Active)
	}
}

func ParseHeader(payload []byte, nalType uint8, sps *nal.SPS, pps *nal.PPS) (*Header, *nal.Reader) {
	r := nal.NewReader(payload)
	h := &Header{}

	h.FirstMbInSlice = r.ReadUE()
	h.SliceType = r.ReadUE()
	if h.SliceType >= 5 {
		h.SliceType -= 5
	}
	h.PPSID = r.ReadUE()
	h.FrameNum = r.ReadBits(int(sps.Log2MaxFrameNum))

	if !sps.FrameMbsOnlyFlag {
		h.FieldPicFlag = r.ReadBool()
		if h.FieldPicFlag {
			h.BottomFieldFlag = r.ReadBool()
		}
	}
	if nalType == nal.TypeSliceIDR {
		h.IdrPicID = r.ReadUE()
	}
	if sps.PicOrderCntType == 0 {
		h.PicOrderCntLsb = r.ReadBits(int(sps.Log2MaxPocLsb))
	}
	if pps.RedundantPicCntPresent {
		h.RedundantPicCnt = r.ReadUE()
	}

	if h.SliceType == SliceTypeB {
		h.DirectSpatialMvPred = r.ReadBool()
	}

	if h.SliceType == SliceTypeP || h.SliceType == SliceTypeB || h.SliceType == SliceTypeSP {
		if r.ReadBool() { // num_ref_idx_active_override_flag
			h.NumRefIdxL0Active = r.ReadUE() + 1
			if h.SliceType == SliceTypeB {
				h.NumRefIdxL1Active = r.ReadUE() + 1
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
		if r.ReadBool() { // ref_pic_list_modification_flag_l0
			for {
				op := r.ReadUE()
				if op == 3 {
					break
				}
				r.ReadUE()
			}
		}
		if h.SliceType == SliceTypeB {
			if r.ReadBool() { // ref_pic_list_modification_flag_l1
				for {
					op := r.ReadUE()
					if op == 3 {
						break
					}
					r.ReadUE()
				}
			}
		}
	}

	// pred_weight_table is present before dec_ref_pic_marking when weighted
	// prediction is enabled. The decoder still does not apply weighted samples,
	// but the header parser must consume the table or CABAC init/QP/deblock fields
	// are read from the wrong bit position on Main/High weighted streams.
	if (pps.WeightedPred && (h.SliceType == SliceTypeP || h.SliceType == SliceTypeSP)) ||
		(pps.WeightedBipredIDC == 1 && h.SliceType == SliceTypeB) {
		skipPredWeightTable(r, h, sps)
	}

	// dec_ref_pic_marking
	if nalType == nal.TypeSliceIDR {
		r.ReadBit() // no_output_of_prior_pics_flag
		r.ReadBit() // long_term_reference_flag
	} else if r.ReadBool() { // adaptive_ref_pic_marking_mode_flag
		for {
			op := r.ReadUE()
			if op == 0 {
				break
			}
			r.ReadUE()
			if op == 3 {
				r.ReadUE()
			}
		}
	}

	if pps.EntropyCodingMode == 1 && h.SliceType != SliceTypeI && h.SliceType != SliceTypeSI {
		h.CabacInitIDC = r.ReadUE()
	}
	h.SliceQPDelta = r.ReadSE()

	if pps.DeblockingFilterControl {
		h.DisableDeblocking = r.ReadSE()
		if h.DisableDeblocking != 1 {
			h.SliceAlphaC0Offset = r.ReadSE() * 2
			h.SliceBetaOffset = r.ReadSE() * 2
		}
	}

	return h, r
}

func (h *Header) IsIntra() bool        { return h.SliceType == SliceTypeI || h.SliceType == SliceTypeSI }
func (h *Header) QP(ppsQP int32) int32 { return ppsQP + h.SliceQPDelta }

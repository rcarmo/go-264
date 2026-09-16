package decode

import (
	"fmt"
	"image"

	"github.com/rcarmo/go-264/filter"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

// pictureState owns reconstructed samples and borrows decoder scratch for
// neighbor/deblocking metadata. Scratch survives later slices of this picture;
// only starting a new picture clears it. Frame samples and saved motion metadata
// have separate storage that is never recycled with scratch.
type pictureState struct {
	referenceFrames             []*frame.Frame
	nextPrevRefFrameNum         int
	nextPrevRefValid            bool
	slices                      []*sliceState
	decoded, lastStart, lastEnd int
	motion                      bMotionCache
	deblock                     []filter.MBDeblockInfo
	referenceIDs                map[*frame.Frame]int
	pocBefore                   pocState
	order                       pictureOrder
	frame                       *frame.Frame
	sps                         *nal.SPS
	pps                         *nal.PPS
	identity                    pictureIdentity
	intraModes                  []int8
	// mbSliceID maps raster-order macroblock addresses to picture-local slice IDs.
	// -1 means unassigned; an entry is claimed before reconstructing the macroblock.
	// Ownership detects overlapping slices and determines neighbor availability
	// and whether deblocking may cross a slice boundary.
	mbSliceID         []int
	mbIsIntra         []bool
	nzCtx             [][16]int
	chromaNZCtx       [][2][4]int
	cbpCtx            []uint32
	mbTypeCtx         []uint32
	nonSkipCtx        []bool
	transform8x8Ctx   []bool
	chromaPredModeCtx []int8
	mbQPCtx           []int
	intra8x8ModeCtx   []int8
	intra8x8RightCtx  []int8
	intra8x8BottomCtx []int8
	mbFFTypeCtx       []uint32
}

// sliceState owns the header, input reader and parameter snapshots. Entropy
// engines and QP predictors are initialized per slice; pictureState owns the
// reusable motion scratch cache, whose contents are reset at slice boundaries.
type sliceState struct {
	unit         nal.Unit
	header       *syntax.Header
	reader       *nal.Reader
	sps          *nal.SPS
	pps          *nal.PPS
	id           int
	referenceErr error
}

// H.264 7.4.1.2.4: first_mb_in_slice and slice_type are not picture identity.
// Nonzero nal_ref_idc values may differ without starting a new picture.
type pictureIdentity struct {
	frameNum, ppsID               uint32
	field, bottom, reference, idr bool
	idrPicID                      uint32
	pocType, pocLSB               uint32
	deltaBottom                   int32
	delta                         [2]int32
}

func identifyPicture(s *sliceState) pictureIdentity {
	h := s.header
	key := pictureIdentity{frameNum: h.FrameNum, ppsID: h.PPSID,
		field: h.FieldPicFlag, bottom: h.FieldPicFlag && h.BottomFieldFlag,
		reference: s.unit.RefIDC != 0, idr: s.unit.Type == nal.TypeSliceIDR,
		pocType: s.sps.PicOrderCntType}
	if key.idr {
		key.idrPicID = h.IdrPicID
	}
	switch key.pocType {
	case 0:
		key.pocLSB, key.deltaBottom = h.PicOrderCntLsb, h.DeltaPicOrderCntBottom
	case 1:
		key.delta = h.DeltaPicOrderCnt
	}
	return key
}

func (d *Decoder) parseSlice(unit nal.Unit) (*sliceState, error) {
	peek := nal.NewReader(unit.Payload)
	_ = peek.ReadUE() // first_mb_in_slice
	_ = peek.ReadUE() // slice_type
	ppsID := peek.ReadUEBounded(255)
	if err := peek.Err(); err != nil {
		return nil, err
	}
	pps := d.PPS[ppsID]
	if pps == nil {
		return nil, fmt.Errorf("PPS %d not available", ppsID)
	}
	sps := d.SPS[pps.SPSID]
	if sps == nil {
		return nil, fmt.Errorf("SPS %d not available", pps.SPSID)
	}
	if !sps.FrameMbsOnlyFlag || sps.ChromaFormatIDC != 1 || sps.BitDepthLuma != 8 || sps.BitDepthChroma != 8 || pps.NumSliceGroups != 1 {
		return nil, fmt.Errorf("unsupported picture format: requires progressive 8-bit 4:2:0 without slice groups")
	}
	limit := d.MaxFrameMacroblocks
	if limit == 0 {
		limit = DefaultMaxFrameMacroblocks
	}
	mbs := int(sps.PicWidthInMbs) * int(sps.PicHeightInMapUnits)
	if mbs > limit || sps.PicWidthInMbs > 1024 || sps.PicHeightInMapUnits > 1024 {
		return nil, fmt.Errorf("coded picture exceeds allocation budget: %dx%d macroblocks (limit %d)", sps.PicWidthInMbs, sps.PicHeightInMapUnits, limit)
	}

	// Parameter-set maps may be replaced while a picture is being assembled.
	// Every slice binds value snapshots, never mutable registry entries.
	spsCopy, ppsCopy := *sps, *pps
	sps, pps = &spsCopy, &ppsCopy
	hdr, r := syntax.ParseHeaderWithRefIDCConfigured(unit.Payload, unit.Type, unit.RefIDC, sps, pps, d.trace.enabled(traceHeader))
	if err := r.Err(); err != nil {
		return nil, err
	}
	if hdr.SliceType == syntax.SliceTypeSP || hdr.SliceType == syntax.SliceTypeSI {
		return nil, fmt.Errorf("unsupported SP/SI slice")
	}
	if pps.PicInitQP < 0 {
		return nil, fmt.Errorf("%w: initial PPS QP outside 8-bit range", nal.ErrInvalidSyntax)
	}
	return &sliceState{unit: unit, header: hdr, reader: r, sps: sps, pps: pps}, nil
}

func (d *Decoder) newPicture(slice *sliceState) *pictureState {
	before := d.pocState()
	sps, hdr := slice.sps, slice.header
	mbWidth, mbHeight := int(sps.PicWidthInMbs), int(sps.PicHeightInMapUnits)
	var f *frame.Frame
	if d.pictureBuffers == nil {
		f = newPictureFrame(mbWidth, mbHeight)
	} else {
		var pending []*frame.Frame
		if d.outputOrder != nil {
			pending = d.outputOrder.pending
		}
		f = d.pictureBuffers.take(mbWidth, mbHeight, d.DPB.Frames, pending)
	}
	// Reconstruct and retain every coded sample, including cropped borders.
	// Cropping changes presentation only, never prediction/reference geometry.
	if sps.FrameCropping {
		cropUnitX, cropUnitY := 1, 1
		if sps.ChromaFormatIDC == 1 {
			cropUnitX, cropUnitY = 2, 2
		} else if sps.ChromaFormatIDC == 2 {
			cropUnitX = 2
		}
		left, top := int(sps.CropLeft)*cropUnitX, int(sps.CropTop)*cropUnitY
		f.CropRect = image.Rectangle{
			Min: image.Pt(left, top),
			Max: image.Pt(left+sps.Width, top+sps.Height),
		}
	}
	f.IsIDR = slice.unit.Type == nal.TypeSliceIDR
	f.NoOutputOfPriorPics = hdr.NoOutputOfPriorPics
	f.ResetsPictureOrder = f.IsIDR
	f.IsRef = slice.unit.RefIDC > 0
	f.FrameNum = int(hdr.FrameNum)

	d.scratch.reset(mbWidth, mbHeight)
	b := &d.scratch
	b.motion.trace = &d.trace
	return &pictureState{
		pocBefore: before, motion: b.motion,
		deblock: b.deblock, referenceIDs: make(map[*frame.Frame]int),
		frame: f, sps: sps, pps: slice.pps, identity: identifyPicture(slice),
		intraModes: b.intraModes, mbSliceID: b.mbSliceID, mbIsIntra: b.mbIsIntra,
		nzCtx: b.nzCtx, chromaNZCtx: b.chromaNZCtx,
		cbpCtx: b.cbpCtx, mbTypeCtx: b.mbTypeCtx,
		nonSkipCtx: b.nonSkipCtx, transform8x8Ctx: b.transform8x8Ctx,
		chromaPredModeCtx: b.chromaPredModeCtx, mbQPCtx: b.mbQPCtx,
		intra8x8ModeCtx:  b.intra8x8ModeCtx,
		intra8x8RightCtx: b.intra8x8RightCtx, intra8x8BottomCtx: b.intra8x8BottomCtx,
		mbFFTypeCtx: b.mbFFTypeCtx,
	}
}

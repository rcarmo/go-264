package decode

// decode/pipeline.go — Decoder type, Decode(), and the decodeSlice() main loop.

import (
	"fmt"
	"io"
	"os"

	cabac "github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

// MBTraceEvent is a compact macroblock syntax snapshot emitted after a
// macroblock is decoded but before the optional CABAC termination bit is read.
// It deliberately records decoded syntax, not reconstructed pixels, so trace
// consumers can compare CABAC decisions against FFmpeg before chasing prediction
// or transform drift.
type MBTraceEvent struct {
	NALType           uint8
	FrameNum          int
	SliceType         uint32
	MBAddr, MBX       int
	MBY               int
	EntropyCABAC      bool
	Kind              string
	MBType            uint32
	SubMBType         [4]uint32
	CBP               uint32
	QPDelta           int32
	QP                int
	Skipped           bool
	Use8x8            bool
	ChromaPred        int8
	Intra4x4Mode      [16]int8
	Intra4x4PredMode  [16]int8
	Intra4x4FinalMode [16]int8
	Intra8x8Mode      [4]int8
	Intra8x8PredMode  [4]int8
	Intra8x8LeftEdge  [2]int8
	Intra8x8TopEdge   [2]int8
	RefIdx            [4]int8
	MV                [4]syntax.MotionVector
	SubMV             [16]syntax.MotionVector
	TotalCoeff        [16]int
	ChromaCoeff       [2][4]int
}

// Decoder is an H.264 Annex B bitstream decoder.
type Decoder struct {
	SPS    map[uint32]*nal.SPS
	PPS    map[uint32]*nal.PPS
	DPB    *frame.DPB
	Frames []*frame.Frame
	// MaxFrames optionally stops Decode after this many complete pictures.
	// Zero means decode all frames.
	MaxFrames int
	// MaxFrameMacroblocks bounds each coded picture before pixel, motion, or
	// macroblock allocation. Zero uses DefaultMaxFrameMacroblocks. This is a
	// resource budget, not a claim of conformance to any H.264 level.
	MaxFrameMacroblocks int
	// Per-frame prediction mode map (4x4 block index → mode)
	intraModes []int8 // [mbW*4 * mbH*4] for current frame
	mbW, mbH   int
	// chromaQPOffset: pps.ChromaQPIndexOffset, set at the start of each slice.
	chromaQPOffset int
	// TraceMB is optional diagnostic output for first-divergence tooling. Leave
	// nil in normal decode paths to avoid overhead and preserve API behaviour.
	TraceMB func(MBTraceEvent)
	// traceFrameIndex is the output-frame index currently being decoded. Decode
	// appends to d.Frames only after processing all NAL units in the input buffer,
	// so reconstruction trace code cannot derive this from len(d.Frames).
	traceFrameIndex       int
	traceIntra4x4PredMode [16]int8
	weightedPred          bool
	weightedBipredIDC     uint32
	lumaWeightDenom       uint32
	lumaWeightL0          [32]int32
	lumaOffsetL0          [32]int32
	chromaWeightDenom     uint32
	chromaWeightL0        [32][2]int32
	chromaOffsetL0        [32][2]int32
	maxPOCLSB             int
	currentFullPOC        int
	// activeL0Refs is the slice-header-modified reference picture list used by
	// P-slice motion compensation. It is rebuilt for every decoded slice.
	activeL0Refs []*frame.Frame

	// Reconstruction binds one picture and one independently initialized slice.
	picture              *pictureState
	slice                *sliceState
	prevRefFrameNum      int
	prevRefFrameNumValid bool
	referenceSPS         *nal.SPS
	pocHistory           pocHistory
	maxLongTermFrameIdx  int
}

// DecodedFrame is an alias for frame.Frame for CLI convenience.
type DecodedFrame = frame.Frame

const DefaultMaxFrameMacroblocks = 36864

func updateQP(current, delta int) int {
	qp := (current + delta) % 52
	if qp < 0 {
		qp += 52
	}
	return qp
}

func traceTotalCoeffFFmpegOrder(tc [16]int) [16]int {
	var out [16]int
	for blkIdx, v := range tc {
		pos := syntax.Blk4x4Row[blkIdx]*4 + syntax.Blk4x4Col[blkIdx]
		out[pos] = v
	}
	return out
}

// NewDecoder creates a new H.264 decoder.
func NewDecoder() *Decoder {
	return &Decoder{
		SPS:                 make(map[uint32]*nal.SPS),
		PPS:                 make(map[uint32]*nal.PPS),
		DPB:                 frame.NewDPB(16),
		maxLongTermFrameIdx: -1,
	}
}

// Decode accepts a complete Annex B buffer. A picture may contain multiple
// slices, but an incomplete final picture is an error, not a streaming buffer.
func (d *Decoder) Decode(data []byte) (frames []*frame.Frame, resultErr error) {
	units, err := nal.SplitNALUnitsChecked(data)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			d.abortPicture()
		}
	}()
	publish := func() error {
		if d.picture == nil {
			return nil
		}
		p := d.picture
		_, err := d.finishPicture()
		if err != nil {
			return err
		}
		// The batch API exposes its parameter maps. Reject invalid caller-set
		// crop geometry before publishing any reference or POC state.
		if _, err := p.frame.OutputView(); err != nil {
			return fmt.Errorf("crop: %w", err)
		}
		if err := d.commitPictureReferences(p); err != nil {
			return err
		}
		d.commitPicturePOC(p)
		// Marking may replace frame metadata, but does not change geometry.
		output, _ := p.frame.OutputView()
		frames = append(frames, output)
		d.picture, d.slice = nil, nil
		return nil
	}
	for _, unit := range units {
		// H.264 7.4.1.2.3: these non-VCL units delimit access units too.
		// Prefix NALs (14) can occur between base-layer slices; ignore them
		// without forcing picture completion.
		switch unit.Type {
		case nal.TypeSEI, nal.TypeSPS, nal.TypePPS, 15, 16, 17, 18:
			if err := publish(); err != nil {
				return nil, err
			}
		}
		if d.MaxFrames > 0 && len(frames) >= d.MaxFrames {
			break
		}
		switch unit.Type {
		case nal.TypeSPS:
			sps, err := nal.ParseSPS(unit.Payload)
			if err != nil {
				return nil, fmt.Errorf("SPS: %w", err)
			}
			d.SPS[sps.SPSID] = sps
		case nal.TypePPS:
			pps, err := nal.ParsePPS(unit.Payload)
			if err != nil {
				return nil, fmt.Errorf("PPS: %w", err)
			}
			d.PPS[pps.PPSID] = pps
		case nal.TypeSliceIDR, nal.TypeSliceNonIDR:
			slice, err := d.parseSlice(unit)
			if err != nil {
				return nil, fmt.Errorf("slice: %w", err)
			}
			if d.picture != nil && d.picture.identity != identifyPicture(slice) {
				if err := publish(); err != nil {
					return nil, err
				}
			}
			if d.MaxFrames > 0 && len(frames) >= d.MaxFrames {
				break
			}
			d.traceFrameIndex = len(d.Frames) + len(frames)
			if err := d.addSlice(slice); err != nil {
				return nil, fmt.Errorf("slice: %w", err)
			}
		case nal.TypeAUD, 10, 11:
			if err := publish(); err != nil {
				return nil, err
			}
		case nal.TypeSlicePartA, nal.TypeSlicePartB, nal.TypeSlicePartC:
			return nil, fmt.Errorf("unsupported coded slice NAL type %d", unit.Type)
		default:
			// Other non-VCL contents, including auxiliary/extension slices and
			// application-defined NALs, do not participate in Annex A decoding.
		}
		if d.MaxFrames > 0 && d.picture != nil && len(frames)+1 == d.MaxFrames && d.picture.decoded == len(d.picture.mbSliceID) {
			if err := publish(); err != nil {
				return nil, err
			}
		}
		if d.MaxFrames > 0 && len(frames) >= d.MaxFrames {
			break
		}
	}
	if err := publish(); err != nil {
		return nil, err
	}
	d.Frames = append(d.Frames, frames...)
	return frames, nil
}

func (d *Decoder) traceMB(ev MBTraceEvent) {
	if d != nil && d.TraceMB != nil {
		d.TraceMB(ev)
	}
}

func finalIntra4x4Modes(modes []int8, mbW, mbX, mbY int) [16]int8 {
	var out [16]int8
	for i := range out {
		out[i] = -1
	}
	if mbW <= 0 || len(modes) == 0 {
		return out
	}
	stride := mbW * 4
	for blk := 0; blk < 16; blk++ {
		col := syntax.Blk4x4Col[blk]
		row := syntax.Blk4x4Row[blk]
		idx := (mbY*4+row)*stride + mbX*4 + col
		if idx >= 0 && idx < len(modes) {
			out[blk] = modes[idx]
		}
	}
	return out
}

// decodeSlice is the single-slice helper used by focused reconstruction tests.
func (d *Decoder) decodeSlice(unit nal.Unit) (*frame.Frame, error) {
	slice, err := d.parseSlice(unit)
	if err != nil {
		return nil, err
	}
	if err := d.addSlice(slice); err != nil {
		d.abortPicture()
		return nil, err
	}
	f, err := d.finishPicture()
	if err != nil {
		d.abortPicture()
	} else {
		d.picture, d.slice = nil, nil
	}
	return f, err
}

func (d *Decoder) decodeSliceData(slice *sliceState) (resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = fmt.Errorf("decode panic: %v", recovered)
		}
	}()
	unit := slice.unit
	hdr, r, sps, pps := slice.header, slice.reader, slice.sps, slice.pps
	isIntra := hdr.IsIntra()
	qp := hdr.QP(pps.PicInitQP)
	d.chromaQPOffset = int(pps.ChromaQPIndexOffset)
	d.weightedPred = pps.WeightedPred && (hdr.SliceType == syntax.SliceTypeP || hdr.SliceType == syntax.SliceTypeSP) && hdr.WeightedTablePresent
	d.weightedBipredIDC = pps.WeightedBipredIDC
	d.lumaWeightDenom = hdr.LumaLog2WeightDenom
	d.lumaWeightL0 = hdr.LumaWeightL0
	d.lumaOffsetL0 = hdr.LumaOffsetL0
	d.chromaWeightDenom = hdr.ChromaLog2WeightDenom
	d.chromaWeightL0 = hdr.ChromaWeightL0
	d.chromaOffsetL0 = hdr.ChromaOffsetL0

	p := d.picture
	f := p.frame
	mbWidth, mbHeight := int(sps.PicWidthInMbs), int(sps.PicHeightInMapUnits)
	d.mbW, d.mbH, d.intraModes = mbWidth, mbHeight, p.intraModes
	d.activeL0Refs = nil
	if hdr.SliceType == syntax.SliceTypeP || hdr.SliceType == syntax.SliceTypeSP {
		var err error
		d.activeL0Refs, err = buildPReferenceList(p.referenceFrames, int(hdr.FrameNum), 1<<sps.Log2MaxFrameNum, int(hdr.NumRefIdxL0Active), hdr.RefModifications[0])
		if err != nil {
			return err
		}
	}
	maxMBs := mbWidth * mbHeight
	currentQP := int(qp)
	nzCtx, chromaNZCtx := p.nzCtx, p.chromaNZCtx
	cbpCtx, mbTypeCtx := p.cbpCtx, p.mbTypeCtx
	nonSkipCtx, transform8x8Ctx := p.nonSkipCtx, p.transform8x8Ctx
	chromaPredModeCtx, mbQPCtx := p.chromaPredModeCtx, p.mbQPCtx
	mbIsIntraCtx := p.mbIsIntra
	intra8x8Stride := mbWidth * 2
	intra8x8ModeCtx := p.intra8x8ModeCtx
	intra8x8RightCtx, intra8x8BottomCtx := p.intra8x8RightCtx, p.intra8x8BottomCtx
	writeBackIntraPredModes := func(mb *syntax.MBIntra, mbX, mbY int) {
		if mb == nil {
			return
		}
		for b := 0; b < 4; b++ {
			bc, br := b%2, b/2
			idx8 := (mbY*2+br)*intra8x8Stride + (mbX*2 + bc)
			if mb.Use8x8Transform {
				mode := mb.I8x8PredMode[b]
				intra8x8ModeCtx[idx8] = mode
				intra8x8RightCtx[idx8] = mode
				intra8x8BottomCtx[idx8] = mode
				for dr := 0; dr < 2; dr++ {
					for dc := 0; dc < 2; dc++ {
						bX := mbX*4 + bc*2 + dc
						bY := mbY*4 + br*2 + dr
						if bX < d.mbW*4 && bY < d.mbH*4 {
							d.intraModes[bY*d.mbW*4+bX] = mode
						}
					}
				}
				continue
			}

			minMode := int8(8)
			for dr := 0; dr < 2; dr++ {
				for dc := 0; dc < 2; dc++ {
					bX := mbX*4 + bc*2 + dc
					bY := mbY*4 + br*2 + dr
					if bX < d.mbW*4 && bY < d.mbH*4 {
						m := d.intraModes[bY*d.mbW*4+bX]
						if m >= 0 && m < minMode {
							minMode = m
						}
					}
				}
			}
			if minMode > 8 {
				minMode = 2
			}
			intra8x8ModeCtx[idx8] = minMode
			rightMode := minMode
			rightIdx := (mbY*4+br*2)*d.mbW*4 + mbX*4 + bc*2 + 1
			if rightIdx >= 0 && rightIdx < len(d.intraModes) && d.intraModes[rightIdx] >= 0 {
				rightMode = d.intraModes[rightIdx]
			}
			intra8x8RightCtx[idx8] = rightMode
			bottomMode := minMode
			bottomIdx := (mbY*4+br*2+1)*d.mbW*4 + mbX*4 + bc*2
			if bottomIdx >= 0 && bottomIdx < len(d.intraModes) && d.intraModes[bottomIdx] >= 0 {
				bottomMode = d.intraModes[bottomIdx]
			}
			intra8x8BottomCtx[idx8] = bottomMode
		}
	}
	bmc := p.motion
	mbFFTypeCtx := p.mbFFTypeCtx
	skipRun := 0
	decodeAfterSkipRun := false
	var cabacDec *cabac.CABACDecoder
	var cabacModels []cabac.CABACCtx
	cabacLastQScaleDiff := 0
	if pps.EntropyCodingMode == 1 {
		// FFmpeg realigns the parsed slice-header bitstream before CABAC init.
		// CABAC arithmetic bytes are byte-aligned after cabac_alignment_one_bit;
		// starting the arithmetic decoder mid-byte desynchronizes every bin.
		if os.Getenv("GO264_HEADER_TRACE") != "" {
			fmt.Fprintf(os.Stderr, "GOHEADER_CABAC_INIT pos=%d poc=%d frame=%d slice=%d qp=%d initIDC=%d refL0=%d refL1=%d directSpatial=%d firstMB=%d modsL0=%d modsL1=%d\n", r.Position(), f.POC, hdr.FrameNum, hdr.SliceType, currentQP, hdr.CabacInitIDC, hdr.NumRefIdxL0Active, hdr.NumRefIdxL1Active, boolInt(hdr.DirectSpatialMvPred), hdr.FirstMbInSlice, len(hdr.RefModifications[0]), len(hdr.RefModifications[1]))
		}
		for !r.ByteAligned() {
			if r.ReadBit() != 1 {
				r.Fail(fmt.Errorf("%w: invalid cabac_alignment_one_bit", nal.ErrInvalidSyntax))
			}
		}
		if err := r.Err(); err != nil {
			return err
		}
		cabacDec = &cabac.CABACDecoder{}
		cabacDec.SetReader(r)
		cabacDec.UseFF = true
		cabacDec.Reset()
		if os.Getenv("GO264_P_BIN_TRACE") != "" && hdr.SliceType == syntax.SliceTypeP && f.POC == 12 {
			cabacDec.BinTrace = 30
		}
		cabacModels = cabac.InitContextModels(currentQP, int(hdr.CabacInitIDC), isIntra)
	}
	traceBState := func(mbIdx, mbX, mbY int, kind string) {
		if os.Getenv("GO264_B_STATE_TRACE") == "" || cabacDec == nil {
			return
		}
		low, rng, _ := cabacDec.DebugState()
		fmt.Fprintf(os.Stderr, "GOBSTATE mb=%04d x=%02d y=%02d poc=%d kind=%s low=%d range=%d\n", mbIdx, mbX, mbY, f.POC, kind, low, rng)
	}
	traceBCABAC := func(mbIdx, mbX, mbY int, mb *syntax.MBBidi, intra *syntax.MBIntra, skipped bool, qp int) {
		if os.Getenv("GO264_B_CABAC_TRACE") == "" {
			return
		}
		if skipped {
			fmt.Fprintf(os.Stderr, "GOCABACB mb=%04d x=%02d y=%02d poc=%d kind=B_SKIP raw=%d type=%d skip=1 cbp=00 qpd=0 qp=%d 8x8=0 tc=[0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0]\n",
				mbIdx, mbX, mbY, f.POC, mb.MBType, ffBidiMBType(mb), qp)
			return
		}
		if intra != nil {
			tc := traceTotalCoeffFFmpegOrder(intra.TotalCoeff)
			fmt.Fprintf(os.Stderr, "GOCABACB mb=%04d x=%02d y=%02d poc=%d kind=I type=%d skip=0 cbp=%02x qpd=%d qp=%d 8x8=%d tc=[%d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d]\n",
				mbIdx, mbX, mbY, f.POC, cabacMBTypeFlag(intra.MBType), intra.CodedBlockPattern, intra.QPDelta, qp, boolInt(intra.Use8x8Transform),
				tc[0], tc[1], tc[2], tc[3], tc[4], tc[5], tc[6], tc[7], tc[8], tc[9], tc[10], tc[11], tc[12], tc[13], tc[14], tc[15])
			return
		}
		if mb != nil {
			tc := traceTotalCoeffFFmpegOrder(mb.TotalCoeff)
			fmt.Fprintf(os.Stderr, "GOCABACB mb=%04d x=%02d y=%02d poc=%d kind=B raw=%d type=%d skip=0 cbp=%02x qpd=%d qp=%d 8x8=%d tc=[%d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d]\n",
				mbIdx, mbX, mbY, f.POC, mb.MBType, ffBidiMBType(mb), mb.CBP, mb.QPDelta, qp, boolInt(mb.Use8x8Transform),
				tc[0], tc[1], tc[2], tc[3], tc[4], tc[5], tc[6], tc[7], tc[8], tc[9], tc[10], tc[11], tc[12], tc[13], tc[14], tc[15])
		}
	}

	decodedMBs := 0
	terminated := false
	endSlice := func() bool {
		terminated = cabacDec.DecodeTerminate() == 1
		return terminated
	}
	for mbIdx := int(hdr.FirstMbInSlice); mbIdx < maxMBs; mbIdx++ {
		if slice.referenceErr != nil {
			return slice.referenceErr
		}
		if err := r.Err(); err != nil {
			return err
		}
		if pps.EntropyCodingMode == 0 && skipRun == 0 && !moreSliceData(r) {
			break
		}
		if p.mbSliceID[mbIdx] != -1 {
			return fmt.Errorf("overlapping slice at macroblock %d", mbIdx)
		}
		decodedMBs = mbIdx + 1
		p.mbSliceID[mbIdx] = slice.id
		mbX := mbIdx % mbWidth
		mbY := mbIdx / mbWidth
		pcm := false
		currentMVPPOC = f.POC
		predMV := bmc.predictSkipL0(mbX*4, mbY*4)
		directRefL0, directMVL0 := int8(0), predMV
		directRefL1, directMVL1 := int8(-1), syntax.MotionVector{}
		applyDirectSpatial := hdr.DirectSpatialMvPred
		if applyDirectSpatial {
			directRefL0, directMVL0 = bmc.predictDirectSpatial(0, mbX*4, mbY*4, f.POC)
			directRefL1, directMVL1 = bmc.predictDirectSpatial(1, mbX*4, mbY*4, f.POC)
			if directRefL0 < 0 && directRefL1 < 0 {
				// FFmpeg spatial Direct promotes fully unavailable neighbour refs to
				// bidirectional ref0/ref0 with zero motion. Leaving both refs at -1
				// prevents later Direct MBs from seeing valid top/left ref caches.
				directRefL0, directRefL1 = 0, 0
				directMVL0, directMVL1 = syntax.MotionVector{}, syntax.MotionVector{}
			}
			if os.Getenv("GO264_DIRECT_CTX_TRACE") != "" {
				a0, ar0 := bmc.get(0, mbX*4-1, mbY*4)
				b0, br0 := bmc.get(0, mbX*4, mbY*4-1)
				c0, cr0 := bmc.get(0, mbX*4+4, mbY*4-1)
				if cr0 == -2 {
					c0, cr0 = bmc.get(0, mbX*4-1, mbY*4-1)
				}
				a1, ar1 := bmc.get(1, mbX*4-1, mbY*4)
				b1, br1 := bmc.get(1, mbX*4, mbY*4-1)
				c1, cr1 := bmc.get(1, mbX*4+4, mbY*4-1)
				if cr1 == -2 {
					c1, cr1 = bmc.get(1, mbX*4-1, mbY*4-1)
				}
				fmt.Fprintf(os.Stderr, "GODIRECTCTX mb=%04d x=%02d y=%02d poc=%d ref0=%d mv0={%d,%d} ref1=%d mv1={%d,%d} A0=%d/{%d,%d} B0=%d/{%d,%d} C0=%d/{%d,%d} A1=%d/{%d,%d} B1=%d/{%d,%d} C1=%d/{%d,%d}\n", mbIdx, mbX, mbY, f.POC, directRefL0, directMVL0.X, directMVL0.Y, directRefL1, directMVL1.X, directMVL1.Y, ar0, a0.X, a0.Y, br0, b0.X, b0.Y, cr0, c0.X, c0.Y, ar1, a1.X, a1.Y, br1, b1.X, b1.Y, cr1, c1.X, c1.Y)
			}
			if directRefL1 < 0 {
				directMVL1 = syntax.MotionVector{}
			}
			if directRefL0 < 0 {
				directMVL0 = syntax.MotionVector{}
			}
		} else if hdr.DirectSpatialMvPred && bmc.directSpatialL0Ref(mbX*4, mbY*4) < 0 {
			directRefL1 = 0
		}

		var leftNZ, topNZ *[16]int
		var leftChromaNZ, topChromaNZ *[2][4]int
		var leftCBP, topCBP uint32
		var leftMBType, topMBType uint32
		var leftNonSkip, topNonSkip bool
		leftIsDirect, topIsDirect := true, true
		transform8x8CABACCtx := 0
		var leftChromaPred, topChromaPred int8
		leftAvailable := mbX > 0 && p.mbSliceID[mbIdx-1] == slice.id
		topAvailable := mbY > 0 && p.mbSliceID[mbIdx-mbWidth] == slice.id
		if leftAvailable {
			leftNZ = &nzCtx[mbIdx-1]
			leftChromaNZ = &chromaNZCtx[mbIdx-1]
			leftCBP = cabacLeftCBPForCurrent(cbpCtx[mbIdx-1])
			leftMBType = mbTypeCtx[mbIdx-1]
			leftNonSkip = nonSkipCtx[mbIdx-1]
			leftIsDirect = mbFFTypeCtx[mbIdx-1] == ffBidiMBType(&syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16})
			if transform8x8Ctx[mbIdx-1] {
				transform8x8CABACCtx++
			}
			leftChromaPred = chromaPredModeCtx[mbIdx-1]
		}
		if topAvailable {
			topNZ = &nzCtx[mbIdx-mbWidth]
			topChromaNZ = &chromaNZCtx[mbIdx-mbWidth]
			topCBP = cbpCtx[mbIdx-mbWidth]
			topMBType = mbTypeCtx[mbIdx-mbWidth]
			topNonSkip = nonSkipCtx[mbIdx-mbWidth]
			topIsDirect = mbFFTypeCtx[mbIdx-mbWidth] == ffBidiMBType(&syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16})
			if transform8x8Ctx[mbIdx-mbWidth] {
				transform8x8CABACCtx++
			}
			topChromaPred = chromaPredModeCtx[mbIdx-mbWidth]
		}

		if isIntra {
			if pps.EntropyCodingMode == 1 && cabacUseFFmpegEdgeContexts() {
				leftCBP, topCBP = cabacUnavailableCBP(leftCBP, topCBP, boolInt(leftAvailable), boolInt(topAvailable), true)
				leftNZ, topNZ = cabacTraceEdgeNZ(boolInt(leftAvailable), boolInt(topAvailable), leftNZ, topNZ)
				leftChromaNZ, topChromaNZ = cabacTraceEdgeChromaNZ(boolInt(leftAvailable), boolInt(topAvailable), leftChromaNZ, topChromaNZ)
			}
			var mb *syntax.MBIntra
			var leftEdge8x8, topEdge8x8 [2]int8
			for i := range leftEdge8x8 {
				leftEdge8x8[i] = -1
				topEdge8x8[i] = -1
			}
			if pps.EntropyCodingMode == 1 {
				for br := 0; br < 2; br++ {
					if mbX > 0 && d.intraNeighborAvailable(mbIdx, mbIdx-1) {
						leftEdge8x8[br] = intra8x8RightCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftEdge8x8[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 && d.intraNeighborAvailable(mbIdx, mbIdx-mbWidth) {
						topEdge8x8[bc] = intra8x8BottomCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
					} else {
						topEdge8x8[bc] = -1
					}
				}
				mb = decodeCABACIntraMB(cabacDec, cabacModels, cabacLastQScaleDiff, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftMBType, topMBType, leftChromaPred, topChromaPred, pps.Transform8x8Mode, transform8x8CABACCtx, leftEdge8x8, topEdge8x8)
				cabacLastQScaleDiff = int(mb.QPDelta)
				currentQP = updateQP(currentQP, int(mb.QPDelta))
			} else {
				mb = syntax.DecodeMBIntra(r, syntax.IntraDecodeOpts{
					SliceQP: int32(currentQP), Transform8x8: pps.Transform8x8Mode,
					LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
				})
				currentQP = updateQP(currentQP, int(mb.QPDelta))
			}
			d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
			pcm = mb.MBType == syntax.MBTypeIPCM
			mbIsIntraCtx[mbIdx] = true
			nzCtx[mbIdx] = mb.TotalCoeff
			chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
			cbpCtx[mbIdx] = mb.CodedBlockPattern
			mbTypeCtx[mbIdx] = cabacMBTypeFlag(mb.MBType)
			nonSkipCtx[mbIdx] = true
			transform8x8Ctx[mbIdx] = mb.Use8x8Transform
			chromaPredModeCtx[mbIdx] = mb.ChromaPredMode
			var traceI8x8Pred [4]int8
			for i := range traceI8x8Pred {
				traceI8x8Pred[i] = -1
			}
			if pps.EntropyCodingMode == 1 && mb.Use8x8Transform {
				for b := 0; b < 4; b++ {
					bc, br := b%2, b/2
					leftMode := int8(2)
					if bc == 0 {
						leftMode = leftEdge8x8[br]
					} else {
						leftMode = mb.I8x8PredMode[b-1]
					}
					topMode := int8(2)
					if br == 0 {
						topMode = topEdge8x8[bc]
					} else {
						topMode = mb.I8x8PredMode[b-2]
					}
					traceI8x8Pred[b] = cabacPredIntraMode(leftMode, topMode)
				}
			}
			writeBackIntraPredModes(mb, mbX, mbY)
			bmc.writeBackIntra(mbX, mbY)
			mbQPCtx[mbIdx] = currentQP
			if pcm {
				mbQPCtx[mbIdx] = 0
			}
			d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: pps.EntropyCodingMode == 1, Kind: "I", MBType: mb.MBType, CBP: mb.CodedBlockPattern, QPDelta: mb.QPDelta, QP: currentQP, Use8x8: mb.Use8x8Transform, ChromaPred: mb.ChromaPredMode, Intra4x4Mode: mb.IntraPredMode, Intra4x4PredMode: d.traceIntra4x4PredMode, Intra4x4FinalMode: finalIntra4x4Modes(d.intraModes, d.mbW, mbX, mbY), Intra8x8Mode: mb.I8x8PredMode, Intra8x8PredMode: traceI8x8Pred, Intra8x8LeftEdge: leftEdge8x8, Intra8x8TopEdge: topEdge8x8, TotalCoeff: traceTotalCoeffFFmpegOrder(mb.TotalCoeff), ChromaCoeff: mb.ChromaTotalCoeff})
			if pps.EntropyCodingMode == 1 && endSlice() {
				break
			}

		} else if hdr.SliceType == syntax.SliceTypeP {
			if pps.EntropyCodingMode == 1 {
				var leftEdge8x8, topEdge8x8 [2]int8
				for br := 0; br < 2; br++ {
					if mbX > 0 && d.intraNeighborAvailable(mbIdx, mbIdx-1) {
						leftEdge8x8[br] = intra8x8RightCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftEdge8x8[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 && d.intraNeighborAvailable(mbIdx, mbIdx-mbWidth) {
						topEdge8x8[bc] = intra8x8BottomCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
					} else {
						topEdge8x8[bc] = -1
					}
				}
				mbInter, mbIntra, skipped := bmc.decodeCABACPInterMB(cabacDec, cabacModels, hdr.NumRefIdxL0Active, cabacLastQScaleDiff, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftNonSkip, topNonSkip, mbX, mbY, f.POC, pps.Transform8x8Mode, transform8x8CABACCtx, leftMBType, topMBType, leftChromaPred, topChromaPred, leftEdge8x8, topEdge8x8)
				if skipped {
					cabacLastQScaleDiff = 0
					skipMV := predMV
					mbInter.MV[0] = skipMV
					d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
					nonSkipCtx[mbIdx] = false
					transform8x8Ctx[mbIdx] = false
					bmc.writeBackInterL0(mbX, mbY, mbInter)
					mbFFTypeCtx[mbIdx] = ffInterMBType(mbInter)
					if os.Getenv("GO264_P_STATE_TRACE") != "" {
						low, rng, _ := cabacDec.DebugState()
						fmt.Fprintf(os.Stderr, "GOPSTATE mb=%04d x=%02d y=%02d poc=%d kind=skip low=%d range=%d\n", mbIdx, mbX, mbY, f.POC, low, rng)
					}
					mbQPCtx[mbIdx] = currentQP
					d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: true, Kind: "P_SKIP", MBType: mbInter.MBType, QP: currentQP, Skipped: true, RefIdx: mbInter.RefIdx, MV: mbInter.MV})
					if endSlice() {
						break
					}
					continue
				}
				if mbIntra != nil {
					cabacLastQScaleDiff = int(mbIntra.QPDelta)
					currentQP = updateQP(currentQP, int(mbIntra.QPDelta))
					d.reconstructMB(f, mbIntra, mbX, mbY, currentQP, sps)
					pcm = mbIntra.MBType == syntax.MBTypeIPCM
					mbIsIntraCtx[mbIdx] = true
					nzCtx[mbIdx] = mbIntra.TotalCoeff
					chromaNZCtx[mbIdx] = mbIntra.ChromaTotalCoeff
					cbpCtx[mbIdx] = mbIntra.CodedBlockPattern
					mbTypeCtx[mbIdx] = cabacMBTypeFlag(mbIntra.MBType)
					nonSkipCtx[mbIdx] = true
					transform8x8Ctx[mbIdx] = mbIntra.Use8x8Transform
					chromaPredModeCtx[mbIdx] = mbIntra.ChromaPredMode
					writeBackIntraPredModes(mbIntra, mbX, mbY)
					bmc.writeBackIntra(mbX, mbY)
					mbQPCtx[mbIdx] = currentQP
					if pcm {
						mbQPCtx[mbIdx] = 0
					}
					d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: true, Kind: "P_INTRA", MBType: mbIntra.MBType, CBP: mbIntra.CodedBlockPattern, QPDelta: mbIntra.QPDelta, QP: currentQP, Use8x8: mbIntra.Use8x8Transform, ChromaPred: mbIntra.ChromaPredMode, Intra4x4Mode: mbIntra.IntraPredMode, Intra4x4FinalMode: finalIntra4x4Modes(d.intraModes, d.mbW, mbX, mbY), Intra8x8Mode: mbIntra.I8x8PredMode, TotalCoeff: traceTotalCoeffFFmpegOrder(mbIntra.TotalCoeff), ChromaCoeff: mbIntra.ChromaTotalCoeff})
					if endSlice() {
						break
					}
					continue
				}
				bmc.applyInterMVPredictors(mbInter, mbX, mbY, f.POC)
				cabacLastQScaleDiff = int(mbInter.QPDelta)
				currentQP = updateQP(currentQP, int(mbInter.QPDelta))
				d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				cbpCtx[mbIdx] = mbInter.CBP
				mbTypeCtx[mbIdx] = 0
				nonSkipCtx[mbIdx] = true
				transform8x8Ctx[mbIdx] = mbInter.Use8x8Transform
				bmc.writeBackInterL0(mbX, mbY, mbInter)
				mbFFTypeCtx[mbIdx] = ffInterMBType(mbInter)
				if os.Getenv("GO264_P_STATE_TRACE") != "" {
					low, rng, _ := cabacDec.DebugState()
					fmt.Fprintf(os.Stderr, "GOPSTATE mb=%04d x=%02d y=%02d poc=%d kind=inter low=%d range=%d\n", mbIdx, mbX, mbY, f.POC, low, rng)
				}
				if os.Getenv("GO264_P_CABAC_TRACE") != "" {
					tc := traceTotalCoeffFFmpegOrder(mbInter.TotalCoeff)
					fmt.Fprintf(os.Stderr, "GOCABAC mb=%04d x=%02d y=%02d poc=%d frame=%d kind=P type=%d skip=0 cbp=%02x qpd=%d qp=%d 8x8=%d tc=[%d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d]\n",
						mbIdx, mbX, mbY, f.POC, hdr.FrameNum, ffInterMBType(mbInter), mbInter.CBP, mbInter.QPDelta, currentQP, boolInt(mbInter.Use8x8Transform),
						tc[0], tc[1], tc[2], tc[3], tc[4], tc[5], tc[6], tc[7], tc[8], tc[9], tc[10], tc[11], tc[12], tc[13], tc[14], tc[15])
				}
				mbQPCtx[mbIdx] = currentQP
				d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: true, Kind: "P", MBType: mbInter.MBType, SubMBType: mbInter.SubMBType, CBP: mbInter.CBP, QPDelta: mbInter.QPDelta, QP: currentQP, Use8x8: mbInter.Use8x8Transform, RefIdx: mbInter.RefIdx, MV: mbInter.MV, SubMV: mbInter.SubMV, TotalCoeff: traceTotalCoeffFFmpegOrder(mbInter.TotalCoeff), ChromaCoeff: mbInter.ChromaTotalCoeff})
				if endSlice() {
					break
				}
				continue
			}
			// CAVLC P slices carry mb_skip_run before the next non-skipped MB.
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 && !decodeAfterSkipRun {
					skipRun = int(r.ReadUEBounded(uint32(maxMBs - mbIdx)))
				}
				if skipRun > 0 {
					if hdr.SliceType == syntax.SliceTypeB {
						d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, RefIdxL1: [4]int8{-1, -1, -1, -1}}, mbX, mbY, currentQP)
					} else {
						skipMV := predMV
						mbSkip := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
						mbSkip.MV[0] = skipMV
						d.reconstructMBInter(f, mbSkip, mbX, mbY, currentQP)
						bmc.writeBackInterL0(mbX, mbY, mbSkip)
						mbFFTypeCtx[mbIdx] = ffInterMBType(mbSkip)
					}
					// Skipped macroblocks inherit QP and still participate in deblocking.
					mbQPCtx[mbIdx] = currentQP
					skipRun--
					decodeAfterSkipRun = skipRun == 0
					continue
				}
				decodeAfterSkipRun = false
			}
			mbInter := syntax.DecodeMBInter(r, syntax.InterDecodeOpts{
				SliceQP: int32(currentQP), NumRefFrames: hdr.NumRefIdxL0Active, Transform8x8: pps.Transform8x8Mode,
				LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
			})
			if mbInter.MBType >= syntax.PMBTypeIntra {
				mb := syntax.DecodeMBIntraWithType(r, mbInter.MBType-syntax.PMBTypeIntra, syntax.IntraDecodeOpts{
					SliceQP: int32(currentQP), Transform8x8: pps.Transform8x8Mode,
					LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
				})
				currentQP = updateQP(currentQP, int(mb.QPDelta))
				d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
				pcm = mb.MBType == syntax.MBTypeIPCM
				mbIsIntraCtx[mbIdx] = true
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
				bmc.writeBackIntra(mbX, mbY)
			} else {
				bmc.applyInterMVPredictors(&mbInter, mbX, mbY, f.POC)
				currentQP = updateQP(currentQP, int(mbInter.QPDelta))
				d.reconstructMBInter(f, &mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				bmc.writeBackInterL0(mbX, mbY, &mbInter)
				mbFFTypeCtx[mbIdx] = ffInterMBType(&mbInter)
			}
		} else {
			// B-slice
			if pps.EntropyCodingMode == 1 {
				// CABAC B-slice: dedicated decoder, avoids CAVLC desynchronization.
				var leftE8B, topE8B [2]int8
				for br := 0; br < 2; br++ {
					if mbX > 0 && d.intraNeighborAvailable(mbIdx, mbIdx-1) {
						leftE8B[br] = intra8x8RightCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftE8B[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 && d.intraNeighborAvailable(mbIdx, mbIdx-mbWidth) {
						topE8B[bc] = intra8x8BottomCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
					} else {
						topE8B[bc] = -1
					}
				}
				colFrame := d.refBidiL1(0, f.POC)
				if applyDirectSpatial {
					colFrame = d.refBidiL1DirectColocated(0, f.POC)
				}
				colPOC := 0
				if colFrame != nil {
					colPOC = colFrame.FullPOC
				}
				mbBidi, mbIntra, skipped := bmc.decodeCABACBidiMB(
					cabacDec, cabacModels,
					hdr.NumRefIdxL0Active, hdr.NumRefIdxL1Active,
					cabacLastQScaleDiff,
					leftNZ, topNZ, leftChromaNZ, topChromaNZ,
					leftCBP, topCBP,
					leftNonSkip, topNonSkip,
					leftIsDirect, topIsDirect,
					mbX, mbY,
					f.FullPOC,
					hdr.DirectSpatialMvPred,
					directRefL0, directMVL0,
					directRefL1, directMVL1,
					colFrame, d.bidiL0FramesWithMods(f.POC, hdr.FrameNum, hdr.RefModifications[0]), colPOC,
					pps.Transform8x8Mode, transform8x8CABACCtx,
					leftMBType, topMBType,
					leftChromaPred, topChromaPred,
					leftE8B, topE8B,
				)
				if skipped {
					// B_Direct_16x16 skip: QP unchanged, lastQScaleDiff resets to 0.
					cabacLastQScaleDiff = 0
					mbBidi.DirectSpatial = hdr.DirectSpatialMvPred
					if applyDirectSpatial {
						bmc.applyDirectSpatial(mbX, mbY, mbBidi, directRefL0, directMVL0, directRefL1, directMVL1, d.refBidiL1DirectColocated(0, f.POC))
					} else {
						colFrame := d.refBidiL1(0, f.POC)
						colPOC := 0
						if colFrame != nil {
							colPOC = colFrame.FullPOC
						}
						bmc.applyDirectTemporal(mbX, mbY, mbBidi, colFrame, f.FullPOC, d.bidiL0FramesWithMods(f.POC, hdr.FrameNum, hdr.RefModifications[0]), colPOC)
					}
					d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
					nzCtx[mbIdx] = mbBidi.TotalCoeff
					chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
					cbpCtx[mbIdx] = 0
					mbTypeCtx[mbIdx] = 0
					mbFFTypeCtx[mbIdx] = ffBidiMBType(mbBidi)
					nonSkipCtx[mbIdx] = false
					transform8x8Ctx[mbIdx] = false
					// Direct skip still contributes motion/ref context for later B macroblocks.
					// FFmpeg writes both spatial and temporal Direct results back into the
					// per-list caches; leaving temporal Direct skips out makes subsequent
					// MVP/ref_idx context see stale zeros.
					bmc.writeBackBidi(mbX, mbY, f.POC, mbBidi)
					mbQPCtx[mbIdx] = currentQP
					traceBCABAC(mbIdx, mbX, mbY, mbBidi, nil, true, currentQP)
					traceBState(mbIdx, mbX, mbY, "skip")
					if endSlice() {
						if os.Getenv("GO264_CABAC_TERMINATE_TRACE") != "" {
							fmt.Fprintf(os.Stderr, "GOTERMINATE mb=%04d x=%02d y=%02d poc=%d skipped=1\n", mbIdx, mbX, mbY, f.POC)
						}
						break
					}
					continue
				}
				if mbIntra != nil {
					cabacLastQScaleDiff = int(mbIntra.QPDelta)
					currentQP = updateQP(currentQP, int(mbIntra.QPDelta))
					d.reconstructMB(f, mbIntra, mbX, mbY, currentQP, sps)
					pcm = mbIntra.MBType == syntax.MBTypeIPCM
					mbIsIntraCtx[mbIdx] = true
					nzCtx[mbIdx] = mbIntra.TotalCoeff
					chromaNZCtx[mbIdx] = mbIntra.ChromaTotalCoeff
					cbpCtx[mbIdx] = mbIntra.CodedBlockPattern
					mbTypeCtx[mbIdx] = cabacMBTypeFlag(mbIntra.MBType)
					nonSkipCtx[mbIdx] = true
					transform8x8Ctx[mbIdx] = mbIntra.Use8x8Transform
					chromaPredModeCtx[mbIdx] = mbIntra.ChromaPredMode
					writeBackIntraPredModes(mbIntra, mbX, mbY)
					bmc.writeBackIntra(mbX, mbY)
				} else {
					cabacLastQScaleDiff = int(mbBidi.QPDelta)
					currentQP = updateQP(currentQP, int(mbBidi.QPDelta))
					mbBidi.DirectSpatial = hdr.DirectSpatialMvPred
					if mbBidi.MBType == syntax.BMBTypeDirect16x16 {
						if applyDirectSpatial {
							bmc.applyDirectSpatial(mbX, mbY, mbBidi, directRefL0, directMVL0, directRefL1, directMVL1, d.refBidiL1DirectColocated(0, f.POC))
						} else {
							colFrame := d.refBidiL1(0, f.POC)
							colPOC := 0
							if colFrame != nil {
								colPOC = colFrame.FullPOC
							}
							bmc.applyDirectTemporal(mbX, mbY, mbBidi, colFrame, f.FullPOC, d.bidiL0FramesWithMods(f.POC, hdr.FrameNum, hdr.RefModifications[0]), colPOC)
						}
					} else if mbBidi.MBType == syntax.BMBTypeB8x8 {
						if applyDirectSpatial {
							bmc.applyDirectSpatial(mbX, mbY, mbBidi, directRefL0, directMVL0, directRefL1, directMVL1, d.refBidiL1DirectColocated(0, f.POC))
						} else {
							colFrame := d.refBidiL1(0, f.POC)
							colPOC := 0
							if colFrame != nil {
								colPOC = colFrame.FullPOC
							}
							bmc.applyDirectTemporal(mbX, mbY, mbBidi, colFrame, f.FullPOC, d.bidiL0FramesWithMods(f.POC, hdr.FrameNum, hdr.RefModifications[0]), colPOC)
						}
					}
					d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
					nzCtx[mbIdx] = mbBidi.TotalCoeff
					chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
					cbpCtx[mbIdx] = mbBidi.CBP
					mbTypeCtx[mbIdx] = 0 // inter B
					mbFFTypeCtx[mbIdx] = ffBidiMBType(mbBidi)
					nonSkipCtx[mbIdx] = true
					transform8x8Ctx[mbIdx] = mbBidi.Use8x8Transform
					// Write back 4×4 MV/ref contexts for future MVP/ref_idx context. FFmpeg keeps
					// separate list caches; B_8x8 and two-part B MBs must fill the same shaped
					// regions, not just MVL0[0] over the whole macroblock.
					bmc.writeBackBidi(mbX, mbY, f.POC, mbBidi)
				}
				mbQPCtx[mbIdx] = currentQP
				if pcm {
					mbQPCtx[mbIdx] = 0
				}
				traceBCABAC(mbIdx, mbX, mbY, mbBidi, mbIntra, false, currentQP)
				if mbIntra != nil {
					traceBState(mbIdx, mbX, mbY, "intra")
				} else if mbBidi != nil && mbBidi.MBType == syntax.BMBTypeDirect16x16 {
					traceBState(mbIdx, mbX, mbY, "direct")
				} else {
					traceBState(mbIdx, mbX, mbY, "inter")
				}
				if endSlice() {
					if os.Getenv("GO264_CABAC_TERMINATE_TRACE") != "" {
						fmt.Fprintf(os.Stderr, "GOTERMINATE mb=%04d x=%02d y=%02d poc=%d skipped=0\n", mbIdx, mbX, mbY, f.POC)
					}
					break
				}
				continue
			}
			// CAVLC B-slice path.
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 && !decodeAfterSkipRun {
					skipRun = int(r.ReadUEBounded(uint32(maxMBs - mbIdx)))
				}
				if skipRun > 0 {
					d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, DirectSpatial: hdr.DirectSpatialMvPred, RefIdxL1: [4]int8{-1, -1, -1, -1}}, mbX, mbY, currentQP)
					skipRun--
					decodeAfterSkipRun = skipRun == 0
					continue
				}
				decodeAfterSkipRun = false
			}
			mbBidi := syntax.DecodeMBBidiWithOpts(r, syntax.BidiDecodeOpts{
				SliceQP: int32(currentQP), NumRefL0: hdr.NumRefIdxL0Active, NumRefL1: hdr.NumRefIdxL1Active,
				Transform8x8: pps.Transform8x8Mode, Direct8x8Inference: sps.Direct8x8Inference,
				LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
			})
			if mbBidi.MBType >= syntax.BMBTypeIntra {
				mb := mbBidi.Intra
				if mb == nil {
					mb = &syntax.MBIntra{MBType: mbBidi.MBType - syntax.BMBTypeIntra}
				}
				currentQP = updateQP(currentQP, int(mb.QPDelta))
				d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
				pcm = mb.MBType == syntax.MBTypeIPCM
				mbIsIntraCtx[mbIdx] = true
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
				bmc.writeBackIntra(mbX, mbY)
				mbFFTypeCtx[mbIdx] = ffBidiMBType(mbBidi)
			} else {
				currentQP = updateQP(currentQP, int(mbBidi.QPDelta))
				mbBidi.DirectSpatial = hdr.DirectSpatialMvPred
				if mbBidi.MBType == syntax.BMBTypeDirect16x16 {
					if applyDirectSpatial {
						bmc.applyDirectSpatial(mbX, mbY, mbBidi, directRefL0, directMVL0, directRefL1, directMVL1, d.refBidiL1DirectColocated(0, f.POC))
					} else {
						colFrame := d.refBidiL1(0, f.POC)
						colPOC := 0
						if colFrame != nil {
							colPOC = colFrame.FullPOC
						}
						bmc.applyDirectTemporal(mbX, mbY, mbBidi, colFrame, f.FullPOC, d.bidiL0FramesWithMods(f.POC, hdr.FrameNum, hdr.RefModifications[0]), colPOC)
					}
				} else if applyDirectSpatial {
					bmc.applyDirectSpatial(mbX, mbY, mbBidi, directRefL0, directMVL0, directRefL1, directMVL1, d.refBidiL1DirectColocated(0, f.POC))
				}
				d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
				mbFFTypeCtx[mbIdx] = ffBidiMBType(mbBidi)
				nzCtx[mbIdx] = mbBidi.TotalCoeff
				chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
			}
		}
		// H.264 8.7.2 uses QP 0 for I_PCM filtering, without changing the
		// running slice QP used to decode subsequent macroblocks.
		mbQPCtx[mbIdx] = currentQP
		if pcm {
			mbQPCtx[mbIdx] = 0
		}
	}

	if err := r.Err(); err != nil {
		return err
	}
	if decodedMBs <= int(hdr.FirstMbInSlice) {
		return fmt.Errorf("empty slice")
	}
	if pps.EntropyCodingMode == 1 {
		if !terminated {
			return fmt.Errorf("CABAC end_of_slice_flag: %w", io.ErrUnexpectedEOF)
		}
	} else if err := r.ReadRBSPTrailingBits(); err != nil {
		return err
	}
	if slice.referenceErr != nil {
		return slice.referenceErr
	}
	p.decoded += decodedMBs - int(hdr.FirstMbInSlice)
	d.saveSlice(slice, int(hdr.FirstMbInSlice), decodedMBs)
	return slice.referenceErr
}

package decode

// decode/pipeline.go — Decoder type, Decode(), and the decodeSlice() main loop.

import (
	"fmt"

	cabac "github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/filter"
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
}

// DecodedFrame is an alias for frame.Frame for CLI convenience.
type DecodedFrame = frame.Frame

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
		SPS: make(map[uint32]*nal.SPS),
		PPS: make(map[uint32]*nal.PPS),
		DPB: frame.NewDPB(16),
	}
}

// Decode processes an Annex B bitstream and returns decoded frames.
func (d *Decoder) Decode(data []byte) ([]*frame.Frame, error) {
	units := nal.SplitNALUnits(data)
	var frames []*frame.Frame

	for _, unit := range units {
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
			if unit.Type == nal.TypeSliceIDR {
				d.DPB.Flush()
			}
			d.traceFrameIndex = len(d.Frames) + len(frames)
			f, err := d.decodeSlice(unit)
			if err != nil {
				return nil, fmt.Errorf("slice: %w", err)
			}
			if f != nil {
				frames = append(frames, f)
				d.DPB.Add(f)
			}

		case nal.TypeSEI, nal.TypeAUD:
			// Skip
		}
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

func (d *Decoder) decodeSlice(unit nal.Unit) (resultFrame *frame.Frame, resultErr error) {
	defer func() {
		if r := recover(); r != nil {
			resultErr = fmt.Errorf("decode panic: %v", r)
			resultFrame = nil
		}
	}()

	peek := nal.NewReader(unit.Payload)
	_ = peek.ReadUE() // first_mb_in_slice
	_ = peek.ReadUE() // slice_type
	ppsID := peek.ReadUE()
	pps := d.PPS[ppsID]
	if pps == nil {
		return nil, fmt.Errorf("PPS %d not available", ppsID)
	}
	sps := d.SPS[pps.SPSID]
	if sps == nil {
		return nil, fmt.Errorf("SPS %d not available", pps.SPSID)
	}

	hdr, r := syntax.ParseHeaderWithRefIDC(unit.Payload, unit.Type, unit.RefIDC, sps, pps)
	isIntra := hdr.IsIntra()
	qp := hdr.QP(pps.PicInitQP)
	d.chromaQPOffset = int(pps.ChromaQPIndexOffset)

	mbAlignedW := int(sps.PicWidthInMbs) * 16
	mbAlignedH := int(sps.PicHeightInMapUnits) * 16
	f := frame.NewFrame(mbAlignedW, mbAlignedH)
	f.Width = sps.Width
	f.Height = sps.Height
	f.IsIDR = unit.Type == nal.TypeSliceIDR
	f.IsRef = unit.RefIDC > 0
	f.FrameNum = int(hdr.FrameNum)
	f.POC = int(hdr.PicOrderCntLsb)

	mbWidth := int(sps.PicWidthInMbs)
	mbHeight := int(sps.PicHeightInMapUnits)
	d.mbW = mbWidth
	d.mbH = mbHeight
	d.intraModes = make([]int8, mbWidth*4*mbHeight*4)
	for i := range d.intraModes {
		d.intraModes[i] = 2
	}

	maxMBs := mbWidth * mbHeight
	if maxMBs > 10000 {
		maxMBs = 10000
	}
	currentQP := int(qp)
	nzCtx := make([][16]int, maxMBs)
	chromaNZCtx := make([][2][4]int, maxMBs)
	cbpCtx := make([]uint32, maxMBs)
	mbTypeCtx := make([]uint32, maxMBs)
	nonSkipCtx := make([]bool, maxMBs)
	transform8x8Ctx := make([]bool, maxMBs)
	chromaPredModeCtx := make([]int8, maxMBs)
	mbQPCtx := make([]int, maxMBs)
	mbIsIntraCtx := make([]bool, maxMBs)
	intra8x8Stride := mbWidth * 2
	intra8x8ModeCtx := make([]int8, intra8x8Stride*mbHeight*2)
	intra8x8RightCtx := make([]int8, intra8x8Stride*mbHeight*2)
	intra8x8BottomCtx := make([]int8, intra8x8Stride*mbHeight*2)
	for i := range intra8x8ModeCtx {
		intra8x8ModeCtx[i] = -1
		intra8x8RightCtx[i] = -1
		intra8x8BottomCtx[i] = -1
	}
	mv4Stride := mbWidth * 4
	mv4Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	mv4L1Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	mvd4Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	mvd4L1Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	ref4Ctx := make([]int8, mv4Stride*mbHeight*4)
	ref4L1Ctx := make([]int8, mv4Stride*mbHeight*4)
	for i := range ref4Ctx {
		ref4Ctx[i] = -2
		ref4L1Ctx[i] = -2
	}
	skipRun := 0
	decodeAfterSkipRun := false
	var cabacDec *cabac.CABACDecoder
	var cabacModels []cabac.CABACCtx
	cabacLastQScaleDiff := 0
	if pps.EntropyCodingMode == 1 {
		// FFmpeg realigns the parsed slice-header bitstream before CABAC init.
		// CABAC arithmetic bytes are byte-aligned after cabac_alignment_one_bit;
		// starting the arithmetic decoder mid-byte desynchronizes every bin.
		r.ByteAlign()
		cabacDec = cabac.NewCABACDecoder(r)
		cabacModels = cabac.InitContextModels(currentQP, int(hdr.CabacInitIDC), isIntra)
	}

	for mbIdx := int(hdr.FirstMbInSlice); mbIdx < maxMBs; mbIdx++ {
		mbX := mbIdx % mbWidth
		mbY := mbIdx / mbWidth
		predMV := predictSkipMV4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX*4, mbY*4)
		directRefL0, directMVL0 := int8(0), predMV
		directRefL1, directMVL1 := int8(-1), syntax.MotionVector{}
		applyDirectSpatial := hdr.DirectSpatialMvPred && hdr.NumRefIdxL0Active <= 2
		if applyDirectSpatial {
			directRefL0, directMVL0 = predictBDirectSpatialL0ForSimpleRefs(mv4Ctx, ref4Ctx, mv4Stride, mbX*4, mbY*4)
			directRefL1, directMVL1 = predictBDirectSpatialL0ForSimpleRefs(mv4L1Ctx, ref4L1Ctx, mv4Stride, mbX*4, mbY*4)
			if directRefL0 < 0 {
				directRefL0 = 0
				directRefL1 = 0
			}
		} else if hdr.DirectSpatialMvPred && predictBDirectSpatialL0Ref(mv4Ctx, ref4Ctx, mv4Stride, mbX*4, mbY*4) < 0 {
			directRefL1 = 0
		}

		var leftNZ, topNZ *[16]int
		var leftChromaNZ, topChromaNZ *[2][4]int
		var leftCBP, topCBP uint32
		var leftMBType, topMBType uint32
		var leftNonSkip, topNonSkip bool
		transform8x8CABACCtx := 0
		var leftChromaPred, topChromaPred int8
		if mbX > 0 {
			leftNZ = &nzCtx[mbIdx-1]
			leftChromaNZ = &chromaNZCtx[mbIdx-1]
			leftCBP = cbpCtx[mbIdx-1]
			leftMBType = mbTypeCtx[mbIdx-1]
			leftNonSkip = nonSkipCtx[mbIdx-1]
			if transform8x8Ctx[mbIdx-1] {
				transform8x8CABACCtx++
			}
			leftChromaPred = chromaPredModeCtx[mbIdx-1]
		}
		if mbY > 0 {
			topNZ = &nzCtx[mbIdx-mbWidth]
			topChromaNZ = &chromaNZCtx[mbIdx-mbWidth]
			topCBP = cbpCtx[mbIdx-mbWidth]
			topMBType = mbTypeCtx[mbIdx-mbWidth]
			topNonSkip = nonSkipCtx[mbIdx-mbWidth]
			if transform8x8Ctx[mbIdx-mbWidth] {
				transform8x8CABACCtx++
			}
			topChromaPred = chromaPredModeCtx[mbIdx-mbWidth]
		}

		if isIntra {
			if pps.EntropyCodingMode == 1 && cabacUseFFmpegEdgeContexts() {
				leftCBP, topCBP = cabacUnavailableCBP(leftCBP, topCBP, mbX, mbY, true)
				leftNZ, topNZ = cabacTraceEdgeNZ(mbX, mbY, leftNZ, topNZ)
				leftChromaNZ, topChromaNZ = cabacTraceEdgeChromaNZ(mbX, mbY, leftChromaNZ, topChromaNZ)
			}
			var mb *syntax.MBIntra
			var leftEdge8x8, topEdge8x8 [2]int8
			for i := range leftEdge8x8 {
				leftEdge8x8[i] = -1
				topEdge8x8[i] = -1
			}
			if pps.EntropyCodingMode == 1 {
				for br := 0; br < 2; br++ {
					if mbX > 0 {
						leftEdge8x8[br] = intra8x8RightCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftEdge8x8[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 {
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
				for b := 0; b < 4; b++ {
					bc, br := b%2, b/2
					mode := mb.I8x8PredMode[b]
					idx8 := (mbY*2+br)*intra8x8Stride + (mbX*2 + bc)
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
				}
			} else {
				for b := 0; b < 4; b++ {
					bc, br := b%2, b/2
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
					idx8 := (mbY*2+br)*intra8x8Stride + (mbX*2 + bc)
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
			writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: pps.EntropyCodingMode == 1, Kind: "I", MBType: mb.MBType, CBP: mb.CodedBlockPattern, QPDelta: mb.QPDelta, QP: currentQP, Use8x8: mb.Use8x8Transform, ChromaPred: mb.ChromaPredMode, Intra4x4Mode: mb.IntraPredMode, Intra4x4PredMode: d.traceIntra4x4PredMode, Intra4x4FinalMode: finalIntra4x4Modes(d.intraModes, d.mbW, mbX, mbY), Intra8x8Mode: mb.I8x8PredMode, Intra8x8PredMode: traceI8x8Pred, Intra8x8LeftEdge: leftEdge8x8, Intra8x8TopEdge: topEdge8x8, TotalCoeff: traceTotalCoeffFFmpegOrder(mb.TotalCoeff), ChromaCoeff: mb.ChromaTotalCoeff})
			if pps.EntropyCodingMode == 1 && cabacDec.DecodeTerminate() == 1 {
				break
			}

		} else if hdr.SliceType == syntax.SliceTypeP {
			if pps.EntropyCodingMode == 1 {
				var leftEdge8x8, topEdge8x8 [2]int8
				for br := 0; br < 2; br++ {
					if mbX > 0 {
						leftEdge8x8[br] = intra8x8RightCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftEdge8x8[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 {
						topEdge8x8[bc] = intra8x8BottomCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
					} else {
						topEdge8x8[bc] = -1
					}
				}
				refIdxCtxs := cabacRefIdxCtxsForMB(ref4Ctx, mv4Stride, mbX, mbY)
				mbInter, mbIntra, skipped := decodeCABACPInterMB(cabacDec, cabacModels, hdr.NumRefIdxL0Active, cabacLastQScaleDiff, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftNonSkip, topNonSkip, refIdxCtxs, mvd4Ctx, mv4Stride, mbX, mbY, pps.Transform8x8Mode, transform8x8CABACCtx, leftMBType, topMBType, leftChromaPred, topChromaPred, leftEdge8x8, topEdge8x8)
				if skipped {
					cabacLastQScaleDiff = 0
					skipMV := predMV
					mbInter.MV[0] = skipMV
					d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
					nonSkipCtx[mbIdx] = false
					transform8x8Ctx[mbIdx] = false
					writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbInter)
					d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: true, Kind: "P_SKIP", MBType: mbInter.MBType, QP: currentQP, Skipped: true, RefIdx: mbInter.RefIdx, MV: mbInter.MV})
					if cabacDec.DecodeTerminate() == 1 {
						break
					}
					continue
				}
				if mbIntra != nil {
					cabacLastQScaleDiff = int(mbIntra.QPDelta)
					currentQP = updateQP(currentQP, int(mbIntra.QPDelta))
					d.reconstructMB(f, mbIntra, mbX, mbY, currentQP, sps)
					mbIsIntraCtx[mbIdx] = true
					nzCtx[mbIdx] = mbIntra.TotalCoeff
					chromaNZCtx[mbIdx] = mbIntra.ChromaTotalCoeff
					cbpCtx[mbIdx] = mbIntra.CodedBlockPattern
					mbTypeCtx[mbIdx] = cabacMBTypeFlag(mbIntra.MBType)
					nonSkipCtx[mbIdx] = true
					transform8x8Ctx[mbIdx] = mbIntra.Use8x8Transform
					chromaPredModeCtx[mbIdx] = mbIntra.ChromaPredMode
					writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
					d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: true, Kind: "P_INTRA", MBType: mbIntra.MBType, CBP: mbIntra.CodedBlockPattern, QPDelta: mbIntra.QPDelta, QP: currentQP, Use8x8: mbIntra.Use8x8Transform, ChromaPred: mbIntra.ChromaPredMode, Intra4x4Mode: mbIntra.IntraPredMode, Intra4x4FinalMode: finalIntra4x4Modes(d.intraModes, d.mbW, mbX, mbY), Intra8x8Mode: mbIntra.I8x8PredMode, TotalCoeff: traceTotalCoeffFFmpegOrder(mbIntra.TotalCoeff), ChromaCoeff: mbIntra.ChromaTotalCoeff})
					if cabacDec.DecodeTerminate() == 1 {
						break
					}
					continue
				}
				applyMVPredictors(mbInter, mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY)
				cabacLastQScaleDiff = int(mbInter.QPDelta)
				currentQP = updateQP(currentQP, int(mbInter.QPDelta))
				d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				cbpCtx[mbIdx] = mbInter.CBP
				mbTypeCtx[mbIdx] = 0
				nonSkipCtx[mbIdx] = true
				transform8x8Ctx[mbIdx] = mbInter.Use8x8Transform
				writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbInter)
				d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: true, Kind: "P", MBType: mbInter.MBType, SubMBType: mbInter.SubMBType, CBP: mbInter.CBP, QPDelta: mbInter.QPDelta, QP: currentQP, Use8x8: mbInter.Use8x8Transform, RefIdx: mbInter.RefIdx, MV: mbInter.MV, SubMV: mbInter.SubMV, TotalCoeff: traceTotalCoeffFFmpegOrder(mbInter.TotalCoeff), ChromaCoeff: mbInter.ChromaTotalCoeff})
				if cabacDec.DecodeTerminate() == 1 {
					break
				}
				continue
			}
			// CAVLC P slices carry mb_skip_run before the next non-skipped MB.
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 && !decodeAfterSkipRun {
					skipRun = int(r.ReadUE())
				}
				if skipRun > 0 {
					if hdr.SliceType == syntax.SliceTypeB {
						d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, RefIdxL1: [4]int8{-1, -1, -1, -1}}, mbX, mbY, currentQP)
					} else {
						skipMV := predMV
						mbSkip := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
						mbSkip.MV[0] = skipMV
						d.reconstructMBInter(f, mbSkip, mbX, mbY, currentQP)
						writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbSkip)
					}
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
				mbIsIntraCtx[mbIdx] = true
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
				writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			} else {
				applyMVPredictors(&mbInter, mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY)
				currentQP = updateQP(currentQP, int(mbInter.QPDelta))
				d.reconstructMBInter(f, &mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, &mbInter)
			}
		} else {
			// B-slice
			if pps.EntropyCodingMode == 1 {
				// CABAC B-slice: dedicated decoder, avoids CAVLC desynchronization.
				var leftE8B, topE8B [2]int8
				for br := 0; br < 2; br++ {
					if mbX > 0 {
						leftE8B[br] = intra8x8RightCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftE8B[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 {
						topE8B[bc] = intra8x8BottomCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
					} else {
						topE8B[bc] = -1
					}
				}
				refIdxCtxsB := cabacRefIdxCtxsForMB(ref4Ctx, mv4Stride, mbX, mbY)
				mbBidi, mbIntra, skipped := decodeCABACBidiMB(
					cabacDec, cabacModels,
					hdr.NumRefIdxL0Active, hdr.NumRefIdxL1Active,
					cabacLastQScaleDiff,
					leftNZ, topNZ, leftChromaNZ, topChromaNZ,
					leftCBP, topCBP,
					leftNonSkip, topNonSkip,
					!leftNonSkip, !topNonSkip, // leftIsDirect/topIsDirect
					refIdxCtxsB, mv4Ctx, ref4Ctx, mv4L1Ctx, ref4L1Ctx, mvd4Ctx, mvd4L1Ctx, mv4Stride, mbX, mbY,
					pps.Transform8x8Mode, transform8x8CABACCtx,
					leftMBType, topMBType,
					leftChromaPred, topChromaPred,
					leftE8B, topE8B,
				)
				if skipped {
					// B_Direct_16x16 skip: QP unchanged, lastQScaleDiff resets to 0.
					cabacLastQScaleDiff = 0
					mbBidi.RefIdxL0[0] = directRefL0
					mbBidi.RefIdxL1 = [4]int8{directRefL1, directRefL1, directRefL1, directRefL1}
					mbBidi.MVL0[0] = directMVL0
					mbBidi.MVL1[0] = directMVL1
					d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
					nzCtx[mbIdx] = mbBidi.TotalCoeff
					chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
					cbpCtx[mbIdx] = 0
					mbTypeCtx[mbIdx] = 0
					nonSkipCtx[mbIdx] = false
					transform8x8Ctx[mbIdx] = false
					if applyDirectSpatial {
						writeBackBidiL0Context(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbBidi)
						writeBackBidiL1Context(mv4L1Ctx, ref4L1Ctx, mv4Stride, mbX, mbY, mbBidi)
					}
					mbQPCtx[mbIdx] = currentQP
					if cabacDec.DecodeTerminate() == 1 {
						break
					}
					continue
				}
				if mbIntra != nil {
					cabacLastQScaleDiff = int(mbIntra.QPDelta)
					currentQP = updateQP(currentQP, int(mbIntra.QPDelta))
					d.reconstructMB(f, mbIntra, mbX, mbY, currentQP, sps)
					mbIsIntraCtx[mbIdx] = true
					nzCtx[mbIdx] = mbIntra.TotalCoeff
					chromaNZCtx[mbIdx] = mbIntra.ChromaTotalCoeff
					cbpCtx[mbIdx] = mbIntra.CodedBlockPattern
					mbTypeCtx[mbIdx] = cabacMBTypeFlag(mbIntra.MBType)
					nonSkipCtx[mbIdx] = true
					writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
				} else {
					cabacLastQScaleDiff = int(mbBidi.QPDelta)
					currentQP = updateQP(currentQP, int(mbBidi.QPDelta))
					if mbBidi.MBType == syntax.BMBTypeDirect16x16 {
						mbBidi.RefIdxL0[0] = directRefL0
						mbBidi.RefIdxL1 = [4]int8{directRefL1, directRefL1, directRefL1, directRefL1}
						mbBidi.MVL0[0] = directMVL0
						mbBidi.MVL1[0] = directMVL1
					} else if applyDirectSpatial {
						applyB8x8DirectSpatial(mbBidi, directRefL0, directMVL0, directRefL1, directMVL1)
					}
					d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
					nzCtx[mbIdx] = mbBidi.TotalCoeff
					chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
					cbpCtx[mbIdx] = mbBidi.CBP
					mbTypeCtx[mbIdx] = 0 // inter B
					nonSkipCtx[mbIdx] = mbBidi.MBType != syntax.BMBTypeDirect16x16
					// transform8x8Ctx not updated: updating propagates wrong values until
					// the B-frame CABAC transform8x8_flag decode is verified correct.
					// Write back 4×4 MV/ref contexts for future MVP/ref_idx context. FFmpeg keeps
					// separate list caches; B_8x8 and two-part B MBs must fill the same shaped
					// regions, not just MVL0[0] over the whole macroblock.
					writeBackBidiL0Context(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbBidi)
					writeBackBidiL1Context(mv4L1Ctx, ref4L1Ctx, mv4Stride, mbX, mbY, mbBidi)
				}
				mbQPCtx[mbIdx] = currentQP
				if cabacDec.DecodeTerminate() == 1 {
					break
				}
				continue
			}
			// CAVLC B-slice path.
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 && !decodeAfterSkipRun {
					skipRun = int(r.ReadUE())
				}
				if skipRun > 0 {
					d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16, RefIdxL1: [4]int8{-1, -1, -1, -1}}, mbX, mbY, currentQP)
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
				mbIsIntraCtx[mbIdx] = true
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
				writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			} else {
				currentQP = updateQP(currentQP, int(mbBidi.QPDelta))
				if mbBidi.MBType == syntax.BMBTypeDirect16x16 {
					mbBidi.RefIdxL0[0] = directRefL0
					mbBidi.RefIdxL1 = [4]int8{directRefL1, directRefL1, directRefL1, directRefL1}
					mbBidi.MVL0[0] = directMVL0
					mbBidi.MVL1[0] = directMVL1
				} else if applyDirectSpatial {
					applyB8x8DirectSpatial(mbBidi, directRefL0, directMVL0, directRefL1, directMVL1)
				}
				d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbBidi.TotalCoeff
				chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
			}
		}
		// Per-MB QP always updated here so deblocking can average neighbours.
		mbQPCtx[mbIdx] = currentQP
	}

	// In-loop deblocking filter (H.264 §8.7), applied in a second pass over all
	// MBs so that each filtered MB uses fully reconstructed (but not yet filtered)
	// neighbour pixels — FFmpeg applies inline but we use a post-pass for clarity.
	// DisableIDC==1: skip entirely. DisableIDC==2: no cross-slice filtering; we
	// treat as fully enabled because we decode single-slice frames here.
	if hdr.DisableDeblocking != 1 {
		dbCtx := filter.DeblockMBContext{
			DisableIDC:  int(hdr.DisableDeblocking),
			AlphaOffset: int(hdr.SliceAlphaC0Offset),
			BetaOffset:  int(hdr.SliceBetaOffset),
		}
		for mbIdx := 0; mbIdx < maxMBs; mbIdx++ {
			mbX := mbIdx % mbWidth
			mbY := mbIdx / mbWidth
			cur := filter.MBDeblockInfo{
				QP:      mbQPCtx[mbIdx],
				IsIntra: mbIsIntraCtx[mbIdx],
				NZC:     nzCtx[mbIdx],
			}
			var left, top *filter.MBDeblockInfo
			if mbX > 0 {
				l := filter.MBDeblockInfo{
					QP:      mbQPCtx[mbIdx-1],
					IsIntra: mbIsIntraCtx[mbIdx-1],
					NZC:     nzCtx[mbIdx-1],
				}
				left = &l
			}
			if mbY > 0 {
				t := filter.MBDeblockInfo{
					QP:      mbQPCtx[mbIdx-mbWidth],
					IsIntra: mbIsIntraCtx[mbIdx-mbWidth],
					NZC:     nzCtx[mbIdx-mbWidth],
				}
				top = &t
			}
			filter.DeblockMBFrame(
				f.Y, f.StrideY,
				f.U, f.V, f.StrideC,
				mbX, mbY, cur, left, top, dbCtx,
			)
		}
	}

	return f, nil
}

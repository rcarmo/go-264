package decode

// decode/pipeline.go — Decoder type, Decode(), and the decodeSlice() main loop.

import (
	"fmt"

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
	Intra4x4FinalMode [16]int8
	Intra8x8Mode      [4]int8
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
	traceFrameIndex int
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
	intra8x8Stride := mbWidth * 2
	intra8x8ModeCtx := make([]int8, intra8x8Stride*mbHeight*2)
	for i := range intra8x8ModeCtx {
		intra8x8ModeCtx[i] = -1
	}
	mv4Stride := mbWidth * 4
	mv4Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	mvd4Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	ref4Ctx := make([]int8, mv4Stride*mbHeight*4)
	for i := range ref4Ctx {
		ref4Ctx[i] = -2
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
			if pps.EntropyCodingMode == 1 {
				var leftEdge8x8, topEdge8x8 [2]int8
				for br := 0; br < 2; br++ {
					if mbX > 0 {
						leftEdge8x8[br] = intra8x8ModeCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftEdge8x8[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 {
						topEdge8x8[bc] = intra8x8ModeCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
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
			nzCtx[mbIdx] = mb.TotalCoeff
			chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
			cbpCtx[mbIdx] = mb.CodedBlockPattern
			mbTypeCtx[mbIdx] = cabacMBTypeFlag(mb.MBType)
			nonSkipCtx[mbIdx] = true
			transform8x8Ctx[mbIdx] = mb.Use8x8Transform
			chromaPredModeCtx[mbIdx] = mb.ChromaPredMode
			if pps.EntropyCodingMode == 1 && mb.Use8x8Transform {
				for b := 0; b < 4; b++ {
					bc, br := b%2, b/2
					mode := mb.I8x8PredMode[b]
					intra8x8ModeCtx[(mbY*2+br)*intra8x8Stride+(mbX*2+bc)] = mode
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
					intra8x8ModeCtx[(mbY*2+br)*intra8x8Stride+(mbX*2+bc)] = minMode
				}
			}
			writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			d.traceMB(MBTraceEvent{NALType: unit.Type, FrameNum: int(hdr.FrameNum), SliceType: hdr.SliceType, MBAddr: mbIdx, MBX: mbX, MBY: mbY, EntropyCABAC: pps.EntropyCodingMode == 1, Kind: "I", MBType: mb.MBType, CBP: mb.CodedBlockPattern, QPDelta: mb.QPDelta, QP: currentQP, Use8x8: mb.Use8x8Transform, ChromaPred: mb.ChromaPredMode, Intra4x4Mode: mb.IntraPredMode, Intra4x4FinalMode: finalIntra4x4Modes(d.intraModes, d.mbW, mbX, mbY), Intra8x8Mode: mb.I8x8PredMode, TotalCoeff: traceTotalCoeffFFmpegOrder(mb.TotalCoeff), ChromaCoeff: mb.ChromaTotalCoeff})
			if pps.EntropyCodingMode == 1 && cabacDec.DecodeTerminate() == 1 {
				break
			}

		} else if hdr.SliceType == syntax.SliceTypeP {
			if pps.EntropyCodingMode == 1 {
				var leftEdge8x8, topEdge8x8 [2]int8
				for br := 0; br < 2; br++ {
					if mbX > 0 {
						leftEdge8x8[br] = intra8x8ModeCtx[(mbY*2+br)*intra8x8Stride+(mbX*2-1)]
					} else {
						leftEdge8x8[br] = -1
					}
				}
				for bc := 0; bc < 2; bc++ {
					if mbY > 0 {
						topEdge8x8[bc] = intra8x8ModeCtx[(mbY*2-1)*intra8x8Stride+(mbX*2+bc)]
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
						d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16}, mbX, mbY, currentQP)
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
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 && !decodeAfterSkipRun {
					skipRun = int(r.ReadUE())
				}
				if skipRun > 0 {
					d.reconstructMBBidi(f, &syntax.MBBidi{MBType: syntax.BMBTypeDirect16x16}, mbX, mbY, currentQP)
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
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
				writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			} else {
				currentQP = updateQP(currentQP, int(mbBidi.QPDelta))
				d.reconstructMBBidi(f, mbBidi, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbBidi.TotalCoeff
				chromaNZCtx[mbIdx] = mbBidi.ChromaTotalCoeff
			}
		}
	}

	return f, nil
}

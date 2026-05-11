package decode

// decode/pipeline.go — Decoder type, Decode(), and the decodeSlice() main loop.

import (
	"fmt"

	cabac "github.com/rcarmo/go-264/entropy/cabac"
	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/syntax"
)

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
}

// DecodedFrame is an alias for frame.Frame for CLI convenience.
type DecodedFrame = frame.Frame

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

	hdr, r := syntax.ParseHeader(unit.Payload, unit.Type, sps, pps)
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
	chromaPredModeCtx := make([]int8, maxMBs)
	intra8x8Stride := mbWidth * 2
	intra8x8ModeCtx := make([]int8, intra8x8Stride*mbHeight*2)
	for i := range intra8x8ModeCtx {
		intra8x8ModeCtx[i] = -1
	}
	mvCtx := make([]syntax.MotionVector, maxMBs)
	refCtx := make([]int8, maxMBs)
	for i := range refCtx {
		refCtx[i] = -1
	}
	mv4Stride := mbWidth * 4
	mv4Ctx := make([]syntax.MotionVector, mv4Stride*mbHeight*4)
	ref4Ctx := make([]int8, mv4Stride*mbHeight*4)
	for i := range ref4Ctx {
		ref4Ctx[i] = -2
	}
	skipRun := 0
	decodeAfterSkipRun := false
	var cabacDec *cabac.CABACDecoder
	var cabacModels []cabac.CABACCtx
	if pps.EntropyCodingMode == 1 {
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
		var leftChromaPred, topChromaPred int8
		if mbX > 0 {
			leftNZ = &nzCtx[mbIdx-1]
			leftChromaNZ = &chromaNZCtx[mbIdx-1]
			leftCBP = cbpCtx[mbIdx-1]
			leftMBType = mbTypeCtx[mbIdx-1]
			leftChromaPred = chromaPredModeCtx[mbIdx-1]
		}
		if mbY > 0 {
			topNZ = &nzCtx[mbIdx-mbWidth]
			topChromaNZ = &chromaNZCtx[mbIdx-mbWidth]
			topCBP = cbpCtx[mbIdx-mbWidth]
			topMBType = mbTypeCtx[mbIdx-mbWidth]
			topChromaPred = chromaPredModeCtx[mbIdx-mbWidth]
		}

		if isIntra {
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
				mb = decodeCABACIntraMB(cabacDec, cabacModels, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, leftMBType, topMBType, leftChromaPred, topChromaPred, pps.Transform8x8Mode, leftEdge8x8, topEdge8x8)
				currentQP = (currentQP + int(mb.QPDelta) + 52) % 52
			} else {
				mb = syntax.DecodeMBIntra(r, syntax.IntraDecodeOpts{
					SliceQP: int32(currentQP), Transform8x8: pps.Transform8x8Mode,
					LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
				})
				currentQP = (currentQP + int(mb.QPDelta) + 52) % 52
			}
			d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
			nzCtx[mbIdx] = mb.TotalCoeff
			chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
			cbpCtx[mbIdx] = mb.CodedBlockPattern
			mbTypeCtx[mbIdx] = cabacMBTypeFlag(mb.MBType)
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
			refCtx[mbIdx] = -1
			writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			if pps.EntropyCodingMode == 1 && cabacDec.DecodeTerminate() == 1 {
				break
			}

		} else if hdr.SliceType == syntax.SliceTypeP {
			if pps.EntropyCodingMode == 1 {
				mbInter, skipped := decodeCABACPInterMB(cabacDec, cabacModels, hdr.NumRefIdxL0Active, leftNZ, topNZ, leftChromaNZ, topChromaNZ, leftCBP, topCBP, pps.Transform8x8Mode)
				if skipped {
					skipMV := predictSkipMV(mvCtx, refCtx, predMV, mbIdx, mbX, mbY, mbWidth)
					mbInter.MV[0] = skipMV
					d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
					mvCtx[mbIdx] = skipMV
					refCtx[mbIdx] = 0
					writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbInter)
					if cabacDec.DecodeTerminate() == 1 {
						break
					}
					continue
				}
				applyMVPredictors(mbInter, mvCtx, refCtx, mv4Ctx, ref4Ctx, mv4Stride, mbIdx, mbX, mbY, mbWidth)
				d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				cbpCtx[mbIdx] = mbInter.CBP
				mbTypeCtx[mbIdx] = 0
				mvCtx[mbIdx], refCtx[mbIdx] = representativeRightEdgeMV(mbInter)
				writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbInter)
				if cabacDec.DecodeTerminate() == 1 {
					break
				}
				continue
			}
			// CAVLC P-slice
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 && !decodeAfterSkipRun {
					skipRun = int(r.ReadUE())
				}
				if skipRun > 0 {
					skipMV := predictSkipMV(mvCtx, refCtx, predMV, mbIdx, mbX, mbY, mbWidth)
					mbSkip := &syntax.MBInter{MBType: syntax.PMBTypeP16x16}
					mbSkip.MV[0] = skipMV
					d.reconstructMBInter(f, mbSkip, mbX, mbY, currentQP)
					mvCtx[mbIdx] = skipMV
					refCtx[mbIdx] = 0
					writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, mbSkip)
					skipRun--
					decodeAfterSkipRun = skipRun == 0
					continue
				}
				decodeAfterSkipRun = false
			}
			mbInter := syntax.DecodeMBInter(r, syntax.InterDecodeOpts{
				SliceQP: int32(currentQP), NumRefFrames: hdr.NumRefIdxL0Active,
				LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
			})
			if mbInter.MBType >= syntax.PMBTypeIntra {
				mb := syntax.DecodeMBIntraWithType(r, mbInter.MBType-syntax.PMBTypeIntra, syntax.IntraDecodeOpts{
					SliceQP: int32(currentQP), Transform8x8: pps.Transform8x8Mode,
					LeftNZ: leftNZ, TopNZ: topNZ, LeftChromaNZ: leftChromaNZ, TopChromaNZ: topChromaNZ,
				})
				currentQP = (currentQP + int(mb.QPDelta) + 52) % 52
				d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
				refCtx[mbIdx] = -1
				writeBackIntra4x4(ref4Ctx, mv4Stride, mbX, mbY)
			} else {
				applyMVPredictors(&mbInter, mvCtx, refCtx, mv4Ctx, ref4Ctx, mv4Stride, mbIdx, mbX, mbY, mbWidth)
				currentQP = (currentQP + int(mbInter.QPDelta) + 52) % 52
				d.reconstructMBInter(f, &mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				mvCtx[mbIdx], refCtx[mbIdx] = representativeRightEdgeMV(&mbInter)
				writeBackInter4x4(mv4Ctx, ref4Ctx, mv4Stride, mbX, mbY, &mbInter)
			}
		} else {
			// B-slice
			mbBidi := syntax.DecodeMBBidi(r, qp, hdr.NumRefIdxL0Active, hdr.NumRefIdxL1Active)
			if mbBidi.MBType >= syntax.BMBTypeIntra {
				mb := &syntax.MBIntra{MBType: mbBidi.MBType - syntax.BMBTypeIntra}
				d.reconstructMB(f, mb, mbX, mbY, int(qp), sps)
			} else {
				d.reconstructMBBidi(f, mbBidi, mbX, mbY, int(qp))
			}
		}
	}

	return f, nil
}

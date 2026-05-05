package decode

// H.264 Baseline decoder — decodes Annex B bitstreams to YUV frames.

import (
	"fmt"

	"github.com/rcarmo/go-264/frame"
	"github.com/rcarmo/go-264/nal"
	"github.com/rcarmo/go-264/pred"
	"github.com/rcarmo/go-264/slice"
	"github.com/rcarmo/go-264/transform"
)

// H.264 4x4 block position within macroblock (inverse raster scan §6.4.3)
var blk4x4X = [16]int{0, 4, 0, 4, 8, 12, 8, 12, 0, 4, 0, 4, 8, 12, 8, 12}
var blk4x4Y = [16]int{0, 0, 4, 4, 0, 0, 4, 4, 8, 8, 12, 12, 8, 8, 12, 12}

// Decoder is an H.264 Baseline profile decoder.
type Decoder struct {
	SPS    map[uint32]*nal.SPS
	PPS    map[uint32]*nal.PPS
	DPB    *frame.DPB
	Frames []*frame.Frame
	// Per-frame prediction mode map (4x4 block index → mode)
	intraModes []int8 // [mbW*4 * mbH*4] for current frame
	mbW, mbH   int
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
	// Select PPS/SPS by pic_parameter_set_id from the slice header. The PPS id
	// appears before fields that require SPS-derived lengths, so it can be
	// safely peeked with raw Exp-Golomb reads.
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

	hdr, r := slice.ParseHeader(unit.Payload, unit.Type, sps, pps)
	// P/B frames need reference frames
	isIntra := hdr.IsIntra()

	qp := hdr.QP(pps.PicInitQP)
	f := frame.NewFrame(sps.Width, sps.Height)
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
	} // default DC

	maxMBs := mbWidth * mbHeight
	if maxMBs > 10000 {
		maxMBs = 10000
	} // safety limit
	currentQP := int(qp)
	nzCtx := make([][16]int, maxMBs)            // CAVLC luma totalCoeff context per decoded MB
	chromaNZCtx := make([][2][4]int, maxMBs)    // CAVLC chroma totalCoeff context per decoded MB/component
	mvCtx := make([]slice.MotionVector, maxMBs) // representative L0 MV context per MB
	skipRun := 0                                // CAVLC P/B-slice mb_skip_run state
	for mbIdx := int(hdr.FirstMbInSlice); mbIdx < maxMBs; mbIdx++ {
		mbX := mbIdx % mbWidth
		mbY := mbIdx / mbWidth
		predMV := predictMBMV(mvCtx, mbIdx, mbX, mbY, mbWidth)

		var leftNZ, topNZ *[16]int
		var leftChromaNZ, topChromaNZ *[2][4]int
		if mbX > 0 {
			leftNZ = &nzCtx[mbIdx-1]
			leftChromaNZ = &chromaNZCtx[mbIdx-1]
		}
		if mbY > 0 {
			topNZ = &nzCtx[mbIdx-mbWidth]
			topChromaNZ = &chromaNZCtx[mbIdx-mbWidth]
		}

		if isIntra {
			mb := slice.DecodeMBIntraCtxFull(r, int32(currentQP), pps.EntropyCodingMode, pps.Transform8x8Mode, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
			mbQPDelta := int(mb.QPDelta)
			currentQP = (currentQP + mbQPDelta%52 + 52) % 52
			d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
			nzCtx[mbIdx] = mb.TotalCoeff
			chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
		} else if hdr.SliceType == slice.SliceTypeP {
			// CAVLC P-slices carry mb_skip_run before each non-skipped MB. Missing
			// this field shifts every P macroblock by one Exp-Golomb code.
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 {
					skipRun = int(r.ReadUE())
				}
				if skipRun > 0 {
					// P_Skip: no residual, no mvd/ref_idx. Motion vector is the normal
					// median predictor from neighbouring L0 vectors.
					mbSkip := &slice.MBInter{MBType: slice.PMBTypeP16x16}
					mbSkip.MV[0] = predMV
					d.reconstructMBInter(f, mbSkip, mbX, mbY, currentQP)
					mvCtx[mbIdx] = predMV
					skipRun--
					continue
				}
			}
			mbInter := slice.DecodeMBInterCtxFull(r, int32(currentQP), hdr.NumRefIdxL0Active, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
			if mbInter.MBType >= 5 {
				// P-slice intra MB: mb_type has already been consumed by
				// DecodeMBInterCtx, so decode the remaining intra payload with the
				// intra type offset (Table 7-13).
				mb := slice.DecodeMBIntraCtxWithTypeFull(r, mbInter.MBType-5, int32(currentQP), pps.EntropyCodingMode, pps.Transform8x8Mode, leftNZ, topNZ, leftChromaNZ, topChromaNZ)
				currentQP = (currentQP + int(mb.QPDelta)%52 + 52) % 52
				d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
				nzCtx[mbIdx] = mb.TotalCoeff
				chromaNZCtx[mbIdx] = mb.ChromaTotalCoeff
			} else {
				applyMVPredictors(mbInter, predMV)
				currentQP = (currentQP + int(mbInter.QPDelta)%52 + 52) % 52
				d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
				chromaNZCtx[mbIdx] = mbInter.ChromaTotalCoeff
				mvCtx[mbIdx] = mbInter.MV[0]
			}
		} else {
			// B-slice
			mbBidi := slice.DecodeMBBidi(r, qp, hdr.NumRefIdxL0Active, hdr.NumRefIdxL1Active)
			if mbBidi.MBType >= slice.BMBTypeIntra {
				mb := &slice.MBIntra{MBType: mbBidi.MBType - slice.BMBTypeIntra}
				d.reconstructMB(f, mb, mbX, mbY, int(qp), sps)
			} else {
				d.reconstructMBBidi(f, mbBidi, mbX, mbY, int(qp))
			}
		}
	}

	// Do not run the old simplified deblocking pass here. It is not spec-complete
	// and measurably reduces reference PSNR. The proper in-loop deblocker should
	// be wired from the filter package once boundary strengths and per-edge QP
	// are tracked accurately.
	return f, nil
}

func (d *Decoder) reconstructMB(f *frame.Frame, mb *slice.MBIntra, mbX, mbY int, qp int, sps *nal.SPS) {
	if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: predict whole 16x16 block
		d.reconstruct16x16(f, mb, mbX, mbY, qp)
	} else if mb.MBType == 0 {
		// I_NxN: predict each 4x4 block
		d.reconstruct4x4(f, mb, mbX, mbY, qp)
	}
	d.reconstructChromaIntra(f, mb, mbX, mbY, qp)
	// I_PCM: raw samples (rare, skip)
}

func (d *Decoder) reconstruct16x16(f *frame.Frame, mb *slice.MBIntra, mbX, mbY, qp int) {
	mode := int(mb.Intra16x16PredMode)
	top := make([]uint8, 16)
	left := make([]uint8, 16)
	topLeft := uint8(128)
	if mbY > 0 {
		for x := 0; x < 16; x++ {
			top[x] = f.PixelY(mbX*16+x, mbY*16-1)
		}
	} else {
		for i := range top {
			top[i] = 128
		}
	}
	if mbX > 0 {
		for y := 0; y < 16; y++ {
			left[y] = f.PixelY(mbX*16-1, mbY*16+y)
		}
	} else {
		for i := range left {
			left[i] = 128
		}
	}
	if mbX > 0 && mbY > 0 {
		topLeft = f.PixelY(mbX*16-1, mbY*16-1)
	}

	predicted := make([]uint8, 256)
	if mode == pred.Intra16x16DC {
		// H.264 DC prediction handles unavailable edges specially. The pred
		// package API receives filled edge samples only, so do the availability
		// aware DC case here.
		var dc uint8
		if mbX > 0 && mbY > 0 {
			sum := 0
			for i := 0; i < 16; i++ {
				sum += int(top[i]) + int(left[i])
			}
			dc = uint8((sum + 16) >> 5)
		} else if mbY > 0 {
			sum := 0
			for i := 0; i < 16; i++ {
				sum += int(top[i])
			}
			dc = uint8((sum + 8) >> 4)
		} else if mbX > 0 {
			sum := 0
			for i := 0; i < 16; i++ {
				sum += int(left[i])
			}
			dc = uint8((sum + 8) >> 4)
		} else {
			dc = 128
		}
		for i := range predicted[:256] {
			predicted[i] = dc
		}
	} else {
		pred.PredIntra16x16(predicted, mode, top, left, topLeft)
	}

	// Hadamard DC transform
	var dcBlock [16]int16
	for i := 0; i < 16; i++ {
		dcBlock[i] = mb.Coeffs[i][0]
	}
	transform.Hadamard4x4DC(dcBlock[:], qp)

	cbpLuma := mb.CodedBlockPattern & 0xF
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		bx := blk4x4X[blkIdx]
		by := blk4x4Y[blkIdx]
		var block [16]int16
		block[0] = dcBlock[blkIdx]
		if cbpLuma != 0 {
			for j := 1; j < 16; j++ {
				block[j] = mb.Coeffs[blkIdx][j]
			}
			// Dequant AC only (DC already handled by Hadamard)
			qpDiv6 := uint(qp / 6)
			qpMod6 := qp % 6
			for j := 1; j < 16; j++ {
				if block[j] != 0 {
					v := int32(transform.DequantVTable()[qpMod6][transform.PosToVTable()[j]])
					block[j] = int16(int32(block[j]) * v << qpDiv6)
				}
			}
		}
		transform.IDCT4x4(block[:])
		for py := 0; py < 4; py++ {
			for px := 0; px < 4; px++ {
				v := int(predicted[(by+py)*16+(bx+px)]) + int(block[py*4+px])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				f.SetPixelY(mbX*16+bx+px, mbY*16+by+py, uint8(v))
			}
		}
	}
}

func (d *Decoder) reconstruct4x4(f *frame.Frame, mb *slice.MBIntra, mbX, mbY, qp int) {
	// For each 4x4 block in raster scan order
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		bx := blk4x4X[blkIdx]
		by := blk4x4Y[blkIdx]

		// Get neighbors
		top := make([]uint8, 4)
		topRight := make([]uint8, 4)
		left := make([]uint8, 4)
		topLeft := uint8(128)

		x0 := mbX*16 + bx
		y0 := mbY*16 + by

		for i := 0; i < 4; i++ {
			if y0 > 0 {
				top[i] = f.PixelY(x0+i, y0-1)
			} else {
				top[i] = 128
			}
			if x0 > 0 {
				left[i] = f.PixelY(x0-1, y0+i)
			} else {
				left[i] = 128
			}
		}
		for i := 0; i < 4; i++ {
			if y0 > 0 && x0+4+i < f.Width {
				topRight[i] = f.PixelY(x0+4+i, y0-1)
			} else {
				topRight[i] = top[3]
			}
		}
		if x0 > 0 && y0 > 0 {
			topLeft = f.PixelY(x0-1, y0-1)
		}

		// Compute predicted mode from neighbors (§8.3.1.1)
		blkX := mbX*4 + bx/4
		blkY := mbY*4 + by/4
		modeA := int8(2) // left neighbor default
		modeB := int8(2) // top neighbor default
		if blkX > 0 {
			modeA = d.intraModes[blkY*d.mbW*4+(blkX-1)]
		}
		if blkY > 0 {
			modeB = d.intraModes[(blkY-1)*d.mbW*4+blkX]
		}
		predMode := modeA
		if modeB < predMode {
			predMode = modeB
		}

		mode := int(predMode) // default: use predicted
		rawMode := mb.IntraPredMode[blkIdx]
		if rawMode == -1 {
			mode = int(predMode)
		} else {
			if int(rawMode) < int(predMode) {
				mode = int(rawMode)
			} else {
				mode = int(rawMode) + 1
			}
		}
		if mode > 8 {
			mode = 2
		}
		// Store the decoded mode
		if blkY < d.mbH*4 && blkX < d.mbW*4 {
			d.intraModes[blkY*d.mbW*4+blkX] = int8(mode)
		}

		predicted := make([]uint8, 16)
		if mode == pred.Intra4x4DC {
			// Availability-aware DC prediction (§8.3.1.2.3).
			topAvail := y0 > 0
			leftAvail := x0 > 0
			var dc uint8
			if topAvail && leftAvail {
				sum := 0
				for i := 0; i < 4; i++ {
					sum += int(top[i]) + int(left[i])
				}
				dc = uint8((sum + 4) >> 3)
			} else if topAvail {
				sum := 0
				for i := 0; i < 4; i++ {
					sum += int(top[i])
				}
				dc = uint8((sum + 2) >> 2)
			} else if leftAvail {
				sum := 0
				for i := 0; i < 4; i++ {
					sum += int(left[i])
				}
				dc = uint8((sum + 2) >> 2)
			} else {
				dc = 128
			}
			for i := range predicted[:16] {
				predicted[i] = dc
			}
		} else {
			pred.PredIntra4x4(predicted, mode, top, topRight, left, topLeft)
		}

		// Add residual
		block := mb.Coeffs[blkIdx]
		hasResidual := (mb.CodedBlockPattern & (1 << uint(blkIdx/4))) != 0
		if hasResidual {
			transform.Dequant4x4(block[:], qp)
			transform.IDCT4x4(block[:])
		}

		for py := 0; py < 4; py++ {
			for px := 0; px < 4; px++ {
				v := int(predicted[py*4+px]) + int(block[py*4+px])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				f.SetPixelY(x0+px, y0+py, uint8(v))
			}
		}
	}
}

func (d *Decoder) reconstructChromaIntra(f *frame.Frame, mb *slice.MBIntra, mbX, mbY, qp int) {
	for comp := 0; comp < 2; comp++ {
		predicted := d.predictChroma8x8(f, comp, mbX, mbY, int(mb.ChromaPredMode))
		var dc [4]int16
		for i := 0; i < 4; i++ {
			dc[i] = mb.CoeffsChroma[comp][i][0]
		}
		transform.Hadamard2x2DC(dc[:], qp)
		var residual [4][16]int16
		for blk := 0; blk < 4; blk++ {
			residual[blk] = mb.CoeffsChroma[comp][blk]
			residual[blk][0] = dc[blk]
			// Chroma DC has already been Hadamard-transformed/dequantized.
			// Dequantize AC only before the inverse 4x4 transform.
			transform.Dequant4x4AC(residual[blk][:], qp)
			transform.IDCT4x4(residual[blk][:])
		}
		for blk := 0; blk < 4; blk++ {
			bx := (blk & 1) * 4
			by := (blk >> 1) * 4
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					v := int(predicted[(by+y)*8+bx+x]) + int(residual[blk][y*4+x])
					if v < 0 {
						v = 0
					}
					if v > 255 {
						v = 255
					}
					cx, cy := mbX*8+bx+x, mbY*8+by+y
					if comp == 0 {
						f.SetPixelU(cx, cy, uint8(v))
					} else {
						f.SetPixelV(cx, cy, uint8(v))
					}
				}
			}
		}
	}
}

func (d *Decoder) predictChroma8x8(f *frame.Frame, comp int, mbX, mbY, mode int) [64]uint8 {
	var out [64]uint8
	get := func(x, y int) uint8 {
		if comp == 0 {
			return f.PixelU(x, y)
		}
		return f.PixelV(x, y)
	}
	var top [8]uint8
	var left [8]uint8
	if mbY > 0 {
		for i := 0; i < 8; i++ {
			top[i] = get(mbX*8+i, mbY*8-1)
		}
	} else {
		for i := 0; i < 8; i++ {
			top[i] = 128
		}
	}
	if mbX > 0 {
		for i := 0; i < 8; i++ {
			left[i] = get(mbX*8-1, mbY*8+i)
		}
	} else {
		for i := 0; i < 8; i++ {
			left[i] = 128
		}
	}
	switch mode {
	case 1: // horizontal
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				out[y*8+x] = left[y]
			}
		}
	case 2: // vertical
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				out[y*8+x] = top[x]
			}
		}
	default: // DC and unsupported plane fallback
		var dc uint8
		if mbX > 0 && mbY > 0 {
			sum := 0
			for i := 0; i < 8; i++ {
				sum += int(top[i]) + int(left[i])
			}
			dc = uint8((sum + 8) >> 4)
		} else if mbY > 0 {
			sum := 0
			for i := 0; i < 8; i++ {
				sum += int(top[i])
			}
			dc = uint8((sum + 4) >> 3)
		} else if mbX > 0 {
			sum := 0
			for i := 0; i < 8; i++ {
				sum += int(left[i])
			}
			dc = uint8((sum + 4) >> 3)
		} else {
			dc = 128
		}
		for i := range out {
			out[i] = dc
		}
	}
	return out
}

func (d *Decoder) reconstructMBInter(f *frame.Frame, mb *slice.MBInter, mbX, mbY, qp int) {
	// Get reference frame
	var ref *frame.Frame
	if len(d.DPB.Frames) > 0 {
		ref = d.DPB.Frames[len(d.DPB.Frames)-1]
	}
	if ref == nil {
		// No reference available — fill with gray
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				f.SetPixelY(mbX*16+x, mbY*16+y, 128)
			}
		}
		return
	}

	// Motion compensation based on partition type
	switch mb.MBType {
	case slice.PMBTypeP16x16:
		// Single 16x16 partition. InterPred16x16At dispatches to SIMD row-copy
		// on amd64/arm64 when the source rectangle is fully in-bounds.
		mv := mb.MV[0]
		predicted := make([]uint8, 256)
		pred.InterPred16x16At(predicted, ref.Y, ref.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv.X, Y: mv.Y})
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case slice.PMBTypeP16x8:
		predicted := make([]uint8, 256)
		tmp := make([]uint8, 256)
		mv0 := mb.MV[0]
		pred.InterPred16x16At(tmp, ref.Y, ref.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		for y := 0; y < 8; y++ {
			copy(predicted[y*16:y*16+16], tmp[y*16:y*16+16])
		}
		mv1 := mb.MV[1]
		pred.InterPred16x16At(tmp, ref.Y, ref.StrideY, mbX*16, mbY*16+8, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		for y := 0; y < 8; y++ {
			copy(predicted[(y+8)*16:(y+8)*16+16], tmp[y*16:y*16+16])
		}
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case slice.PMBTypeP8x16:
		predicted := make([]uint8, 256)
		tmp := make([]uint8, 256)
		mv0 := mb.MV[0]
		pred.InterPred16x16At(tmp, ref.Y, ref.StrideY, mbX*16, mbY*16, pred.MotionVector{X: mv0.X, Y: mv0.Y})
		for y := 0; y < 16; y++ {
			copy(predicted[y*16:y*16+8], tmp[y*16:y*16+8])
		}
		mv1 := mb.MV[1]
		pred.InterPred16x16At(tmp, ref.Y, ref.StrideY, mbX*16+8, mbY*16, pred.MotionVector{X: mv1.X, Y: mv1.Y})
		for y := 0; y < 16; y++ {
			copy(predicted[y*16+8:y*16+16], tmp[y*16:y*16+8])
		}
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	case slice.PMBTypeP8x8, slice.PMBTypeP8x8ref0:
		predicted := make([]uint8, 256)
		for part := 0; part < 4; part++ {
			baseX := (part & 1) * 8
			baseY := (part >> 1) * 8
			switch mb.SubMBType[part] {
			case 0: // P_L0_8x8
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 8, mb.SubMV[part*4])
			case 1: // P_L0_8x4
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 4, mb.SubMV[part*4])
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY+4, baseX, baseY+4, 8, 4, mb.SubMV[part*4+1])
			case 2: // P_L0_4x8
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 4, 8, mb.SubMV[part*4])
				d.copyInterSubRect(predicted, ref, mbX*16+baseX+4, mbY*16+baseY, baseX+4, baseY, 4, 8, mb.SubMV[part*4+1])
			case 3: // P_L0_4x4
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 4, 4, mb.SubMV[part*4])
				d.copyInterSubRect(predicted, ref, mbX*16+baseX+4, mbY*16+baseY, baseX+4, baseY, 4, 4, mb.SubMV[part*4+1])
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY+4, baseX, baseY+4, 4, 4, mb.SubMV[part*4+2])
				d.copyInterSubRect(predicted, ref, mbX*16+baseX+4, mbY*16+baseY+4, baseX+4, baseY+4, 4, 4, mb.SubMV[part*4+3])
			default:
				d.copyInterSubRect(predicted, ref, mbX*16+baseX, mbY*16+baseY, baseX, baseY, 8, 8, mb.SubMV[part*4])
			}
		}
		d.writeInterResidual(f, mb, predicted, mbX, mbY, qp)
		d.reconstructChromaInter(f, ref, mb, mbX, mbY, qp)

	default:
		// For other partition types, copy from reference with zero MV as fallback
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				srcX := mbX*16 + x
				srcY := mbY*16 + y
				if srcX < ref.Width && srcY < ref.Height {
					f.SetPixelY(srcX, srcY, ref.PixelY(srcX, srcY))
				}
			}
		}
	}
}

func (d *Decoder) reconstructChromaInter(f, ref *frame.Frame, mb *slice.MBInter, mbX, mbY, qp int) {
	var predU, predV [64]uint8
	// Conservative chroma MC: derive integer chroma displacement from the
	// representative luma MV. Sub-partition chroma and fractional interpolation
	// are future refinements, but this writes U/V planes and applies residuals.
	mv := mb.MV[0]
	if mb.MBType == slice.PMBTypeP8x8 || mb.MBType == slice.PMBTypeP8x8ref0 {
		mv = mb.SubMV[0]
	}
	d.fillChromaInterPred(predU[:], ref.U, ref.StrideC, ref.Width/2, ref.Height/2, mbX*8, mbY*8, mv)
	d.fillChromaInterPred(predV[:], ref.V, ref.StrideC, ref.Width/2, ref.Height/2, mbX*8, mbY*8, mv)
	d.writeChromaInterResidual(f, mb, predU[:], 0, mbX, mbY, qp)
	d.writeChromaInterResidual(f, mb, predV[:], 1, mbX, mbY, qp)
}

func (d *Decoder) fillChromaInterPred(dst []uint8, plane []uint8, stride, width, height, baseX, baseY int, mv slice.MotionVector) {
	dx := int(mv.X) >> 3
	dy := int(mv.Y) >> 3
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sx, sy := baseX+dx+x, baseY+dy+y
			if sx < 0 {
				sx = 0
			}
			if sy < 0 {
				sy = 0
			}
			if sx >= width {
				sx = width - 1
			}
			if sy >= height {
				sy = height - 1
			}
			dst[y*8+x] = plane[sy*stride+sx]
		}
	}
}

func (d *Decoder) writeChromaInterResidual(f *frame.Frame, mb *slice.MBInter, predicted []uint8, comp int, mbX, mbY, qp int) {
	var dc [4]int16
	for i := 0; i < 4; i++ {
		dc[i] = mb.CoeffsChroma[comp][i][0]
	}
	transform.Hadamard2x2DC(dc[:], qp)
	var residual [4][16]int16
	for blk := 0; blk < 4; blk++ {
		residual[blk] = mb.CoeffsChroma[comp][blk]
		residual[blk][0] = dc[blk]
		transform.Dequant4x4AC(residual[blk][:], qp)
		transform.IDCT4x4(residual[blk][:])
	}
	for blk := 0; blk < 4; blk++ {
		bx, by := (blk&1)*4, (blk>>1)*4
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				v := int(predicted[(by+y)*8+bx+x]) + int(residual[blk][y*4+x])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				cx, cy := mbX*8+bx+x, mbY*8+by+y
				if comp == 0 {
					f.SetPixelU(cx, cy, uint8(v))
				} else {
					f.SetPixelV(cx, cy, uint8(v))
				}
			}
		}
	}
}

func (d *Decoder) copyInterSubRect(dst []uint8, ref *frame.Frame, srcBaseX, srcBaseY, dstX, dstY, w, h int, mv slice.MotionVector) {
	// Reuse the 16x16 MC primitive (and its SIMD fast path when in-bounds), then
	// copy the requested sub-rectangle into the macroblock prediction buffer.
	var tmp [256]uint8
	pred.InterPred16x16At(tmp[:], ref.Y, ref.StrideY, srcBaseX, srcBaseY, pred.MotionVector{X: mv.X, Y: mv.Y})
	for y := 0; y < h; y++ {
		copy(dst[(dstY+y)*16+dstX:(dstY+y)*16+dstX+w], tmp[y*16:y*16+w])
	}
}

func (d *Decoder) writeInterResidual(f *frame.Frame, mb *slice.MBInter, predicted []uint8, mbX, mbY, qp int) {
	// Dequant + IDCT residual blocks, then add to prediction. Keep a
	// transformed residual copy; mb.Coeffs remains quantized bitstream data.
	cbpLuma := mb.CBP & 0xF
	var residual [16][16]int16
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		group := blkIdx / 4
		if cbpLuma&(1<<uint(group)) != 0 {
			residual[blkIdx] = mb.Coeffs[blkIdx]
			transform.Dequant4x4(residual[blkIdx][:], qp)
			transform.IDCT4x4(residual[blkIdx][:])
		}
	}
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		bx := blk4x4X[blkIdx]
		by := blk4x4Y[blkIdx]
		for py := 0; py < 4; py++ {
			for px := 0; px < 4; px++ {
				pidx := (by+py)*16 + (bx + px)
				v := int(predicted[pidx]) + int(residual[blkIdx][py*4+px])
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				f.SetPixelY(mbX*16+bx+px, mbY*16+by+py, uint8(v))
			}
		}
	}
}

func (d *Decoder) reconstructMBBidi(f *frame.Frame, mb *slice.MBBidi, mbX, mbY, qp int) {
	// B-frame reconstruction: blend L0 and L1 predictions
	// For Direct mode (mb_type=0): use co-located MV from future reference
	// For L0/L1/Bi: use respective reference frames

	// Get reference frames
	var refL0, refL1 *frame.Frame
	if len(d.DPB.Frames) > 0 {
		refL0 = d.DPB.Frames[len(d.DPB.Frames)-1]
	}
	if len(d.DPB.Frames) > 1 {
		refL1 = d.DPB.Frames[len(d.DPB.Frames)-2]
	}
	if refL0 == nil {
		refL0 = f // self-reference fallback
	}
	if refL1 == nil {
		refL1 = refL0
	}

	// Simple implementation: copy from L0 reference (Direct/L0) or blend (Bi)
	predL0 := make([]uint8, 256)
	predL1 := make([]uint8, 256)

	mvL0 := mb.MVL0[0]
	mvL1 := mb.MVL1[0]

	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			sx0 := clampInt(mbX*16+x+int(mvL0.X>>2), 0, refL0.Width-1)
			sy0 := clampInt(mbY*16+y+int(mvL0.Y>>2), 0, refL0.Height-1)
			predL0[y*16+x] = refL0.PixelY(sx0, sy0)

			sx1 := clampInt(mbX*16+x+int(mvL1.X>>2), 0, refL1.Width-1)
			sy1 := clampInt(mbY*16+y+int(mvL1.Y>>2), 0, refL1.Height-1)
			predL1[y*16+x] = refL1.PixelY(sx1, sy1)
		}
	}

	// Blend and write
	blended := make([]uint8, 256)
	useBi := mb.MBType == slice.BMBTypeBi16x16 || mb.MBType == slice.BMBTypeDirect16x16
	if useBi {
		slice.BiPredBlend(blended, predL0, predL1, 256)
	} else if slice.BMBTypeL116x16 == mb.MBType {
		copy(blended, predL1)
	} else {
		copy(blended, predL0)
	}

	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			f.SetPixelY(mbX*16+x, mbY*16+y, blended[y*16+x])
		}
	}
}

// DecodedFrame is an alias for frame.Frame for CLI convenience.
type DecodedFrame = frame.Frame

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func predictMBMV(ctx []slice.MotionVector, mbIdx, mbX, mbY, mbWidth int) slice.MotionVector {
	var a, b, c slice.MotionVector
	availA := mbX > 0
	availB := mbY > 0
	availC := mbY > 0 && mbX+1 < mbWidth
	if availA {
		a = ctx[mbIdx-1]
	}
	if availB {
		b = ctx[mbIdx-mbWidth]
	}
	if availC {
		c = ctx[mbIdx-mbWidth+1]
	} else if mbY > 0 && mbX > 0 {
		// Spec fallback for unavailable top-right C: use top-left.
		c = ctx[mbIdx-mbWidth-1]
		availC = true
	}
	return slice.PredictMV(a, b, c, availA, availB, availC)
}

func applyMVPredictors(mb *slice.MBInter, pred slice.MotionVector) {
	add := func(mv *slice.MotionVector) {
		mv.X += pred.X
		mv.Y += pred.Y
	}
	switch mb.MBType {
	case slice.PMBTypeP16x16:
		add(&mb.MV[0])
	case slice.PMBTypeP16x8, slice.PMBTypeP8x16:
		add(&mb.MV[0])
		add(&mb.MV[1])
	case slice.PMBTypeP8x8, slice.PMBTypeP8x8ref0:
		for i := 0; i < 16; i++ {
			add(&mb.SubMV[i])
		}
		mb.MV[0] = mb.SubMV[0]
	}
}

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
	// Find PPS/SPS (peek at pps_id in slice header)
	// For simplicity, use first available PPS/SPS
	var pps *nal.PPS
	var sps *nal.SPS
	for _, p := range d.PPS {
		pps = p
		break
	}
	if pps == nil {
		return nil, fmt.Errorf("no PPS available")
	}
	for _, s := range d.SPS {
		sps = s
		break
	}
	if sps == nil {
		return nil, fmt.Errorf("no SPS available")
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
	for i := range d.intraModes { d.intraModes[i] = 2 } // default DC

	maxMBs := mbWidth * mbHeight
	if maxMBs > 10000 { maxMBs = 10000 } // safety limit
	currentQP := int(qp)
	nzCtx := make([][16]int, maxMBs) // CAVLC totalCoeff context per decoded MB
	skipRun := 0 // CAVLC P/B-slice mb_skip_run state
	for mbIdx := int(hdr.FirstMbInSlice); mbIdx < maxMBs; mbIdx++ {
		mbX := mbIdx % mbWidth
		mbY := mbIdx / mbWidth

		var leftNZ, topNZ *[16]int
		if mbX > 0 { leftNZ = &nzCtx[mbIdx-1] }
		if mbY > 0 { topNZ = &nzCtx[mbIdx-mbWidth] }

		if isIntra {
			mb := slice.DecodeMBIntraCtx(r, int32(currentQP), pps.EntropyCodingMode, pps.Transform8x8Mode, leftNZ, topNZ)
			mbQPDelta := int(mb.QPDelta); currentQP = (currentQP + mbQPDelta%52 + 52) % 52
			d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
			nzCtx[mbIdx] = mb.TotalCoeff
		} else if hdr.SliceType == slice.SliceTypeP {
			// CAVLC P-slices carry mb_skip_run before each non-skipped MB. Missing
			// this field shifts every P macroblock by one Exp-Golomb code.
			if pps.EntropyCodingMode == 0 {
				if skipRun == 0 {
					skipRun = int(r.ReadUE())
				}
				if skipRun > 0 {
					// P_Skip: no residual, no mvd/ref_idx. Use zero-MV P16x16 copy as
					// the conservative fallback until neighbour MV prediction is wired.
					d.reconstructMBInter(f, &slice.MBInter{MBType: slice.PMBTypeP16x16}, mbX, mbY, currentQP)
					skipRun--
					continue
				}
			}
			mbInter := slice.DecodeMBInterCtx(r, int32(currentQP), hdr.NumRefIdxL0Active, leftNZ, topNZ)
			currentQP = (currentQP + int(mbInter.QPDelta)%52 + 52) % 52
			if mbInter.MBType >= 5 {
				// P-slice intra MB syntax is not fully delegated yet; reconstruct as
				// intra type with no residual rather than corrupting the bitstream.
				mb := &slice.MBIntra{MBType: mbInter.MBType - 5}
				d.reconstructMB(f, mb, mbX, mbY, currentQP, sps)
			} else {
				d.reconstructMBInter(f, mbInter, mbX, mbY, currentQP)
				nzCtx[mbIdx] = mbInter.TotalCoeff
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

	// Apply deblocking filter
	d.applyDeblocking(f, sps, int(qp))

	return f, nil
}

func (d *Decoder) applyDeblocking(f *frame.Frame, sps *nal.SPS, qp int) {
	mbW := int(sps.PicWidthInMbs)
	mbH := int(sps.PicHeightInMapUnits)
	for mbY := 0; mbY < mbH; mbY++ {
		for mbX := 0; mbX < mbW; mbX++ {
			// Vertical edges (between left and current MB)
			if mbX > 0 {
				for y := 0; y < 16; y++ {
					x := mbX * 16
					// Simple 4-tap filter at MB boundary
					p1 := int(f.PixelY(x-2, mbY*16+y))
					p0 := int(f.PixelY(x-1, mbY*16+y))
					q0 := int(f.PixelY(x, mbY*16+y))
					q1 := int(f.PixelY(x+1, mbY*16+y))
					// Boundary strength 4 (intra edge) filter
					alpha := 7 + qp // simplified threshold
					if alpha > 255 { alpha = 255 }
					if abs264(p0-q0) < alpha && abs264(p1-p0) < alpha/2 && abs264(q1-q0) < alpha/2 {
						delta := clip264(-alpha/4, alpha/4, ((q0-p0)*4+(p1-q1)+4)>>3)
						f.SetPixelY(x-1, mbY*16+y, clip1_264(p0+delta))
						f.SetPixelY(x, mbY*16+y, clip1_264(q0-delta))
					}
				}
			}
			// Horizontal edges
			if mbY > 0 {
				for x := 0; x < 16; x++ {
					y := mbY * 16
					p1 := int(f.PixelY(mbX*16+x, y-2))
					p0 := int(f.PixelY(mbX*16+x, y-1))
					q0 := int(f.PixelY(mbX*16+x, y))
					q1 := int(f.PixelY(mbX*16+x, y+1))
					alpha := 7 + qp
					if alpha > 255 { alpha = 255 }
					if abs264(p0-q0) < alpha && abs264(p1-p0) < alpha/2 && abs264(q1-q0) < alpha/2 {
						delta := clip264(-alpha/4, alpha/4, ((q0-p0)*4+(p1-q1)+4)>>3)
						f.SetPixelY(mbX*16+x, y-1, clip1_264(p0+delta))
						f.SetPixelY(mbX*16+x, y, clip1_264(q0-delta))
					}
				}
			}
		}
	}
}

func abs264(x int) int { if x < 0 { return -x }; return x }
func clip264(lo, hi, v int) int { if v < lo { return lo }; if v > hi { return hi }; return v }
func clip1_264(v int) uint8 { if v < 0 { return 0 }; if v > 255 { return 255 }; return uint8(v) }

func (d *Decoder) reconstructMB(f *frame.Frame, mb *slice.MBIntra, mbX, mbY int, qp int, sps *nal.SPS) {
	if mb.MBType >= 1 && mb.MBType <= 24 {
		// I_16x16: predict whole 16x16 block
		d.reconstruct16x16(f, mb, mbX, mbY, qp)
	} else if mb.MBType == 0 {
		// I_NxN: predict each 4x4 block
		d.reconstruct4x4(f, mb, mbX, mbY, qp)
	}
	// I_PCM: raw samples (rare, skip)
}

func (d *Decoder) reconstruct16x16(f *frame.Frame, mb *slice.MBIntra, mbX, mbY, qp int) {
	mode := int(mb.Intra16x16PredMode)
	top := make([]uint8, 16)
	left := make([]uint8, 16)
	topLeft := uint8(128)
	if mbY > 0 {
		for x := 0; x < 16; x++ { top[x] = f.PixelY(mbX*16+x, mbY*16-1) }
	} else {
		for i := range top { top[i] = 128 }
	}
	if mbX > 0 {
		for y := 0; y < 16; y++ { left[y] = f.PixelY(mbX*16-1, mbY*16+y) }
	} else {
		for i := range left { left[i] = 128 }
	}
	if mbX > 0 && mbY > 0 { topLeft = f.PixelY(mbX*16-1, mbY*16-1) }

	predicted := make([]uint8, 256)
	if mode == pred.Intra16x16DC {
		// H.264 DC prediction handles unavailable edges specially. The pred
		// package API receives filled edge samples only, so do the availability
		// aware DC case here.
		var dc uint8
		if mbX > 0 && mbY > 0 {
			sum := 0
			for i := 0; i < 16; i++ { sum += int(top[i]) + int(left[i]) }
			dc = uint8((sum + 16) >> 5)
		} else if mbY > 0 {
			sum := 0
			for i := 0; i < 16; i++ { sum += int(top[i]) }
			dc = uint8((sum + 8) >> 4)
		} else if mbX > 0 {
			sum := 0
			for i := 0; i < 16; i++ { sum += int(left[i]) }
			dc = uint8((sum + 8) >> 4)
		} else {
			dc = 128
		}
		for i := range predicted[:256] { predicted[i] = dc }
	} else {
		pred.PredIntra16x16(predicted, mode, top, left, topLeft)
	}

	// Hadamard DC transform
	var dcBlock [16]int16
	for i := 0; i < 16; i++ { dcBlock[i] = mb.Coeffs[i][0] }
	transform.Hadamard4x4DC(dcBlock[:], qp)

	cbpLuma := mb.CodedBlockPattern & 0xF
	for blkIdx := 0; blkIdx < 16; blkIdx++ {
		bx := blk4x4X[blkIdx]
		by := blk4x4Y[blkIdx]
		var block [16]int16
		block[0] = dcBlock[blkIdx]
		if cbpLuma != 0 {
			for j := 1; j < 16; j++ { block[j] = mb.Coeffs[blkIdx][j] }
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
				if v < 0 { v = 0 }
				if v > 255 { v = 255 }
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
		if modeB < predMode { predMode = modeB }
		
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
		if mode > 8 { mode = 2 }
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
				for i := 0; i < 4; i++ { sum += int(top[i]) + int(left[i]) }
				dc = uint8((sum + 4) >> 3)
			} else if topAvail {
				sum := 0
				for i := 0; i < 4; i++ { sum += int(top[i]) }
				dc = uint8((sum + 2) >> 2)
			} else if leftAvail {
				sum := 0
				for i := 0; i < 4; i++ { sum += int(left[i]) }
				dc = uint8((sum + 2) >> 2)
			} else {
				dc = 128
			}
			for i := range predicted[:16] { predicted[i] = dc }
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
				if v < 0 { v = 0 }
				if v > 255 { v = 255 }
				f.SetPixelY(x0+px, y0+py, uint8(v))
			}
		}
	}
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
		// Dequant + IDCT residual blocks, then add to prediction
		cbpLuma := mb.CBP & 0xF
		for blkIdx := 0; blkIdx < 16; blkIdx++ {
			group := blkIdx / 4
			if cbpLuma&(1<<uint(group)) != 0 {
				block := mb.Coeffs[blkIdx]
				transform.Dequant4x4(block[:], qp)
				transform.IDCT4x4(block[:])
			}
		}
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				_ = blk4x4X
				// Simplified: use raster order for residual
				bi := (y/4)*4 + (x/4)
				py, px := y%4, x%4
				v := int(predicted[y*16+x]) + int(mb.Coeffs[bi][py*4+px])
				if v < 0 { v = 0 }
				if v > 255 { v = 255 }
				f.SetPixelY(mbX*16+x, mbY*16+y, uint8(v))
			}
		}

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
	if v < lo { return lo }
	if v > hi { return hi }
	return v
}

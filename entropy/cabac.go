package entropy

// CABAC (Context-Adaptive Binary Arithmetic Coding) decoder.
// ITU-T H.264 §9.3
//
// CABAC is the entropy coding mode for Main and High profiles.
// It uses an arithmetic coder with context models that adapt based on
// previously decoded syntax elements.

import "github.com/rcarmo/go-264/nal"

// CABACDecoder is a context-adaptive binary arithmetic coding engine.
type CABACDecoder struct {
	r         *nal.Reader
	codILow   uint32 // current interval low
	codIRange uint32 // current interval range
	count     int    // bits consumed
}

// Context model state (6 bits: pState + valMPS)
type CABACCtx struct {
	PState uint8 // probability state index (0..63)
	ValMPS uint8 // most probable symbol (0 or 1)
}

// Probability state transition tables (ITU-T H.264 Tables 9-45, 9-46)
var transIdxLPS = [64]uint8{
	0, 0, 1, 2, 2, 4, 4, 5, 6, 7, 8, 9, 9, 11, 11, 12,
	13, 13, 15, 15, 16, 16, 18, 18, 19, 19, 21, 21, 22, 22, 23, 24,
	24, 25, 26, 26, 27, 27, 28, 29, 29, 30, 30, 30, 31, 32, 32, 33,
	33, 33, 34, 34, 35, 35, 35, 36, 36, 36, 37, 37, 37, 38, 38, 63,
}

var transIdxMPS = [64]uint8{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48,
	49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 62, 63,
}

// Range lookup table for LPS (Table 9-48)
// Indexed by [pState][qRange] where qRange = (codIRange >> 6) & 3
var rangeTabLPS = [64][4]uint32{
	{128, 176, 208, 240}, {128, 167, 197, 227}, {128, 158, 187, 216},
	{123, 150, 178, 205}, {116, 142, 169, 195}, {111, 135, 160, 185},
	{105, 128, 152, 175}, {100, 122, 144, 166}, {95, 116, 137, 158},
	{90, 110, 130, 150}, {85, 104, 123, 142}, {81, 99, 117, 135},
	{77, 94, 111, 128}, {73, 89, 105, 122}, {69, 85, 100, 116},
	{66, 80, 95, 110}, {62, 76, 90, 104}, {59, 72, 86, 99},
	{56, 69, 81, 94}, {53, 65, 77, 89}, {51, 62, 73, 85},
	{48, 59, 69, 80}, {46, 56, 66, 76}, {43, 53, 63, 72},
	{41, 50, 59, 69}, {39, 48, 56, 65}, {37, 45, 54, 62},
	{35, 43, 51, 59}, {33, 41, 48, 56}, {32, 39, 46, 53},
	{30, 37, 43, 50}, {29, 35, 41, 48}, {27, 33, 39, 45},
	{26, 31, 37, 43}, {24, 30, 35, 41}, {23, 28, 33, 39},
	{22, 27, 32, 37}, {21, 26, 30, 35}, {20, 24, 29, 33},
	{19, 23, 27, 31}, {18, 22, 26, 30}, {17, 21, 25, 28},
	{16, 20, 23, 27}, {15, 19, 22, 25}, {14, 18, 21, 24},
	{14, 17, 20, 23}, {13, 16, 19, 22}, {12, 15, 18, 21},
	{12, 14, 17, 20}, {11, 14, 16, 19}, {11, 13, 15, 18},
	{10, 12, 15, 17}, {10, 12, 14, 16}, {9, 11, 13, 15},
	{9, 11, 12, 14}, {8, 10, 12, 14}, {8, 9, 11, 13},
	{7, 9, 11, 12}, {7, 9, 10, 12}, {7, 8, 10, 11},
	{6, 8, 9, 11}, {6, 7, 9, 10}, {6, 7, 8, 9}, {2, 2, 2, 2},
}

// NewCABACDecoder initializes the arithmetic decoder.
func NewCABACDecoder(r *nal.Reader) *CABACDecoder {
	d := &CABACDecoder{r: r}
	d.codIRange = 510
	d.codILow = uint32(r.ReadBits(9))
	d.count = 9
	return d
}

// DecodeBin decodes one binary decision using the given context.
func (d *CABACDecoder) DecodeBin(ctx *CABACCtx) uint32 {
	qIdx := (d.codIRange >> 6) & 3
	codIRangeLPS := rangeTabLPS[ctx.PState][qIdx]
	d.codIRange -= codIRangeLPS

	var binVal uint32
	if d.codILow >= d.codIRange {
		// LPS path
		binVal = 1 - uint32(ctx.ValMPS)
		d.codILow -= d.codIRange
		d.codIRange = codIRangeLPS
		if ctx.PState == 0 {
			ctx.ValMPS = 1 - ctx.ValMPS
		}
		ctx.PState = transIdxLPS[ctx.PState]
	} else {
		// MPS path
		binVal = uint32(ctx.ValMPS)
		ctx.PState = transIdxMPS[ctx.PState]
	}

	// Renormalize
	d.renorm()
	return binVal
}

// DecodeBypass decodes a binary decision with equal probability (p=0.5).
func (d *CABACDecoder) DecodeBypass() uint32 {
	d.codILow <<= 1
	d.codILow |= d.r.ReadBit()
	d.count++

	if d.codILow >= d.codIRange {
		d.codILow -= d.codIRange
		return 1
	}
	return 0
}

// DecodeTerminate decodes the end-of-slice flag.
func (d *CABACDecoder) DecodeTerminate() uint32 {
	d.codIRange -= 2
	if d.codILow >= d.codIRange {
		return 1 // end of slice
	}
	d.renorm()
	return 0
}

// DecodeUEG decodes an unsigned exp-Golomb binarization via CABAC bypass.
func (d *CABACDecoder) DecodeUEG(k int) uint32 {
	// Truncated unary + exp-Golomb suffix
	var v uint32
	for d.DecodeBypass() == 1 {
		v++
		if v > 32 {
			break
		}
	}
	if v < uint32(k) {
		return v
	}
	// Exp-Golomb suffix
	suffix := uint32(0)
	for i := 0; i < int(v)-k+1; i++ {
		suffix = (suffix << 1) | d.DecodeBypass()
	}
	return v + suffix
}

func (d *CABACDecoder) renorm() {
	for d.codIRange < 256 {
		d.codIRange <<= 1
		d.codILow <<= 1
		d.codILow |= d.r.ReadBit()
		d.count++
	}
}

// InitContextModels initializes CABAC context models for a given slice QP and cabac_init_idc.
// ITU-T H.264 §9.3.1
func InitContextModels(sliceQP int, cabacInitIDC int, isIntra bool) []CABACCtx {
	models := make([]CABACCtx, 1024)
	qp := clip3(sliceQP, 0, 51)
	if cabacInitIDC < 0 || cabacInitIDC > 2 {
		cabacInitIDC = 0
	}
	for i := range models {
		var mn [2]int8
		if isIntra {
			mn = cabacContextInitI[i]
		} else {
			mn = cabacContextInitPB[cabacInitIDC][i]
		}
		preCtxState := clip3(((int(mn[0])*qp)>>4)+int(mn[1]), 1, 126)
		if preCtxState <= 63 {
			models[i] = CABACCtx{PState: uint8(63 - preCtxState), ValMPS: 0}
		} else {
			models[i] = CABACCtx{PState: uint8(preCtxState - 64), ValMPS: 1}
		}
	}
	return models
}

func clip3(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

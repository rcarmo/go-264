package syntax

// CABAC macroblock-level syntax decoders.
// These decode pure H.264 syntax elements and carry no dependency on
// frame reconstruction; they belong in the slice package alongside the
// CAVLC equivalents in macroblock.go and pslice.go.

import (
	"fmt"
	"os"

	cabac "github.com/rcarmo/go-264/entropy/cabac"
)

// DecodeCABACCBP decodes the CABAC coded_block_pattern for one macroblock.
// H.264 §9.3.2.6 / FFmpeg h264_cabac.c decode_cabac_mb_cbp_luma/chroma.
func DecodeCABACCBP(dec *cabac.CABACDecoder, models []cabac.CABACCtx, leftCBP, topCBP uint32) uint32 {
	return DecodeCABACCBPWithTrace(dec, models, leftCBP, topCBP, "")
}

// DecodeCABACCBPWithTrace decodes CBP like DecodeCABACCBP and appends traceTag
// to optional GOCBP diagnostic rows. The tag keeps decoder-level fields such as
// macroblock index and POC out of the pure syntax helper's normal API.
func DecodeCABACCBPWithTrace(dec *cabac.CABACDecoder, models []cabac.CABACCtx, leftCBP, topCBP uint32, traceTag string) uint32 {
	if dec == nil || len(models) <= 83 {
		return 0
	}
	cbpA, cbpB := int(leftCBP), int(topCBP)
	cbp := uint32(0)
	traceCBP := os.Getenv("GO264_CABAC_CBP_TRACE") != ""
	traceBin := func(part string, idx, ctx int, cbpBefore uint32) uint32 {
		preLow, preRange, _ := dec.DebugState()
		preState := models[idx].DebugPackedState()
		bin := dec.DecodeBin(&models[idx])
		postLow, postRange, _ := dec.DebugState()
		if traceCBP {
			if traceTag != "" {
				fmt.Fprintf(os.Stderr, "GOCBP part=%s %s ctx=%d idx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d left=%03x top=%03x cbp_before=%02x\n", part, traceTag, ctx, idx, preState, preLow, preRange, bin, models[idx].DebugPackedState(), postLow, postRange, leftCBP, topCBP, cbpBefore)
			} else {
				fmt.Fprintf(os.Stderr, "GOCBP part=%s ctx=%d idx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d left=%03x top=%03x cbp_before=%02x\n", part, ctx, idx, preState, preLow, preRange, bin, models[idx].DebugPackedState(), postLow, postRange, leftCBP, topCBP, cbpBefore)
			}
		}
		return bin
	}
	ctx := boolInt(cbpA&0x02 == 0) + 2*boolInt(cbpB&0x04 == 0)
	bin := traceBin("luma0", 73+ctx, ctx, cbp)
	cbp |= bin
	ctx = boolInt(cbp&0x01 == 0) + 2*boolInt(cbpB&0x08 == 0)
	bin = traceBin("luma1", 73+ctx, ctx, cbp)
	cbp |= bin << 1
	ctx = boolInt(cbpA&0x08 == 0) + 2*boolInt(cbp&0x01 == 0)
	bin = traceBin("luma2", 73+ctx, ctx, cbp)
	cbp |= bin << 2
	ctx = boolInt(cbp&0x04 == 0) + 2*boolInt(cbp&0x02 == 0)
	bin = traceBin("luma3", 73+ctx, ctx, cbp)
	cbp |= bin << 3

	ctx = 0
	if (leftCBP>>4)&0x03 > 0 {
		ctx++
	}
	if (topCBP>>4)&0x03 > 0 {
		ctx += 2
	}
	bin = traceBin("chroma0", 77+ctx, ctx, cbp)
	if bin != 0 {
		ctx = 4
		if (leftCBP>>4)&0x03 == 2 {
			ctx++
		}
		if (topCBP>>4)&0x03 == 2 {
			ctx += 2
		}
		bin = traceBin("chroma1", 77+ctx, ctx, cbp)
		cbp |= (1 + bin) << 4
	}
	return cbp
}

// DecodeCABACDQP decodes the CABAC QP delta for one macroblock.
// H.264 §9.3.2.7 / FFmpeg h264_cabac.c decode_cabac_mb_dqp.
func DecodeCABACDQP(dec *cabac.CABACDecoder, models []cabac.CABACCtx, lastQScaleDiff int) int {
	if dec == nil || len(models) <= 63 {
		return 0
	}
	if dec.DecodeBin(&models[60+boolInt(lastQScaleDiff != 0)]) == 0 {
		return 0
	}
	val := 1
	ctx := 2
	for dec.DecodeBin(&models[60+ctx]) == 1 {
		ctx = 3
		val++
		if val > 102 {
			return 0
		}
	}
	if val&1 != 0 {
		return (val + 1) >> 1
	}
	return -((val + 1) >> 1)
}

// DecodeCABACRef decodes a CABAC reference frame index.
// H.264 §9.3.2.3 / FFmpeg h264_cabac.c decode_cabac_mb_ref.
func DecodeCABACRef(dec *cabac.CABACDecoder, models []cabac.CABACCtx, ctx int) uint32 {
	return DecodeCABACRefWithTrace(dec, models, ctx, "")
}

func DecodeCABACRefWithTrace(dec *cabac.CABACDecoder, models []cabac.CABACCtx, ctx int, traceTag string) uint32 {
	if dec == nil || len(models) <= 58 {
		return 0
	}
	if ctx < 0 {
		ctx = 0
	}
	if ctx > 3 {
		ctx = 3
	}
	ref := uint32(0)
	for 54+ctx < len(models) {
		preLow, preRange, _ := dec.DebugState()
		preState := models[54+ctx].DebugPackedState()
		bin := dec.DecodeBin(&models[54+ctx])
		postLow, postRange, _ := dec.DebugState()
		if traceTag != "" {
			fmt.Fprintf(os.Stderr, "GOREFBIN %s ctx=%d idx=%d state=%d low=%d range=%d bin=%d post_state=%d post_low=%d post_range=%d ref_before=%d\n", traceTag, ctx, 54+ctx, preState, preLow, preRange, bin, models[54+ctx].DebugPackedState(), postLow, postRange, ref)
		}
		if bin == 0 {
			break
		}
		ref++
		ctx = (ctx >> 2) + 4
		if ref >= 32 {
			return 0
		}
	}
	return ref
}

// DecodeCABACMVD decodes one CABAC motion vector difference component.
// ctxBase: 40 for mvd_x, 47 for mvd_y.
// amvd: |left_mvd| + |top_mvd| context sum (0 when unavailable).
// H.264 §9.3.2.4 / FFmpeg h264_cabac.c decode_cabac_mb_mvd.
func DecodeCABACMVD(dec *cabac.CABACDecoder, models []cabac.CABACCtx, ctxBase int, amvd int) int16 {
	if dec == nil || ctxBase < 0 || len(models) <= ctxBase+6 {
		return 0
	}
	if amvd < 0 {
		amvd = 0
	}
	ctx := 0
	if amvd > 2 {
		ctx++
	}
	if amvd > 32 {
		ctx++
	}
	if dec.DecodeBin(&models[ctxBase+ctx]) == 0 {
		return 0
	}
	mvd := 1
	ctxBase += 3
	ctx = ctxBase
	for mvd < 9 && dec.DecodeBin(&models[ctx]) == 1 {
		if mvd < 4 {
			ctx++
		}
		mvd++
	}
	if mvd >= 9 {
		k := 3
		for dec.DecodeBypass() == 1 {
			mvd += 1 << uint(k)
			k++
			if k > 24 {
				return 0
			}
		}
		for k--; k >= 0; k-- {
			mvd += int(dec.DecodeBypass()) << uint(k)
		}
	}
	if dec.DecodeBypass() == 1 {
		return int16(-mvd)
	}
	return int16(mvd)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

package cabac

// FFmpeg-compatible CABAC arithmetic decoder.
// Uses the same table layout and 32-bit low register as FFmpeg's get_cabac_inline.
// This produces bit-exact results matching x264/FFmpeg-encoded H.264 streams.
import (
	"fmt"
	"os"
)

const cabacBits = 16
const cabacMask = (1 << cabacBits) - 1

// InitFFCompat reinitializes the decoder using FFmpeg's 2-byte init and combined
// state representation. Call this INSTEAD of the old Reset path.
func (d *CABACDecoder) InitFFCompat() {
	if d == nil || d.r == nil {
		return
	}
	// FFmpeg init: low = (byte0<<18) + (byte1<<10) + (byte2<<2) + 2
	// For aligned streams (which H.264 always is after byte-align):
	d.ffBuf = d.r.RemainingBytes()
	d.ffPos = 0
	if len(d.ffBuf) < 2 {
		return
	}
	b0 := uint32(d.ffBuf[0])
	b1 := uint32(d.ffBuf[1])
	d.ffPos = 2
	d.codILow = (b0 << 18) + (b1 << 10) + (1 << 9)
	d.codIRange = 0x1FE
	d.count = 16
	if os.Getenv("GO264_FF_INIT_TRACE") != "" {
		fmt.Fprintf(os.Stderr, "FFINIT b0=%02x b1=%02x low=%d range=%d\n", b0, b1, d.codILow, d.codIRange)
	}
}

// DecodeBinFF decodes one binary decision using FFmpeg's exact arithmetic.
// The context uses the combined state byte (same as FFmpeg's cabac_state[]).
func (d *CABACDecoder) DecodeBinFF(state *uint8) uint32 {
	if d == nil || d.r == nil || state == nil {
		return 0
	}
	s := int(*state)
	rangeLPS := uint32(lpsRange[2*int(d.codIRange&0xC0)+s])
	d.codIRange -= rangeLPS
	// Branchless LPS/MPS decision: if (range<<17) <= low → LPS
	lpsMask := int32((int64(d.codIRange)<<(cabacBits+1) - int64(d.codILow))) >> 31
	d.codILow -= (d.codIRange << (cabacBits + 1)) & uint32(lpsMask)
	d.codIRange += (rangeLPS - d.codIRange) & uint32(lpsMask)
	s ^= int(lpsMask)
	*state = mlpsState[128+s]
	bit := uint32(s & 1)
	// Renormalize
	shift := normShift[d.codIRange]
	d.codIRange <<= uint(shift)
	d.codILow <<= uint(shift)
	if d.codILow&cabacMask == 0 {
		d.refill()
	}
	if d.BinTrace > 0 {
		d.BinTrace--
		fmt.Fprintf(os.Stderr, "GOBIN bin=%d range=%d low=%d\n", bit, d.codIRange, d.codILow>>1)
	}
	return bit
}

// refill reads 2 bytes from the bitstream into the low register.
// Matches FFmpeg's refill2 for CABAC_BITS=16.
func (d *CABACDecoder) refill() {
	if d.ffBuf == nil || d.ffPos+1 >= len(d.ffBuf) {
		return
	}
	// Count trailing zeros above CABAC_BITS to find shift
	i := uint(0)
	for bit := uint(cabacBits); bit < 32; bit++ {
		if d.codILow&(1<<bit) != 0 {
			break
		}
		i++
	}
	b0 := uint32(d.ffBuf[d.ffPos])
	b1 := uint32(0)
	if d.ffPos+1 < len(d.ffBuf) {
		b1 = uint32(d.ffBuf[d.ffPos+1])
	}
	d.ffPos += 2
	x := (b0 << 9) + (b1 << 1) - cabacMask
	d.codILow += x << i
	d.count += 16
}

// DecodeBypassFF decodes a bypass bin (equiprobable) using FFmpeg's arithmetic.
func (d *CABACDecoder) DecodeBypassFF() uint32 {
	if d == nil || d.r == nil {
		return 0
	}
	d.codILow += d.codILow
	if d.codILow&cabacMask == 0 {
		d.refill()
	}
	rng := d.codIRange << (cabacBits + 1)
	var bit uint32
	if d.codILow >= rng {
		d.codILow -= rng
		bit = 1
	}
	if d.BinTrace > 0 {
		d.BinTrace--
		fmt.Fprintf(os.Stderr, "GOBYPASS bin=%d range=%d low=%d\n", bit, d.codIRange, d.codILow>>1)
	}
	return bit
}

// DecodeTerminateFF decodes the end_of_slice_flag using FFmpeg's arithmetic.
func (d *CABACDecoder) DecodeTerminateFF() uint32 {
	if d == nil || d.r == nil {
		return 0
	}
	d.codIRange -= 2
	rng := d.codIRange << (cabacBits + 1)
	if d.codILow < rng {
		// Not terminated — renormalize
		shift := normShift[d.codIRange]
		d.codIRange <<= uint(shift)
		d.codILow <<= uint(shift)
		if d.codILow&cabacMask == 0 {
			d.refill()
		}
		return 0
	}
	return 1
}

// InitContextModelsFF initializes context models as FFmpeg's combined state bytes.
func InitContextModelsFF(sliceQP int, cabacInitIDC int, isIntra bool) []uint8 {
	models := make([]uint8, 1024)
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
			models[i] = uint8(2 * (63 - preCtxState))
		} else {
			models[i] = uint8(2*(preCtxState-64) + 1)
		}
	}
	return models
}

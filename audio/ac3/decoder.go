package ac3

import (
	"context"
	"fmt"
	"math"

	"github.com/rcarmo/go-264/audio/pcm"
)

type blockState struct {
	blockSwitch                                             [5]uint8
	ditherFlag                                              [5]uint8
	dynRange, dynRange2, dynRangeExists, dynRange2Exists    uint8
	coupling, phaseFlagsInUse                               uint8
	channelInCoupling                                       [5]uint8
	couplingBegin, couplingEnd                              uint8
	couplingBandStructure                                   [18]uint8
	couplingCoordinates                                     [5]uint8
	masterCouplingCoordinate                                [5]uint8
	couplingCoordinateExponent                              [5][18]uint8
	couplingCoordinateMantissa                              [5][18]uint8
	phaseFlag                                               [18]uint8
	rematrixFlag                                            [4]uint8
	couplingExponentStrategy                                uint8
	channelExponentStrategy                                 [5]uint8
	lfeExponentStrategy                                     uint8
	channelBandwidth                                        [5]uint8
	exponents                                               [5][256]uint8
	couplingExponents                                       [256]uint8
	lfeExponents                                            [7]uint8
	bap                                                     [5][256]uint8
	couplingBAP                                             [256]uint8
	lfeBAP                                                  [7]uint8
	coefficients                                            [MaxChannels][256]float64
	couplingCoefficients                                    [256]float64
	endMantissa                                             [5]uint16
	couplingStartMantissa, couplingEndMantissa              uint16
	couplingBands                                           uint8
	bitAllocationExists                                     uint8
	slowDecayCode, fastDecayCode, slowGainCode              uint8
	dbPerBitCode, floorCode                                 uint8
	snrOffsetExists, coarseSNROffset                        uint8
	fineSNROffset, fineGainCode                             [5]uint8
	couplingFineSNROffset, couplingFineGainCode             uint8
	lfeFineSNROffset, lfeFineGainCode                       uint8
	couplingFastLeak, couplingSlowLeak                      uint8
	couplingDeltaMode, couplingDeltaSegments                uint8
	couplingDeltaOffset, couplingDeltaLength, couplingDelta [8]uint8
	deltaMode, deltaSegments                                [5]uint8
	deltaOffset, deltaLength, delta                         [5][8]uint8
}

type mantissaGroups struct {
	v1 [3]int
	p1 int
	v2 [3]int
	p2 int
	v4 [2]int
	p4 int
}

func (d *Decoder) decodeBlocks(ctx context.Context, br bitReader, h header, planar *[MaxChannels][FrameSamples]float64) error {
	s := &d.state
	*s = blockState{couplingDeltaMode: 2, couplingFastLeak: 3, couplingSlowLeak: 3}
	for ch := range s.deltaMode {
		s.deltaMode[ch] = 2
	}
	for block := 0; block < 6; block++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		groups := mantissaGroups{p1: 3, p2: 3, p4: 2}
		for ch := 0; ch < int(h.nfchans); ch++ {
			s.blockSwitch[ch] = uint8(br.read(1))
		}
		for ch := 0; ch < int(h.nfchans); ch++ {
			s.ditherFlag[ch] = uint8(br.read(1))
		}
		s.dynRangeExists = uint8(br.read(1))
		if s.dynRangeExists != 0 {
			s.dynRange = uint8(br.read(8))
		}
		if h.acmod == 0 {
			s.dynRange2Exists = uint8(br.read(1))
			if s.dynRange2Exists != 0 {
				s.dynRange2 = uint8(br.read(8))
			}
		}
		if br.read(1) != 0 {
			s.coupling = uint8(br.read(1))
			clear(s.channelInCoupling[:])
			if s.coupling != 0 {
				for ch := 0; ch < int(h.nfchans); ch++ {
					s.channelInCoupling[ch] = uint8(br.read(1))
				}
				if h.acmod == 2 {
					s.phaseFlagsInUse = uint8(br.read(1))
				}
				s.couplingBegin = uint8(br.read(4))
				s.couplingEnd = uint8(br.read(4))
				if int(s.couplingEnd)+3 <= int(s.couplingBegin) {
					return malformed("coupling band range")
				}
				subbands := 3 + int(s.couplingEnd) - int(s.couplingBegin)
				s.couplingBandStructure[0] = 0
				s.couplingBands = 1
				for i := 1; i < subbands; i++ {
					s.couplingBandStructure[i] = uint8(br.read(1))
					if s.couplingBandStructure[i] == 0 {
						s.couplingBands++
					}
				}
				s.couplingStartMantissa = uint16(37 + 12*int(s.couplingBegin))
				s.couplingEndMantissa = uint16(37 + 12*(int(s.couplingEnd)+3))
			}
		}
		if s.coupling != 0 {
			for ch := 0; ch < int(h.nfchans); ch++ {
				if s.channelInCoupling[ch] == 0 {
					continue
				}
				s.couplingCoordinates[ch] = uint8(br.read(1))
				if s.couplingCoordinates[ch] != 0 {
					s.masterCouplingCoordinate[ch] = uint8(br.read(2))
					for i := 0; i < int(s.couplingBands); i++ {
						s.couplingCoordinateExponent[ch][i] = uint8(br.read(4))
						s.couplingCoordinateMantissa[ch][i] = uint8(br.read(4))
					}
				}
			}
			if h.acmod == 2 && s.phaseFlagsInUse != 0 && (s.couplingCoordinates[0] != 0 || s.couplingCoordinates[1] != 0) {
				for i := 0; i < int(s.couplingBands); i++ {
					s.phaseFlag[i] = uint8(br.read(1))
				}
			}
		}
		if h.acmod == 2 && br.read(1) != 0 {
			clear(s.rematrixFlag[:])
			for i := 0; i < rematrixBands(s); i++ {
				s.rematrixFlag[i] = uint8(br.read(1))
			}
		}
		couplingExponentNew := false
		if s.coupling != 0 {
			s.couplingExponentStrategy = uint8(br.read(2))
			couplingExponentNew = s.couplingExponentStrategy != 0
		}
		var exponentNew [5]bool
		for ch := 0; ch < int(h.nfchans); ch++ {
			s.channelExponentStrategy[ch] = uint8(br.read(2))
			exponentNew[ch] = s.channelExponentStrategy[ch] != 0
		}
		lfeExponentNew := false
		if h.lfeon != 0 {
			s.lfeExponentStrategy = uint8(br.read(1))
			lfeExponentNew = s.lfeExponentStrategy != 0
		}
		for ch := 0; ch < int(h.nfchans); ch++ {
			if exponentNew[ch] && s.channelInCoupling[ch] == 0 {
				s.channelBandwidth[ch] = uint8(br.read(6))
			}
		}
		if s.coupling != 0 && couplingExponentNew {
			count := int(s.couplingEndMantissa - s.couplingStartMantissa)
			var expanded [257]uint8
			if !decodeExponents(&br, int(s.couplingExponentStrategy), int(br.read(4)<<1), count+1, expanded[:]) {
				return malformed("coupling exponents")
			}
			copy(s.couplingExponents[s.couplingStartMantissa:s.couplingEndMantissa], expanded[1:count+1])
		}
		for ch := 0; ch < int(h.nfchans); ch++ {
			if s.channelInCoupling[ch] != 0 {
				s.endMantissa[ch] = s.couplingStartMantissa
			} else {
				s.endMantissa[ch] = uint16(37 + 3*(int(s.channelBandwidth[ch])+12))
			}
			if s.endMantissa[ch] > 253 {
				return malformed("channel bandwidth")
			}
			if exponentNew[ch] {
				if !decodeExponents(&br, int(s.channelExponentStrategy[ch]), int(br.read(4)), int(s.endMantissa[ch]), s.exponents[ch][:]) {
					return malformed("channel exponents")
				}
				br.skip(2) // gainrng
			}
		}
		if h.lfeon != 0 && lfeExponentNew {
			if !decodeExponents(&br, 1, int(br.read(4)), 7, s.lfeExponents[:]) {
				return malformed("LFE exponents")
			}
		}
		if br.read(1) != 0 {
			s.bitAllocationExists = 1
			s.slowDecayCode = uint8(br.read(2))
			s.fastDecayCode = uint8(br.read(2))
			s.slowGainCode = uint8(br.read(2))
			s.dbPerBitCode = uint8(br.read(2))
			s.floorCode = uint8(br.read(3))
		}
		if br.read(1) != 0 {
			s.snrOffsetExists = 1
			s.coarseSNROffset = uint8(br.read(6))
			if s.coupling != 0 {
				s.couplingFineSNROffset = uint8(br.read(4))
				s.couplingFineGainCode = uint8(br.read(3))
			}
			for ch := 0; ch < int(h.nfchans); ch++ {
				s.fineSNROffset[ch] = uint8(br.read(4))
				s.fineGainCode[ch] = uint8(br.read(3))
			}
			if h.lfeon != 0 {
				s.lfeFineSNROffset = uint8(br.read(4))
				s.lfeFineGainCode = uint8(br.read(3))
			}
		}
		if s.coupling != 0 && br.read(1) != 0 {
			s.couplingFastLeak = uint8(br.read(3))
			s.couplingSlowLeak = uint8(br.read(3))
		}
		if br.read(1) != 0 {
			if s.coupling != 0 {
				s.couplingDeltaMode = uint8(br.read(2))
			}
			for ch := 0; ch < int(h.nfchans); ch++ {
				s.deltaMode[ch] = uint8(br.read(2))
			}
			if s.coupling != 0 && s.couplingDeltaMode == 1 {
				s.couplingDeltaSegments = uint8(br.read(3))
				for i := 0; i <= int(s.couplingDeltaSegments); i++ {
					s.couplingDeltaOffset[i] = uint8(br.read(5))
					s.couplingDeltaLength[i] = uint8(br.read(4))
					s.couplingDelta[i] = uint8(br.read(3))
				}
			}
			for ch := 0; ch < int(h.nfchans); ch++ {
				if s.deltaMode[ch] != 1 {
					continue
				}
				s.deltaSegments[ch] = uint8(br.read(3))
				for i := 0; i <= int(s.deltaSegments[ch]); i++ {
					s.deltaOffset[ch][i] = uint8(br.read(5))
					s.deltaLength[ch][i] = uint8(br.read(4))
					s.delta[ch][i] = uint8(br.read(3))
				}
			}
		}
		if br.read(1) != 0 {
			br.skip(int(br.read(9)) * 8)
		}
		if br.failed {
			return malformed("audio block side information")
		}
		for ch := 0; ch < int(h.nfchans); ch++ {
			bitAllocate(h, s, s.exponents[ch][:], 0, int(s.endMantissa[ch]), int(s.fineGainCode[ch]), int(s.fineSNROffset[ch]), false, s.deltaMode[ch], s.deltaSegments[ch], s.deltaOffset[ch][:], s.deltaLength[ch][:], s.delta[ch][:], s.bap[ch][:])
		}
		if s.coupling != 0 {
			bitAllocate(h, s, s.couplingExponents[:], int(s.couplingStartMantissa), int(s.couplingEndMantissa), int(s.couplingFineGainCode), int(s.couplingFineSNROffset), true, s.couplingDeltaMode, s.couplingDeltaSegments, s.couplingDeltaOffset[:], s.couplingDeltaLength[:], s.couplingDelta[:], s.couplingBAP[:])
		}
		if h.lfeon != 0 {
			bitAllocate(h, s, s.lfeExponents[:], 0, 7, int(s.lfeFineGainCode), int(s.lfeFineSNROffset), false, 2, 0, nil, nil, nil, s.lfeBAP[:])
		}
		for ch := range s.coefficients {
			clear(s.coefficients[ch][:])
		}
		clear(s.couplingCoefficients[:])
		gotCoupling := false
		for ch := 0; ch < int(h.nfchans); ch++ {
			for i := 0; i < int(s.endMantissa[ch]); i++ {
				value, ok := d.nextMantissa(&br, int(s.bap[ch][i]), int(s.exponents[ch][i]), &groups)
				if !ok {
					return malformed("channel mantissas")
				}
				if s.bap[ch][i] == 0 && s.ditherFlag[ch] != 0 {
					value = d.ditherSample(int(s.exponents[ch][i]))
				}
				s.coefficients[ch][i] = value
			}
			if s.coupling != 0 && s.channelInCoupling[ch] != 0 && !gotCoupling {
				for i := int(s.couplingStartMantissa); i < int(s.couplingEndMantissa); i++ {
					value, ok := d.nextMantissa(&br, int(s.couplingBAP[i]), int(s.couplingExponents[i]), &groups)
					if !ok {
						return malformed("coupling mantissas")
					}
					s.couplingCoefficients[i] = value
				}
				gotCoupling = true
			}
		}
		if h.lfeon != 0 {
			for i := 0; i < 7; i++ {
				value, ok := d.nextMantissa(&br, int(s.lfeBAP[i]), int(s.lfeExponents[i]), &groups)
				if !ok {
					return malformed("LFE mantissas")
				}
				s.coefficients[h.nfchans][i] = value
			}
		}
		d.uncouple(h, s)
		rematrix(h, s)
		applyGain(h, s)
		for ch := 0; ch < int(h.nfchans+h.lfeon); ch++ {
			d.imdctBlock(ch, s.coefficients[ch], ch < int(h.nfchans) && s.blockSwitch[ch] != 0, planar[ch][block*256:(block+1)*256])
		}
	}
	if br.failed || br.remaining() < 16 {
		return malformed("auxdata/CRC boundary")
	}
	return nil
}

func malformed(stage string) error { return fmt.Errorf("%w: AC-3 %s", pcm.ErrMalformed, stage) }

func rematrixBands(s *blockState) int {
	if s.coupling == 0 || s.couplingBegin > 2 {
		return 4
	}
	if s.couplingBegin > 0 {
		return 3
	}
	return 2
}

func decodeExponents(br *bitReader, strategy, absolute, count int, out []uint8) bool {
	if strategy == 0 || count == 0 || count > len(out) {
		return false
	}
	groupSize := 1 << (strategy - 1)
	groups := 0
	if count > 1 {
		groups = (count - 1 + 3*(groupSize-1)) / (3 * groupSize)
	}
	position, previous := 1, absolute
	out[0] = uint8(absolute)
	for range groups {
		code := int(br.read(7))
		values := [3]int{code / 25, (code % 25) / 5, code % 5}
		for _, value := range values {
			previous += value - 2
			if previous < 0 || previous > 24 {
				return false
			}
			for range groupSize {
				if position >= count {
					break
				}
				out[position] = uint8(previous)
				position++
			}
		}
	}
	for position < count {
		out[position] = uint8(previous)
		position++
	}
	return !br.failed
}

func (d *Decoder) nextMantissa(br *bitReader, bap, exponent int, g *mantissaGroups) (float64, bool) {
	bits := [...]uint{0, 5, 7, 3, 7, 4, 5, 6, 7, 8, 9, 10, 11, 12, 14, 16}
	if bap == 0 {
		return 0, true
	}
	code, levels := 0, 0
	switch bap {
	case 1:
		levels = 3
		if g.p1 >= 3 {
			value := int(br.read(5))
			if value >= 27 {
				return 0, false
			}
			g.v1 = [3]int{value / 9, (value % 9) / 3, value % 3}
			g.p1 = 0
		}
		code = g.v1[g.p1]
		g.p1++
	case 2:
		levels = 5
		if g.p2 >= 3 {
			value := int(br.read(7))
			if value >= 125 {
				return 0, false
			}
			g.v2 = [3]int{value / 25, (value % 25) / 5, value % 5}
			g.p2 = 0
		}
		code = g.v2[g.p2]
		g.p2++
	case 4:
		levels = 11
		if g.p4 >= 2 {
			value := int(br.read(7))
			if value >= 121 {
				return 0, false
			}
			g.v4 = [2]int{value / 11, value % 11}
			g.p4 = 0
		}
		code = g.v4[g.p4]
		g.p4++
	case 3, 5:
		if bap == 3 {
			levels = 7
		} else {
			levels = 15
		}
		code = int(br.read(bits[bap]))
		if code >= levels {
			return 0, false
		}
	default:
		n := bits[bap]
		raw := br.read(n)
		signed := int32(raw)
		if raw&(1<<(n-1)) != 0 {
			signed = int32(int64(raw) - (1 << n))
		}
		return math.Ldexp(float64(signed)/float64(uint64(1)<<(n-1)), -exponent), !br.failed
	}
	return math.Ldexp(float64(2*code-(levels-1))/float64(levels), -exponent), !br.failed
}

func (d *Decoder) ditherSample(exponent int) float64 {
	x := d.dither
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	d.dither = x
	return math.Ldexp(float64(int32(x&0xffff)-32768)/32768/math.Sqrt2, -exponent)
}

func (d *Decoder) uncouple(h header, s *blockState) {
	if s.coupling == 0 {
		return
	}
	for ch := 0; ch < int(h.nfchans); ch++ {
		if s.channelInCoupling[ch] == 0 {
			continue
		}
		subband, band := 0, 0
		for i := int(s.couplingStartMantissa); i < int(s.couplingEndMantissa); i++ {
			if i == int(s.couplingStartMantissa) || (i-int(s.couplingStartMantissa))%12 == 0 {
				if subband != 0 && s.couplingBandStructure[subband] == 0 {
					band++
				}
				subband++
			}
			exponent := s.couplingCoordinateExponent[ch][band]
			mantissa := float64(s.couplingCoordinateMantissa[ch][band]+16) / 32
			if exponent == 15 {
				mantissa = float64(s.couplingCoordinateMantissa[ch][band]) / 16
			}
			coordinate := math.Ldexp(mantissa, -int(exponent)-3*int(s.masterCouplingCoordinate[ch])) * 8
			value := s.couplingCoefficients[i] * coordinate
			if h.acmod == 2 && ch == 1 && s.phaseFlagsInUse != 0 && s.phaseFlag[band] != 0 {
				value = -value
			}
			if s.couplingBAP[i] == 0 && s.ditherFlag[ch] != 0 {
				value = d.ditherSample(int(s.couplingExponents[i]))
			}
			s.coefficients[ch][i] = value
		}
	}
}

func rematrix(h header, s *blockState) {
	if h.acmod != 2 {
		return
	}
	edges := [...]int{13, 25, 37, 61, 253}
	for band := 0; band < rematrixBands(s); band++ {
		if s.rematrixFlag[band] == 0 {
			continue
		}
		end := min(edges[band+1], min(int(s.endMantissa[0]), int(s.endMantissa[1])))
		for i := edges[band]; i < end; i++ {
			left, right := s.coefficients[0][i], s.coefficients[1][i]
			s.coefficients[0][i], s.coefficients[1][i] = left+right, left-right
		}
	}
}

func gainWord(word uint8, mantissaBits uint) float64 {
	exponentBits := uint(8) - mantissaBits
	raw := uint(word) >> mantissaBits
	sign := uint(1) << (exponentBits - 1)
	exponent := int(raw^sign) - int(sign)
	mask := uint(1)<<mantissaBits - 1
	return math.Ldexp(float64((uint(1)<<mantissaBits)+(uint(word)&mask))/float64(uint(1)<<mantissaBits), exponent)
}

func applyGain(h header, s *blockState) {
	for ch := 0; ch < int(h.nfchans+h.lfeon); ch++ {
		second := h.acmod == 0 && ch == 1
		dial, dynamic := h.dialnorm, s.dynRange
		if second {
			dial, dynamic = h.dialnorm2, s.dynRange2
		}
		gain := math.Pow(10, (float64(dial)-31)/20) * gainWord(dynamic, 5)
		for i := range s.coefficients[ch] {
			s.coefficients[ch][i] *= gain
		}
	}
}

func bitAllocate(h header, s *blockState, exponents []uint8, start, end, gainCode, fineOffset int, coupling bool, deltaMode, deltaSegments uint8, deltaOffset, deltaLength, delta []uint8, bap []uint8) {
	if end <= start || end > 256 {
		return
	}
	clear(bap[start:end])
	if s.coarseSNROffset == 0 && fineOffset == 0 {
		return
	}
	var psd [256]int
	var bandPSD, excite, mask [50]int
	for i := start; i < end; i++ {
		psd[i] = 3072 - int(exponents[i])*128
	}
	j, band := start, maskBand(start)
	lastBin := 0
	for {
		lastBin = min(int(bandStart[band])+int(bandSize[band]), end)
		bandPSD[band] = psd[j]
		j++
		for j < lastBin {
			bandPSD[band] = logarithmicAdd(bandPSD[band], psd[j])
			j++
		}
		band++
		if end <= lastBin {
			break
		}
	}
	bandBegin, bandEnd := maskBand(start), maskBand(end-1)+1
	sdecay, fdecay := slowDecay[s.slowDecayCode], fastDecay[s.fastDecayCode]
	sgain, fgain := slowGain[s.slowGainCode], fastGain[gainCode]
	fastLeak, slowLeak, lowComp, begin := 0, 0, 0, bandBegin
	if !coupling && bandBegin == 0 {
		lowComp = lowCompCalc(lowComp, bandPSD[0], bandPSD[1], 0)
		excite[0] = bandPSD[0] - fgain - lowComp
		lowComp = lowCompCalc(lowComp, bandPSD[1], bandPSD[2], 1)
		excite[1] = bandPSD[1] - fgain - lowComp
		begin = 7
		for b := 2; b < 7; b++ {
			if bandEnd != 7 || b != 6 {
				lowComp = lowCompCalc(lowComp, bandPSD[b], bandPSD[b+1], b)
			}
			fastLeak, slowLeak = bandPSD[b]-fgain, bandPSD[b]-sgain
			excite[b] = fastLeak - lowComp
			if (bandEnd != 7 || b != 6) && bandPSD[b] <= bandPSD[b+1] {
				begin = b + 1
				break
			}
		}
		for b := begin; b < min(bandEnd, 22); b++ {
			if bandEnd != 7 || b != 6 {
				lowComp = lowCompCalc(lowComp, bandPSD[b], bandPSD[b+1], b)
			}
			fastLeak = max(fastLeak-fdecay, bandPSD[b]-fgain)
			slowLeak = max(slowLeak-sdecay, bandPSD[b]-sgain)
			excite[b] = max(fastLeak-lowComp, slowLeak)
		}
		begin = 22
	} else if coupling {
		fastLeak, slowLeak = int(s.couplingFastLeak)*256+768, int(s.couplingSlowLeak)*256+768
	}
	for b := begin; b < bandEnd; b++ {
		fastLeak = max(fastLeak-fdecay, bandPSD[b]-fgain)
		slowLeak = max(slowLeak-sdecay, bandPSD[b]-sgain)
		excite[b] = max(fastLeak, slowLeak)
	}
	knee := dbPerBit[s.dbPerBitCode]
	for b := bandBegin; b < bandEnd; b++ {
		if bandPSD[b] < knee {
			excite[b] += (knee - bandPSD[b]) >> 2
		}
		mask[b] = max(excite[b], hearingThreshold[h.fscod][b])
	}
	if deltaMode <= 1 {
		bb := 0
		for segment := 0; segment <= int(deltaSegments) && segment < 8; segment++ {
			bb += int(deltaOffset[segment])
			value := int(delta[segment])
			change := 0
			if value >= 4 {
				change = (value - 3) << 7
			} else {
				change = (value - 4) << 7
			}
			for n := 0; n < int(deltaLength[segment]) && bb < 50; n++ {
				mask[bb] += change
				bb++
			}
		}
	}
	snrOffset := (((int(s.coarseSNROffset) - 15) << 4) + fineOffset) << 2
	i, b := start, maskBand(start)
	for {
		lastBin = min(int(bandStart[b])+int(bandSize[b]), end)
		mask[b] -= snrOffset
		mask[b] -= floorTable[s.floorCode]
		if mask[b] < 0 {
			mask[b] = 0
		}
		mask[b] &= 0x1fe0
		mask[b] += floorTable[s.floorCode]
		for i < lastBin {
			address := (psd[i] - mask[b]) >> 5
			address = min(63, max(0, address))
			bap[i] = bapTable[address]
			i++
		}
		b++
		if end <= lastBin {
			break
		}
	}
}

func maskBand(bin int) int {
	for i := 0; i < 50; i++ {
		if bin < int(bandStart[i])+int(bandSize[i]) {
			return i
		}
	}
	return 49
}
func logarithmicAdd(a, b int) int {
	difference := a - b
	p := difference
	if p < 0 {
		p = -p
	}
	p >>= 1
	p = min(p, 255)
	if difference >= 0 {
		return a + int(logAdd[p])
	}
	return b + int(logAdd[p])
}
func lowCompCalc(a, b0, b1, band int) int {
	if band < 7 {
		if b0+256 == b1 {
			a = 384
		} else if b0 > b1 {
			a = max(0, a-64)
		}
	} else if band < 20 {
		if b0+256 == b1 {
			a = 320
		} else if b0 > b1 {
			a = max(0, a-64)
		}
	} else {
		a = max(0, a-128)
	}
	return a
}

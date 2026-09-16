package lc

// AAC-LC reconstruction adapted from MIT OxideAV at
// 7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885, src/{dequant,pns,
// ms_stereo,intensity_stereo,tns_coef,tns_frame,tns_max}.rs.
// Copyright (c) 2026 Karpelès Lab Inc. See MIT-NOTICE.txt.
import (
	"math"
)

// Reconstruct converts validated syntax to window-major spectral coefficients.
// The caller owns PRNG state and must roll it back if an entire frame fails.
func Reconstruct(f *Frame, rate int, random *uint32) ([2][1024]float64, error) {
	var spec [2][1024]float64
	if f == nil || random == nil || f.Count < 1 || f.Count > 2 {
		return spec, malformedf("reconstruction input")
	}
	fs, ok := sampleRateToIndex[rate]
	if !ok {
		return spec, unsupportedf("rate")
	}
	for c := 0; c < f.Count; c++ {
		ch := &f.Channels[c]
		base := 0
		winLen := windowLen(ch.Sequence)
		for g := 0; g < ch.NumGroups; g++ {
			for sfb := 0; sfb < ch.MaxSFB; sfb++ {
				book := ch.Codebook[g][sfb]
				if book == 14 || book == 15 {
					if c != 1 || !f.CommonWindow {
						return spec, unsupportedf("intensity requires common stereo window")
					}
					continue
				}
				if book == 0 || book == 13 {
					continue
				}
				gain := math.Exp2(float64(ch.Scale[g][sfb]-100) * 0.25)
				for w := 0; w < ch.GroupLength[g]; w++ {
					start := (base+w)*winLen + ch.Offsets[sfb]
					end := (base+w)*winLen + ch.Offsets[sfb+1]
					if !dequantBand(spec[c][start:end], ch.Quant[start:end], gain) {
						return spec, malformedf("quantised coefficient out of range")
					}
				}
			}
			base += ch.GroupLength[g]
		}
	}
	// PNS draws one independent vector per channel/band/window. Noise bands are
	// not transformed by ordinary M/S below; the mask does not share PRNG output.
	for c := 0; c < f.Count; c++ {
		ch := &f.Channels[c]
		base := 0
		winLen := windowLen(ch.Sequence)
		for g := 0; g < ch.NumGroups; g++ {
			for b := 0; b < ch.MaxSFB; b++ {
				if ch.Codebook[g][b] != 13 {
					continue
				}
				for w := 0; w < ch.GroupLength[g]; w++ {
					start := (base+w)*winLen + ch.Offsets[b]
					end := (base+w)*winLen + ch.Offsets[b+1]
					energy := 0.0
					for i := start; i < end; i++ {
						*random = *random*1664525 + 1013904223
						v := float64(int32(*random)) / 2147483648
						spec[c][i] = v
						energy += v * v
					}
					if energy == 0 {
						return spec, malformedf("zero noise energy")
					}
					gain := math.Exp2(float64(ch.Scale[g][b])*0.25) / math.Sqrt(energy)

					scaleBand(spec[c][start:end], spec[c][start:end], gain)
				}
			}
			base += ch.GroupLength[g]
		}
	}
	if f.Count == 2 && f.CommonWindow {
		left, right := &f.Channels[0], &f.Channels[1]
		base := 0
		winLen := windowLen(left.Sequence)
		for g := 0; g < left.NumGroups; g++ {
			for b := 0; b < left.MaxSFB; b++ {
				rb := right.Codebook[g][b]
				for w := 0; w < left.GroupLength[g]; w++ {
					start := (base+w)*winLen + left.Offsets[b]
					end := (base+w)*winLen + left.Offsets[b+1]
					if rb == 14 || rb == 15 {
						gain := math.Exp2(-float64(right.Scale[g][b]) * 0.25)
						if rb == 14 {
							gain = -gain
						}
						if f.MS[g][b] {
							gain = -gain
						}
						scaleBand(spec[1][start:end], spec[0][start:end], gain)
					} else if f.MS[g][b] && left.Codebook[g][b] != 13 && rb != 13 {
						midSide(spec[0][start:end], spec[1][start:end])
					}
				}
			}
			base += left.GroupLength[g]
		}
	}
	for c := 0; c < f.Count; c++ {
		applyTNS(&spec[c], &f.Channels[c], fs)
		for _, v := range spec[c] {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return spec, malformedf("non-finite spectrum")
			}
		}
	}
	return spec, nil
}

var tnsLong = [12]int{31, 31, 34, 40, 42, 51, 46, 46, 42, 42, 42, 39}
var tnsShort = [12]int{9, 9, 10, 14, 14, 14, 14, 14, 14, 14, 14, 14}

func applyTNS(spec *[1024]float64, ch *Channel, fs int) {
	winLen := windowLen(ch.Sequence)
	maxBand := tnsLong[fs]
	if winLen == 128 {
		maxBand = tnsShort[fs]
	}
	maxBand = min(maxBand, ch.MaxSFB)
	for w := 0; w < 1024/winLen; w++ {
		tw := ch.TNS[w]
		bottom := ch.NumOffsets - 1
		for f := 0; f < tw.Count; f++ {
			filter := tw.Filters[f]
			top := bottom
			bottom = max(0, top-filter.Length)
			if filter.Order == 0 {
				continue
			}
			res := 3
			if tw.CoefRes {
				res++
			}
			width := res
			if filter.CoefCompress {
				width--
			}
			var a, temp [13]float64
			a[0] = 1
			for m := 1; m <= filter.Order; m++ {
				v := int(filter.Coef[m-1])
				if v&(1<<(width-1)) != 0 {
					v -= 1 << width
				}
				den := float64(int(1)<<(res-1)) - 0.5
				if v < 0 {
					den += 1
				}
				k := math.Sin(float64(v) * (math.Pi / 2) / den)
				for i := 1; i < m; i++ {
					temp[i] = a[i] + k*a[m-i]
				}
				for i := 1; i < m; i++ {
					a[i] = temp[i]
				}
				a[m] = k
			}
			start, end := ch.Offsets[min(bottom, maxBand)], ch.Offsets[min(top, maxBand)]
			tnsBand(spec[w*winLen+start:w*winLen+end], a[1:filter.Order+1], filter.Direction)
		}
	}
}
